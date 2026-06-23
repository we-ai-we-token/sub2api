//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeImageQualityOrDefault(t *testing.T) {
	cases := map[string]string{
		"low":     ImageQualityLow,
		"LOW":     ImageQualityLow,
		" high ":  ImageQualityHigh,
		"medium":  ImageQualityMedium,
		"":        ImageQualityMedium,
		"auto":    ImageQualityMedium,
		"unknown": ImageQualityMedium,
	}
	for input, want := range cases {
		require.Equalf(t, want, NormalizeImageQualityOrDefault(input), "input=%q", input)
	}
}

func TestCalculateImageCostByQuality_UsesQualityTierPrice(t *testing.T) {
	svc := newTestBillingService()

	low := 0.05
	medium := 0.10
	high := 0.20
	cfg := &ImageQualityPriceConfig{PriceLow: &low, PriceMedium: &medium, PriceHigh: &high}

	cost := svc.CalculateImageCostByQuality("gpt-image-1", "high", 2, cfg, 1.0)
	require.InDelta(t, high*2, cost.TotalCost, 1e-10)
	require.InDelta(t, high*2, cost.ActualCost, 1e-10)
	require.Equal(t, string(BillingModeImage), cost.BillingMode)
}

func TestCalculateImageCostByQuality_DefaultsToMediumWhenMissing(t *testing.T) {
	svc := newTestBillingService()

	low := 0.05
	medium := 0.10
	high := 0.20
	cfg := &ImageQualityPriceConfig{PriceLow: &low, PriceMedium: &medium, PriceHigh: &high}

	// 空 quality 归一化为 medium。
	cost := svc.CalculateImageCostByQuality("gpt-image-1", "", 1, cfg, 1.0)
	require.InDelta(t, medium, cost.TotalCost, 1e-10)
}

func TestCalculateImageCostByQuality_EmptyTierFallsBackToDefaultImagePrice(t *testing.T) {
	svc := newTestBillingService()

	// low 未配置（nil），应回退到模型默认图片价（1K 基准）。
	cfg := &ImageQualityPriceConfig{}
	fallback := svc.getDefaultImagePrice("gpt-image-1", "1K")

	cost := svc.CalculateImageCostByQuality("gpt-image-1", "low", 3, cfg, 1.0)
	require.InDelta(t, fallback*3, cost.TotalCost, 1e-10)
}

func TestCalculateImageCostByQuality_AppliesRateMultiplier(t *testing.T) {
	svc := newTestBillingService()

	medium := 0.10
	cfg := &ImageQualityPriceConfig{PriceMedium: &medium}

	cost := svc.CalculateImageCostByQuality("gpt-image-1", "medium", 2, cfg, 1.5)
	require.InDelta(t, medium*2, cost.TotalCost, 1e-10)
	require.InDelta(t, medium*2*1.5, cost.ActualCost, 1e-10)
}

func TestCalculateImageCostByQuality_ZeroCount(t *testing.T) {
	svc := newTestBillingService()
	cfg := &ImageQualityPriceConfig{}
	cost := svc.CalculateImageCostByQuality("gpt-image-1", "low", 0, cfg, 1.0)
	require.InDelta(t, 0, cost.TotalCost, 1e-10)
	require.InDelta(t, 0, cost.ActualCost, 1e-10)
}
