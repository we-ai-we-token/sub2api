package handler

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// 二开：Gemini 原生生图保留上游 token 口径，但参考图 base64 不再按 len/4 折算成输入 token；
// 估算用映射前的请求模型（估算器自己解析映射）。
func TestGeminiNativeInflightEstimate_StripsReferenceImageBytes(t *testing.T) {
	refImage := string(bytes.Repeat([]byte("A"), 4<<20))
	withRefImage := []byte(`{"contents":[{"parts":[{"text":"edit"},{"inlineData":{"mimeType":"image/png","data":"` + refImage + `"}},{"inline_data":{"mime_type":"image/png","data":"` + refImage + `"}}]}],"generationConfig":{"maxOutputTokens":2048}}`)

	req := geminiNativeInflightEstimate("my-banana", "gemini-3-pro-image-preview", withRefImage)
	require.Equal(t, service.InflightEstimateToken, req.Kind, "keep upstream token kind: exact for per-request/token cards")
	require.Equal(t, "my-banana", req.Model, "estimate on the requested model like every other handler")
	require.Equal(t, 2048, req.MaxTokens)
	require.Less(t, req.BodyBytes, 1024, "reference image bytes must not drive the estimate")

	// 非生图模型维持上游行为（视觉输入仍按字节估算），只是模型名改用请求模型。
	text := []byte(`{"contents":[{"parts":[{"text":"describe"},{"inlineData":{"mimeType":"image/png","data":"AAAA"}}]}]}`)
	req = geminiNativeInflightEstimate("gemini-2.5-pro", "gemini-2.5-pro", text)
	require.Equal(t, tokenInflightEstimate("gemini-2.5-pro", text), req)
}

// 二开：gemini 分组的 /v1/images/*（GeminiImages）同样接上游在途预留，并发透支会被拦下。
func TestGeminiImages_RejectsWhenInflightExceedsBalance(t *testing.T) {
	cache := newHandlerInflightCache(1.5)
	cfg := &config.Config{}
	cfg.Billing.InflightReservation = config.InflightReservationConfig{Enabled: true, TTLSeconds: 60}
	billingCache := service.NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	billing := service.NewBillingService(cfg, nil)
	gw := service.NewOpenAIGatewayService(
		nil, nil, nil, nil, nil, nil, nil, cfg, nil, nil, billing, nil, billingCache,
		nil, nil, nil, nil, service.NewModelPricingResolver(nil, billing), nil, nil, nil, nil,
	)
	h := newGeminiImagesTestHandler()
	h.gatewayService = gw
	h.billingCacheService = billingCache

	c, rec := geminiImagesTestContext(t, []byte(`{"model":"gemini-2.5-flash-image","prompt":"draw","n":1}`))
	apiKeyVal, _ := c.Get(string(middleware2.ContextKeyAPIKey))
	apiKey, ok := apiKeyVal.(*service.APIKey)
	require.True(t, ok)
	price := 1.0
	apiKey.User.Balance = 1.5
	apiKey.Group.RateMultiplier = 1
	apiKey.Group.ImagePrice1K, apiKey.Group.ImagePrice2K, apiKey.Group.ImagePrice4K = &price, &price, &price
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: apiKey.User.ID})

	// 同一用户已有一个在途请求占着 $1，余额 $1.5 不够再预留一张 $1 的图。
	held, err := billingCache.ReserveInflight(context.Background(), apiKey.User, apiKey.Group, nil, 1.0)
	require.NoError(t, err)
	defer held.HandlerDone()

	h.GeminiImages(c)

	require.NotEqual(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "balance")
	require.Equal(t, 1, cache.count(), "rejected request must not leave a reservation behind")
}
