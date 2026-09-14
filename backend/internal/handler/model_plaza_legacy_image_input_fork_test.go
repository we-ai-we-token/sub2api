package handler

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func fptr(v float64) *float64 { return &v }

// 二开：自研模型广场（/model-plaza-legacy）要展示图片输入单价。
// 线上 channel_model_pricing 里 gpt-image-2 的 token 行确实配了这个字段
// （0.000008 = $8/1M token），此前 legacy DTO 没有该字段，前端拿不到。
func TestLegacyPlazaExposesImageInputPriceForTokenBilling(t *testing.T) {
	m := service.SupportedModel{
		Name:     "gpt-image-2",
		Platform: "openai",
		Pricing: &service.ChannelModelPricing{
			BillingMode:      service.BillingModeToken,
			InputPrice:       fptr(0.000005),
			OutputPrice:      fptr(0.00001),
			ImageInputPrice:  fptr(0.000008),
			ImageOutputPrice: fptr(0.00003),
		},
	}

	out := toModelPlazaLegacyModel(m, &service.Group{})

	require.NotNil(t, out.ImageInputPrice, "token 计费必须带出图片输入单价")
	require.InDelta(t, 0.000008, *out.ImageInputPrice, 1e-12)
	require.NotNil(t, out.ImageOutputPrice)
	require.InDelta(t, 0.00003, *out.ImageOutputPrice, 1e-12)
}

// 没配图片输入价时保持 nil，前端渲染成「-」，不要凭空造 0。
func TestLegacyPlazaImageInputPriceAbsentStaysNil(t *testing.T) {
	m := service.SupportedModel{
		Name:     "gpt-4o",
		Platform: "openai",
		Pricing: &service.ChannelModelPricing{
			BillingMode: service.BillingModeToken,
			InputPrice:  fptr(0.000005),
		},
	}
	out := toModelPlazaLegacyModel(m, &service.Group{})
	require.Nil(t, out.ImageInputPrice)
}

// 只配了图片输入价的模型也算「有定价」，否则会被整行过滤掉不展示。
func TestLegacyPlazaModelWithOnlyImageInputPriceCountsAsPriced(t *testing.T) {
	require.True(t, modelHasPricingLegacy(&service.ChannelModelPricing{
		ImageInputPrice: fptr(0.000008),
	}))
	require.False(t, modelHasPricingLegacy(&service.ChannelModelPricing{}))
	require.False(t, modelHasPricingLegacy(nil))
}
