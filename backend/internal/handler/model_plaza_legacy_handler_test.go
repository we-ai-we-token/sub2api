//go:build unit

package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func floatPtr(v float64) *float64 { return &v }

func TestModelPlaza_Unauthenticated401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &ModelPlazaLegacyHandler{} // nil services — 401 路径不触达依赖
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/model-plaza/models", nil)

	h.Models(c)

	require.Equal(t, http.StatusUnauthorized, w.Code)
}

// 通配符按次定价 + 账号实际模型 → 展开成具体模型行（仅命中前缀的、且平台一致的）。
func TestExpandWildcardModels_PrefixMatch(t *testing.T) {
	group := &service.Group{ID: 1, Platform: "gemini"}
	price := service.ChannelModelPricing{
		Platform:        "gemini",
		Models:          []string{"gemini-3.1-flash-image_*"},
		BillingMode:     service.BillingModePerRequest,
		PerRequestPrice: floatPtr(0.1),
	}
	ch := service.AvailableChannel{
		Status:       service.StatusActive,
		Groups:       []service.AvailableGroupRef{{ID: 1}},
		ModelPricing: []service.ChannelModelPricing{price},
	}
	accountModels := []string{
		"gemini-3.1-flash-image_3-4_1k",
		"gemini-3.1-flash-image_3-4_2k",
		"claude-sonnet-4", // 前缀不匹配，应排除
	}

	got := expandWildcardModelsLegacy([]service.AvailableChannel{ch}, group, accountModels)

	require.Len(t, got, 2)
	names := map[string]bool{}
	for _, m := range got {
		names[m.Name] = true
		require.Equal(t, "gemini", m.Platform)
		require.NotNil(t, m.Pricing)
		require.NotNil(t, m.Pricing.PerRequestPrice)
		require.InDelta(t, 0.1, *m.Pricing.PerRequestPrice, 1e-9)
	}
	require.True(t, names["gemini-3.1-flash-image_3-4_1k"])
	require.True(t, names["gemini-3.1-flash-image_3-4_2k"])
	require.False(t, names["claude-sonnet-4"])
}

// 平台不一致时不展开（防止跨平台信息泄漏）。
func TestExpandWildcardModels_PlatformMismatch(t *testing.T) {
	group := &service.Group{ID: 1, Platform: "openai"}
	price := service.ChannelModelPricing{
		Platform:        "gemini",
		Models:          []string{"gemini-3.1-flash-image_*"},
		BillingMode:     service.BillingModePerRequest,
		PerRequestPrice: floatPtr(0.1),
	}
	ch := service.AvailableChannel{
		Status:       service.StatusActive,
		Groups:       []service.AvailableGroupRef{{ID: 1}},
		ModelPricing: []service.ChannelModelPricing{price},
	}

	got := expandWildcardModelsLegacy([]service.AvailableChannel{ch}, group,
		[]string{"gemini-3.1-flash-image_3-4_1k"})

	require.Empty(t, got)
}

// 无账号模型时不展开。
func TestExpandWildcardModels_NoAccountModels(t *testing.T) {
	group := &service.Group{ID: 1, Platform: "gemini"}
	ch := service.AvailableChannel{
		Status: service.StatusActive,
		Groups: []service.AvailableGroupRef{{ID: 1}},
		ModelPricing: []service.ChannelModelPricing{{
			Platform:        "gemini",
			Models:          []string{"gemini-3.1-flash-image_*"},
			BillingMode:     service.BillingModePerRequest,
			PerRequestPrice: floatPtr(0.1),
		}},
	}

	require.Empty(t, expandWildcardModelsLegacy([]service.AvailableChannel{ch}, group, nil))
}

