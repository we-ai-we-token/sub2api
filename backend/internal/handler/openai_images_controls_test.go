package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIImagesSchedulerSessionHashIgnoresExplicitSessionSignals(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-image-2","prompt":"draw","prompt_cache_key":"image-session"}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	c.Request.Header.Set("session_id", "header-session")
	c.Request.Header.Set("conversation_id", "conversation-session")

	require.Empty(t, openAIImagesSchedulerSessionHash(c, body))
}

func TestOpenAIGatewayHandlerImages_DisabledGroupRejectsBeforeScheduling(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-image-2","prompt":"draw","size":"1024x1024"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req
	groupID := int64(111)
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		ID:      222,
		GroupID: &groupID,
		Group: &service.Group{
			ID:                   groupID,
			AllowImageGeneration: false,
		},
		User: &service.User{ID: 333},
	})
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 333, Concurrency: 1})

	h := &OpenAIGatewayHandler{
		gatewayService:      &service.OpenAIGatewayService{},
		billingCacheService: &service.BillingCacheService{},
		apiKeyService:       &service.APIKeyService{},
		concurrencyHelper:   &ConcurrencyHelper{concurrencyService: &service.ConcurrencyService{}},
	}

	h.Images(c)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, "permission_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
	require.Contains(t, rec.Body.String(), service.ImageGenerationPermissionMessage())
}

func TestShouldSkipOpenAIImagesForwardFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)

	require.False(t, shouldSkipOpenAIImagesForwardFallback(nil, false))

	t.Run("fresh non-streaming response needs fallback", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)

		require.False(t, shouldSkipOpenAIImagesForwardFallback(c, false))
	})

	t.Run("written non-streaming response skips fallback", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "upstream detail"}})

		require.True(t, shouldSkipOpenAIImagesForwardFallback(c, false))
	})

	t.Run("streaming response still appends terminal error", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Writer.WriteHeader(http.StatusOK)

		require.False(t, shouldSkipOpenAIImagesForwardFallback(c, true))
	})
}
