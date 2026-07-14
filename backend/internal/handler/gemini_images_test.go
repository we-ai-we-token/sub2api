package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func newGeminiImagesTestHandler() *OpenAIGatewayHandler {
	return &OpenAIGatewayHandler{
		gatewayService:      &service.OpenAIGatewayService{},
		geminiCompatService: &service.GeminiMessagesCompatService{},
		billingCacheService: &service.BillingCacheService{},
		apiKeyService:       &service.APIKeyService{},
		concurrencyHelper:   &ConcurrencyHelper{concurrencyService: &service.ConcurrencyService{}},
	}
}

func geminiImagesTestContext(t *testing.T, body []byte) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	// GroupID 置空：跳过渠道映射的 DB 依赖（映射结果 = 请求模型原样）
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		ID:    222,
		Group: &service.Group{ID: 111, Platform: service.PlatformGemini, AllowImageGeneration: true},
		User:  &service.User{ID: 333},
	})
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 333, Concurrency: 1})
	return c, rec
}

func TestGeminiImagesRejectsStream(t *testing.T) {
	c, rec := geminiImagesTestContext(t, []byte(`{"model":"gemini-2.5-flash-image","prompt":"draw","stream":true}`))
	h := newGeminiImagesTestHandler()
	h.GeminiImages(c)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "stream")
}

func TestGeminiImagesRejectsDisabledGroup(t *testing.T) {
	c, rec := geminiImagesTestContext(t, []byte(`{"model":"gemini-2.5-flash-image","prompt":"draw"}`))
	apiKeyVal, _ := c.Get(string(middleware2.ContextKeyAPIKey))
	apiKeyVal.(*service.APIKey).Group.AllowImageGeneration = false
	h := newGeminiImagesTestHandler()
	h.GeminiImages(c)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, "permission_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
}

func TestGeminiImagesRejectsNonGeminiMappedModel(t *testing.T) {
	// 请求模型 gpt-image-1，无渠道映射（GroupID 为空）→ 映射后仍为 gpt-image-1 → 404
	c, rec := geminiImagesTestContext(t, []byte(`{"model":"gpt-image-1","prompt":"draw"}`))
	h := newGeminiImagesTestHandler()
	h.GeminiImages(c)
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Contains(t, rec.Body.String(), "gpt-image-1")
}

func TestBuildGeminiImagesAccountSelectionProvidesWaitPlan(t *testing.T) {
	account := &service.Account{ID: 5, Concurrency: 2}

	sel := buildGeminiImagesAccountSelection(account, nil)
	require.NotNil(t, sel.WaitPlan, "缺少 WaitPlan 会被 acquireResponsesAccountSlot 直接 503")
	require.Equal(t, int64(5), sel.WaitPlan.AccountID)
	require.Equal(t, 2, sel.WaitPlan.MaxConcurrency)
	require.Positive(t, sel.WaitPlan.Timeout)
	require.Positive(t, sel.WaitPlan.MaxWaiting)

	cfg := &config.Config{}
	cfg.Gateway.Scheduling.FallbackWaitTimeout = 7 * time.Second
	cfg.Gateway.Scheduling.FallbackMaxWaiting = 9
	sel = buildGeminiImagesAccountSelection(account, cfg)
	require.Equal(t, 7*time.Second, sel.WaitPlan.Timeout)
	require.Equal(t, 9, sel.WaitPlan.MaxWaiting)
}
