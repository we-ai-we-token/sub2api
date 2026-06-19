package repository

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestImageModelWhere(t *testing.T) {
	require.Contains(t, imageModelWhere(service.OperationPlatformOpenAI), "gpt-image-2%")
	require.NotContains(t, imageModelWhere(service.OperationPlatformOpenAI), "gemini")
	require.Contains(t, imageModelWhere(service.OperationPlatformGemini), "gemini-%image%")
	require.NotContains(t, imageModelWhere(service.OperationPlatformGemini), "gpt-image")
	both := imageModelWhere("")
	require.Contains(t, both, "gpt-image-2%")
	require.Contains(t, both, "gemini-%image%")
}

func TestBucketIntervalArg(t *testing.T) {
	require.Equal(t, "5 minutes", bucketIntervalArg("5m"))
	require.Equal(t, "1 hour", bucketIntervalArg("1h"))
	require.Equal(t, "1 hour", bucketIntervalArg("garbage"))
}