// 线上场景：渠道 flat 按次价 $0.1 + 分组生图价 0.12 → 各档位取渠道 flat $0.1，
// 与真实计费一致（不能跳过渠道 flat 价直接用分组价）。
func TestResolveImageTier_ChannelFlatBeatsGroup(t *testing.T) {
	group := &service.Group{
		ID: 1, Platform: "gemini",
		ImagePrice1K: floatPtr(0.12),
		ImagePrice2K: floatPtr(0.12),
		ImagePrice4K: floatPtr(0.12),
	}
	p := &service.ChannelModelPricing{
		Platform:        "gemini",
		BillingMode:     service.BillingModePerRequest,
		PerRequestPrice: floatPtr(0.1), // flat，无档位区间
	}
	for _, tier := range []string{"1K", "2K", "4K"} {
		got := resolveImageTierLegacy(p, group, tier)
		require.NotNil(t, got)
		require.InDelta(t, 0.1, *got, 1e-9, "档位 %s 应取渠道 flat 0.1，而非分组 0.12", tier)
	}
}

// 渠道完全没有按次价时，才回落分组生图定价。
func TestResolveImageTier_GroupFallbackWhenNoChannelPrice(t *testing.T) {
	group := &service.Group{ID: 1, Platform: "gemini", ImagePrice2K: floatPtr(0.12)}
	p := &service.ChannelModelPricing{Platform: "gemini", BillingMode: service.BillingModeImage}
	got := resolveImageTierLegacy(p, group, "2K")
	require.NotNil(t, got)
	require.InDelta(t, 0.12, *got, 1e-9)
}

// 渠道档位区间优先于 flat 价；未配区间的档位回落 flat 价。
func TestResolveImageTier_TierIntervalBeatsFlat(t *testing.T) {
	group := &service.Group{ID: 1, Platform: "gemini"}
	p := &service.ChannelModelPricing{
		Platform:        "gemini",
		BillingMode:     service.BillingModePerRequest,
		PerRequestPrice: floatPtr(0.1),
		Intervals: []service.PricingInterval{
			{TierLabel: "4K", PerRequestPrice: floatPtr(0.3)},
		},
	}
	require.InDelta(t, 0.3, *resolveImageTierLegacy(p, group, "4K"), 1e-9) // 区间命中
	require.InDelta(t, 0.1, *resolveImageTierLegacy(p, group, "1K"), 1e-9) // 回落 flat
}

// 端到端：buildModelPlazaLegacyModels 把展开出的具体模型作为按次行返回（原价，未折算）。
func TestBuildModelPlazaModels_WildcardExpandedAsPerRequest(t *testing.T) {
	group := &service.Group{ID: 1, Platform: "gemini"}
	ch := service.AvailableChannel{
		Status: service.StatusActive,
		Groups: []service.AvailableGroupRef{{ID: 1}},
		ModelPricing: []service.ChannelModelPricing{{
			Platform:        "gemini",
			Models:          []string{"gemini-3.1-flash-image_*"},
			BillingMode:     service.BillingModePerRequest,
			PerRequestPrice: floatPtr(0.1),
		}},
		// SupportedModels 已剔除通配符，这里为空：完全依赖账号模型展开。
		SupportedModels: nil,
	}
	accountModels := []string{"gemini-3.1-flash-image_3-4_1k"}

	models := buildModelPlazaLegacyModels([]service.AvailableChannel{ch}, group, accountModels, nil)

	require.Len(t, models, 1)
	require.Equal(t, "gemini-3.1-flash-image_3-4_1k", models[0].Name)
	require.Equal(t, string(service.BillingModePerRequest), models[0].BillingMode)
	require.NotNil(t, models[0].PerRequestPrice)
	require.InDelta(t, 0.1, *models[0].PerRequestPrice, 1e-9)
}

