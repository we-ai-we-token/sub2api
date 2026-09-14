package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// gpt-image-2.5-flare / gpt-image-2.5-sunburst 新增 xhigh / max 两档质量。
// 这些质量要被如实记录，但因为没有独立价格列，计费一律收敛到 high。
// 独立成文件，不跟上游抢 image_billing_size 相关测试文件。

func TestNormalizeOpenAIImageQualityAcceptsXHighAndMax(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"low", "low"},
		{"medium", "medium"},
		{"high", "high"},
		{"xhigh", "xhigh"},
		{"max", "max"},
		{"  XHIGH  ", "xhigh"},
		{"Max", "max"},
		{"", ""},
		{"ultra", ""},
	} {
		require.Equal(t, tc.want, NormalizeOpenAIImageQualityOrEmpty(tc.in), "input %q", tc.in)
	}
}

// 计费收敛：xhigh/max -> high；其余原样透传（含 1K/2K/4K 尺寸档）。
func TestImageQualityBillingTierCollapsesToHigh(t *testing.T) {
	require.Equal(t, OpenAIImageQualityHigh, ImageQualityBillingTier("xhigh"))
	require.Equal(t, OpenAIImageQualityHigh, ImageQualityBillingTier("max"))
	require.Equal(t, OpenAIImageQualityHigh, ImageQualityBillingTier("XHIGH"))

	require.Equal(t, OpenAIImageQualityLow, ImageQualityBillingTier("low"))
	require.Equal(t, OpenAIImageQualityMedium, ImageQualityBillingTier("medium"))
	require.Equal(t, OpenAIImageQualityHigh, ImageQualityBillingTier("high"))
	require.Equal(t, "2K", ImageQualityBillingTier("2K"))
	require.Equal(t, "", ImageQualityBillingTier(""))
}

// 关键行为：开启质量计费的分组，记录值保留 xhigh/max，计费档是 high。
// 两者若都取同一个值，要么记录不准，要么查价落空掉到固定默认单价。
func TestImageBillingDecisionRecordsTrueQualityButBillsAsHigh(t *testing.T) {
	apiKey := &APIKey{Group: &Group{Platform: PlatformOpenAI, ImageQualityBilling: true}}

	for _, quality := range []string{"xhigh", "max"} {
		result := &OpenAIForwardResult{ImageSize: "2048x2048"}
		result.Usage.Quality = quality

		decision := resolveOpenAIGroupImageBillingDecision(apiKey, result)
		require.Equal(t, quality, decision.Quality, "记录值必须保留真实质量 %q", quality)
		require.Equal(t, OpenAIImageQualityHigh, decision.BillingTier, "计费档必须收敛到 high")
	}
}

// 未开启质量计费的分组（DB 默认）不受影响：计费仍按尺寸档，只是记录值变准了。
func TestImageBillingDecisionWithoutQualityBillingKeepsSizeTier(t *testing.T) {
	apiKey := &APIKey{Group: &Group{Platform: PlatformOpenAI, ImageQualityBilling: false}}
	result := &OpenAIForwardResult{ImageSize: "2048x2048"}
	result.Usage.Quality = "xhigh"

	decision := resolveOpenAIGroupImageBillingDecision(apiKey, result)
	require.Equal(t, "xhigh", decision.Quality)
	require.Equal(t, decision.SizeTier, decision.BillingTier, "未开质量计费时计费档应仍是尺寸档")
}

// 分组价格查询不得让 xhigh/max 落到「未知尺寸按 2K」的默认分支。
func TestGroupGetImagePriceMapsXHighAndMaxToHigh(t *testing.T) {
	high := 0.30
	twoK := 0.05
	g := &Group{ImagePriceHigh: &high, ImagePrice2K: &twoK}

	require.Equal(t, &high, g.GetImagePrice("xhigh"))
	require.Equal(t, &high, g.GetImagePrice("max"))
	require.Equal(t, &high, g.GetImagePrice("high"))
	require.Equal(t, &twoK, g.GetImagePrice("2K"))
}
