package service

import (
	"bytes"
	"mime/multipart"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func parseOpenAIImagesGIFTestJSON(t *testing.T, body string) *OpenAIImagesRequest {
	t.Helper()
	c, _ := newOpenAIImagesTestContext(t, []byte(body))
	parsed, err := newOpenAIImagesTestService(nil).ParseOpenAIImagesRequest(c, []byte(body))
	require.NoError(t, err)
	return parsed
}

// GIF 帧参数在 OAuth 生图出站链路上的口径（v0.2.20 起解析与 direct 链路改用上游实现）：
//   - 二开 codex images 端点（Group.ImageUseResponsesAPI=false 选中）与上游 direct 端点：顶层透传；
//   - Responses image_generation 工具：上游口径是直接报错「require the native Images endpoint」，
//     不再像二开早先那样塞进 tool 里（Codex 后端把工具模型钉死在 gpt-image-2-codex，塞了也是 400）。
func TestOpenAIImagesGIFFrameParams_ForwardedOnAllOAuthPaths(t *testing.T) {
	parsed := parseOpenAIImagesGIFTestJSON(t, `{"model":"gpt-image-2.6-sunburst","prompt":"a cat waving","output_format":"gif","frame_count":6,"frame_duration_ms":150}`)
	require.NotNil(t, parsed.FrameCount)
	require.NotNil(t, parsed.FrameDurationMS)
	require.Equal(t, 6, *parsed.FrameCount)
	require.Equal(t, 150, *parsed.FrameDurationMS)
	require.True(t, parsed.HasNativeOptions)

	codexBody, _, err := buildOpenAIImagesCodexRequestBody(parsed, "gpt-image-2.6-sunburst")
	require.NoError(t, err)
	require.Equal(t, int64(6), gjson.GetBytes(codexBody, "frame_count").Int())
	require.Equal(t, int64(150), gjson.GetBytes(codexBody, "frame_duration_ms").Int())

	directBody, _, err := buildOpenAIImagesOAuthPayload(parsed, "gpt-image-2.5-flare")
	require.NoError(t, err)
	require.Equal(t, int64(6), gjson.GetBytes(directBody, "frame_count").Int())
	require.Equal(t, int64(150), gjson.GetBytes(directBody, "frame_duration_ms").Int())

	// 2.6 必须走原生端点（上游 requiresCodexDirectImages），帧参数同样顶层透传。
	direct26Body, _, err := buildOpenAIImagesOAuthPayload(parsed, "gpt-image-2.6-sunburst")
	require.NoError(t, err)
	require.Equal(t, int64(6), gjson.GetBytes(direct26Body, "frame_count").Int())
	require.Equal(t, int64(150), gjson.GetBytes(direct26Body, "frame_duration_ms").Int())

	_, err = buildOpenAIImagesResponsesRequest(parsed, "gpt-image-2")
	require.ErrorContains(t, err, "native Images endpoint", "帧参数不能在 Responses 回退链路上被静默吞掉")
}

func TestOpenAIImagesGIFFrameParams_OmittedWhenAbsent(t *testing.T) {
	parsed := parseOpenAIImagesGIFTestJSON(t, `{"model":"gpt-image-2.6-sunburst","prompt":"a cat"}`)
	require.Nil(t, parsed.FrameCount)
	require.Nil(t, parsed.FrameDurationMS)

	codexBody, _, err := buildOpenAIImagesCodexRequestBody(parsed, "gpt-image-2.6-sunburst")
	require.NoError(t, err)
	directBody, _, err := buildOpenAIImagesOAuthPayload(parsed, "gpt-image-2.5-flare")
	require.NoError(t, err)
	responsesBody, err := buildOpenAIImagesResponsesRequest(parsed, "gpt-image-2")
	require.NoError(t, err)
	for _, path := range []string{"frame_count", "frame_duration_ms"} {
		require.False(t, gjson.GetBytes(codexBody, path).Exists(), "codex %s", path)
		require.False(t, gjson.GetBytes(directBody, path).Exists(), "direct %s", path)
		require.False(t, gjson.GetBytes(responsesBody, "tools.0."+path).Exists(), "responses %s", path)
	}
}

func TestOpenAIImagesGIFFrameParams_RejectsNonNumericJSON(t *testing.T) {
	for _, body := range []string{
		`{"model":"gpt-image-2.6-sunburst","prompt":"x","frame_count":"6"}`,
		`{"model":"gpt-image-2.6-sunburst","prompt":"x","frame_duration_ms":"150"}`,
	} {
		c, _ := newOpenAIImagesTestContext(t, []byte(body))
		_, err := newOpenAIImagesTestService(nil).ParseOpenAIImagesRequest(c, []byte(body))
		require.Error(t, err, body)
	}
}

func TestOpenAIImagesGIFFrameParams_Multipart(t *testing.T) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for key, value := range map[string]string{"model": "gpt-image-2.6-sunburst", "prompt": "a cat", "output_format": "gif", "frame_count": "4", "frame_duration_ms": "80"} {
		require.NoError(t, w.WriteField(key, value))
	}
	require.NoError(t, w.Close())
	c, _ := newOpenAIImagesTestContext(t, body.Bytes())
	c.Request.Header.Set("Content-Type", w.FormDataContentType())
	parsed, err := newOpenAIImagesTestService(nil).ParseOpenAIImagesRequest(c, body.Bytes())
	require.NoError(t, err)
	require.NotNil(t, parsed.FrameCount)
	require.NotNil(t, parsed.FrameDurationMS)
	require.Equal(t, 4, *parsed.FrameCount)
	require.Equal(t, 80, *parsed.FrameDurationMS)
	require.True(t, parsed.HasNativeOptions)
}
