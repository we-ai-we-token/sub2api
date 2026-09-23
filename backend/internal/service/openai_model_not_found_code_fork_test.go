//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// 二开给 extractUpstreamErrorCode 加了顶层 code 兜底；中转常把 HTTP 状态码镜像进顶层 code，
// 模型不存在判定若按它一票否决，v0.2.8 新增的「401 模型不存在走模型级冷却」会落空。
func TestIsOpenAICompatibleModelNotFoundBodyIgnoresMirroredTopLevelCode(t *testing.T) {
	for _, tc := range []struct {
		body string
		want bool
	}{
		{`{"code":401,"message":"unknown model x"}`, true},
		{`{"code":"invalid_model","message":"model not found"}`, true},
		{`{"error":{"message":"unknown model x"},"code":401}`, true},
		{`{"code":"model_not_found","message":"No such deployment"}`, true},
		{`{"error":{"code":"model_not_found","message":"x"}}`, true},
		{`{"code":401,"message":"Incorrect API key provided"}`, false},
		// 结构化的嵌套 code 仍按上游规则一票否决。
		{`{"error":{"code":"invalid_api_key","message":"unknown model x"}}`, false},
	} {
		require.Equal(t, tc.want, isOpenAICompatibleModelNotFoundBody([]byte(tc.body)), tc.body)
	}
}

func TestRateLimitService_HandleUpstreamError_APIKeyModel401FlatBodyUsesModelRateLimit(t *testing.T) {
	repo := &modelNotFoundAccountRepoStub{}
	svc := &RateLimitService{accountRepo: repo}
	account := openAIModelNotFoundTempAccount()

	handled := svc.HandleUpstreamError(
		context.Background(),
		account,
		http.StatusUnauthorized,
		http.Header{},
		[]byte(`{"code":401,"message":"unknown model definitely-not-real"}`),
		"definitely-not-real",
	)

	require.True(t, handled)
	require.Zero(t, repo.tempCalls)
	require.Len(t, repo.modelRateLimitCalls, 1)
	require.Equal(t, upstreamModelNotFound401Reason, repo.modelRateLimitCalls[0].reason)
}
