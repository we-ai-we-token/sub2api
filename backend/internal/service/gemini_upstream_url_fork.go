package service

import "strings"

// 二开：new-api 中转站的渠道模型名普遍带方括号前缀（如 "[A.s]nano-banana2"、
// "[arj]gemini-3-pro-image-preview"），且只提供这种名字，Gemini API Key 账号的
// model_mapping 只能映射过去。上游 sub2api 的路径片段护栏（upstream_path_guard.go）
// 是闭集允许清单，不含方括号，于是这类账号的测试与真实转发都报
// "invalid gemini model for upstream url path"。
//
// 这里只在拼上游 URL 这一处放行方括号，并一律编码成 %5B/%5D：编码后的片段只比允许
// 清单多出 '%'，不可能改变上游路径结构（batch_image_provider_gemini.go 拼同一个 URL
// 时用 url.PathEscape，效果相同）。方括号之外的字符仍走上游护栏；handler 层对客户端
// 直接书写的模型名（IsSafeGeminiModelPathSegment）保持上游口径不变。

var (
	// 用等长的 '_' 替换方括号后再过护栏，长度上限与"不能只由点组成"的规则照常生效。
	geminiModelBracketValidationReplacer = strings.NewReplacer("[", "_", "]", "_")
	geminiModelBracketEscapeReplacer     = strings.NewReplacer("[", "%5B", "]", "%5D")
)

// geminiUpstreamModelPathSegment 校验 model（已 trim）并返回可直接拼进上游 URL 的片段。
func geminiUpstreamModelPathSegment(model string) (string, error) {
	if err := validateUpstreamPathSegment("gemini model", geminiModelBracketValidationReplacer.Replace(model)); err != nil {
		return "", err
	}
	return geminiModelBracketEscapeReplacer.Replace(model), nil
}
