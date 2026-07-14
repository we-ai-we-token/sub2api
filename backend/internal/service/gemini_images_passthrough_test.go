//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsGeminiImageGenerationModel(t *testing.T) {
	cases := []struct {
		model string
		want  bool
	}{
		{"gemini-2.5-flash-image", true},
		{"gemini-3.1-flash-image", true},
		{"Gemini-2.5-Flash-Image-Preview", true},
		{" gemini-2.5-flash-image ", true},
		{"gemini-2.5-flash", false}, // 不含 image
		{"gpt-image-2", false},
		{"grok-imagine", false},
		{"imagen-3", false}, // 无 gemini- 前缀
		{"", false},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, IsGeminiImageGenerationModel(tc.model), "model=%q", tc.model)
	}
}

func TestValidateOpenAIImagesModelAcceptsGeminiImageModels(t *testing.T) {
	require.NoError(t, validateOpenAIImagesModel("gemini-2.5-flash-image"))
	require.Error(t, validateOpenAIImagesModel("gemini-2.5-flash"))
	require.Error(t, validateOpenAIImagesModel("dall-e-3"))
}
