package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	chatgptCodexImagesGenerationsURL = "https://chatgpt.com/backend-api/codex/images/generations"
	chatgptCodexImagesEditsURL       = "https://chatgpt.com/backend-api/codex/images/edits"
)

// buildOpenAIImagesCodexRequestBody builds the native OpenAI images request for
// the dedicated codex image endpoints. Both flows use application/json: the
// codex edits endpoint rejects multipart/form-data ("Unsupported content type")
// and expects image inputs as an `images` array of `{image_url}` objects.
func buildOpenAIImagesCodexRequestBody(parsed *OpenAIImagesRequest, model string) ([]byte, string, error) {
	if parsed.IsEdits() {
		return buildOpenAIImagesCodexEditsBody(parsed, model)
	}
	return buildOpenAIImagesCodexGenerationsBody(parsed, model), "application/json", nil
}

func buildOpenAIImagesCodexGenerationsBody(parsed *OpenAIImagesRequest, model string) []byte {
	body := []byte(`{}`)
	body, _ = sjson.SetBytes(body, "model", strings.TrimSpace(model))
	body, _ = sjson.SetBytes(body, "prompt", parsed.Prompt)
	body, _ = sjson.SetBytes(body, "n", parsed.N)
	body, _ = sjson.SetBytes(body, "stream", parsed.Stream)
	for _, f := range []struct{ path, value string }{
		{"size", parsed.Size},
		{"quality", parsed.Quality},
		{"output_format", parsed.OutputFormat},
		{"background", parsed.Background},
	} {
		if v := strings.TrimSpace(f.value); v != "" {
			body, _ = sjson.SetBytes(body, f.path, v)
		}
	}
	return body
}

// buildOpenAIImagesCodexEditsBody builds the JSON edits request for the codex
// images endpoint. Image inputs are inlined as an `images` array of
// `{image_url: <base64 data URL>}` objects (the multipart/form-data shape the
// public OpenAI API uses is rejected here as "Unsupported content type").
func buildOpenAIImagesCodexEditsBody(parsed *OpenAIImagesRequest, model string) ([]byte, string, error) {
	imageURLs, err := openAIImagesCodexEditImageURLs(parsed)
	if err != nil {
		return nil, "", err
	}
	if len(imageURLs) == 0 {
		return nil, "", fmt.Errorf("image input is required")
	}

	body := buildOpenAIImagesCodexGenerationsBody(parsed, model)

	images := make([]map[string]string, 0, len(imageURLs))
	for _, u := range imageURLs {
		images = append(images, map[string]string{"image_url": u})
	}
	body, err = sjson.SetBytes(body, "images", images)
	if err != nil {
		return nil, "", fmt.Errorf("set images field: %w", err)
	}

	maskURL, ok, err := openAIImagesCodexEditMaskURL(parsed)
	if err != nil {
		return nil, "", err
	}
	if ok {
		body, err = sjson.SetBytes(body, "mask", map[string]string{"image_url": maskURL})
		if err != nil {
			return nil, "", fmt.Errorf("set mask field: %w", err)
		}
	}
	return body, "application/json", nil
}

// openAIImagesCodexEditImageURLs returns base64 data URLs for edit inputs: from
// Uploads (raw bytes) or data-URL InputImageURLs. Remote http(s) URLs are
// rejected because the codex edits endpoint needs inline image bytes.
func openAIImagesCodexEditImageURLs(parsed *OpenAIImagesRequest) ([]string, error) {
	out := make([]string, 0, len(parsed.Uploads)+len(parsed.InputImageURLs))
	for _, up := range parsed.Uploads {
		if len(up.Data) == 0 {
			continue
		}
		out = append(out, openAIImagesBytesDataURL(up.Data, up.ContentType))
	}
	for _, raw := range parsed.InputImageURLs {
		dataURL, err := normalizeOpenAIImagesEditDataURL(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, dataURL)
	}
	return out, nil
}

