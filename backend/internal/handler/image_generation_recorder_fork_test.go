package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 二开生图记录：上游 v0.2.9 起客户端断开统一标 499，无上游上下文时归为 client_canceled。
func TestClassifyImageGenerationErrorType_ClientCanceled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.5-flash-image:generateContent", nil)

	require.Equal(t, "client_canceled", classifyImageGenerationErrorType(c, statusClientClosedRequest, false))
	require.Equal(t, "upstream_error", classifyImageGenerationErrorType(c, statusClientClosedRequest, true),
		"a 499 after real upstream attempts keeps its upstream attribution")
	require.Equal(t, "invalid_request_error", classifyImageGenerationErrorType(c, http.StatusBadRequest, false))
}
