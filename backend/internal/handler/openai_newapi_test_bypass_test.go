package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAINewAPITestBypassChatCompletions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-image-2","messages":[{"role":"user","content":"hi"}],"max_tokens":16,"stream":false}`)

	require.True(t, isNewAPITestChatCompletionsBody(body))

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, EndpointChatCompletions, strings.NewReader(string(body)))
	h := &OpenAIGatewayHandler{cfg: &config.Config{}}
	h.cfg.Gateway.NewAPITestBypassEnabled = true

	require.True(t, h.maybeBypassNewAPITestChatCompletions(c, body, "gpt-image-2", false, nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "chat.completion", gjson.GetBytes(rec.Body.Bytes(), "object").String())
	require.Equal(t, "ok", gjson.GetBytes(rec.Body.Bytes(), "choices.0.message.content").String())
}

func TestOpenAINewAPITestBypassChatCompletionsStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-image-2","messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}],"stream":true}`)

	require.True(t, isNewAPITestChatCompletionsBody(body))

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, EndpointChatCompletions, strings.NewReader(string(body)))
	h := &OpenAIGatewayHandler{cfg: &config.Config{}}
	h.cfg.Gateway.NewAPITestBypassEnabled = true

	require.True(t, h.maybeBypassNewAPITestChatCompletions(c, body, "gpt-image-2", true, nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "text/event-stream")
	require.Contains(t, rec.Body.String(), `"object":"chat.completion.chunk"`)
	require.Contains(t, rec.Body.String(), "data: [DONE]")
}

func TestOpenAINewAPITestBypassResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-image-2","input":[{"role":"user","content":"hi"}],"stream":false}`)

	require.True(t, isNewAPITestResponsesBody(body))

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, EndpointResponses, strings.NewReader(string(body)))
	h := &OpenAIGatewayHandler{cfg: &config.Config{}}
	h.cfg.Gateway.NewAPITestBypassEnabled = true

	require.True(t, h.maybeBypassNewAPITestResponses(c, body, "gpt-image-2", false, nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "response", gjson.GetBytes(rec.Body.Bytes(), "object").String())
	require.Equal(t, "ok", gjson.GetBytes(rec.Body.Bytes(), "output.0.content.0.text").String())
}

func TestOpenAINewAPITestBypassResponsesStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-image-2","input":[{"role":"user","content":[{"type":"input_text","text":"hi"}]}],"stream":true}`)

	require.True(t, isNewAPITestResponsesBody(body))

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, EndpointResponses, strings.NewReader(string(body)))
	h := &OpenAIGatewayHandler{cfg: &config.Config{}}
	h.cfg.Gateway.NewAPITestBypassEnabled = true

	require.True(t, h.maybeBypassNewAPITestResponses(c, body, "gpt-image-2", true, nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "text/event-stream")
	require.Contains(t, rec.Body.String(), "event: response.completed")
	require.Contains(t, rec.Body.String(), "data: [DONE]")
}

func TestOpenAINewAPITestBypassRequiresSwitchModelAndProbeBody(t *testing.T) {
	h := &OpenAIGatewayHandler{cfg: &config.Config{}}
	h.cfg.Gateway.NewAPITestBypassEnabled = true

	tests := []struct {
		name  string
		model string
		body  []byte
	}{
		{
			name:  "disabled",
			model: "gpt-image-2",
			body:  []byte(`{"model":"gpt-image-2","messages":[{"role":"user","content":"hi"}]}`),
		},
		{
			name:  "different model",
			model: "gpt-4o",
			body:  []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`),
		},
		{
			name:  "real prompt",
			model: "gpt-image-2",
			body:  []byte(`{"model":"gpt-image-2","messages":[{"role":"user","content":"draw a cat"}]}`),
		},
		{
			name:  "multi turn",
			model: "gpt-image-2",
			body:  []byte(`{"model":"gpt-image-2","messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"ok"}]}`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, EndpointChatCompletions, strings.NewReader(string(tt.body)))
			if tt.name == "disabled" {
				h.cfg.Gateway.NewAPITestBypassEnabled = false
				defer func() { h.cfg.Gateway.NewAPITestBypassEnabled = true }()
			}

			require.False(t, h.maybeBypassNewAPITestChatCompletions(c, tt.body, tt.model, false, nil))
			require.Equal(t, http.StatusOK, rec.Code)
			require.Empty(t, rec.Body.String())
		})
	}
}
