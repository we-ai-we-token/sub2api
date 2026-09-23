package service

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestBuildGeminiAIStudioModelActionURLEscapesBracketedMappedModel 锁定二开行为：
// new-api 中转站的渠道模型名带方括号前缀（如 "[A.s]nano-banana2"），账号 model_mapping
// 只能映射到这类名字；拼上游 URL 时必须放行并编码成 %5B/%5D，而不是报
// "invalid gemini model for upstream url path"。
func TestBuildGeminiAIStudioModelActionURLEscapesBracketedMappedModel(t *testing.T) {
	const base = "http://38.145.220.94:3000"

	got, err := buildGeminiAIStudioModelActionURL(base+"/", "[A.s]nano-banana2", "generateContent", false)
	require.NoError(t, err)
	require.Equal(t, base+"/v1beta/models/%5BA.s%5Dnano-banana2:generateContent", got)

	got, err = buildGeminiAIStudioModelActionURL(base, " [arj]gemini-3.1-flash-image-preview ", "streamGenerateContent", true)
	require.NoError(t, err)
	require.Equal(t, base+"/v1beta/models/%5Barj%5Dgemini-3.1-flash-image-preview:streamGenerateContent?alt=sse", got)

	// 线上发出去的请求行必须保留编码形式（net/http 按 RawPath 写出），
	// 已实测中转站对 %5B/%5D 与字面量方括号解析结果一致。
	req, err := http.NewRequest(http.MethodPost, got, nil)
	require.NoError(t, err)
	require.Equal(t, "/v1beta/models/%5Barj%5Dgemini-3.1-flash-image-preview:streamGenerateContent?alt=sse", req.URL.RequestURI())
}

// TestBuildGeminiAIStudioModelActionURLBracketsDoNotWidenOtherBytes 确认放行方括号
// 没有顺带放开其它字符：方括号之外仍按上游闭集允许清单校验。
func TestBuildGeminiAIStudioModelActionURLBracketsDoNotWidenOtherBytes(t *testing.T) {
	const base = "https://generativelanguage.googleapis.com"

	for _, model := range []string{
		"[A.s]nano/../x",
		"[A.s]../../x",
		"[A.s] nano",
		"[A.s]nano%2f..",
		"[A.s]nano?a=b",
		"[A.s]nano#frag",
		"[A.s]nano@001",
		"[A.s]\x00",
		"[A.s]" + strings.Repeat("a", maxUpstreamPathSegmentLen),
	} {
		t.Run("model_"+model, func(t *testing.T) {
			_, err := buildGeminiAIStudioModelActionURL(base, model, "generateContent", false)
			require.Error(t, err, "model %q must be rejected", model)
		})
	}

	// 长度上限按编码前计算：恰好到上限的仍然放行。
	_, err := buildGeminiAIStudioModelActionURL(base, "[A.s]"+strings.Repeat("a", maxUpstreamPathSegmentLen-len("[A.s]")), "generateContent", false)
	require.NoError(t, err)
}

// TestIsSafeGeminiModelPathSegmentStaysStrictForBrackets 客户端直接书写的模型名
// 仍按上游口径拒绝方括号：放行只发生在账号映射之后拼上游 URL 的那一步。
func TestIsSafeGeminiModelPathSegmentStaysStrictForBrackets(t *testing.T) {
	require.False(t, IsSafeGeminiModelPathSegment("[A.s]nano-banana2"))
	require.True(t, IsSafeGeminiModelPathSegment("gemini-3.1-flash-image-preview"))
}
