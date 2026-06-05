package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIImagesInputImagesRetryState(t *testing.T) {
	state := newOpenAIImagesInputImagesRetryState(3)

	firstErr := &service.OpenAIImagesUpstreamError{
		StatusCode: http.StatusTooManyRequests,
		ErrorType:  "input-images",
		Code:       "rate_limit_exceeded",
		Message:    "Please try again in 15ms.",
	}
	firstFailover := &service.UpstreamFailoverError{StatusCode: firstErr.StatusCode, ResponseBody: []byte(firstErr.Error())}
	state.remember(firstFailover)
	retry, delay := state.consumeForSameAccountRetry(firstErr, 500*time.Millisecond)
	require.True(t, retry)
	require.Equal(t, 500*time.Millisecond, delay)
	require.Nil(t, state.lastFailoverErr, "retry must discard the previous error before the next attempt")
	require.Equal(t, 1, state.cyclesStarted)

	secondErr := &service.OpenAIImagesUpstreamError{
		StatusCode: http.StatusTooManyRequests,
		ErrorType:  "input-images",
		Code:       "rate_limit_exceeded",
		Message:    "Please try again in 20ms.",
	}
	secondFailover := &service.UpstreamFailoverError{StatusCode: secondErr.StatusCode, ResponseBody: []byte(secondErr.Error())}
	state.remember(secondFailover)
	switchAccount := state.consumeForAccountSwitch(secondErr)
	require.True(t, switchAccount)
	require.Nil(t, state.lastFailoverErr, "switch retry must discard the previous error before selecting another account")
	require.Equal(t, 1, state.cyclesStarted)

	state.remember(secondFailover)
	retry, delay = state.consumeForSameAccountRetry(secondErr, 500*time.Millisecond)
	require.True(t, retry)
	require.Equal(t, 500*time.Millisecond, delay)
	require.Equal(t, 2, state.cyclesStarted)

	state.remember(secondFailover)
	switchAccount = state.consumeForAccountSwitch(secondErr)
	require.True(t, switchAccount)
	require.Nil(t, state.lastFailoverErr)
	require.Equal(t, 2, state.cyclesStarted)

	state.remember(secondFailover)
	retry, delay = state.consumeForSameAccountRetry(secondErr, 500*time.Millisecond)
	require.True(t, retry)
	require.Equal(t, 500*time.Millisecond, delay)
	require.Equal(t, 3, state.cyclesStarted)

	state.remember(secondFailover)
	retry, _ = state.consumeForSameAccountRetry(secondErr, 500*time.Millisecond)
	require.False(t, retry)
	require.Same(t, secondFailover, state.lastFailoverErr, "exhausted retry keeps final error for downstream response")
}

func TestOpenAIImagesTransientRetryStateAllowsThreeRetryCycles(t *testing.T) {
	state := newOpenAIImagesInputImagesRetryState(3)
	err := &service.OpenAIImagesUpstreamError{
		StatusCode: http.StatusBadGateway,
		ErrorType:  "service_unavailable_error",
		Code:       "server_is_overloaded",
		Message:    "Our servers are currently overloaded. Please try again later.",
	}
	failover := &service.UpstreamFailoverError{StatusCode: err.StatusCode, ResponseBody: []byte(err.Error())}

	for i := 1; i <= 3; i++ {
		state.remember(failover)
		retry, delay := state.consumeForSameAccountRetry(err, 500*time.Millisecond)
		require.True(t, retry)
		require.Equal(t, 500*time.Millisecond, delay)
		require.Nil(t, state.lastFailoverErr)
		require.Equal(t, i, state.cyclesStarted)
	}

	state.remember(failover)
	retry, _ := state.consumeForSameAccountRetry(err, 500*time.Millisecond)
	require.False(t, retry)
	require.Same(t, failover, state.lastFailoverErr)
}

func TestOpenAIImagesInputImagesRetryStateIgnoresOtherErrors(t *testing.T) {
	state := newOpenAIImagesInputImagesRetryState(3)
	err := &service.OpenAIImagesUpstreamError{
		StatusCode: http.StatusTooManyRequests,
		ErrorType:  "requests",
		Code:       "rate_limit_exceeded",
		Message:    "Rate limit reached on requests per min.",
	}
	failover := &service.UpstreamFailoverError{StatusCode: err.StatusCode, ResponseBody: []byte(err.Error())}
	state.remember(failover)

	retry, _ := state.consumeForSameAccountRetry(err, 500*time.Millisecond)
	require.False(t, retry)
	require.Same(t, failover, state.lastFailoverErr)
	require.False(t, state.consumeForAccountSwitch(err))
	require.Same(t, failover, state.lastFailoverErr)
}

func TestOpenAIImagesSpecialTransientRetryGate(t *testing.T) {
	overloadedErr := &service.OpenAIImagesUpstreamError{
		StatusCode: http.StatusBadGateway,
		ErrorType:  "service_unavailable_error",
		Code:       "server_is_overloaded",
		Message:    "Our servers are currently overloaded. Please try again later.",
	}
	unrelatedErr := &service.OpenAIImagesUpstreamError{
		StatusCode: http.StatusTooManyRequests,
		ErrorType:  "requests",
		Code:       "rate_limit_exceeded",
		Message:    "Rate limit reached on requests per min.",
	}

	require.True(t, shouldUseOpenAIImagesSpecialTransientRetry(overloadedErr))
	require.False(t, shouldUseOpenAIImagesSpecialTransientRetry(unrelatedErr))
}

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
