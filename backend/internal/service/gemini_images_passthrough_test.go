//go:build unit

package service

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestIsGeminiImageGenerationModel(t *testing.T) {
	cases := []struct {
		model string
		want  bool
	}{
		{"gemini-2.5-flash-image", true},
		{"gemini-3.1-flash-image", true},
		{"Gemini-2.5-Flash-Image-Preview", true},
		{" gemini-2.5-flash-image ", true},
		{"gemini-2.5-flash", false}, // 不含 image
		{"gpt-image-2", false},
		{"grok-imagine", false},
		{"imagen-3", false}, // 无 gemini- 前缀
		{"", false},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, IsGeminiImageGenerationModel(tc.model), "model=%q", tc.model)
	}
}

func TestValidateOpenAIImagesModelAcceptsGeminiImageModels(t *testing.T) {
	require.NoError(t, validateOpenAIImagesModel("gemini-2.5-flash-image"))
	require.Error(t, validateOpenAIImagesModel("gemini-2.5-flash"))
	require.Error(t, validateOpenAIImagesModel("dall-e-3"))
}

func geminiPassthroughAccount() *Account {
	return &Account{
		ID:          91,
		Name:        "gemini-pool",
		Platform:    PlatformGemini,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "pool-key",
			"base_url": "https://gemini-pool.test",
		},
	}
}

func geminiPassthroughTestContext(t *testing.T, body []byte, contentType string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", contentType)
	return c, rec
}

func TestForwardGeminiImagesPassthroughSuccess(t *testing.T) {
	body := []byte(`{"model":"gpt-image-1","prompt":"draw a cat","response_format":"url"}`)
	c, rec := geminiPassthroughTestContext(t, body, "application/json")
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"created":1,"data":[{"url":"https://img.test/1.png"}],"usage":{"input_tokens":8,"output_tokens":1290}}`)),
	}}}
	upstream := svc.httpUpstream.(*httpUpstreamRecorder)

	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)

	result, err := svc.ForwardGeminiImagesPassthrough(context.Background(), c, geminiPassthroughAccount(), body, parsed, "gemini-2.5-flash-image")
	require.NoError(t, err)

	// 上游侧：URL / 认证 / model 改写
	require.Equal(t, "https://gemini-pool.test/v1/images/generations", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer pool-key", upstream.lastReq.Header.Get("Authorization"))
	require.JSONEq(t, `{"model":"gemini-2.5-flash-image","prompt":"draw a cat","response_format":"url"}`, string(upstream.lastBody))

	// 客户端侧：响应原样透传（url 字段保留）
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "https://img.test/1.png")

	// 计费字段
	require.Equal(t, "gemini-2.5-flash-image", result.UpstreamModel)
	require.Equal(t, 1, result.ImageCount)
	require.Equal(t, 8, result.Usage.InputTokens)
	require.Equal(t, 1290, result.Usage.OutputTokens)
}

func TestForwardGeminiImagesPassthroughRejectsNonGeminiMappedModel(t *testing.T) {
	body := []byte(`{"model":"gpt-image-1","prompt":"draw"}`)
	c, _ := geminiPassthroughTestContext(t, body, "application/json")
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: &httpUpstreamRecorder{}}
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)

	// 未配置映射：channelMappedModel == 请求模型 gpt-image-1，不是 gemini 生图模型
	_, err = svc.ForwardGeminiImagesPassthrough(context.Background(), c, geminiPassthroughAccount(), body, parsed, "gpt-image-1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "gemini image model")
}

func TestForwardGeminiImagesPassthroughMissingBaseURLFailsOver(t *testing.T) {
	body := []byte(`{"model":"gemini-2.5-flash-image","prompt":"draw"}`)
	c, _ := geminiPassthroughTestContext(t, body, "application/json")
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: &httpUpstreamRecorder{}}
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)

	account := geminiPassthroughAccount()
	account.Credentials = map[string]any{"api_key": "pool-key"} // 无 base_url

	_, err = svc.ForwardGeminiImagesPassthrough(context.Background(), c, account, body, parsed, "")
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
}

func TestForwardGeminiImagesPassthrough429ReturnsFailover(t *testing.T) {
	body := []byte(`{"model":"gemini-2.5-flash-image","prompt":"draw"}`)
	c, rec := geminiPassthroughTestContext(t, body, "application/json")
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"rate limited"}}`)),
	}}}
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)

	_, err = svc.ForwardGeminiImagesPassthrough(context.Background(), c, geminiPassthroughAccount(), body, parsed, "")
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.Equal(t, http.StatusTooManyRequests, failover.StatusCode)
	require.Zero(t, rec.Body.Len(), "failover 错误不应写客户端响应")
}

func TestForwardGeminiImagesPassthroughUserErrorWritesThrough(t *testing.T) {
	body := []byte(`{"model":"gemini-2.5-flash-image","prompt":"draw"}`)
	c, rec := geminiPassthroughTestContext(t, body, "application/json")
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"bad prompt","type":"invalid_request_error"}}`)),
	}}}
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)

	_, err = svc.ForwardGeminiImagesPassthrough(context.Background(), c, geminiPassthroughAccount(), body, parsed, "")
	var imgErr *OpenAIImagesUpstreamError
	require.ErrorAs(t, err, &imgErr)
	require.Equal(t, http.StatusBadRequest, imgErr.StatusCode)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "bad prompt")
}

func TestForwardGeminiImagesPassthroughMultipartEditsRewritesModel(t *testing.T) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf) // import "mime/multipart"
	require.NoError(t, writer.WriteField("model", "gpt-image-1"))
	require.NoError(t, writer.WriteField("prompt", "make it blue"))
	part, err := writer.CreateFormFile("image", "in.png")
	require.NoError(t, err)
	_, err = part.Write([]byte("\x89PNG fake"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	body := buf.Bytes()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())

	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"created":1,"data":[{"b64_json":"aGk="}]}`)),
	}}}
	upstream := svc.httpUpstream.(*httpUpstreamRecorder)

	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)

	result, err := svc.ForwardGeminiImagesPassthrough(context.Background(), c, geminiPassthroughAccount(), body, parsed, "gemini-2.5-flash-image")
	require.NoError(t, err)
	require.Equal(t, "https://gemini-pool.test/v1/images/edits", upstream.lastReq.URL.String())
	require.Contains(t, upstream.lastReq.Header.Get("Content-Type"), "multipart/form-data")
	require.Contains(t, string(upstream.lastBody), "gemini-2.5-flash-image")
	require.NotContains(t, string(upstream.lastBody), "gpt-image-1")
	require.Contains(t, string(upstream.lastBody), "make it blue")
	require.Equal(t, 1, result.ImageCount)
}
