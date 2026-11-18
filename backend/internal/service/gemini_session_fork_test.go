//go:build unit

package service

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
)

func TestGeminiFirstUserPartsSeed_SmallPartsMatchUpstreamNormalization(t *testing.T) {
	compact := `[{"text":"hello"},{"inlineData":{"mimeType":"image/png","data":"AA=="}}]`
	spaced := `[ { "text" : "hello" }, { "inlineData" : { "mimeType" : "image/png", "data" : "AA==" } } ]`

	require.Equal(t, geminiFirstUserPartsSeed(compact), geminiFirstUserPartsSeed(spaced), "上限以内仍按上游口径归一化排版")

	body := func(parts string) []byte {
		return []byte(fmt.Sprintf(`{"contents":[{"role":"user","parts":%s}]}`, parts))
	}
	require.Equal(t,
		GenerateGeminiContentSessionHash(1, "gemini-4-carbon", body(compact)),
		GenerateGeminiContentSessionHash(1, "gemini-4-carbon", body(spaced)))
}

func TestGeminiFirstUserPartsSeed_LargePartsAreNotCopied(t *testing.T) {
	image := strings.Repeat("A", geminiFirstUserPartsSeedNormalizeMaxBytes)
	raw := fmt.Sprintf(`[{"text":"edit"},{"inlineData":{"mimeType":"image/png","data":%q}}]`, image)
	require.Greater(t, len(raw), geminiFirstUserPartsSeedNormalizeMaxBytes)

	got := geminiFirstUserPartsSeed(raw)

	require.Equal(t, len(raw), len(got))
	require.Same(t, unsafe.StringData(raw), unsafe.StringData(got), "带大图的 parts 必须原样返回，不得解码重编码")

	// 哈希仍然稳定，且随图片内容变化。
	body := func(parts string) []byte {
		return []byte(fmt.Sprintf(`{"contents":[{"role":"user","parts":%s}]}`, parts))
	}
	h1 := GenerateGeminiContentSessionHash(7, "gemini-3-pro-image", body(raw))
	h2 := GenerateGeminiContentSessionHash(7, "gemini-3-pro-image", body(raw))
	other := strings.Replace(raw, "AAAA", "AAAB", 1)
	require.NotEmpty(t, h1)
	require.Equal(t, h1, h2)
	require.NotEqual(t, h1, GenerateGeminiContentSessionHash(7, "gemini-3-pro-image", body(other)))
}

// GenerateGeminiContentSessionHash 是上游函数，二开只把取种子的那一行换成了
// geminiFirstUserPartsSeed。合并冲突时取上游版本会静默丢掉这个 hook（功能不变、只是每个生图请求
// 多分配几倍请求体的内存），所以直接读源码兜底。
func TestGenerateGeminiContentSessionHash_UsesForkSeedHook(t *testing.T) {
	src, err := os.ReadFile("gemini_session.go")
	require.NoError(t, err)
	require.Contains(t, string(src), "geminiFirstUserPartsSeed(firstUserParts.Raw)")
	require.NotContains(t, string(src), "normalizeCompatSeedJSON(json.RawMessage(firstUserParts.Raw))")
}
