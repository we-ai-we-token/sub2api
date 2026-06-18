package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"mime/multipart"
	"net/http"
	"strconv"
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
// the dedicated codex image endpoints. generations -> JSON; edits -> multipart.
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

func buildOpenAIImagesCodexEditsBody(parsed *OpenAIImagesRequest, model string) ([]byte, string, error) {
	images, err := openAIImagesCodexEditImageBytes(parsed)
	if err != nil {
		return nil, "", err
	}
	if len(images) == 0 {
		return nil, "", fmt.Errorf("image input is required")
	}

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	for _, img := range images {
		fileName := img.FileName
		if strings.TrimSpace(fileName) == "" {
			fileName = "image.png"
		}
		fw, err := w.CreateFormFile("image", fileName)
		if err != nil {
			return nil, "", err
		}
		if _, err := fw.Write(img.Data); err != nil {
			return nil, "", err
		}
	}

	if mask, ok, err := openAIImagesCodexEditMaskBytes(parsed); err != nil {
		return nil, "", err
	} else if ok {
		fileName := mask.FileName
		if strings.TrimSpace(fileName) == "" {
			fileName = "mask.png"
		}
		fw, err := w.CreateFormFile("mask", fileName)
		if err != nil {
			return nil, "", err
		}
		if _, err := fw.Write(mask.Data); err != nil {
			return nil, "", err
		}
	}

	_ = w.WriteField("prompt", parsed.Prompt)
	_ = w.WriteField("model", strings.TrimSpace(model))
	_ = w.WriteField("n", strconv.Itoa(parsed.N))
	if parsed.Stream {
		_ = w.WriteField("stream", "true")
	}
	for _, f := range []struct{ field, value string }{
		{"size", parsed.Size},
		{"quality", parsed.Quality},
		{"output_format", parsed.OutputFormat},
		{"background", parsed.Background},
	} {
		if v := strings.TrimSpace(f.value); v != "" {
			_ = w.WriteField(f.field, v)
		}
	}

	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), w.FormDataContentType(), nil
}

// openAIImagesCodexEditImageBytes returns raw image bytes for edits: from
// Uploads (raw) or data-URL InputImageURLs. Remote http(s) URLs are rejected.
func openAIImagesCodexEditImageBytes(parsed *OpenAIImagesRequest) ([]OpenAIImagesUpload, error) {
	out := make([]OpenAIImagesUpload, 0, len(parsed.Uploads)+len(parsed.InputImageURLs))
	for _, up := range parsed.Uploads {
		if len(up.Data) == 0 {
			continue
		}
		out = append(out, up)
	}
	for _, raw := range parsed.InputImageURLs {
		data, fileName, err := decodeOpenAIImagesDataURL(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, OpenAIImagesUpload{FieldName: "image", FileName: fileName, Data: data})
	}
	return out, nil
}

func openAIImagesCodexEditMaskBytes(parsed *OpenAIImagesRequest) (OpenAIImagesUpload, bool, error) {
	if parsed.MaskUpload != nil && len(parsed.MaskUpload.Data) > 0 {
		return *parsed.MaskUpload, true, nil
	}
	if raw := strings.TrimSpace(parsed.MaskImageURL); raw != "" {
		data, fileName, err := decodeOpenAIImagesDataURL(raw)
		if err != nil {
			return OpenAIImagesUpload{}, false, err
		}
		return OpenAIImagesUpload{FieldName: "mask", FileName: fileName, Data: data}, true, nil
	}
	return OpenAIImagesUpload{}, false, nil
}

// decodeOpenAIImagesDataURL decodes a base64 data: URL into bytes. Remote URLs
// (no inline bytes) are rejected because multipart needs the actual file.
func decodeOpenAIImagesDataURL(raw string) ([]byte, string, error) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "data:") {
		return nil, "", fmt.Errorf("image input must be uploaded bytes or a data URL")
	}
	idx := strings.Index(raw, ",")
	if idx < 0 {
		return nil, "", fmt.Errorf("invalid data URL image input")
	}
	meta, payload := raw[5:idx], raw[idx+1:]
	if !strings.Contains(meta, "base64") {
		return nil, "", fmt.Errorf("only base64 data URL image input is supported")
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(payload)
	}
	if err != nil {
		return nil, "", fmt.Errorf("decode data URL image input: %w", err)
	}
	return data, "image.png", nil
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

	// 复用现有自定义 UA / 浏览器 UA 兜底（仅 OAuth 生效）。
	if customUA := account.GetOpenAIUserAgent(); customUA != "" {
		req.Header.Set("User-Agent", customUA)
	}
	if s.cfg != nil && s.cfg.Gateway.ForceCodexCLI {
		req.Header.Set("User-Agent", codexCLIUserAgent)
	}
	s.overrideBrowserUserAgent(ctx, account, req)
	return req, nil
}
