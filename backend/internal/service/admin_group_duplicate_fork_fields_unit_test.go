//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func forkGroupPricePointer(value float64) *float64 {
	return &value
}

// cloneGroupForDuplicate 与上游保持字节一致，二开新增的 Group 字段落进去之后
// 没有任何编译期提示会告诉我们这里漏拷了。而 groupRepository.CreateFromSource
// 是无条件 Set 的：漏拷 = 按零值写库，image_use_responses_api 的 schema 默认值
// true 会被复制出来的分组静默覆盖成 false，OAuth 生图链路直接换一条。
// 独立成文件是为了不跟上游抢 admin_group_duplicate_test.go。
func TestCloneGroupForDuplicateCopiesForkOnlyFields(t *testing.T) {
	source := &Group{
		ImageUseResponsesAPI: true,
		ImageQualityBilling:  true,
		ImagePriceLow:        forkGroupPricePointer(0.011),
		ImagePriceMedium:     forkGroupPricePointer(0.042),
		ImagePriceHigh:       forkGroupPricePointer(0.167),
	}

	cloned := cloneGroupForDuplicate(source, "operation-fork-fields")

	require.True(t, cloned.ImageUseResponsesAPI, "生图链路开关必须跟着复制，否则默认值 true 被写成 false")
	require.True(t, cloned.ImageQualityBilling)
	require.Equal(t, source.ImagePriceLow, cloned.ImagePriceLow)
	require.Equal(t, source.ImagePriceMedium, cloned.ImagePriceMedium)
	require.Equal(t, source.ImagePriceHigh, cloned.ImagePriceHigh)
}

// 质量分级价必须是值拷贝而不是共享指针，否则改副本会改到源分组。
func TestCloneGroupForDuplicateDeepCopiesQualityTierPrices(t *testing.T) {
	source := &Group{
		ImagePriceLow:    forkGroupPricePointer(0.011),
		ImagePriceMedium: forkGroupPricePointer(0.042),
		ImagePriceHigh:   forkGroupPricePointer(0.167),
	}

	cloned := cloneGroupForDuplicate(source, "operation-fork-fields")
	*cloned.ImagePriceLow = 9.99
	*cloned.ImagePriceMedium = 9.99
	*cloned.ImagePriceHigh = 9.99

	require.Equal(t, 0.011, *source.ImagePriceLow)
	require.Equal(t, 0.042, *source.ImagePriceMedium)
	require.Equal(t, 0.167, *source.ImagePriceHigh)
}

// 同一个 cloneGroupForDuplicate 里，上游自己也漏拷了 LongContextPricingEnabled 和
// ModelPricing：CreateFromSource 同样是无条件 Set（ModelPricing 还会先 json.Marshal），
// 所以复制出来的分组会丢掉长上下文定价开关和整套模型定价覆盖，按默认价计费。
// 跟 fork 字段放同一个文件，是因为这是同一个函数的同一类漏拷，且这个文件不跟上游抢。
func TestCloneGroupForDuplicateCopiesLongContextAndModelPricing(t *testing.T) {
	source := &Group{
		LongContextPricingEnabled: true,
		ModelPricing: []ChannelModelPricing{{
			Platform:   PlatformOpenAI,
			Models:     []string{"gpt-5.4"},
			InputPrice: forkGroupPricePointer(1.25),
			Intervals:  []PricingInterval{{MinTokens: 0, TierLabel: "base"}},
			TimePricing: &ChannelTimePricing{
				Timezone: "Asia/Shanghai",
				Periods:  []ChannelTimePricingPeriod{{StartTime: "09:00", EndTime: "18:00", Multiplier: 1.2}},
			},
		}},
	}

	cloned := cloneGroupForDuplicate(source, "operation-upstream-pricing")

	require.True(t, cloned.LongContextPricingEnabled, "长上下文定价开关必须跟着复制")
	require.Equal(t, source.ModelPricing, cloned.ModelPricing)
}

// 模型定价里的切片必须独立，否则改副本会改到源分组。
func TestCloneGroupForDuplicateDeepCopiesModelPricingSlices(t *testing.T) {
	source := &Group{
		ModelPricing: []ChannelModelPricing{{
			Models:    []string{"gpt-5.4"},
			Intervals: []PricingInterval{{MinTokens: 0, TierLabel: "base"}},
			TimePricing: &ChannelTimePricing{
				Timezone: "Asia/Shanghai",
				Periods:  []ChannelTimePricingPeriod{{StartTime: "09:00", EndTime: "18:00", Multiplier: 1.2}},
			},
		}},
	}

	cloned := cloneGroupForDuplicate(source, "operation-upstream-pricing")
	cloned.ModelPricing[0].Models[0] = "changed"
	cloned.ModelPricing[0].Intervals[0].TierLabel = "changed"
	cloned.ModelPricing[0].TimePricing.Timezone = "changed"
	cloned.ModelPricing[0].TimePricing.Periods[0].Multiplier = 9.99

	require.Equal(t, "gpt-5.4", source.ModelPricing[0].Models[0])
	require.Equal(t, "base", source.ModelPricing[0].Intervals[0].TierLabel)
	require.Equal(t, "Asia/Shanghai", source.ModelPricing[0].TimePricing.Timezone)
	require.Equal(t, 1.2, source.ModelPricing[0].TimePricing.Periods[0].Multiplier)
}
