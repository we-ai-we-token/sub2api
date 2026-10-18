//go:build unit

package repository

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// 二开：Gemini 本地配额预检读的是调度快照里的 meta 账号，tier_id 必须保留，
// 否则所有 Gemini 账号在 OpenAI Images 选号时都被当成 aistudio_free。
func TestSchedulerMetadataKeepsGeminiTierID(t *testing.T) {
	acc := service.Account{
		ID: 1, Platform: service.PlatformGemini, Type: service.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "k", "tier_id": "aistudio_paid", "unprojected_sentinel": "v"},
	}
	meta := buildSchedulerMetadataAccount(acc)
	require.Equal(t, "aistudio_paid", meta.GeminiTierID())
	// 上游 v0.2.17 起 base_url 已进投影白名单（Gemini 显式缓存候选过滤要用），
	// 这里改用一个未投影的哨兵键，确认这条测试真的走过过滤、而不是原样拷贝了凭据
	require.Empty(t, meta.GetCredential("unprojected_sentinel"))
}
