package service

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// 白名单口径的回归护栏。判据只有一条：上游能否已经收到并处理了这次请求。
func TestIsRetryableOpenAIImagesTransportError(t *testing.T) {
	retryable := []string{
		// 线上真实样本（已脱去具体 host）
		`upstream request failed: Post "https://x.openai.azure.com/openai/v1/images/edits": net/http: TLS handshake timeout`,
		`Post "https://x.openai.azure.com/openai/v1/images/edits": write tcp 172.18.0.4:43846->23.139.220.177:35933: write: connection reset by peer`,
		`upstream request failed: Post "https://x/openai/v1/images/edits": write tcp 172.18.0.4:1->2.2.2.2:3: write: broken pipe`,
	}
	for _, m := range retryable {
		require.True(t, IsRetryableOpenAIImagesTransportError(errors.New(m)), "应可重试: %s", m)
	}

	notRetryable := []string{
		// 请求已发出，上游可能已出图并计费
		`upstream request failed: Post "https://x/openai/v1/images/edits": unexpected EOF`,
		`Post "https://x/openai/v1/images/edits": read tcp 172.18.0.4:56134->23.139.220.3:36263: read: connection reset by peer`,
		// 阶段不可分辨
		`Post "https://x/openai/v1/images/edits": context deadline exceeded`,
		`Post "https://x/openai/v1/images/edits": dial tcp 1.1.1.1:443: i/o timeout`,
		`net/http: timeout awaiting response headers`,
		// h2 连接丢失：生图已锁 HTTP/1.1，不该再出现；即使出现也不重试
		`Post "https://x/openai/v1/images/edits": http2: client connection lost`,
		// 其它
		`Post "https://x/openai/v1/images/edits": EOF`,
		`some unrelated failure`,
		``,
	}
	for _, m := range notRetryable {
		require.False(t, IsRetryableOpenAIImagesTransportError(errors.New(m)), "不应重试: %s", m)
	}

	require.False(t, IsRetryableOpenAIImagesTransportError(nil))
}

// read 方向一票否决必须压过 write 的匹配，顺序写反就会把「上游可能已出图」的
// 错误也重试掉。
func TestTransportRetryReadDirectionVetoesWrite(t *testing.T) {
	mixed := errors.New(`write tcp 1.1.1.1:1->2.2.2.2:2: read tcp 3.3.3.3:3->4.4.4.4:4: read: connection reset by peer`)
	require.False(t, IsRetryableOpenAIImagesTransportError(mixed))
}

// 包装过的错误同样要能识别（handler 侧拿到的是层层 fmt.Errorf 包过的）。
func TestTransportRetryMatchesWrappedErrors(t *testing.T) {
	inner := errors.New("net/http: TLS handshake timeout")
	wrapped := fmt.Errorf("forward images: %w", fmt.Errorf("upstream request failed: %w", inner))
	require.True(t, IsRetryableOpenAIImagesTransportError(wrapped))
}
