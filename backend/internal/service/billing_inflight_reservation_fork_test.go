//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// 二开：OpenAI 分组启用按质量计费时，在途预留按 low/medium/high 质量价估算（与
// resolveOpenAIGroupImageBillingDecision 同口径），不再落到计费从不使用的尺寸档价。
func TestInflightEstimate_ForkQualityBillingGroupUsesQualityPrices(t *testing.T) {
	groupID := int64(60)
	low, medium, high, legacy4K := 0.01, 0.05, 0.5, 3.0
	group := &Group{
		ID: groupID, Platform: PlatformOpenAI, RateMultiplier: 1,
		ImageQualityBilling: true,
		ImagePriceLow:       &low, ImagePriceMedium: &medium, ImagePriceHigh: &high,
		// 启用质量计费后前端隐藏、计费不用的遗留尺寸档价，估算也不该用它。
		ImagePrice4K: &legacy4K,
	}
	svc := newInflightEstimateGateway(t, nil)
	apiKey := &APIKey{User: &User{ID: 1}, GroupID: &groupID, Group: group}
	estimate := func(quality string) float64 {
		t.Helper()
		est, priced := svc.EstimateInflightReservation(context.Background(), apiKey,
			InflightEstimateRequest{Model: "gpt-image-2", Kind: InflightEstimateImage, Units: 4, ImageQuality: quality})
		require.True(t, priced)
		return est
	}

	require.InDelta(t, 4*low, estimate("low"), 1e-9, "explicit quality reserves exactly that quality price")
	require.InDelta(t, 4*medium, estimate("Medium"), 1e-9)
	require.InDelta(t, 4*high, estimate("high"), 1e-9)
	require.InDelta(t, 4*high, estimate("xhigh"), 1e-9, "xhigh/max bill as high")
	require.InDelta(t, 4*high, estimate(""), 1e-9, "unspecified quality reserves the highest quality price")
	require.InDelta(t, 4*high, estimate("auto"), 1e-9)

	// 未启用质量计费的分组维持上游口径：尺寸档取最高价。
	group.ImageQualityBilling = false
	require.InDelta(t, 4*legacy4K, estimate("low"), 1e-9)
}
