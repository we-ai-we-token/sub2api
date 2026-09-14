package service

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// AZURE-openai 分组的账号 base_url 形如
// https://<resource>.openai.azure.com/openai/v1，比标准 OpenAI 多垫了 /openai。
// 这个用例锁住「垫了路径的 Azure 账号依然走 buildOpenAIImagesRequest，
// 因而依然受生图 HTTP/1.1 开关控制」——base_url 的形状只影响 URL 字符串，
// 不改变走哪条代码路径。

// withForcedImagesHTTP1 直接给包级的网关转发设置缓存塞一个未过期的值，
// 绕开 DB。测试结束后恢复，避免污染同包其它用例。
func withForcedImagesHTTP1(t *testing.T, enabled bool) {
	t.Helper()
	prev, hadPrev := gatewayForwardingCache.Load().(*cachedGatewayForwardingSettings)
	t.Cleanup(func() {
		gatewayForwardingSF.Forget("gateway_forwarding")
		if hadPrev && prev != nil {
			gatewayForwardingCache.Store(prev)
			return
		}
		// 没有原值时塞一个已过期的条目，等价于冷缓存：下一次读会重新走 DB。
		gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{
			expiresAt: time.Now().Add(-time.Hour).UnixNano(),
		})
	})
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{
		openAIImagesForceHTTP1: enabled,
		expiresAt:              time.Now().Add(time.Hour).UnixNano(),
	})
}

func azureImagesEditRequest(t *testing.T) (*gin.Context, *httptest.ResponseRecorder, []byte) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "gpt-image-2"))
	require.NoError(t, writer.WriteField("prompt", "replace background"))
	part, err := writer.CreateFormFile("image", "source.png")
	require.NoError(t, err)
	_, err = part.Write([]byte("png-image-content"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	req := httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(body.Bytes()))
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req
	return c, rec, body.Bytes()
}

func azureImagesService() *OpenAIGatewayService {
	return &OpenAIGatewayService{
		cfg:            &config.Config{},
		settingService: &SettingService{},
		httpUpstream: &httpUpstreamRecorder{
			resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"created":1,"data":[{"b64_json":"ZWRpdGVk"}]}`)),
			},
		},
	}
}

func azureAPIKeyAccount() *Account {
	return &Account{
		ID:       7,
		Name:     "5000-hari-1",
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key": "test-api-key",
			// 生产里的真实形状，注意多出来的 /openai
			"base_url": "https://hari-3802-resource.openai.azure.com/openai/v1",
		},
	}
}

func TestForwardImages_AzureBaseURLStillHonorsForcedHTTP1(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withForcedImagesHTTP1(t, true)

	svc := azureImagesService()
	c, _, raw := azureImagesEditRequest(t)
	parsed, err := svc.ParseOpenAIImagesRequest(c, raw)
	require.NoError(t, err)

	_, err = svc.ForwardImages(context.Background(), c, azureAPIKeyAccount(), raw, parsed, "")
	require.NoError(t, err)

	upstream := svc.httpUpstream.(*httpUpstreamRecorder)
	require.NotNil(t, upstream.lastReq)
	// 与生产 ops_error_logs 里记录的 URL 完全一致
	require.Equal(t,
		"https://hari-3802-resource.openai.azure.com/openai/v1/images/edits",
		upstream.lastReq.URL.String())
	require.Equal(t, HTTPUpstreamProfileOpenAIImages,
		HTTPUpstreamProfileFromContext(upstream.lastReq.Context()),
		"垫了 /openai 的 Azure 账号必须同样拿到生图 profile")
}

func TestForwardImages_AzureBaseURLKeepsHTTP2WhenSwitchOff(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withForcedImagesHTTP1(t, false)

	svc := azureImagesService()
	c, _, raw := azureImagesEditRequest(t)
	parsed, err := svc.ParseOpenAIImagesRequest(c, raw)
	require.NoError(t, err)

	_, err = svc.ForwardImages(context.Background(), c, azureAPIKeyAccount(), raw, parsed, "")
	require.NoError(t, err)

	upstream := svc.httpUpstream.(*httpUpstreamRecorder)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, HTTPUpstreamProfileOpenAI,
		HTTPUpstreamProfileFromContext(upstream.lastReq.Context()),
		"开关关闭时必须保持改动前的行为")
}
