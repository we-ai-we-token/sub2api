//go:build unit

package service

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 生图返回 URL 的准入矩阵。核心约束是「客户端优先」：开关只是准入，
// 客户端没显式要 url 就绝不改写响应 —— 这是老客户端零变化的唯一保证。
// 独立成文件以免跟上游抢 openai_images_test.go。

func imageURLReturnContext(t *testing.T, platform string, groupToggle bool) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("api_key", &APIKey{Group: &Group{
		Platform:       platform,
		ImageReturnURL: groupToggle,
	}})
	return c
}

func TestImageStorageUploaderForRequest_Matrix(t *testing.T) {
	cases := []struct {
		name           string
		platform       string
		groupToggle    bool
		responseFormat string
		asyncManaged   bool
		want           bool
	}{
		{"开关关+客户端要url = 不改写", PlatformOpenAI, false, "url", false, false},
		{"开关关+客户端未指定 = 不改写", PlatformOpenAI, false, "", false, false},
		{"开关开+客户端未指定 = 不改写（老客户端零变化）", PlatformOpenAI, true, "", false, false},
		{"开关开+客户端要b64_json = 不改写", PlatformOpenAI, true, "b64_json", false, false},
		{"开关开+客户端要url = 改写", PlatformOpenAI, true, "url", false, true},
		{"大小写不敏感", PlatformOpenAI, true, "URL", false, true},
		{"gemini 平台同样生效", PlatformGemini, true, "url", false, true},
		{"grok 平台不生效（平台限定）", PlatformGrok, true, "url", false, false},
		{"anthropic 平台不生效", PlatformAnthropic, true, "url", false, false},
		{"异步托管时跳过（避免重复上传产生孤儿对象）", PlatformOpenAI, true, "url", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &OpenAIGatewayService{}
			// 解析器恒返回一个非 nil uploader，把判定压力全留给准入条件本身。
			svc.SetImageStorageResolver(func() (*ImageResultUploader, bool) {
				return &ImageResultUploader{}, true
			})
			c := imageURLReturnContext(t, tc.platform, tc.groupToggle)
			if tc.asyncManaged {
				MarkImagesAsyncManaged(c)
			}
			_, ok := svc.imageStorageUploaderForRequest(c, &OpenAIImagesRequest{ResponseFormat: tc.responseFormat})
			require.Equal(t, tc.want, ok)
		})
	}
}

// 对象存储没配置（解析器报未启用）时，即使开关开着也必须不改写。
func TestImageStorageUploaderForRequest_StorageDisabled(t *testing.T) {
	svc := &OpenAIGatewayService{}
	svc.SetImageStorageResolver(func() (*ImageResultUploader, bool) { return nil, false })
	c := imageURLReturnContext(t, PlatformOpenAI, true)
	_, ok := svc.imageStorageUploaderForRequest(c, &OpenAIImagesRequest{ResponseFormat: "url"})
	require.False(t, ok)
}

// 未注入解析器（wire 没接上/旧部署）时功能整体不可用，而不是 panic。
func TestImageStorageUploaderForRequest_NilResolver(t *testing.T) {
	svc := &OpenAIGatewayService{}
	c := imageURLReturnContext(t, PlatformOpenAI, true)
	_, ok := svc.imageStorageUploaderForRequest(c, &OpenAIImagesRequest{ResponseFormat: "url"})
	require.False(t, ok)
}

// 无分组的裸 context（退化场景）不得触发改写。
func TestImageStorageUploaderForRequest_NoGroup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &OpenAIGatewayService{}
	svc.SetImageStorageResolver(func() (*ImageResultUploader, bool) {
		return &ImageResultUploader{}, true
	})
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	_, ok := svc.imageStorageUploaderForRequest(c, &OpenAIImagesRequest{ResponseFormat: "url"})
	require.False(t, ok)
}

// 不满足准入时 rewrite 必须原样返回 body（字节级不变）。
func TestRewriteOpenAIImagesToStorageURL_PassthroughWhenNotEligible(t *testing.T) {
	svc := &OpenAIGatewayService{}
	c := imageURLReturnContext(t, PlatformOpenAI, false)
	body := []byte(`{"created":1,"data":[{"b64_json":"AAAA","size":"1024x1024"}]}`)
	got := svc.rewriteOpenAIImagesToStorageURL(t.Context(), c, &OpenAIImagesRequest{ResponseFormat: "url"}, body)
	require.Equal(t, string(body), string(got))
}

// 错误响应体（没有 data 数组）必须原样透传，不能被当成生图结果处理。
func TestRewriteOpenAIImagesToStorageURL_PassthroughErrorBody(t *testing.T) {
	svc := &OpenAIGatewayService{}
	svc.SetImageStorageResolver(func() (*ImageResultUploader, bool) {
		return &ImageResultUploader{}, true
	})
	c := imageURLReturnContext(t, PlatformOpenAI, true)
	body := []byte(`{"error":{"type":"invalid_request_error","message":"bad prompt"}}`)
	got := svc.rewriteOpenAIImagesToStorageURL(t.Context(), c, &OpenAIImagesRequest{ResponseFormat: "url"}, body)
	require.Equal(t, string(body), string(got))
}

// 平台被改成不支持的平台时，管理端归一化必须把开关压回 false。
func TestSanitizeGroupImageReturnURL(t *testing.T) {
	for _, tc := range []struct {
		platform string
		want     bool
	}{
		{PlatformOpenAI, true},
		{PlatformGemini, true},
		{PlatformGrok, false},
		{PlatformAnthropic, false},
	} {
		g := &Group{Platform: tc.platform, ImageReturnURL: true}
		sanitizeGroupImageReturnURL(g)
		require.Equal(t, tc.want, g.ImageReturnURL, "platform=%s", tc.platform)
	}
}
