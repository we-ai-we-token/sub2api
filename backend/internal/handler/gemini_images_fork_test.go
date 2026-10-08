package handler

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// 2.1 必须能穿过 handler 里的两道名字闸（解析闸 400、映射闸 404）；之后依赖为空会在别处失败，这里不关心。
func TestGeminiImagesNanoBanana21PassesNameGates(t *testing.T) {
	c, rec := geminiImagesTestContext(t, []byte(`{"model":"gemini-nano-banana-2.1","prompt":"draw"}`))
	h := newGeminiImagesTestHandler()
	func() {
		defer func() { _ = recover() }()
		h.GeminiImages(c)
	}()
	require.NotEqual(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.NotEqual(t, http.StatusNotFound, rec.Code, rec.Body.String())
	require.NotContains(t, rec.Body.String(), "requires an image model")
	require.NotContains(t, rec.Body.String(), "not available for image generation")
}

func TestGeminiImagesRejectsUnlistedNanoBanana(t *testing.T) {
	c, rec := geminiImagesTestContext(t, []byte(`{"model":"gemini-nano-banana-pro","prompt":"draw"}`))
	h := newGeminiImagesTestHandler()
	h.GeminiImages(c)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "requires an image model")
}
