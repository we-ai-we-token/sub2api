//go:build unit

package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 二开的生图链路（OAuth codex images 端点、gemini 生图透传）是上游没有的代码，
// 上游给 OpenAIForwardResult 加 UpstreamHeaders 时自然不会带上它们，而 usage_logs
// 的 upstream_request_id 正是从这个字段取值 —— 漏了就是该链路的上游请求标识恒为 NULL，
// 编译期和现有用例都发现不了。
//
// 判据：同一个 OpenAIForwardResult 字面量里只要设了 ResponseHeaders（说明手上有上游响应头），
// 就必须同时设 UpstreamHeaders。本地模拟、无真实上游响应的场景不会设 ResponseHeaders，
// 因此不会被这条规则误伤。
//
// WS 链路不在范围内：usageUpstreamRequestIDPtr 对 wsMode 直接返回 nil，补了也是死代码。
func TestImageForwardResultsCarryUpstreamHeaders(t *testing.T) {
	files := []string{
		"openai_images.go",
		"openai_images_responses.go",
		"openai_images_responses_upstream.go",
		"gemini_images_passthrough.go",
	}

	for _, name := range files {
		path := filepath.Join(".", name)
		raw, err := os.ReadFile(path)
		require.NoErrorf(t, err, "读取 %s 失败，文件被改名或删除时请同步更新本用例", name)
		src := string(raw)

		for _, lit := range openAIForwardResultLiterals(src) {
			if !strings.Contains(lit.body, "ResponseHeaders:") {
				continue
			}
			require.Containsf(t, lit.body, "UpstreamHeaders:",
				"%s:%d 的 OpenAIForwardResult 设了 ResponseHeaders 却没设 UpstreamHeaders，"+
					"该链路的 usage_logs.upstream_request_id 会恒为 NULL", name, lit.line)
		}
	}
}

type forwardResultLiteral struct {
	line int
	body string
}

// openAIForwardResultLiterals 用花括号配对切出每个 &OpenAIForwardResult{...} 的字面量体。
func openAIForwardResultLiterals(src string) []forwardResultLiteral {
	const marker = "&OpenAIForwardResult{"
	var out []forwardResultLiteral
	for offset := 0; ; {
		idx := strings.Index(src[offset:], marker)
		if idx < 0 {
			return out
		}
		start := offset + idx + len(marker)
		depth, end := 1, start
		for depth > 0 && end < len(src) {
			switch src[end] {
			case '{':
				depth++
			case '}':
				depth--
			}
			end++
		}
		out = append(out, forwardResultLiteral{
			line: strings.Count(src[:offset+idx], "\n") + 1,
			body: src[start : end-1],
		})
		offset = end
	}
}
