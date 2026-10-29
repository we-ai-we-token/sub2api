//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 上游 v0.2.19 的分组「按张计费」只对 ImageEndpoint=true 的结果生效
// （imageCountBillingApplies）。二开独有的生图链路自己构造 OpenAIForwardResult，
// 漏标 ImageEndpoint 时编译和计费都不报错，只是开关静默失效，这里在转发结果上兜底。

func TestForkCodexImagesPathMarksImageEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	// ImageUseResponsesAPI=false：走二开专用 codex images 端点链路。
	c.Set("api_key", &APIKey{Group: &Group{ImageUseResponsesAPI: false, ImageCountBilling: true}})

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"created":1,"data":[{"b64_json":"QUJD"}],"size":"1024x1024","usage":{"input_tokens":8,"output_tokens":1290}}`)),
	}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	account := &Account{
		ID:          74,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "oauth-token"},
	}
	parsed := &OpenAIImagesRequest{
		Endpoint:       openAIImagesGenerationsEndpoint,
		Model:          "gpt-image-2",
		Prompt:         "draw a square",
		N:              1,
		ResponseFormat: "b64_json",
	}

	result, err := svc.ForwardImages(context.Background(), c, account, nil, parsed, "")

	require.NoError(t, err)
	require.Equal(t, chatgptCodexImagesGenerationsURL, upstream.lastReq.URL.String())
	require.True(t, result.ImageEndpoint)
	require.Equal(t, 1, result.ImageCount)
}

func TestForkGeminiImagesPassthroughMarksImageEndpoint(t *testing.T) {
	body := []byte(`{"model":"gpt-image-1","prompt":"draw a cat"}`)
	c, _ := geminiPassthroughTestContext(t, body, "application/json")
	c.Set("api_key", &APIKey{Group: &Group{ImageCountBilling: true}})
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"created":1,"data":[{"b64_json":"QUJD"},{"b64_json":"REVG"}],"usage":{"input_tokens":8,"output_tokens":1290}}`)),
	}}}

	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)

	result, err := svc.ForwardGeminiImagesPassthrough(context.Background(), c, geminiPassthroughAccount(), body, parsed, "gemini-2.5-flash-image")

	require.NoError(t, err)
	require.True(t, result.ImageEndpoint)
	// 按上游实际返回的张数计费，不回退到请求 n。
	require.Equal(t, 2, result.ImageCount)
}
