package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/sjson"
)

// 说明：
// OAuth 生图存在两条链路（见 docs/CODEX_IMAGE_GENERATION.md）：
//   - 上游 Responses 链路：把 /v1/images/* 转换成 Responses-API 的 image_generation
//     工具请求，发往 /backend-api/codex/responses（本文件恢复自上游 v0.1.146）。
//   - 二开专用 images 端点链路：直接调用 codex images 端点（openai_images_codex.go）。
// 通过分组开关 Group.ImageUseResponsesAPI 选择：开（默认）走上游 Responses 链路，
// 关走二开专用 images 端点链路。无分组的 key 默认走 Responses 链路。

// groupUsesResponsesImageAPI 依据请求所属分组决定 OAuth 生图是否走上游 Responses 链路。
// 分组存在时按其开关（DB 列默认 true，故所有真实分组默认走 Responses 链路）；
// 无分组（退化场景，实际无法调度到账号，仅单元测试触及）保留二开专用 codex images 端点旧行为。
func groupUsesResponsesImageAPI(c *gin.Context) bool {
	group := apiKeyGroup(getAPIKeyFromContext(c))
	if group == nil {
		return false
	}
	return group.ImageUseResponsesAPI
}

// buildOpenAIImagesResponsesRequest 将下游 images 请求转换为 Responses-API 的
// image_generation 工具请求体（上游 v0.1.146 实现，恢复备用）。
func buildOpenAIImagesResponsesRequest(parsed *OpenAIImagesRequest, toolModel string) ([]byte, error) {
	if parsed == nil {
		return nil, fmt.Errorf("parsed images request is required")
	}
	prompt := strings.TrimSpace(parsed.Prompt)
	if prompt == "" {
		return nil, fmt.Errorf("prompt is required")
	}

	inputImages := make([]string, 0, len(parsed.InputImageURLs)+len(parsed.Uploads))
	for _, imageURL := range parsed.InputImageURLs {
		if trimmed := strings.TrimSpace(imageURL); trimmed != "" {
			inputImages = append(inputImages, trimmed)
		}
	}
	for _, upload := range parsed.Uploads {
		dataURL, err := openAIImageUploadToDataURL(upload)
		if err != nil {
			return nil, err
		}
		inputImages = append(inputImages, dataURL)
	}
	if parsed.IsEdits() && len(inputImages) == 0 {
		return nil, fmt.Errorf("image input is required")
	}

	req := []byte(`{"instructions":"","stream":true,"reasoning":{"effort":"medium","summary":"auto"},"parallel_tool_calls":true,"include":["reasoning.encrypted_content"],"model":"","store":false,"tool_choice":{"type":"image_generation"}}`)
	req, _ = sjson.SetBytes(req, "model", openAIImagesResponsesMainModel)

	input := []byte(`[{"type":"message","role":"user","content":[{"type":"input_text","text":""}]}]`)
	input, _ = sjson.SetBytes(input, "0.content.0.text", prompt)
	for index, imageURL := range inputImages {
		part := []byte(`{"type":"input_image","image_url":""}`)
		part, _ = sjson.SetBytes(part, "image_url", imageURL)
		input, _ = sjson.SetRawBytes(input, fmt.Sprintf("0.content.%d", index+1), part)
	}
	req, _ = sjson.SetRawBytes(req, "input", input)

	action := "generate"
	if parsed.IsEdits() {
		action = "edit"
	}
	tool := []byte(`{"type":"image_generation","action":"","model":""}`)
	tool, _ = sjson.SetBytes(tool, "action", action)
	tool, _ = sjson.SetBytes(tool, "model", strings.TrimSpace(toolModel))
	if shouldPassOpenAIImagesN(toolModel, parsed.N) {
		tool, _ = sjson.SetBytes(tool, "n", parsed.N)
	}

	for _, field := range []struct {
		path  string
		value string
	}{
		{path: "size", value: parsed.Size},
		{path: "quality", value: parsed.Quality},
		{path: "background", value: parsed.Background},
		{path: "output_format", value: parsed.OutputFormat},
		{path: "moderation", value: parsed.Moderation},
		{path: "style", value: parsed.Style},
	} {
		if trimmed := strings.TrimSpace(field.value); trimmed != "" {
			tool, _ = sjson.SetBytes(tool, field.path, trimmed)
		}
	}
	if parsed.OutputCompression != nil {
		tool, _ = sjson.SetBytes(tool, "output_compression", *parsed.OutputCompression)
	}
	if parsed.PartialImages != nil {
		tool, _ = sjson.SetBytes(tool, "partial_images", *parsed.PartialImages)
	}

	maskImageURL := strings.TrimSpace(parsed.MaskImageURL)
	if parsed.MaskUpload != nil {
		dataURL, err := openAIImageUploadToDataURL(*parsed.MaskUpload)
		if err != nil {
			return nil, err
		}
		maskImageURL = dataURL
	}
	if maskImageURL != "" {
		tool, _ = sjson.SetBytes(tool, "input_image_mask.image_url", maskImageURL)
	}

	req, _ = sjson.SetRawBytes(req, "tools", []byte(`[]`))
	req, _ = sjson.SetRawBytes(req, "tools.-1", tool)
	return req, nil
}

