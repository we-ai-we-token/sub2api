//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 分组开启 quality 计费时，应按 quality 单价计费，而非分辨率（1K/2K/4K）。
func TestOpenAIGatewayServiceRecordUsage_QualityBilling(t *testing.T) {
	price1K := 0.10  // 分辨率价（不应被使用）
	priceHigh := 0.5 // quality=high 价
	groupID := int64(900)

	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{
			RequestID:    "resp_quality_high",
			Model:        "gpt-image-2",
			ImageCount:   1,
			ImageSize:    "1K",
			ImageQuality: "high",
			Duration:     time.Second,
		},
		APIKey: &APIKey{
			ID:      9001,
			GroupID: i64p(groupID),
			Group: &Group{
				ID:                  groupID,
				RateMultiplier:      1.0,
				ImageQualityBilling: true,
				ImagePrice1K:        &price1K,
				ImagePriceHigh:      &priceHigh,
			},
		},
		User:    &User{ID: 9002},
		Account: &Account{ID: 9003},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, string(BillingModeImage), *usageRepo.lastLog.BillingMode)
	// 必须按 high 价（0.5），不能按 1K 分辨率价（0.10）。
	require.InDelta(t, priceHigh, usageRepo.lastLog.TotalCost, 1e-12)
	require.InDelta(t, priceHigh, usageRepo.lastLog.ActualCost, 1e-12)
	require.NotNil(t, usageRepo.lastLog.ImageQuality)
	require.Equal(t, "high", *usageRepo.lastLog.ImageQuality)
}
