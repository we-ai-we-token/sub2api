package service

import (
	"strings"

	"github.com/tidwall/gjson"
)

// 二开：extractUpstreamErrorCode 带了顶层 code 兜底（生图 unsupported_parameter 等判定依赖它），
// 但「模型不存在」判定不能按它一票否决——中转常把 HTTP 状态码镜像进顶层 code
// （{"code":401,"message":"unknown model x"}），否决后消息短语不再参与，v0.2.8 新增的
// 401 模型不存在保护（ratelimit_service.go 的 upstream_401_model_not_found）落空，
// API Key 账号被当成凭证失效直接停用。
//
// 这里恢复上游口径：只有结构化的嵌套 code（error.code / error.message 内嵌 JSON）有否决权；
// 顶层 code 只在恰好是 model_not_found 时采信，否则交给消息短语判定。

// openAICompatibleModelNotFoundByCode 返回 decided=false 表示错误码不足以下结论，需看消息。
func openAICompatibleModelNotFoundByCode(body []byte) (decided bool, notFound bool) {
	if code := extractNestedUpstreamErrorCode(body); code != "" {
		return true, strings.EqualFold(code, "model_not_found")
	}
	if strings.EqualFold(strings.TrimSpace(gjson.GetBytes(body, "code").String()), "model_not_found") {
		return true, true
	}
	return false, false
}

// extractNestedUpstreamErrorCode 与上游 extractUpstreamErrorCode 一致，不含二开的顶层 code 兜底。
func extractNestedUpstreamErrorCode(body []byte) string {
	if code := strings.TrimSpace(gjson.GetBytes(body, "error.code").String()); code != "" {
		return code
	}
	inner := strings.TrimSpace(gjson.GetBytes(body, "error.message").String())
	if !strings.HasPrefix(inner, "{") {
		return ""
	}
	if code := strings.TrimSpace(gjson.Get(inner, "error.code").String()); code != "" {
		return code
	}
	if lastBrace := strings.LastIndex(inner, "}"); lastBrace >= 0 {
		return strings.TrimSpace(gjson.Get(inner[:lastBrace+1], "error.code").String())
	}
	return ""
}
