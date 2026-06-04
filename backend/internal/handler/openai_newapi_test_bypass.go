package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

const (
	newAPITestBypassModel = "gpt-image-2"
	newAPITestBypassText  = "hi"
)

func (h *OpenAIGatewayHandler) maybeBypassNewAPITestChatCompletions(c *gin.Context, body []byte, reqModel string, reqStream bool, reqLog *zap.Logger) bool {
	if !h.newAPITestBypassEnabled() || !isNewAPITestBypassModel(reqModel) || !isNewAPITestChatCompletionsBody(body) {
		return false
	}
	if reqLog != nil {
		reqLog.Info("openai.newapi_test_bypass",
			zap.String("endpoint", EndpointChatCompletions),
			zap.String("model", reqModel),
			zap.Bool("stream", reqStream),
		)
	}
	writeNewAPITestBypassChatCompletions(c, reqModel, reqStream)
	return true
}

func (h *OpenAIGatewayHandler) maybeBypassNewAPITestResponses(c *gin.Context, body []byte, reqModel string, reqStream bool, reqLog *zap.Logger) bool {
	if !h.newAPITestBypassEnabled() || !isNewAPITestBypassModel(reqModel) || !isNewAPITestResponsesBody(body) {
		return false
	}
	if reqLog != nil {
		reqLog.Info("openai.newapi_test_bypass",
			zap.String("endpoint", EndpointResponses),
			zap.String("model", reqModel),
			zap.Bool("stream", reqStream),
		)
	}
	writeNewAPITestBypassResponses(c, reqModel, reqStream)
	return true
}

func (h *OpenAIGatewayHandler) newAPITestBypassEnabled() bool {
	return h != nil && h.cfg != nil && h.cfg.Gateway.NewAPITestBypassEnabled
}

func isNewAPITestBypassModel(model string) bool {
	return strings.EqualFold(strings.TrimSpace(model), newAPITestBypassModel)
}

func isNewAPITestChatCompletionsBody(body []byte) bool {
	if !gjson.ValidBytes(body) {
		return false
	}
	messages := gjson.GetBytes(body, "messages")
	if !messages.IsArray() || len(messages.Array()) != 1 {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(gjson.GetBytes(body, "messages.0.role").String()), "user") &&
		extractNewAPITestText(gjson.GetBytes(body, "messages.0.content")) == newAPITestBypassText
}

func isNewAPITestResponsesBody(body []byte) bool {
	if !gjson.ValidBytes(body) {
		return false
	}
	input := gjson.GetBytes(body, "input")
	if input.IsArray() {
		items := input.Array()
		if len(items) != 1 {
			return false
		}
		return strings.EqualFold(strings.TrimSpace(gjson.GetBytes(body, "input.0.role").String()), "user") &&
			extractNewAPITestText(gjson.GetBytes(body, "input.0.content")) == newAPITestBypassText
	}
	return extractNewAPITestText(input) == newAPITestBypassText
}

func extractNewAPITestText(value gjson.Result) string {
	if value.Type == gjson.String {
		return strings.ToLower(strings.TrimSpace(value.String()))
	}
	if value.IsArray() {
		items := value.Array()
		if len(items) != 1 {
			return ""
		}
		item := items[0]
		itemType := strings.ToLower(strings.TrimSpace(item.Get("type").String()))
		switch itemType {
		case "text", "input_text":
			return strings.ToLower(strings.TrimSpace(item.Get("text").String()))
		default:
			return ""
		}
	}
	return ""
}

func writeNewAPITestBypassChatCompletions(c *gin.Context, model string, stream bool) {
	if stream {
		id := "chatcmpl-newapi-test"
		writeSSEJSON(c, map[string]any{
			"id":      id,
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"model":   model,
			"choices": []map[string]any{
				{
					"index":         0,
					"delta":         map[string]any{"content": "ok"},
					"finish_reason": nil,
				},
			},
		})
		writeSSEJSON(c, map[string]any{
			"id":      id,
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"model":   model,
			"choices": []map[string]any{
				{
					"index":         0,
					"delta":         map[string]any{},
					"finish_reason": "stop",
				},
			},
		})
		fmt.Fprint(c.Writer, "data: [DONE]\n\n") //nolint:errcheck
		c.Writer.Flush()
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":      "chatcmpl-newapi-test",
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []gin.H{
			{
				"index": 0,
				"message": gin.H{
					"role":    "assistant",
					"content": "ok",
				},
				"finish_reason": "stop",
			},
		},
		"usage": gin.H{
			"prompt_tokens":     1,
			"completion_tokens": 1,
			"total_tokens":      2,
		},
	})
}

func writeNewAPITestBypassResponses(c *gin.Context, model string, stream bool) {
	response := map[string]any{
		"id":         "resp_newapi_test",
		"object":     "response",
		"created_at": time.Now().Unix(),
		"status":     "completed",
		"model":      model,
		"output": []map[string]any{
			{
				"type":   "message",
				"role":   "assistant",
				"status": "completed",
				"content": []map[string]any{
					{
						"type": "output_text",
						"text": "ok",
					},
				},
			},
		},
		"usage": map[string]any{
			"input_tokens":  1,
			"output_tokens": 1,
			"total_tokens":  2,
		},
	}
	if stream {
		writeNamedSSEJSON(c, "response.completed", map[string]any{
			"type":     "response.completed",
			"response": response,
		})
		fmt.Fprint(c.Writer, "data: [DONE]\n\n") //nolint:errcheck
		c.Writer.Flush()
		return
	}
	c.JSON(http.StatusOK, response)
}

func writeSSEJSON(c *gin.Context, payload map[string]any) {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	raw, _ := json.Marshal(payload)
	fmt.Fprintf(c.Writer, "data: %s\n\n", raw) //nolint:errcheck
	c.Writer.Flush()
}

func writeNamedSSEJSON(c *gin.Context, event string, payload map[string]any) {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	raw, _ := json.Marshal(payload)
	fmt.Fprintf(c.Writer, "event: %s\n", event) //nolint:errcheck
	fmt.Fprintf(c.Writer, "data: %s\n\n", raw)  //nolint:errcheck
	c.Writer.Flush()
}