// 线上场景：文本分组，账号只有 gpt-5.5 / gpt-5.4 两个 token 模型，无渠道定价。
// 广场用 sub2api 内置定价填充，按 token 列展示（输入/输出/缓存读 等，每百万 token）。
func TestBuildModelPlazaModels_TokenModelsFromBuiltinPricing(t *testing.T) {
	group := &service.Group{ID: 1, Platform: "openai"} // 文本分组，无生图价
	builtin := map[string]*service.ChannelModelPricing{
		"gpt-5.5": {
			BillingMode:    service.BillingModeToken,
			InputPrice:     floatPtr(5e-06),
			OutputPrice:    floatPtr(3e-05),
			CacheReadPrice: floatPtr(5e-07),
		},
		"gpt-5.4": {
			BillingMode:    service.BillingModeToken,
			InputPrice:     floatPtr(2.5e-06),
			OutputPrice:    floatPtr(1.5e-05),
			CacheReadPrice: floatPtr(2.5e-07),
		},
	}
	pricingFor := func(model string) *service.ChannelModelPricing { return builtin[model] }

	models := buildModelPlazaLegacyModels(nil, group, []string{"gpt-5.5", "gpt-5.4"}, pricingFor)

	byName := map[string]modelPlazaLegacyModel{}
	for _, m := range models {
		byName[m.Name] = m
	}
	require.Len(t, models, 2)

	m55 := byName["gpt-5.5"]
	require.Equal(t, string(service.BillingModeToken), m55.BillingMode)
	require.Nil(t, m55.ImageTiers) // token 模型不渲染三档
	require.NotNil(t, m55.InputPrice)
	require.InDelta(t, 5e-06, *m55.InputPrice, 1e-12)  // 每百万 → 前端 ×1e6 = $5
	require.InDelta(t, 3e-05, *m55.OutputPrice, 1e-12) // $30 / 1M
	require.InDelta(t, 5e-07, *m55.CacheReadPrice, 1e-12)
	require.Nil(t, m55.CacheWritePrice) // 无内置缓存写价 → 前端显示 "-"

	m54 := byName["gpt-5.4"]
	require.Equal(t, string(service.BillingModeToken), m54.BillingMode)
	require.InDelta(t, 2.5e-06, *m54.InputPrice, 1e-12)
}

// 线上场景：生图分组只有一个账号模型 gpt-image-2，无渠道定价（甚至无渠道），
// 分组三档都设 0.08 → 广场显示 1 个模型，三档均 0.08（与计费一致）。
func TestBuildModelPlazaModels_SingleModelGroupPriceOnly(t *testing.T) {
	group := &service.Group{
		ID: 1, Platform: "openai",
		ImagePrice1K: floatPtr(0.08),
		ImagePrice2K: floatPtr(0.08),
		ImagePrice4K: floatPtr(0.08),
	}
	// gpt-image-2 内置 image 模式、无 flat per-image 价 → 走分组分辨率价。
	pricingFor := func(model string) *service.ChannelModelPricing {
		return &service.ChannelModelPricing{BillingMode: service.BillingModeImage, Platform: "openai"}
	}

	models := buildModelPlazaLegacyModels(nil, group, []string{"gpt-image-2"}, pricingFor)

	require.Len(t, models, 1)
	m := models[0]
	require.Equal(t, "gpt-image-2", m.Name)
	require.Equal(t, string(service.BillingModeImage), m.BillingMode)
	require.NotNil(t, m.ImageTiers)
	require.InDelta(t, 0.08, *m.ImageTiers.Price1K, 1e-9)
	require.InDelta(t, 0.08, *m.ImageTiers.Price2K, 1e-9)
	require.InDelta(t, 0.08, *m.ImageTiers.Price4K, 1e-9)
}

