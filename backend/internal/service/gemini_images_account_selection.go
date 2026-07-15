package service

import (
	"context"
	"fmt"
)

// SelectGeminiAPIKeyAccountForImages 为 OpenAI Images 透传选取 Gemini 平台的
// AI Studio API Key 账号。
// 与 SelectAccountForModelWithExclusions 的差异：
//   - 只接受 Platform==gemini && Type==apikey（OAuth / Vertex SA / Antigravity 混调账号不参与）；
//   - 不使用粘性会话（生图请求无会话语义，与生图调度禁用粘性的策略一致）。
func (s *GeminiMessagesCompatService) SelectGeminiAPIKeyAccountForImages(
	ctx context.Context,
	groupID *int64,
	requestedModel string,
	excludedIDs map[int64]struct{},
) (*Account, error) {
	accounts, err := s.listSchedulableAccountsOnce(ctx, groupID, PlatformGemini, false)
	if err != nil {
		return nil, fmt.Errorf("query accounts failed: %w", err)
	}
	candidates := make([]Account, 0, len(accounts))
	for i := range accounts {
		if accounts[i].Platform != PlatformGemini || accounts[i].Type != AccountTypeAPIKey {
			continue
		}
		candidates = append(candidates, accounts[i])
	}
	selected := s.selectBestGeminiAccount(ctx, candidates, requestedModel, excludedIDs, PlatformGemini, false)
	if selected == nil {
		if requestedModel != "" {
			return nil, fmt.Errorf("%w supporting model: %s", ErrNoAvailableAccounts, requestedModel)
		}
		return nil, ErrNoAvailableAccounts
	}
	return s.hydrateSelectedAccount(ctx, selected)
}