func openAIImagesCodexEditMaskURL(parsed *OpenAIImagesRequest) (string, bool, error) {
	if parsed.MaskUpload != nil && len(parsed.MaskUpload.Data) > 0 {
		return openAIImagesBytesDataURL(parsed.MaskUpload.Data, parsed.MaskUpload.ContentType), true, nil
	}
	if raw := strings.TrimSpace(parsed.MaskImageURL); raw != "" {
		dataURL, err := normalizeOpenAIImagesEditDataURL(raw)
		if err != nil {
			return "", false, err
		}
		return dataURL, true, nil
	}
	return "", false, nil
}

// openAIImagesBytesDataURL encodes raw image bytes into a base64 data URL,
// detecting the MIME type from the bytes when contentType is empty.
func openAIImagesBytesDataURL(data []byte, contentType string) string {
	ct := strings.TrimSpace(contentType)
	if ct == "" {
		ct = http.DetectContentType(data)
	}
	return "data:" + ct + ";base64," + base64.StdEncoding.EncodeToString(data)
}

// normalizeOpenAIImagesEditDataURL validates a base64 data: URL image input and
// re-emits it with standard (padded) base64 while preserving the original MIME
// type. Remote http(s) URLs are rejected because the codex edits endpoint needs
// inline image bytes.
func normalizeOpenAIImagesEditDataURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "data:") {
		return "", fmt.Errorf("image input must be uploaded bytes or a data URL")
	}
	meta, payload, ok := strings.Cut(raw[len("data:"):], ",")
	if !ok {
		return "", fmt.Errorf("invalid data URL image input")
	}
	if !strings.Contains(meta, "base64") {
		return "", fmt.Errorf("only base64 data URL image input is supported")
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(payload)
	}
	if err != nil {
		return "", fmt.Errorf("decode data URL image input: %w", err)
	}
	mimeType := strings.TrimSpace(strings.SplitN(meta, ";", 2)[0])
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

func (s *OpenAIGatewayService) parseOpenAIImagesCodexNonStreamingOutput(
	resp *http.Response,
	c *gin.Context,
	fallbackModel string,
) (*openAIImagesOAuthForwardOutput, error) {
	body, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, err
	}
	root := gjson.ParseBytes(body)

	createdAt := root.Get("created").Int()

	var usage OpenAIUsage
	var usageRaw []byte
	if u := root.Get("usage"); u.Exists() && u.IsObject() {
		usageRaw = []byte(u.Raw)
		if parsed, ok := openAIUsageFromGJSON(u); ok {
			usage = parsed
		}
	}

	firstMeta := openAIResponsesImageResult{
		OutputFormat: strings.TrimSpace(root.Get("output_format").String()),
		Size:         strings.TrimSpace(root.Get("size").String()),
		Background:   strings.TrimSpace(root.Get("background").String()),
		Quality:      strings.TrimSpace(root.Get("quality").String()),
		Model:        strings.TrimSpace(fallbackModel),
	}

	var results []openAIResponsesImageResult
	var sizes []string
	for _, item := range root.Get("data").Array() {
		b64 := strings.TrimSpace(item.Get("b64_json").String())
		if b64 == "" {
			continue
		}
		size := strings.TrimSpace(item.Get("size").String())
		if size == "" {
			size = firstMeta.Size
		}
		results = append(results, openAIResponsesImageResult{
			Result:        b64,
			RevisedPrompt: strings.TrimSpace(item.Get("revised_prompt").String()),
			OutputFormat:  firstMeta.OutputFormat,
			Size:          size,
			Background:    firstMeta.Background,
			Quality:       firstMeta.Quality,
		})
		sizes = append(sizes, size)
	}

	if len(results) == 0 {
		return &openAIImagesOAuthForwardOutput{
			Usage:           usage,
			CreatedAt:       createdAt,
			UsageRaw:        usageRaw,
			FirstMeta:       firstMeta,
			ResponseHeaders: resp.Header.Clone(),
			StatusCode:      resp.StatusCode,
		}, errOpenAIImagesEmptyOutputRetryable
	}

	return &openAIImagesOAuthForwardOutput{
		Usage:           usage,
		ImageResults:    results,
		ImageSizes:      sizes,
		CreatedAt:       createdAt,
		UsageRaw:        usageRaw,
		FirstMeta:       firstMeta,
		ResponseHeaders: resp.Header.Clone(),
		UpstreamModel:   strings.TrimSpace(fallbackModel),
		RequestID:       resp.Header.Get("x-request-id"),
		StatusCode:      resp.StatusCode,
	}, nil
}