// OpenAI 分组开启质量计费时，「渠道未配价」的生图模型（gpt-image-2）回落分组质量价，
// 按 low/medium/high 展示，尺寸档 1K/2K/4K 为空。
func TestBuildModelPlazaModels_QualityBilling(t *testing.T) {
	group := &service.Group{
		ID: 1, Platform: "openai",
		ImageQualityBilling: true,
		ImagePriceLow:       floatPtr(0.03),
		ImagePriceMedium:    floatPtr(0.05),
		ImagePriceHigh:      floatPtr(0.1),
	}
	pricingFor := func(model string) *service.ChannelModelPricing {
		return &service.ChannelModelPricing{BillingMode: service.BillingModeImage, Platform: "openai"}
	}

	models := buildModelPlazaLegacyModels(nil, group, []string{"gpt-image-2"}, pricingFor)

	require.Len(t, models, 1)
	m := models[0]
	require.Equal(t, "gpt-image-2", m.Name)
	require.Equal(t, string(service.BillingModeImage), m.BillingMode)
	require.NotNil(t, m.ImageTiers)
	require.InDelta(t, 0.03, *m.ImageTiers.PriceLow, 1e-9)
	require.InDelta(t, 0.05, *m.ImageTiers.PriceMedium, 1e-9)
	require.InDelta(t, 0.1, *m.ImageTiers.PriceHigh, 1e-9)
	// 质量回落档下不填尺寸档。
	require.Nil(t, m.ImageTiers.Price1K)
	require.Nil(t, m.ImageTiers.Price2K)
	require.Nil(t, m.ImageTiers.Price4K)
}

// 质量计费分组里，「渠道配了按次价」的模型（gpt-image-2-low/-medium/-high）计费走渠道价，
// 仍按 1K/2K/4K 展示（三档同为该渠道 flat 价）；不回落成质量档。
// 与「渠道未配价的 gpt-image-2 显示 low/medium/high」形成对照——同组内逐模型区分。
func TestBuildModelPlazaModels_QualityBilling_ChannelPricedShowsSizeTiers(t *testing.T) {
	group := &service.Group{
		ID: 1, Platform: "openai",
		ImageQualityBilling: true,
		ImagePriceLow:       floatPtr(0.03),
		ImagePriceMedium:    floatPtr(0.05),
		ImagePriceHigh:      floatPtr(0.1),
	}
	mk := func(model string, price float64) service.SupportedModel {
		return service.SupportedModel{
			Name:     model,
			Platform: "openai",
			Pricing: &service.ChannelModelPricing{
				Platform:        "openai",
				Models:          []string{model},
				BillingMode:     service.BillingModePerRequest,
				PerRequestPrice: floatPtr(price),
			},
		}
	}
	ch := service.AvailableChannel{
		Status: service.StatusActive,
		Groups: []service.AvailableGroupRef{{ID: 1}},
		SupportedModels: []service.SupportedModel{
			mk("gpt-image-2-low", 0.02),
			mk("gpt-image-2-medium", 0.04),
			mk("gpt-image-2-high", 0.08),
		},
	}
	// gpt-image-2 无渠道价 → 回落分组质量价 → low/medium/high。
	pricingFor := func(model string) *service.ChannelModelPricing {
		return &service.ChannelModelPricing{BillingMode: service.BillingModeImage, Platform: "openai"}
	}

	models := buildModelPlazaLegacyModels(
		[]service.AvailableChannel{ch}, group, []string{"gpt-image-2"}, pricingFor,
	)

	byName := map[string]modelPlazaLegacyModel{}
	for _, m := range models {
		byName[m.Name] = m
	}

	// 渠道配价的三个模型：展示 1K/2K/4K（同为 flat 渠道价），质量档为空。
	for name, price := range map[string]float64{
		"gpt-image-2-low": 0.02, "gpt-image-2-medium": 0.04, "gpt-image-2-high": 0.08,
	} {
		m, ok := byName[name]
		require.True(t, ok, name)
		require.NotNil(t, m.ImageTiers, name)
		require.InDelta(t, price, *m.ImageTiers.Price1K, 1e-9, name)
		require.InDelta(t, price, *m.ImageTiers.Price2K, 1e-9, name)
		require.InDelta(t, price, *m.ImageTiers.Price4K, 1e-9, name)
		require.Nil(t, m.ImageTiers.PriceLow, name)
		require.Nil(t, m.ImageTiers.PriceMedium, name)
		require.Nil(t, m.ImageTiers.PriceHigh, name)
	}

	// 无渠道价的 gpt-image-2：回落分组质量价 → low/medium/high，尺寸档为空。
	base := byName["gpt-image-2"]
	require.NotNil(t, base.ImageTiers)
	require.InDelta(t, 0.03, *base.ImageTiers.PriceLow, 1e-9)
	require.InDelta(t, 0.05, *base.ImageTiers.PriceMedium, 1e-9)
	require.InDelta(t, 0.1, *base.ImageTiers.PriceHigh, 1e-9)
	require.Nil(t, base.ImageTiers.Price1K)
}