func shouldPassOpenAIImagesN(model string, n int) bool {
	if n <= 1 {
		return false
	}
	return !strings.EqualFold(strings.TrimSpace(model), "dall-e-3")
}

func openAIImageUploadToDataURL(upload OpenAIImagesUpload) (string, error) {
	if len(upload.Data) == 0 {
		return "", fmt.Errorf("upload %q is empty", strings.TrimSpace(upload.FileName))
	}
	contentType := strings.TrimSpace(upload.ContentType)
	if contentType == "" {
		contentType = http.DetectContentType(upload.Data)
	}
	return "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(upload.Data), nil
}

func openAIImagesUpstreamErrorResponseBody(err *OpenAIImagesUpstreamError) []byte {
	if err == nil {
		return nil
	}
	body := []byte(`{"error":{"type":"","message":""}}`)
	body, _ = sjson.SetBytes(body, "error.type", err.clientErrorType())
	body, _ = sjson.SetBytes(body, "error.message", err.clientMessage())
	if code := strings.TrimSpace(err.Code); code != "" {
		body, _ = sjson.SetBytes(body, "error.code", code)
	}
	if param := strings.TrimSpace(err.Param); param != "" {
		body, _ = sjson.SetBytes(body, "error.param", param)
	}
	return body
}

func (s *OpenAIGatewayService) handleOpenAIImagesOAuthResponseError(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	requestedModel string,
	upstreamURL string,
	resp *http.Response,
	writerSizeBeforeResponse int,
	err error,
) error {
	var upstreamErr *OpenAIImagesUpstreamError
	if !errors.As(err, &upstreamErr) {
		return err
	}

	retryable := IsOpenAIImagesRetryableUpstreamError(upstreamErr)
	responseWritten := c != nil && c.Writer != nil && c.Writer.Size() != writerSizeBeforeResponse
	kind := "http_error"
	if retryable {
		kind = "failover"
		if responseWritten {
			kind = "retry_exhausted_failover"
		}
	}

	requestID := strings.TrimSpace(upstreamErr.UpstreamRequestID)
	headers := http.Header(nil)
	if resp != nil {
		headers = resp.Header.Clone()
		if requestID == "" {
			requestID = strings.TrimSpace(resp.Header.Get("x-request-id"))
		}
	}
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform:           account.Platform,
		AccountID:          account.ID,
		AccountName:        account.Name,
		UpstreamStatusCode: upstreamErr.StatusCode,
		UpstreamRequestID:  requestID,
		UpstreamURL:        upstreamURL,
		Kind:               kind,
		Message:            upstreamErr.clientMessage(),
	})

	if !retryable || responseWritten {
		return err
	}

	responseBody := openAIImagesUpstreamErrorResponseBody(upstreamErr)
	shouldDisable := s.handleOpenAIAccountUpstreamError(ctx, account, upstreamErr.StatusCode, headers, responseBody, requestedModel)
	return &UpstreamFailoverError{
		StatusCode:             upstreamErr.StatusCode,
		ResponseBody:           responseBody,
		ResponseHeaders:        headers,
		RetryableOnSameAccount: !shouldDisable && account.IsPoolMode() && account.IsPoolModeRetryableStatus(upstreamErr.StatusCode),
	}
}

