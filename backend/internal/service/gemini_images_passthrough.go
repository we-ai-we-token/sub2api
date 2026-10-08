package service

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// IsGeminiImageGenerationModel 判断是否为 Gemini 生图模型。
// 口径与运营生图报表一致：platform='gemini' AND model ILIKE 'gemini-%image%'。
func IsGeminiImageGenerationModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(model, "gemini-") && strings.Contains(model, "image")
}

// geminiImageModelsNamedWithoutImage 二开：名字里不含 "image" 的 Gemini 生图模型，只能逐个点名。
// 刻意不用 "nano-banana" 子串：AI Studio 还挂着 nano-banana / nano-banana-pro 等没验证过的名字。
var geminiImageModelsNamedWithoutImage = map[string]struct{}{
	"gemini-nano-banana-2.1": {},
}

func isGeminiImageModelNamedWithoutImage(model string) bool {
	_, ok := geminiImageModelsNamedWithoutImage[strings.ToLower(strings.TrimSpace(model))]
	return ok
}

// IsGeminiImagesPassthroughModel 二开：OpenAI Images（gemini 分组）透传链路专用判据，
// = IsGeminiImageGenerationModel ∪ geminiImageModelsNamedWithoutImage。
// 刻意不改 IsGeminiImageGenerationModel 本身：它还驱动原生 /v1beta 的生图记录
// （gemini_v1beta_handler.go）与原生在途预留估算（gateway_inflight_reservation_fork.go）。
func IsGeminiImagesPassthroughModel(model string) bool {
	return IsGeminiImageGenerationModel(model) || isGeminiImageModelNamedWithoutImage(model)
}

// ForwardGeminiImagesPassthrough 将 OpenAI Images 请求原样透传到 Gemini AI Studio
// API Key 账号 base_url 指向的 OpenAI 兼容上游（如自建号池）。
// 前提：上游支持 OpenAI Images 协议；真·Google AI Studio 原生接口不适用，
// 因此账号必须显式配置 base_url。
// 错误契约与 forwardOpenAIImagesAPIKey 一致：
//   - *UpstreamFailoverError：可切号（429/5xx/账号缺配置）；
//   - *OpenAIImagesUpstreamError：用户侧上游错误，响应已原样写回客户端；
//   - 其他 error：未写响应，由 handler 兜底 502。
func (s *OpenAIGatewayService) ForwardGeminiImagesPassthrough(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	parsed *OpenAIImagesRequest,
	channelMappedModel string,
) (*OpenAIForwardResult, error) {
	if parsed == nil {
		return nil, fmt.Errorf("parsed images request is required")
	}
	if account.Type != AccountTypeAPIKey {
		return nil, fmt.Errorf("unsupported account type for gemini images passthrough: %s", account.Type)
	}
	startTime := time.Now()

	requestModel := strings.TrimSpace(parsed.Model)
	if mapped := strings.TrimSpace(channelMappedModel); mapped != "" {
		requestModel = mapped
	}
	upstreamModel := account.GetMappedModel(requestModel)
	// 账号 model_mapping 是管理员显式配置，信任它；名字检查只看映射前的名字
	// （上游别名如 "[A.s]nano-banana-pro" 不含 gemini-/image，按名字判必然误拒）。
	if !IsGeminiImagesPassthroughModel(requestModel) {
		return nil, fmt.Errorf("gemini images passthrough requires a gemini image model, got %q", requestModel)
	}

	forwardBody, forwardContentType, err := rewriteOpenAIImagesModel(body, parsed.ContentType, upstreamModel)
	// 同 openai_images.go：开关开启时 response_format 由网关消费，不转发给上游。
	forwardBody, forwardContentType = s.stripOpenAIImagesResponseFormatForURLReturn(c, parsed, forwardBody, forwardContentType)
	if err != nil {
		return nil, err
	}

	apiKey := strings.TrimSpace(account.GetCredential("api_key"))
	if apiKey == "" {
		// 账号缺配置按可切号处理，让调度循环尝试下一个账号
		return nil, &UpstreamFailoverError{
			StatusCode:   http.StatusServiceUnavailable,
			ResponseBody: []byte("gemini account api_key not configured"),
		}
	}
	baseURL := strings.TrimSpace(account.GetCredential("base_url"))
	if baseURL == "" {
		return nil, &UpstreamFailoverError{
			StatusCode:   http.StatusServiceUnavailable,
			ResponseBody: []byte("gemini account base_url not configured for openai images passthrough"),
		}
	}
	validatedURL, err := s.validateUpstreamBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	targetURL := buildOpenAIEndpointURL(validatedURL, parsed.Endpoint)

	upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(forwardBody))
	if err != nil {
		return nil, fmt.Errorf("create upstream request failed: %w", err)
	}
	upstreamReq.Header.Set("Authorization", "Bearer "+apiKey)
	if forwardContentType != "" {
		upstreamReq.Header.Set("Content-Type", forwardContentType)
	}
	account.ApplyHeaderOverrides(upstreamReq.Header)

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

	if resp.StatusCode >= 400 {
		respBody := s.readUpstreamErrorBody(resp)
		_ = resp.Body.Close()
		upstreamMsg := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(respBody)))
		if s.shouldFailoverOpenAIUpstreamResponse(account, resp.StatusCode, upstreamMsg, respBody) {
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
			return nil, &UpstreamFailoverError{
				StatusCode:             resp.StatusCode,
				ResponseBody:           respBody,
				RetryableOnSameAccount: account.IsPoolMode() && account.IsPoolModeRetryableStatus(resp.StatusCode),
			}
		}
		// 用户侧错误：状态码与 body 原样透传给客户端
		imgErr := openAIImagesUpstreamErrorFromHTTP(resp.StatusCode, resp.Header, respBody)
		contentType := resp.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "application/json"
		}
		c.Data(resp.StatusCode, contentType, respBody)
		return nil, imgErr
	}
	defer func() { _ = resp.Body.Close() }()

	usage, imageCount, outputSizes, err := s.handleOpenAIImagesNonStreamingResponse(ctx, resp, c, account, parsed)
	if err != nil {
		return nil, err
	}
	finalCount := parsed.N
	if imageCount > 0 {
		finalCount = imageCount
	}
	return &OpenAIForwardResult{
		RequestID:        resp.Header.Get("x-request-id"),
		UpstreamHeaders:  resp.Header,
		Usage:            usage,
		Model:            requestModel,
		UpstreamModel:    upstreamModel,
		ResponseHeaders:  resp.Header.Clone(),
		Duration:         time.Since(startTime),
		ImageCount:       finalCount,
		ImageSize:        parsed.SizeTier,
		ImageInputSize:   parsed.Size,
		ImageOutputSizes: outputSizes,
	}, nil
}
