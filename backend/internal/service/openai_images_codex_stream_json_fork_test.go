package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 上游 v0.2.20（7f2876def）发现 Codex images 端点（尤其 edits）在 stream=true 时可能直接回一整块
// JSON，并在上游 direct 链路里把它转成 completed 事件。二开 codex images 端点链路
// （Group.ImageUseResponsesAPI=false；裸 context 无分组也落在这条）此前只认 SSE data 行，
// 会把这张已付费的图当成空输出丢掉、再换号重画一次。本文件兜底二开链路与上游同口径。

func TestOpenAIImagesCodexForkStream_DeliversCompleteJSONBody(t *testing.T) {
	for _, model := range []string{"gpt-image-2", "gpt-image-2.6-flare"} {
		for _, disconnect := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/disconnect=%t", model, disconnect), func(t *testing.T) {
				body := []byte(fmt.Sprintf(`{"model":%q,"prompt":"edit","images":[{"image_url":"data:image/png;base64,AA=="}],"stream":true,"response_format":"url"}`, model))
				c, rec := newOpenAIImagesTestContext(t, body)
				c.Request.URL.Path = openAIImagesEditsEndpoint
				require.False(t, groupUsesResponsesImageAPI(c), "裸 context 必须落在二开 codex images 端点链路")
				if disconnect {
					c.Writer = &failingOpenAIImageWriter{ResponseWriter: c.Writer, failAfter: 1}
				}
				response := `{"created":1710000000,"size":"1024x1024","output_format":"png","data":[{"b64_json":"AQ=="}],"usage":{"input_tokens":100,"input_tokens_details":{"image_tokens":80},"output_tokens":40}}`
				upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(response))}}
				svc := newOpenAIImagesTestService(upstream)
				parsed, err := svc.ParseOpenAIImagesRequest(c, body)
				require.NoError(t, err)

				result, err := svc.ForwardImages(context.Background(), c, directImagesTestAccount(), body, parsed, "")
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, 1, result.ImageCount)
				require.Equal(t, 40, result.Usage.ImageOutputTokens)
				require.Equal(t, 80, result.Usage.ImageInputTokens)
				require.True(t, result.ImageEndpoint)
				require.Len(t, upstream.requests, 1, "已付费的图不能再换号重画")
				require.Equal(t, "/backend-api/codex/images/edits", upstream.lastReq.URL.Path)
				if !disconnect {
					require.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))
					require.Equal(t, 1, strings.Count(rec.Body.String(), "event: image_edit.completed"))
					require.Equal(t, 1, strings.Count(rec.Body.String(), `"usage"`))
					require.Contains(t, rec.Body.String(), "data:image/png;base64,AQ==")
				}
			})
		}
	}
}

func TestOpenAIImagesCodexForkStream_JSONBodyWithoutImage(t *testing.T) {
	cases := []struct {
		response       string
		wantErrorEvent bool
	}{
		// 既无图也无错误、或根本不是 JSON：沿用二开空输出语义（可 failover），不能当成功。
		{`{}`, false},
		{`{"data":[]}`, false},
		{`{"data":[{"b64_json":""}]}`, false},
		{`not json`, false},
		// 上游明确报错：按上游错误返回，不可重试的错误要写给客户端。
		{`{"error":{"type":"invalid_request_error","message":"bad image"}}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.response, func(t *testing.T) {
			body := []byte(`{"model":"gpt-image-2.6-flare","prompt":"draw","stream":true}`)
			c, rec := newOpenAIImagesTestContext(t, body)
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(tc.response))}}
			svc := newOpenAIImagesTestService(upstream)
			parsed, err := svc.ParseOpenAIImagesRequest(c, body)
			require.NoError(t, err)

			result, err := svc.ForwardImages(context.Background(), c, directImagesTestAccount(), body, parsed, "")
			require.Error(t, err)
			require.Nil(t, result)
			require.NotContains(t, rec.Body.String(), "event: image_generation.completed")
			if tc.wantErrorEvent {
				var upstreamErr *OpenAIImagesUpstreamError
				require.ErrorAs(t, err, &upstreamErr)
				require.Contains(t, rec.Body.String(), "bad image")
			} else {
				var failoverErr *UpstreamFailoverError
				require.ErrorAs(t, err, &failoverErr, "无图时保持二开空输出 failover 语义")
			}
		})
	}
}

// 正常 SSE 流里夹着的非 data 行（event:/注释）不能被误当成 JSON 兜底。
func TestOpenAIImagesCodexForkStream_SSEStreamUnaffected(t *testing.T) {
	body := []byte(`{"model":"gpt-image-2","prompt":"draw","stream":true}`)
	c, rec := newOpenAIImagesTestContext(t, body)
	stream := ": keepalive\n\nevent: image_generation.completed\ndata: {\"b64_json\":\"AQ==\",\"usage\":{\"input_tokens\":5,\"output_tokens\":7}}\n\n"
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream))}}
	svc := newOpenAIImagesTestService(upstream)
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)

	result, err := svc.ForwardImages(context.Background(), c, directImagesTestAccount(), body, parsed, "")
	require.NoError(t, err)
	require.Equal(t, 1, result.ImageCount)
	require.Equal(t, 1, strings.Count(rec.Body.String(), "event: image_generation.completed"))
}
