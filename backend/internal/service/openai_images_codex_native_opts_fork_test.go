package service

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/textproto"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// 上游 v0.2.20（7f2876def）给 GPT Image 2.6 加了 complexity / svg / markup，只接进了上游 direct
// 链路（buildOpenAIImagesOAuthPayload）。二开 codex images 端点链路（Group.ImageUseResponsesAPI=false，
// 裸 context 无分组也落在这条）打到的是同一个 chatgpt.com/backend-api/codex/images/* 端点，
// 漏透传就会静默吞参数照样 200 计费。本文件兜底两条链路口径一致。

func TestOpenAIImagesCodexForkPath_ForwardsGPTImage26NativeOptions(t *testing.T) {
	body := []byte(`{"model":"gpt-image-2.6-flare","prompt":"draw","quality":"medium","complexity":"high","output_format":"svg","svg":{"model":"gpt-5.6-sol","reasoning":{"effort":"low"},"ignored":true}}`)
	c, _ := newOpenAIImagesTestContext(t, body)
	upstream := &httpUpstreamRecorder{resp: openAIImagesJSONResponse()}
	svc := newOpenAIImagesTestService(upstream)
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)
	require.False(t, groupUsesResponsesImageAPI(c), "裸 context 必须落在二开 codex images 端点链路")

	_, err = svc.ForwardImages(context.Background(), c, directImagesTestAccount(), body, parsed, "")
	require.NoError(t, err)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, "/backend-api/codex/images/generations", upstream.lastReq.URL.Path)
	require.Equal(t, "high", gjson.GetBytes(upstream.lastBody, "complexity").String())
	require.Equal(t, "medium", gjson.GetBytes(upstream.lastBody, "quality").String())
	require.Equal(t, "gpt-5.6-sol", gjson.GetBytes(upstream.lastBody, "svg.model").String())
	require.Equal(t, "low", gjson.GetBytes(upstream.lastBody, "svg.reasoning.effort").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "svg.ignored").Exists())

	// 与上游 direct 链路逐字段对齐。
	direct, _, err := buildOpenAIImagesOAuthPayload(parsed, parsed.Model)
	require.NoError(t, err)
	for _, path := range []string{"complexity", "svg"} {
		require.JSONEq(t, gjson.GetBytes(direct, path).Raw, gjson.GetBytes(upstream.lastBody, path).Raw, path)
	}
}

func TestOpenAIImagesCodexForkPath_OmitsGPTImage26OptionsWhenAbsent(t *testing.T) {
	body := []byte(`{"model":"gpt-image-2.6-flare","prompt":"draw"}`)
	c, _ := newOpenAIImagesTestContext(t, body)
	parsed, err := newOpenAIImagesTestService(nil).ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)
	codexBody, _, err := buildOpenAIImagesCodexRequestBody(parsed, parsed.Model)
	require.NoError(t, err)
	for _, path := range []string{"complexity", "svg", "markup"} {
		require.False(t, gjson.GetBytes(codexBody, path).Exists(), path)
	}
}

func TestOpenAIImagesCodexForkPath_ForwardsMarkupOnEdits(t *testing.T) {
	for _, multipartInput := range []bool{false, true} {
		t.Run(fmt.Sprintf("multipart=%t", multipartInput), func(t *testing.T) {
			body := []byte(`{"model":"gpt-image-2.6-flare","prompt":"edit","complexity":"low","images":[{"image_url":"data:image/png;base64,aW1hZ2U="}],"markup":{"image_url":"data:image/png;base64,bWFya3Vw"}}`)
			contentType := "application/json"
			if multipartInput {
				var buf bytes.Buffer
				writer := multipart.NewWriter(&buf)
				for key, value := range map[string]string{"model": "gpt-image-2.6-flare", "prompt": "edit", "complexity": "low"} {
					require.NoError(t, writer.WriteField(key, value))
				}
				for _, name := range []string{"image", "markup"} {
					header := textproto.MIMEHeader{}
					header.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename="input.png"`, name))
					header.Set("Content-Type", "image/png")
					part, err := writer.CreatePart(header)
					require.NoError(t, err)
					_, err = part.Write([]byte(name))
					require.NoError(t, err)
				}
				require.NoError(t, writer.Close())
				body, contentType = buf.Bytes(), writer.FormDataContentType()
			}
			c, _ := newOpenAIImagesTestContext(t, body)
			c.Request.URL.Path = openAIImagesEditsEndpoint
			c.Request.Header.Set("Content-Type", contentType)
			parsed, err := newOpenAIImagesTestService(nil).ParseOpenAIImagesRequest(c, body)
			require.NoError(t, err)

			codexBody, ct, err := buildOpenAIImagesCodexRequestBody(parsed, parsed.Model)
			require.NoError(t, err)
			require.Equal(t, "application/json", ct)
			require.Equal(t, "data:image/png;base64,bWFya3Vw", gjson.GetBytes(codexBody, "markup.image_url").String())
			require.Equal(t, "low", gjson.GetBytes(codexBody, "complexity").String())
			require.Len(t, gjson.GetBytes(codexBody, "images").Array(), 1)
			require.False(t, gjson.GetBytes(codexBody, "mask").Exists())
		})
	}
}