// 渠道只给 low/medium/high 配了 flat 按次价（0.03/0.05/0.1），gpt-image-2 无渠道定价，
// 计费走分组分辨率价（1K0.06/2K0.08/4K0.12）。广场应显示全部 4 个，价格与计费一致。
func TestBuildModelPlazaModels_FourImageModels(t *testing.T) {
	group := &service.Group{
		ID: 1, Platform: "openai",
		ImagePrice1K: floatPtr(0.06),
		ImagePrice2K: floatPtr(0.08),
		ImagePrice4K: floatPtr(0.12),
	}
	mk := func(model string, price float64) service.SupportedModel {
		return service.SupportedModel{
			Name:     model,
			Platform: "openai",
			Pricing: &service.ChannelModelPricing{
				Platform:        "openai",
				Models:          []string{model},
				BillingMode:     service.BillingModePerRequest,
				PerRequestPrice: floatPtr(price),
			},
		}
	}
	ch := service.AvailableChannel{
		Status: service.StatusActive,
		Groups: []service.AvailableGroupRef{{ID: 1}},
		SupportedModels: []service.SupportedModel{
			mk("gpt-image-2-low", 0.03),
			mk("gpt-image-2-medium", 0.05),
			mk("gpt-image-2-high", 0.1),
		},
	}
	accountModels := []string{
		"gpt-image-2", "gpt-image-2-low", "gpt-image-2-medium", "gpt-image-2-high",
	}
	// gpt-image-2 内置 image 模式但无 flat 价 → 走分组分辨率价。
	pricingFor := func(model string) *service.ChannelModelPricing {
		if model == "gpt-image-2" {
			return &service.ChannelModelPricing{BillingMode: service.BillingModeImage, Platform: "openai"}
		}
		return nil
	}

	models := buildModelPlazaLegacyModels([]service.AvailableChannel{ch}, group, accountModels, pricingFor)

	byName := map[string]modelPlazaLegacyModel{}
	for _, m := range models {
		byName[m.Name] = m
	}
	require.Len(t, models, 4, "应显示全部 4 个模型")

	// low/medium/high：渠道 flat 价，三档同价。
	for model, price := range map[string]float64{
		"gpt-image-2-low":    0.03,
		"gpt-image-2-medium": 0.05,
		"gpt-image-2-high":   0.1,
	} {
		m := byName[model]
		require.NotNil(t, m.ImageTiers, model)
		require.InDelta(t, price, *m.ImageTiers.Price1K, 1e-9, model)
		require.InDelta(t, price, *m.ImageTiers.Price2K, 1e-9, model)
		require.InDelta(t, price, *m.ImageTiers.Price4K, 1e-9, model)
	}

	// gpt-image-2：无渠道定价 → 分组分辨率价 1K0.06/2K0.08/4K0.12。
	base := byName["gpt-image-2"]
	require.NotNil(t, base.ImageTiers)
	require.InDelta(t, 0.06, *base.ImageTiers.Price1K, 1e-9)
	require.InDelta(t, 0.08, *base.ImageTiers.Price2K, 1e-9)
	require.InDelta(t, 0.12, *base.ImageTiers.Price4K, 1e-9)
}
