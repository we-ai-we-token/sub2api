package service

import (
	"bytes"
	"mime"
	"mime/multipart"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestBuildOpenAIImagesCodexRequestBodyGenerations(t *testing.T) {
	parsed := &OpenAIImagesRequest{
		Endpoint: openAIImagesGenerationsEndpoint,
		Prompt:   "a red apple",
		Size:     "1024x1024",
		Quality:  "medium",
		N:        2,
		Stream:   false,
	}
	body, ct, err := buildOpenAIImagesCodexRequestBody(parsed, "gpt-image-2")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if ct != "application/json" {
		t.Fatalf("content-type = %q, want application/json", ct)
	}
	if got := gjson.GetBytes(body, "model").String(); got != "gpt-image-2" {
		t.Fatalf("model = %q", got)
	}
	if got := gjson.GetBytes(body, "prompt").String(); got != "a red apple" {
		t.Fatalf("prompt = %q", got)
	}
	if got := gjson.GetBytes(body, "size").String(); got != "1024x1024" {
		t.Fatalf("size = %q", got)
	}
	if got := gjson.GetBytes(body, "n").Int(); got != 2 {
		t.Fatalf("n = %d", got)
	}
	if gjson.GetBytes(body, "stream").Bool() {
		t.Fatalf("stream should be false")
	}
	// 空字段不应写入
	if gjson.GetBytes(body, "background").Exists() {
		t.Fatalf("background should be omitted when empty")
	}
}

func TestBuildOpenAIImagesCodexRequestBodyEditsMultipart(t *testing.T) {
	parsed := &OpenAIImagesRequest{
		Endpoint: openAIImagesEditsEndpoint,
		Prompt:   "make it blue",
		N:        1,
		Uploads: []OpenAIImagesUpload{
			{FieldName: "image", FileName: "a.png", ContentType: "image/png", Data: []byte("PNGDATA")},
		},
	}
	body, ct, err := buildOpenAIImagesCodexRequestBody(parsed, "gpt-image-2")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	mediaType, params, err := mime.ParseMediaType(ct)
	if err != nil || mediaType != "multipart/form-data" {
		t.Fatalf("content-type = %q (mediaType=%q err=%v)", ct, mediaType, err)
	}
	mr := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	form, err := mr.ReadForm(1 << 20)
	if err != nil {
		t.Fatalf("read form: %v", err)
	}
	if got := form.Value["prompt"]; len(got) != 1 || got[0] != "make it blue" {
		t.Fatalf("prompt field = %v", got)
	}
	if got := form.Value["model"]; len(got) != 1 || got[0] != "gpt-image-2" {
		t.Fatalf("model field = %v", got)
	}
	if len(form.File["image"]) != 1 {
		t.Fatalf("want 1 image file, got %d", len(form.File["image"]))
	}
}

func TestBuildOpenAIImagesCodexRequestBodyEditsRemoteURLErrors(t *testing.T) {
	parsed := &OpenAIImagesRequest{
		Endpoint:       openAIImagesEditsEndpoint,
		Prompt:         "x",
		N:              1,
		InputImageURLs: []string{"https://example.com/a.png"},
	}
	_, _, err := buildOpenAIImagesCodexRequestBody(parsed, "gpt-image-2")
	if err == nil || !strings.Contains(err.Error(), "image input") {
		t.Fatalf("want remote-url image error, got %v", err)
	}
}
