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