func (s *OpenAIGatewayService) buildOpenAIImagesCodexUpstreamRequest(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	parsed *OpenAIImagesRequest,
	body []byte,
	contentType string,
	token string,
) (*http.Request, error) {
	targetURL := chatgptCodexImagesGenerationsURL
	if parsed.IsEdits() {
		targetURL = chatgptCodexImagesEditsURL
	}
	req, err := http.NewRequestWithContext(ctx, "POST", targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))
	req.Host = "chatgpt.com"

	req.Header.Set("Authorization", "Bearer "+token)
	if accountID := account.GetChatGPTAccountID(); accountID != "" {
		req.Header.Set("chatgpt-account-id", accountID)
	}
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	req.Header.Set("originator", "codex_cli_rs")
	req.Header.Set("session_id", uuid.NewString())
	req.Header.Set("User-Agent", codexCLIUserAgent)
	req.Header.Set("Content-Type", contentType)
	if parsed.Stream {
		req.Header.Set("Accept", "text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json")
	}

	// 复用现有自定义 UA（仅 OAuth 生效）。
	if customUA := account.GetOpenAIUserAgent(); customUA != "" {
		req.Header.Set("User-Agent", customUA)
	}
	if s.cfg != nil && s.cfg.Gateway.ForceCodexCLI {
		req.Header.Set("User-Agent", codexCLIUserAgent)
	}
	// 浏览器型 UA 兜底（仅 OAuth 生效）：codex 图片端点保留自有出站身份
	// （originator=codex_cli_rs），不走网关统一身份收口--v0.1.172 起默认身份改为
	// codex-tui，enforceCodexIdentityHeadersWithUA 会把 originator 改写成 codex-tui。
	// 仅当最终 UA 仍为浏览器型（Mozilla/）时替换为后台配置的 Codex UA，规避 Cloudflare
	// 对浏览器型 UA 在 ChatGPT 内部接口上的 JS 质询。必须在所有 User-Agent 改写之后调用。
	if account.Type == AccountTypeOAuth {
		if finalUA := strings.TrimSpace(req.Header.Get("User-Agent")); strings.HasPrefix(strings.ToLower(finalUA), "mozilla/") {
			codexUA := DefaultOpenAICodexUserAgent
			if s.settingService != nil {
				if v := strings.TrimSpace(s.settingService.GetOpenAICodexUserAgent(ctx)); v != "" {
					codexUA = v
				}
			}
			req.Header.Set("User-Agent", codexUA)
		}
	}
	return req, nil
}

// parseOpenAIImagesCodexStreamLine inspects one native SSE data line. The codex
// images endpoint emits the final image as a flat object carrying b64_json, and
// usage in a (possibly separate) object. No partial-preview frames.
func parseOpenAIImagesCodexStreamLine(data []byte) (img openAIResponsesImageResult, isImage bool, usage OpenAIUsage, hasUsage bool) {
	if !gjson.ValidBytes(data) {
		return openAIResponsesImageResult{}, false, OpenAIUsage{}, false
	}
	root := gjson.ParseBytes(data)
	if u := root.Get("usage"); u.Exists() && u.IsObject() {
		if parsed, ok := openAIUsageFromGJSON(u); ok {
			usage = parsed
			hasUsage = true
		}
	}
	b64 := strings.TrimSpace(root.Get("b64_json").String())
	if b64 == "" {
		// 兼容 data[].b64_json 包裹形态
		b64 = strings.TrimSpace(root.Get("data.0.b64_json").String())
	}
	if b64 != "" {
		img = openAIResponsesImageResult{
			Result:        b64,
			RevisedPrompt: strings.TrimSpace(root.Get("revised_prompt").String()),
			OutputFormat:  strings.TrimSpace(root.Get("output_format").String()),
			Size:          strings.TrimSpace(root.Get("size").String()),
			Background:    strings.TrimSpace(root.Get("background").String()),
			Quality:       strings.TrimSpace(root.Get("quality").String()),
		}
		isImage = true
	}
	return img, isImage, usage, hasUsage
}
