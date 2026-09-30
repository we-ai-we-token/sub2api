//go:build unit

package handler

import (
	"os"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestInflightReservationForkHooksPresent 钉住二开在上游 handler 文件里的在途预留 hook。
//
// 为什么要读源码：这两处都是往上游那一行里塞参数，合并时若冲突取了上游版本，
// Go 允许结构体字面量省略字段、helper 也仍被测试引用，go build / vet / lint / 行为单测
// 全部沉默——按质量计费分组的显式 quality 会退回按三档最高价预留，Gemini 原生生图的
// 参考图字节又会把预留顶到 200k 输入 token，重新制造「余额不足」误拒。
func TestInflightReservationForkHooksPresent(t *testing.T) {
	images, err := os.ReadFile("openai_images.go")
	require.NoError(t, err)
	require.Regexp(t, regexp.MustCompile(`reserveInflightBalance\([^\n]*InflightEstimateImage[^\n]*ImageQuality: parsed\.Quality`), string(images),
		"openai_images.go must pass the requested quality into the in-flight estimate (see billing_inflight_reservation_fork.go)")

	gemini, err := os.ReadFile("gemini_v1beta_handler.go")
	require.NoError(t, err)
	require.Regexp(t, regexp.MustCompile(`reserveInflightBalance\([^\n]*geminiNativeInflightEstimate\(reqModel, modelName, body\)`), string(gemini),
		"gemini_v1beta_handler.go must estimate via geminiNativeInflightEstimate (see gateway_inflight_reservation_fork.go)")
}
