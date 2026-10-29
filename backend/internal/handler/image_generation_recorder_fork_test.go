package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
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

// 上游 v0.2.18（2b1f12938）起 Gemini 原生流客户端中途断开会按已收集用量计费并返回
// (result{ClientDisconnect:true}, nil)；这类请求已计费，但生图记录不能记成成功，
// 否则会混进运营报表只取 success 行的分段耗时。
func TestImageGenerationRecord_ClientDisconnectIsNotSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	newCtx := func() *gin.Context {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-3-pro-image-preview:streamGenerateContent", nil)
		c.Status(http.StatusOK)
		c.Writer.WriteHeaderNow()
		return c
	}

	c := newCtx()
	st := newImageGenerationRecordState(c, nil, service.PlatformGemini, "gemini-3-pro-image-preview", true, time.Now())
	st.noteSuccessNative(&service.ForwardResult{ClientDisconnect: true, UpstreamModel: "gemini-3-pro-image-preview", ImageCount: 0})
	input := buildImageGenerationRecordInput(c, st)
	require.False(t, input.Success)
	require.Equal(t, "client_canceled", input.ErrorType)
	require.Equal(t, http.StatusOK, input.StatusCode)
	require.Equal(t, "gemini-3-pro-image-preview", input.UpstreamModel, "summary fields are kept for the billed request")

	c = newCtx()
	st = newImageGenerationRecordState(c, nil, service.PlatformOpenAI, "gpt-image-2", true, time.Now())
	st.noteSuccess(&service.OpenAIForwardResult{ClientDisconnect: true, ImageCount: 1})
	input = buildImageGenerationRecordInput(c, st)
	require.False(t, input.Success)
	require.Equal(t, "client_canceled", input.ErrorType)
	require.Equal(t, 1, input.ImageCount)

	c = newCtx()
	st = newImageGenerationRecordState(c, nil, service.PlatformGemini, "gemini-3-pro-image-preview", false, time.Now())
	st.noteSuccessNative(&service.ForwardResult{ImageCount: 1})
	input = buildImageGenerationRecordInput(c, st)
	require.True(t, input.Success)
	require.Empty(t, input.ErrorType)
}
