package service

import (
	"strings"

	"github.com/tidwall/gjson"
)

// 二开：「模型不存在」判定额外采信顶层 code == model_not_found。
//
// 中转常回扁平错误体。顶层 code 可能是镜像进来的 HTTP 状态码
// （{"code":401,"message":"unknown model x"}），也可能就是语义码
// （{"code":"model_not_found","message":"No such deployment"}）。
//
// 上游 v0.2.23 起 extractUpstreamErrorCode 明确不认顶层 code（03a489e43，二开原先加在里面的
// 顶层兜底已随之撤掉，见 openai_images_error_code_fork.go），所以镜像状态码不再有否决权，
// 消息短语照常参与判定——这部分已与上游一致。二开只多留一条：顶层 code 恰好是
// model_not_found 时直接采信，不要求消息里再带「unknown model」之类的短语；否则这类中转的
// 401 会绕过 ratelimit_service.go 的 upstream_401_model_not_found 保护，API Key 账号被当成
// 凭证失效直接停用。
//
// openAICompatibleModelNotFoundByCode 返回 decided=false 表示错误码不足以下结论，需看消息。
func openAICompatibleModelNotFoundByCode(body []byte) (decided bool, notFound bool) {
	if code := strings.TrimSpace(extractUpstreamErrorCode(body)); code != "" {
		return true, strings.EqualFold(code, "model_not_found")
	}
	if strings.EqualFold(strings.TrimSpace(gjson.GetBytes(body, "code").String()), "model_not_found") {
		return true, true
	}
	return false, false
}
