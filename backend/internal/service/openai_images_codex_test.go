package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
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

func TestBuildOpenAIImagesCodexRequestBodyEditsDataURL(t *testing.T) {
	raw := []byte("PNGBYTES")
	for _, tc := range []struct {
		name    string
		encoded string
	}{
		{"padded", base64.StdEncoding.EncodeToString(raw)},
		{"unpadded", base64.RawStdEncoding.EncodeToString(raw)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parsed := &OpenAIImagesRequest{
				Endpoint:       openAIImagesEditsEndpoint,
				Prompt:         "x",
				N:              1,
				InputImageURLs: []string{"data:image/png;base64," + tc.encoded},
			}
			body, ct, err := buildOpenAIImagesCodexRequestBody(parsed, "gpt-image-2")
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			_, params, err := mime.ParseMediaType(ct)
			if err != nil {
				t.Fatalf("parse media type: %v", err)
			}
			form, err := multipart.NewReader(bytes.NewReader(body), params["boundary"]).ReadForm(1 << 20)
			if err != nil {
				t.Fatalf("read form: %v", err)
			}
			files := form.File["image"]
			if len(files) != 1 {
				t.Fatalf("want 1 image file, got %d", len(files))
			}
			f, _ := files[0].Open()
			defer f.Close()
			got, _ := io.ReadAll(f)
			if !bytes.Equal(got, raw) {
				t.Fatalf("decoded bytes = %q, want %q", got, raw)
			}
		})
	}
}

func TestBuildOpenAIImagesCodexUpstreamRequestHeaders(t *testing.T) {
	s := &OpenAIGatewayService{}
	acc := &Account{Type: AccountTypeOAuth}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/images/generations", nil)

	parsedGen := &OpenAIImagesRequest{Endpoint: openAIImagesGenerationsEndpoint, Prompt: "x", N: 1}
	req, err := s.buildOpenAIImagesCodexUpstreamRequest(context.Background(), c, acc, parsedGen, []byte(`{}`), "application/json", "tok")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if req.URL.String() != chatgptCodexImagesGenerationsURL {
		t.Fatalf("url = %s", req.URL.String())
	}
	if req.Host != "chatgpt.com" {
		t.Fatalf("host = %s", req.Host)
	}
	for k, want := range map[string]string{
		"Authorization": "Bearer tok",
		"OpenAI-Beta":   "responses=experimental",
		"originator":    "codex_cli_rs",
		"Content-Type":  "application/json",
		"Accept":        "application/json",
	} {
		if got := req.Header.Get(k); got != want {
			t.Fatalf("header %s = %q, want %q", k, got, want)
		}
	}
	if req.Header.Get("session_id") == "" {
		t.Fatalf("session_id must be set")
	}
	if req.Header.Get("User-Agent") != codexCLIUserAgent {
		t.Fatalf("ua = %q", req.Header.Get("User-Agent"))
	}

	// 流式 edits：Accept event-stream，URL=edits
	parsedEdit := &OpenAIImagesRequest{Endpoint: openAIImagesEditsEndpoint, Prompt: "x", N: 1, Stream: true}
	reqE, err := s.buildOpenAIImagesCodexUpstreamRequest(context.Background(), c, acc, parsedEdit, []byte("multipart-bytes"), "multipart/form-data; boundary=zzz", "tok")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if reqE.URL.String() != chatgptCodexImagesEditsURL {
		t.Fatalf("edit url = %s", reqE.URL.String())
	}
	if reqE.Header.Get("Accept") != "text/event-stream" {
		t.Fatalf("stream accept = %q", reqE.Header.Get("Accept"))
	}
	if reqE.Header.Get("Content-Type") != "multipart/form-data; boundary=zzz" {
		t.Fatalf("edit content-type = %q", reqE.Header.Get("Content-Type"))
	}
}

func TestParseOpenAIImagesCodexNonStreamingOutput(t *testing.T) {
	s := &OpenAIGatewayService{}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	bodyJSON := `{"created":1781809378,"background":"opaque","data":[{"b64_json":"QUJD"}],` +
		`"output_format":"png","quality":"medium","size":"2880x2880",` +
		`"usage":{"input_tokens":24,"output_tokens":5930,"output_tokens_details":{"image_tokens":5930}}}`
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(bodyJSON))}

	out, err := s.parseOpenAIImagesCodexNonStreamingOutput(resp, c, "gpt-image-2")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(out.ImageResults) != 1 || out.ImageResults[0].Result != "QUJD" {
		t.Fatalf("results = %#v", out.ImageResults)
	}
	if out.ImageSizes[0] != "2880x2880" {
		t.Fatalf("sizes = %v", out.ImageSizes)
	}
	if out.FirstMeta.Quality != "medium" || out.FirstMeta.OutputFormat != "png" || out.FirstMeta.Background != "opaque" {
		t.Fatalf("meta = %#v", out.FirstMeta)
	}
	if out.FirstMeta.Model != "gpt-image-2" {
		t.Fatalf("FirstMeta.Model = %q", out.FirstMeta.Model)
	}
	if len(out.UsageRaw) == 0 {
		t.Fatalf("UsageRaw must be set")
	}
	if out.Usage.OutputTokens != 5930 || out.Usage.ImageOutputTokens != 5930 || out.Usage.InputTokens != 24 {
		t.Fatalf("usage = %#v", out.Usage)
	}
	if out.CreatedAt != 1781809378 {
		t.Fatalf("createdAt = %d", out.CreatedAt)
	}
}

func TestParseOpenAIImagesCodexNonStreamingEmptyData(t *testing.T) {
	s := &OpenAIGatewayService{}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"created":1,"data":[],"usage":{"output_tokens":0}}`))}
	out, err := s.parseOpenAIImagesCodexNonStreamingOutput(resp, c, "gpt-image-2")
	if err != errOpenAIImagesEmptyOutputRetryable {
		t.Fatalf("err = %v, want errOpenAIImagesEmptyOutputRetryable", err)
	}
	if out == nil {
		t.Fatalf("empty-data output must be non-nil to preserve billing usage")
	}
	if out.CreatedAt != 1 {
		t.Fatalf("createdAt = %d, want 1", out.CreatedAt)
	}
	if len(out.UsageRaw) == 0 {
		t.Fatalf("UsageRaw must be set even on empty-data retryable error")
	}
}

func TestOpenAIImagesStreamNGreaterThanOneRejected(t *testing.T) {
	s := &OpenAIGatewayService{}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/v1/images/generations", nil)
	acc := &Account{Type: AccountTypeOAuth}
	parsed := &OpenAIImagesRequest{Endpoint: openAIImagesGenerationsEndpoint, Prompt: "x", Stream: true, N: 2}

	_, err := s.forwardOpenAIImagesOAuth(context.Background(), c, acc, parsed, "")
	var upErr *OpenAIImagesUpstreamError
	if !errors.As(err, &upErr) {
		t.Fatalf("err type = %T (%v)", err, err)
	}
	if upErr.StatusCode != 400 || upErr.Code != "unsupported_parameter" {
		t.Fatalf("upErr = %#v", upErr)
	}
	if IsRetryableOpenAIImagesUpstreamError(upErr) {
		t.Fatalf("must be non-retryable")
	}
	if rec.Code != 400 {
		t.Fatalf("status written = %d", rec.Code)
	}
}
