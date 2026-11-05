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

// 三条 OAuth 生图出站链路都要带上 GIF 帧参数；漏任何一条，走该链路的分组就会被上游拒成
// 「GIF output is not enabled」。codex images 端点由 Group.ImageUseResponsesAPI=false 选中。
func TestOpenAIImagesGIFFrameParams_ForwardedOnAllOAuthPaths(t *testing.T) {
	parsed := parseOpenAIImagesGIFTestJSON(t, `{"model":"gpt-image-2.6-sunburst","prompt":"a cat waving","output_format":"gif","frame_count":6,"frame_duration_ms":150}`)
	require.NotNil(t, parsed.FrameCount)
	require.NotNil(t, parsed.FrameDurationMs)
	require.Equal(t, 6, *parsed.FrameCount)
	require.Equal(t, 150, *parsed.FrameDurationMs)
	require.True(t, parsed.HasNativeOptions)

	codexBody, _, err := buildOpenAIImagesCodexRequestBody(parsed, "gpt-image-2.6-sunburst")
	require.NoError(t, err)
	require.Equal(t, int64(6), gjson.GetBytes(codexBody, "frame_count").Int())
	require.Equal(t, int64(150), gjson.GetBytes(codexBody, "frame_duration_ms").Int())

	directBody, _, err := buildOpenAIImagesOAuthPayload(parsed, "gpt-image-2.5-flare")
	require.NoError(t, err)
	require.Equal(t, int64(6), gjson.GetBytes(directBody, "frame_count").Int())
	require.Equal(t, int64(150), gjson.GetBytes(directBody, "frame_duration_ms").Int())

	responsesBody, _, err := buildOpenAIImagesOAuthPayload(parsed, "gpt-image-2.6-sunburst")
	require.NoError(t, err)
	require.Equal(t, int64(6), gjson.GetBytes(responsesBody, "tools.0.frame_count").Int())
	require.Equal(t, int64(150), gjson.GetBytes(responsesBody, "tools.0.frame_duration_ms").Int())
}

func TestOpenAIImagesGIFFrameParams_OmittedWhenAbsent(t *testing.T) {
	parsed := parseOpenAIImagesGIFTestJSON(t, `{"model":"gpt-image-2.6-sunburst","prompt":"a cat"}`)
	require.Nil(t, parsed.FrameCount)
	require.Nil(t, parsed.FrameDurationMs)

	codexBody, _, err := buildOpenAIImagesCodexRequestBody(parsed, "gpt-image-2.6-sunburst")
	require.NoError(t, err)
	directBody, _, err := buildOpenAIImagesOAuthPayload(parsed, "gpt-image-2.5-flare")
	require.NoError(t, err)
	responsesBody, _, err := buildOpenAIImagesOAuthPayload(parsed, "gpt-image-2.6-sunburst")
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
	require.NotNil(t, parsed.FrameDurationMs)
	require.Equal(t, 4, *parsed.FrameCount)
	require.Equal(t, 80, *parsed.FrameDurationMs)
	require.True(t, parsed.HasNativeOptions)
}
