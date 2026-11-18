package service

import (
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// 二开：生图错误透传要认扁平错误体的顶层 code。
//
// Codex 生图后端与部分中转回的错误体是扁平的：
//
//	{"code":"unsupported_parameter","message":"Streaming is only supported with n=1."}
//
// 客户端靠 code 判断是不是自己的参数问题，丢掉就只剩一句 message。
//
// 这个兜底原先（b09ded779）直接加在共享的 extractUpstreamErrorCode 里。但那个函数同时驱动
// 所有平台的 failover、「模型不存在」冷却、401 停号（token_revoked）与拒绝字段重试；中转常把
// HTTP 状态码镜像进顶层 code，二开自己在 v0.2.8 就因此误停过 API Key 账号。上游 v0.2.23
// （03a489e43）用 TestSharedUpstreamErrorExtractorsIgnoreXAIFlatShape 把「共享抽取器不认扁平体」
// 钉死，Grok 改走自己的 grokUpstreamErrorCode。这里照同样的做法收窄：共享抽取器保持与上游
// 一致，顶层 code 只在生图错误透传这一处采信。
//
// 只认字符串/数字，对象、数组、布尔一律不当 code 透出（避免把上游诊断信息原样回给客户端）。
func openAIImagesUpstreamErrorCode(body []byte) string {
	if code := strings.TrimSpace(extractUpstreamErrorCode(body)); code != "" {
		return code
	}
	if code := gjson.GetBytes(body, "code"); code.Type == gjson.String || code.Type == gjson.Number {
		return strings.TrimSpace(code.String())
	}
	return ""
}

// openAIImagesPromoteFlatErrorCode 把扁平错误体的顶层字符串 code 提升成 error.code，返回新副本。
//
// 二开 codex images 端点的流式链路（forwardOpenAIImagesOAuthStreaming）是生图里唯一一条把
// 非 failover 错误交给通用 handleErrorResponse 的出口——那里有生图专用出口没有的 cyber_policy
// 标记与通用错误归一，不宜整个换掉。通用出口经 writeOpenAIUpstreamClientError 用共享抽取器取
// code，共享抽取器不认顶层 code 之后，「stream=true 且 n>1」这类 400
// （{"code":"unsupported_parameter","message":"Streaming is only supported with n=1."}）
// 回给客户端时就丢了 error.code，而 b09ded779 当初要修的正是这条链路。
//
// 只提升字符串：数字多半是中转镜像进来的 HTTP 状态码，提升后会在通用出口里获得
// 「结构化 code」的否决权（模型不存在判定等），正是上游 03a489e43 要堵的口子。
// 已有嵌套 code、error 不是对象（{"error":"..."}）或不是 JSON 时原样返回。
func openAIImagesPromoteFlatErrorCode(body []byte) []byte {
	if strings.TrimSpace(extractUpstreamErrorCode(body)) != "" {
		return body
	}
	code := gjson.GetBytes(body, "code")
	if code.Type != gjson.String || strings.TrimSpace(code.String()) == "" {
		return body
	}
	if e := gjson.GetBytes(body, "error"); e.Exists() && !e.IsObject() {
		return body
	}
	promoted, err := sjson.SetBytes(body, "error.code", strings.TrimSpace(code.String()))
	if err != nil {
		return body
	}
	return promoted
}
