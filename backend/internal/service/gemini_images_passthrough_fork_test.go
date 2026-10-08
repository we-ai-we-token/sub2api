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

func TestGeminiImagesNameGatesAcceptNanoBanana21Only(t *testing.T) {
	// 解析闸（客户请求名）
	require.NoError(t, validateCompatibleImagesModel("gemini-nano-banana-2.1"))
	require.NoError(t, validateCompatibleImagesModel(" Gemini-Nano-Banana-2.1 "))
	require.Error(t, validateCompatibleImagesModel("gemini-nano-banana-pro"))
	require.Error(t, validateCompatibleImagesModel("nano-banana"))
	require.Error(t, validateCompatibleImagesModel("gemini-nano-banana-2.1x"))
	// 透传判据（渠道映射后 / 账号映射后）
	require.True(t, IsGeminiImagesPassthroughModel("gemini-nano-banana-2.1"))
	require.True(t, IsGeminiImagesPassthroughModel("gemini-3-pro-image-preview"))
	require.False(t, IsGeminiImagesPassthroughModel("gemini-nano-banana-pro"))
	require.False(t, IsGeminiImagesPassthroughModel("gpt-image-1"))
	// 原生口径不变：原生生图记录 / 在途预留仍按旧判据
	require.False(t, IsGeminiImageGenerationModel("gemini-nano-banana-2.1"))
}

func TestForwardGeminiImagesPassthroughNanoBanana21(t *testing.T) {
	body := []byte(`{"model":"gemini-nano-banana-2.1","prompt":"draw a cat","response_format":"b64_json"}`)
	c, rec := geminiPassthroughTestContext(t, body, "application/json")
	up := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"created":1,"data":[{"b64_json":"aGVsbG8="}]}`)),
	}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: up}
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)

	result, err := svc.ForwardGeminiImagesPassthrough(context.Background(), c, geminiPassthroughAccount(), body, parsed, "gemini-nano-banana-2.1")
	require.NoError(t, err)
	require.JSONEq(t, `{"model":"gemini-nano-banana-2.1","prompt":"draw a cat","response_format":"b64_json"}`, string(up.lastBody))
	require.Equal(t, "gemini-nano-banana-2.1", result.UpstreamModel)
	require.Equal(t, 1, result.ImageCount)
	require.Equal(t, http.StatusOK, rec.Code)
}
