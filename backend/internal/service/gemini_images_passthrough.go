package service

import "strings"

// IsGeminiImageGenerationModel 判断是否为 Gemini 生图模型。
// 口径与运营生图报表一致：platform='gemini' AND model ILIKE 'gemini-%image%'。
func IsGeminiImageGenerationModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(model, "gemini-") && strings.Contains(model, "image")
}
