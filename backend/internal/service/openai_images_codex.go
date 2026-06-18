package service

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"mime/multipart"
	"strconv"
	"strings"

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
		return nil, "", fmt.Errorf("decode data URL image input: %w", err)
	}
	return data, "image.png", nil
}
