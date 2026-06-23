//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCollectOpenAIResponseImageOutputQuality_FromOutputItem(t *testing.T) {
	body := []byte(`{
		"output": [
			{"type": "image_generation_call", "id": "ig_1", "result": "aGVsbG8=", "size": "1024x1024", "quality": "high"}
		]
	}`)
	require.Equal(t, "high", collectOpenAIResponseImageOutputQualityFromJSONBytes(body))
}

func TestCollectOpenAIResponseImageOutputQuality_FromToolsEcho(t *testing.T) {
	// 图片项本身没有 quality，但 tools 回显了 quality。
	body := []byte(`{
		"tools": [
			{"type": "image_generation", "model": "gpt-image-2", "size": "1024x1024", "quality": "low"}
		],
		"output": [
			{"type": "image_generation_call", "id": "ig_1", "result": "aGVsbG8="}
		]
	}`)
	require.Equal(t, "low", collectOpenAIResponseImageOutputQualityFromJSONBytes(body))
}

func TestCollectOpenAIResponseImageOutputQuality_Missing(t *testing.T) {
	body := []byte(`{
		"output": [
			{"type": "image_generation_call", "id": "ig_1", "result": "aGVsbG8="}
		]
	}`)
	require.Equal(t, "", collectOpenAIResponseImageOutputQualityFromJSONBytes(body))
}

func TestCollectOpenAIImageOutputQuality_FromSSE(t *testing.T) {
	body := "data: {\"type\":\"response.created\",\"response\":{\"tools\":[{\"type\":\"image_generation\",\"model\":\"gpt-image-2\",\"quality\":\"medium\",\"size\":\"1024x1024\"}]}}\n\n" +
		"data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"ig_1\",\"type\":\"image_generation_call\",\"result\":\"aGVsbG8=\"}}\n\n" +
		"data: [DONE]\n\n"
	require.Equal(t, "medium", collectOpenAIImageOutputQualityFromSSEBody(body))
}
