//go:build unit

package handler

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// openAIImagesFailoverAccountRepo 是 failover 相关测试共用的 AccountRepository mock。
// 原随上游 openai_images_failover_test.go 提供；本 fork 删除了该文件中的图像 failover
// 用例(与二开图像逻辑不符),但上游 openai_responses_failover_cancel_test.go 仍复用此
// 帮助类型,故单独抽出保留。
type openAIImagesFailoverAccountRepo struct {
	service.AccountRepository
	accounts []service.Account
}

func (r openAIImagesFailoverAccountRepo) GetByID(_ context.Context, id int64) (*service.Account, error) {
	for i := range r.accounts {
		if r.accounts[i].ID == id {
			account := r.accounts[i]
			return &account, nil
		}
	}
	return nil, service.ErrNoAvailableAccounts
}

func (r openAIImagesFailoverAccountRepo) ListSchedulableByGroupIDAndPlatform(_ context.Context, _ int64, platform string) ([]service.Account, error) {
	return r.accountsForPlatform(platform), nil
}

func (r openAIImagesFailoverAccountRepo) ListSchedulableByPlatform(_ context.Context, platform string) ([]service.Account, error) {
	return r.accountsForPlatform(platform), nil
}

func (r openAIImagesFailoverAccountRepo) ListSchedulableUngroupedByPlatform(_ context.Context, platform string) ([]service.Account, error) {
	return r.accountsForPlatform(platform), nil
}

func (r openAIImagesFailoverAccountRepo) accountsForPlatform(platform string) []service.Account {
	out := make([]service.Account, 0, len(r.accounts))
	for _, account := range r.accounts {
		if account.Platform == platform {
			out = append(out, account)
		}
	}
	return out
}
