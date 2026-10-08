//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// 二开：账号 model_mapping 把生图模型映射成上游别名（如 new-api 的 "[A.s]nano-banana-pro"）时，
// 透传不能再按映射后的名字拒掉，而是带着别名发给上游。
func TestForwardGeminiImagesPassthroughTrustsAccountMappingAlias(t *testing.T) {
	body := []byte(`{"model":"gemini-3-pro-image-preview","prompt":"draw a cat"}`)
	c, _ := geminiPassthroughTestContext(t, body, "application/json")
	up := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"created":1,"data":[{"b64_json":"aGVsbG8="}]}`)),
	}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: up}
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)

	account := geminiPassthroughAccount()
	account.Credentials["model_mapping"] = map[string]any{"gemini-3-pro-image-preview": "[A.s]nano-banana-pro"}

	result, err := svc.ForwardGeminiImagesPassthrough(context.Background(), c, account, body, parsed, "gemini-3-pro-image-preview")
	require.NoError(t, err)
	require.JSONEq(t, `{"model":"[A.s]nano-banana-pro","prompt":"draw a cat"}`, string(up.lastBody))
	require.Equal(t, "[A.s]nano-banana-pro", result.UpstreamModel)
}

// 映射前的名字仍然必须是生图模型：非生图模型照旧在发上游之前被拒。
func TestForwardGeminiImagesPassthroughStillRejectsNonImageRequestModel(t *testing.T) {
	body := []byte(`{"model":"gemini-3-pro-image-preview","prompt":"draw a cat"}`)
	c, _ := geminiPassthroughTestContext(t, body, "application/json")
	up := &httpUpstreamRecorder{}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: up}
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)

	_, err = svc.ForwardGeminiImagesPassthrough(context.Background(), c, geminiPassthroughAccount(), body, parsed, "gemini-2.5-flash")
	require.ErrorContains(t, err, "requires a gemini image model")
	require.Zero(t, up.calls)
}