// forwardOpenAIImagesOAuthResponses 走上游 Responses 链路（image_generation 工具，
// 发往 /backend-api/codex/responses）。恢复自上游 v0.1.146 的 forwardOpenAIImagesOAuth，
// 共享响应处理器按二开签名传入 retryableEmptyOutput=false（保持上游不做空输出重试的行为）。
func (s *OpenAIGatewayService) forwardOpenAIImagesOAuthResponses(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	parsed *OpenAIImagesRequest,
	channelMappedModel string,
) (*OpenAIForwardResult, error) {
	startTime := time.Now()
	requestModel := strings.TrimSpace(parsed.Model)
	if mapped := strings.TrimSpace(channelMappedModel); mapped != "" {
		requestModel = mapped
	}
	if requestModel == "" {
		requestModel = "gpt-image-2"
	}
	if err := validateOpenAIImagesModel(requestModel); err != nil {
		return nil, err
	}
	logger.LegacyPrintf(
		"service.openai_gateway",
		"[OpenAI] Images request routing (responses) request_model=%s endpoint=%s account_type=%s uploads=%d",
		requestModel,
		parsed.Endpoint,
		account.Type,
		len(parsed.Uploads),
	)
	upstreamCtx, releaseUpstreamCtx := detachUpstreamContext(ctx)
	defer releaseUpstreamCtx()

	token, _, err := s.GetAccessToken(upstreamCtx, account)
	if err != nil {
		return nil, err
	}

	responsesBody, err := buildOpenAIImagesResponsesRequest(parsed, requestModel)
	if err != nil {
		return nil, err
	}
	upstreamReq, err := s.buildUpstreamRequest(upstreamCtx, c, account, responsesBody, token, true, parsed.StickySessionSeed(), false)
	if err != nil {
		return nil, err
	}
	upstreamReq.Header.Set("Content-Type", "application/json")
	upstreamReq.Header.Set("Accept", "text/event-stream")

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	upstreamStart := time.Now()
	resp, err := s.httpUpstream.Do(upstreamReq, proxyURL, account.ID, account.Concurrency)
	SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
	if err != nil {
		safeErr := sanitizeUpstreamErrorMessage(err.Error())
		setOpsUpstreamError(c, 0, safeErr, "")
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform:           account.Platform,
			AccountID:          account.ID,
			AccountName:        account.Name,
			UpstreamStatusCode: 0,
			UpstreamURL:        safeUpstreamURL(upstreamReq.URL.String()),
			Kind:               "request_error",
			Message:            safeErr,
		})
		return nil, fmt.Errorf("upstream request failed: %s", safeErr)
	}
	if resp == nil {
		return nil, fmt.Errorf("upstream request failed: empty response")
	}
	if resp.StatusCode >= 400 {
		respBody := s.readUpstreamErrorBody(resp)
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(respBody))
		upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(respBody))
		upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)
		if s.shouldFailoverOpenAIUpstreamResponse(resp.StatusCode, upstreamMsg, respBody) {
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: resp.StatusCode,
				UpstreamRequestID:  resp.Header.Get("x-request-id"),
				UpstreamURL:        safeUpstreamURL(upstreamReq.URL.String()),
				Kind:               "failover",
				Message:            upstreamMsg,
			})
			s.handleFailoverSideEffects(upstreamCtx, resp, account, respBody, requestModel)
			return nil, &UpstreamFailoverError{
				StatusCode:             resp.StatusCode,
				ResponseBody:           respBody,
				RetryableOnSameAccount: account.IsPoolMode() && account.IsPoolModeRetryableStatus(resp.StatusCode),
			}
		}
		return s.handleOpenAIImagesErrorResponse(upstreamCtx, resp, c, account, requestModel)
	}
	defer func() { _ = resp.Body.Close() }()

	var (
		usage            OpenAIUsage
		imageCount       int
		imageOutputSizes []string
		firstTokenMs     *int
	)
	writerSizeBeforeResponse := c.Writer.Size()
	if parsed.Stream {
		usage, imageCount, imageOutputSizes, firstTokenMs, err = s.handleOpenAIImagesOAuthStreamingResponse(resp, c, startTime, parsed.ResponseFormat, openAIImagesStreamPrefix(parsed), requestModel, false)
		if err != nil {
			if imageCount > 0 {
				return &OpenAIForwardResult{
					RequestID:        resp.Header.Get("x-request-id"),
					Usage:            usage,
					Model:            requestModel,
					UpstreamModel:    requestModel,
					Stream:           parsed.Stream,
					ResponseHeaders:  resp.Header.Clone(),
					Duration:         time.Since(startTime),
					FirstTokenMs:     firstTokenMs,
					ImageCount:       imageCount,
					ImageSize:        parsed.SizeTier,
					ImageInputSize:   parsed.Size,
					ImageOutputSizes: imageOutputSizes,
				}, err
			}
			return nil, s.handleOpenAIImagesOAuthResponseError(
				upstreamCtx,
				c,
				account,
				requestModel,
				safeUpstreamURL(upstreamReq.URL.String()),
				resp,
				writerSizeBeforeResponse,
				err,
			)
		}
	} else {
		usage, imageCount, imageOutputSizes, err = s.handleOpenAIImagesOAuthNonStreamingResponse(resp, c, parsed.ResponseFormat, requestModel, false)
		if err != nil {
			return nil, s.handleOpenAIImagesOAuthResponseError(
				upstreamCtx,
				c,
				account,
				requestModel,
				safeUpstreamURL(upstreamReq.URL.String()),
				resp,
				writerSizeBeforeResponse,
				err,
			)
		}
	}
	if imageCount <= 0 {
		imageCount = parsed.N
	}
	return &OpenAIForwardResult{
		RequestID:        resp.Header.Get("x-request-id"),
		Usage:            usage,
		Model:            requestModel,
		UpstreamModel:    requestModel,
		Stream:           parsed.Stream,
		ResponseHeaders:  resp.Header.Clone(),
		Duration:         time.Since(startTime),
		FirstTokenMs:     firstTokenMs,
		ImageCount:       imageCount,
		ImageSize:        parsed.SizeTier,
		ImageInputSize:   parsed.Size,
		ImageOutputSizes: imageOutputSizes,
	}, nil
}
