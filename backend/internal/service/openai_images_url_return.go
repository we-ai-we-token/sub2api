package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// 生图返回对象存储 URL（分组开关 Group.ImageReturnURL，仅 openai / gemini 平台）。
//
// 语义（刻意选的「客户端优先」，保证老客户端零变化）：
//   - 开关关闭          -> 行为完全不变。
//   - 开关开启 + 客户端未显式要求 url -> 行为完全不变（gpt-image-* 默认仍返回 b64_json）。
//   - 开关开启 + 客户端显式 response_format=url -> 图片转存对象存储，
//     响应里 data[i].url 换成对象存储短链接（预签名，过期时间见后台设置，默认 24h），
//     同时删除 data[i].b64_json。
//
// 开关只是「准入」，不改写 parsed.ResponseFormat —— 那个字段还参与账号能力分级
// （见 resolveOpenAIImagesCapability），改它会连带改变可调度的账号池。
//
// 失败一律降级为「原样返回上游响应体」：同步链路上 HTTP 200 可能已经由
// keepalive 提交，改报错发不出去；宁可不省带宽，也不能让客户拿不到图。

// imagesAsyncManagedKey 标记「本次请求由异步生图任务托管」。
//
// 异步链路 (/v1/images/*/async) 会用 c.Copy() 重入同步 handler，而它自己在
// ImageTaskService.Complete 里已经无条件调用 ImageResultUploader.Rewrite。
// 不加这个标记的话同一张图会被上传两次，且同步侧那份永远没人引用（孤儿对象）。
const imagesAsyncManagedKey = "images_async_managed"

// MarkImagesAsyncManaged 由异步生图 handler 在重入同步链路前调用。
func MarkImagesAsyncManaged(c *gin.Context) {
	if c != nil {
		c.Set(imagesAsyncManagedKey, true)
	}
}

func imagesAsyncManaged(c *gin.Context) bool {
	if c == nil {
		return false
	}
	managed, _ := c.Get(imagesAsyncManagedKey)
	flag, _ := managed.(bool)
	return flag
}

// groupSupportsImageReturnURLPlatform 与 admin 侧 groupSupportsImageReturnURL 同义，
// 在网关侧再判一次：分组平台可能在开关打开之后才被改掉。
func groupSupportsImageReturnURLPlatform(platform string) bool {
	return platform == PlatformOpenAI || platform == PlatformGemini
}

// groupWantsImageReturnURL 判断请求所属分组是否开启了生图返回 URL。
func groupWantsImageReturnURL(c *gin.Context) bool {
	group := apiKeyGroup(getAPIKeyFromContext(c))
	if group == nil {
		return false
	}
	return group.ImageReturnURL && groupSupportsImageReturnURLPlatform(group.Platform)
}

// clientRequestedImageURL 仅在客户端显式要求 url 时为真。
// 空值（未传）不算 —— gpt-image-* 的默认是 b64_json，保持默认即保持旧行为。
func clientRequestedImageURL(parsed *OpenAIImagesRequest) bool {
	if parsed == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(parsed.ResponseFormat), "url")
}

// imageStorageUploaderForRequest 三个条件全满足才返回可用的 uploader。
func (s *OpenAIGatewayService) imageStorageUploaderForRequest(
	c *gin.Context,
	parsed *OpenAIImagesRequest,
) (*ImageResultUploader, bool) {
	if s == nil || s.imageStorageResolver == nil {
		return nil, false
	}
	if imagesAsyncManaged(c) {
		return nil, false
	}
	if !clientRequestedImageURL(parsed) || !groupWantsImageReturnURL(c) {
		return nil, false
	}
	uploader, enabled := s.imageStorageResolver()
	if !enabled || uploader == nil {
		return nil, false
	}
	return uploader, true
}

