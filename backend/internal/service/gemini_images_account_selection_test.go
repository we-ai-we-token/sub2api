//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type geminiImagesSelectionAccountRepoStub struct {
	AccountRepository
	accounts []Account
}

func (s *geminiImagesSelectionAccountRepoStub) ListSchedulableByGroupIDAndPlatforms(ctx context.Context, groupID int64, platforms []string) ([]Account, error) {
	return s.accounts, nil
}

func geminiImagesSelectionService(accounts []Account) *GeminiMessagesCompatService {
	return &GeminiMessagesCompatService{
		accountRepo: &geminiImagesSelectionAccountRepoStub{accounts: accounts},
	}
}

func TestSelectGeminiAPIKeyAccountForImagesFiltersTypesAndPlatforms(t *testing.T) {
	groupID := int64(7)
	oauthUsed := time.Now().Add(-time.Hour)
	accounts := []Account{
		{ID: 1, Platform: PlatformGemini, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, LastUsedAt: &oauthUsed},
		{ID: 2, Platform: PlatformAntigravity, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Extra: map[string]any{"mixed_scheduling": true}},
		{ID: 3, Platform: PlatformGemini, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true},
	}
	svc := geminiImagesSelectionService(accounts)

	selected, err := svc.SelectGeminiAPIKeyAccountForImages(context.Background(), &groupID, "gemini-2.5-flash-image", nil)
	require.NoError(t, err)
	require.Equal(t, int64(3), selected.ID)
}

func TestSelectGeminiAPIKeyAccountForImagesHonorsExclusionsAndLRU(t *testing.T) {
	groupID := int64(7)
	older := time.Now().Add(-2 * time.Hour)
	newer := time.Now().Add(-time.Minute)
	accounts := []Account{
		{ID: 11, Platform: PlatformGemini, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, LastUsedAt: &newer},
		{ID: 12, Platform: PlatformGemini, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, LastUsedAt: &older},
	}
	svc := geminiImagesSelectionService(accounts)

	// LRU：先选最久未用的 12
	selected, err := svc.SelectGeminiAPIKeyAccountForImages(context.Background(), &groupID, "", nil)
	require.NoError(t, err)
	require.Equal(t, int64(12), selected.ID)

	// 排除 12 后选 11
	selected, err = svc.SelectGeminiAPIKeyAccountForImages(context.Background(), &groupID, "", map[int64]struct{}{12: {}})
	require.NoError(t, err)
	require.Equal(t, int64(11), selected.ID)

	// 全部排除 → error
	_, err = svc.SelectGeminiAPIKeyAccountForImages(context.Background(), &groupID, "", map[int64]struct{}{11: {}, 12: {}})
	require.Error(t, err)
}

func TestSelectGeminiAPIKeyAccountForImagesNoAccounts(t *testing.T) {
	groupID := int64(7)
	svc := geminiImagesSelectionService(nil)
	_, err := svc.SelectGeminiAPIKeyAccountForImages(context.Background(), &groupID, "gemini-2.5-flash-image", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "gemini-2.5-flash-image")
}
