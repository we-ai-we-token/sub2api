//go:build unit

package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// 生图错误透传认扁平错误体的顶层 code；共享的 extractUpstreamErrorCode 不认
// （上游 TestSharedUpstreamErrorExtractorsIgnoreXAIFlatShape 兜底）。
func TestOpenAIImagesUpstreamErrorCode(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{"flat string code", `{"code":"unsupported_parameter","message":"Streaming is only supported with n=1."}`, "unsupported_parameter"},
		{"flat numeric code", `{"code":400,"message":"bad"}`, "400"},
		{"nested code retains priority", `{"error":{"code":"server_error","message":"x"},"code":"top"}`, "server_error"},
		{"embedded code retains priority", `{"error":{"message":"{\"error\":{\"code\":\"embedded\"}}"},"code":"top"}`, "embedded"},
		{"blank nested code falls back", `{"error":{"code":" "},"code":" top "}`, "top"},
		{"object code is not surfaced", `{"code":{"private":"diagnostic"}}`, ""},
		{"array code is not surfaced", `{"code":["diagnostic"]}`, ""},
		{"boolean code is not surfaced", `{"code":true}`, ""},
		{"null code", `{"code":null}`, ""},
		{"no code", `{"error":{"message":"reason"}}`, ""},
		{"not json", `upstream internal diagnostic`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, openAIImagesUpstreamErrorCode([]byte(tc.body)))
		})
	}
}

// openAIImagesUpstreamErrorFromHTTP 是上游函数，二开只把取 code 的那一行换成了
// openAIImagesUpstreamErrorCode。合并时那一行被取成上游版本，这条就会挂。
func TestOpenAIImagesUpstreamErrorFromHTTP_SurfacesFlatBodyCode(t *testing.T) {
	flat := []byte(`{"code":"unsupported_parameter","message":"Streaming is only supported with n=1."}`)

	got := openAIImagesUpstreamErrorFromHTTP(http.StatusBadRequest, http.Header{"X-Request-Id": []string{"req_1"}}, flat)

	require.Equal(t, http.StatusBadRequest, got.StatusCode)
	require.Equal(t, "unsupported_parameter", got.Code)
	require.Equal(t, "Streaming is only supported with n=1.", got.Message)
	require.Equal(t, "invalid_request_error", got.ErrorType)
	require.Equal(t, "req_1", got.UpstreamRequestID)

	// 同一个扁平体不得影响共享抽取器：它驱动停号、模型冷却与 failover。
	require.Empty(t, extractUpstreamErrorCode(flat))
}

func TestOpenAIImagesPromoteFlatErrorCode(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string // 期望的 error.code；"" 表示 body 必须原样返回
	}{
		{"flat string code", `{"code":"unsupported_parameter","message":"Streaming is only supported with n=1."}`, "unsupported_parameter"},
		{"trims", `{"code":" unsupported_parameter ","message":"m"}`, "unsupported_parameter"},
		{"error object without code", `{"error":{"message":"m"},"code":"top"}`, "top"},
		// 数字多半是中转镜像的 HTTP 状态码，不能让它在通用出口里变成结构化 code。
		{"numeric code is not promoted", `{"code":400,"message":"unknown model x"}`, ""},
		{"nested code wins", `{"error":{"code":"nested"},"code":"top"}`, ""},
		{"string error is left alone", `{"error":"Image could not be decoded.","code":"invalid_image"}`, ""},
		{"object code", `{"code":{"private":"diagnostic"}}`, ""},
		{"blank code", `{"code":" "}`, ""},
		{"not json", `upstream internal diagnostic`, ""},
		{"empty", ``, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := openAIImagesPromoteFlatErrorCode([]byte(tc.body))
			if tc.want == "" {
				require.Equal(t, tc.body, string(got))
				return
			}
			require.Equal(t, tc.want, gjson.GetBytes(got, "error.code").String())
			require.Equal(t, tc.want, extractUpstreamErrorCode(got), "通用出口靠共享抽取器取 code")
			// 顶层字段原样保留，message 抽取不受影响。
			require.Equal(t, gjson.Get(tc.body, "message").String(), gjson.GetBytes(got, "message").String())
			require.Equal(t, extractUpstreamErrorMessage([]byte(tc.body)), extractUpstreamErrorMessage(got))
		})
	}
}

// 二开 codex images 端点（Group.ImageUseResponsesAPI=false；裸 context 无分组也落在这条）收到
// Codex 生图后端的扁平 400 时，流式与非流式两个出口都必须把 error.code 带给客户端——
// b09ded779 修的就是这件事，v0.2.23 收窄共享抽取器后流式出口一度丢失。
// （「stream=true 且 n>1」现在由网关本地先拦，这里只借用那个扁平体的形状。）
func TestOpenAIImagesCodexFork_FlatUpstream400KeepsErrorCode(t *testing.T) {
	const flat = `{"code":"unsupported_parameter","message":"Unsupported parameter: background."}`
	for _, stream := range []bool{true, false} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"model":"gpt-image-2","prompt":"draw","stream":%t}`, stream))
			c, rec := newOpenAIImagesTestContext(t, body)
			require.False(t, groupUsesResponsesImageAPI(c), "裸 context 必须落在二开 codex images 端点链路")
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusBadRequest,
				Header:     http.Header{"Content-Type": {"application/json"}},
				Body:       io.NopCloser(strings.NewReader(flat)),
			}}
			svc := newOpenAIImagesTestService(upstream)
			parsed, err := svc.ParseOpenAIImagesRequest(c, body)
			require.NoError(t, err)

			result, err := svc.ForwardImages(context.Background(), c, directImagesTestAccount(), body, parsed, "")
			require.Error(t, err)
			require.Nil(t, result)
			require.Len(t, upstream.requests, 1, "确定性的 400 不换号重放")
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			require.Equal(t, "unsupported_parameter", gjson.Get(rec.Body.String(), "error.code").String(), rec.Body.String())
			require.Equal(t, "Unsupported parameter: background.", gjson.Get(rec.Body.String(), "error.message").String())
		})
	}
}