// imageStorageObjectKeyBase 用 client_request_id 作为对象 key 前缀，
// 这样拿到一条 R2 key 可以直接反查 usage_logs / image_generation_records。
// 取不到时回落到 ImageResultUploader 自己的兜底（空串仍能生成 "-0.png"），
// 但正常链路上 ClientRequestID 中间件保证它一定存在。
func imageStorageObjectKeyBase(ctx context.Context, c *gin.Context) string {
	if c != nil && c.Request != nil {
		if v, _ := c.Request.Context().Value(ctxkey.ClientRequestID).(string); strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	if ctx != nil {
		if v, _ := ctx.Value(ctxkey.ClientRequestID).(string); strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// rewriteOpenAIImagesToStorageURL 把响应体里的图片转存对象存储并改写为 url。
//
// 返回值永远可用：任何一步失败都原样返回入参 body（降级为 base64）。
// 调用点必须排在尺寸检测/计费之后 —— OAuth 链路要解 base64 读像素尺寸，
// 提前删掉 b64_json 会让 image_size_source 掉回 default、按 2K 默认价重新计费。
func (s *OpenAIGatewayService) rewriteOpenAIImagesToStorageURL(
	ctx context.Context,
	c *gin.Context,
	parsed *OpenAIImagesRequest,
	body []byte,
) []byte {
	uploader, ok := s.imageStorageUploaderForRequest(c, parsed)
	if !ok {
		return body
	}
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return body
	}
	// 只处理正常的 images 响应；错误体没有 data 数组，交给下游原样返回。
	if !gjson.GetBytes(body, "data").IsArray() {
		return body
	}
	if limit := uploader.MaxImageBytes(); limit > 0 {
		for _, item := range gjson.GetBytes(body, "data").Array() {
			// base64 解码后约为原长的 3/4，这里按原长比较，偏保守。
			if int64(len(item.Get("b64_json").String())) > limit {
				logger.LegacyPrintf(
					"service.openai_gateway",
					"[OpenAI] Images url-return skipped: payload exceeds object storage limit limit=%d",
					limit,
				)
				return body
			}
		}
	}

	rewritten, err := uploader.Rewrite(ctx, imageStorageObjectKeyBase(ctx, c), body)
	if err != nil {
		// 降级：原样返回 base64。此时 HTTP 200 可能已提交，无法改成错误响应。
		logger.LegacyPrintf(
			"service.openai_gateway",
			"[OpenAI] Images url-return fell back to b64_json err=%s",
			sanitizeUpstreamErrorMessage(err.Error()),
		)
		return body
	}
	if len(rewritten) == 0 {
		return body
	}
	return rewritten
}

// openAIImagesResponseFormatField 是客户端用来要求返回 URL 的标准 OpenAI 参数名。
const openAIImagesResponseFormatField = "response_format"

// stripOpenAIImagesResponseFormatForURLReturn 在分组开启「生图返回 URL」时，
// 把 response_format 从**转发给上游**的请求体里摘掉。
//
// 为什么必须摘：Azure / OpenAI 的 gpt-image-* 系列根本不认这个参数（它是 dall-e 时代的
// 遗留），原样转发会被上游直接 400 `Unknown parameter: 'response_format'`，
// 网关的改写逻辑压根没机会执行。而分组开启这个开关，本身就等于声明「上游没有返回 URL
// 的能力，由网关来补」，所以这个值没有转发的必要。
//
// 只改转发体，不动 parsed.ResponseFormat —— 后者既是改写判据
// （clientRequestedImageURL），又参与账号能力分级（resolveOpenAIImagesCapability），
// 改它会连带收窄可调度账号池。
//
// 任何一步失败都原样返回入参，降级为「照旧转发」而不是让请求失败。
func (s *OpenAIGatewayService) stripOpenAIImagesResponseFormatForURLReturn(
	c *gin.Context,
	parsed *OpenAIImagesRequest,
	body []byte,
	contentType string,
) ([]byte, string) {
	if parsed == nil || strings.TrimSpace(parsed.ResponseFormat) == "" {
		return body, contentType
	}
	if !groupWantsImageReturnURL(c) {
		return body, contentType
	}
	stripped, strippedType, err := stripOpenAIImagesResponseFormatField(body, contentType)
	if err != nil {
		logger.LegacyPrintf(
			"service.openai_gateway",
			"[OpenAI] Images response_format strip skipped err=%s",
			sanitizeUpstreamErrorMessage(err.Error()),
		)
		return body, contentType
	}
	return stripped, strippedType
}

// stripOpenAIImagesResponseFormatField 同时处理 JSON 与 multipart 两种请求体。
func stripOpenAIImagesResponseFormatField(body []byte, contentType string) ([]byte, string, error) {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err == nil && strings.EqualFold(mediaType, "multipart/form-data") {
		return stripOpenAIImagesMultipartResponseFormat(body, contentType)
	}
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return body, contentType, nil
	}
	if !gjson.GetBytes(body, openAIImagesResponseFormatField).Exists() {
		return body, contentType, nil
	}
	stripped, err := sjson.DeleteBytes(body, openAIImagesResponseFormatField)
	if err != nil {
		return nil, "", fmt.Errorf("delete response_format: %w", err)
	}
	return stripped, contentType, nil
}

// stripOpenAIImagesMultipartResponseFormat 重建 multipart 体并跳过 response_format 字段。
// 结构照搬 rewriteOpenAIImagesMultipartModel，差别是「跳过」而非「改写」，
// 因此要在 CreatePart 之前判断字段名。
func stripOpenAIImagesMultipartResponseFormat(body []byte, contentType string) ([]byte, string, error) {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, "", fmt.Errorf("parse multipart content-type: %w", err)
	}
	boundary := strings.TrimSpace(params["boundary"])
	if boundary == "" {
		return nil, "", fmt.Errorf("multipart boundary is required")
	}

	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	dropped := false

	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, "", fmt.Errorf("read multipart body: %w", err)
		}
		// 只丢普通表单字段，不碰同名的文件分片（理论上不存在，防御性判断）。
		if strings.TrimSpace(part.FormName()) == openAIImagesResponseFormatField && part.FileName() == "" {
			dropped = true
			_ = part.Close()
			continue
		}
		target, err := writer.CreatePart(cloneMultipartHeader(part.Header))
		if err != nil {
			_ = part.Close()
			return nil, "", fmt.Errorf("create multipart part: %w", err)
		}
		if _, err := io.Copy(target, part); err != nil {
			_ = part.Close()
			return nil, "", fmt.Errorf("copy multipart part: %w", err)
		}
		_ = part.Close()
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("finalize multipart body: %w", err)
	}
	if !dropped {
		return body, contentType, nil
	}
	return buffer.Bytes(), writer.FormDataContentType(), nil
}
