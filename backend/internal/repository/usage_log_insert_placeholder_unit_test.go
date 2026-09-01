//go:build unit

package repository

import (
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestUsageLogInsertStaticPlaceholdersMatchArgTypes pins the hand-written
// `$1..$N` VALUES lists in usage_log_repo_insert.go to usageLogInsertArgTypes.
//
// 为什么要读源码而不是执行 SQL：出事的正是**静态文本**。createSingle 里有两处
// 手写的 `$1..$N`，而 usageLogInsertArgTypes 与 INSERT 列清单是逐行列表。
// 上游和本地二开各自加列时，两边都会把末位 `$N` 各自 +1；git 认为改的是同一行
// 的不同版本，会"干净地"选一侧，结果占位符比列数少一个——go build、go vet、
// gofmt 全都看不出来，只有真正插入时才炸。批量插入路径是动态拼的，不受影响，
// 所以现有那些"批量 args 数量对得上"的断言也盖不住这里。
//
// 2026-09-02 合并 v0.1.185 时再次命中：base $59，本地 +image_quality=$60，
// 上游 +requested_reasoning_effort/native_compaction_v2=$61，正确答案是 $62。
func TestUsageLogInsertStaticPlaceholdersMatchArgTypes(t *testing.T) {
	src, err := os.ReadFile("usage_log_repo_insert.go")
	require.NoError(t, err, "read insert source")

	valuesBlocks := regexp.MustCompile(`(?s)\) VALUES \(\n(.*?)\n\t*\)`).FindAllStringSubmatch(string(src), -1)
	require.NotEmpty(t, valuesBlocks, "expected at least one static VALUES list")

	placeholder := regexp.MustCompile(`\$(\d+)`)
	for i, block := range valuesBlocks {
		nums := make([]int, 0, len(usageLogInsertArgTypes))
		for _, m := range placeholder.FindAllStringSubmatch(block[1], -1) {
			n, convErr := strconv.Atoi(m[1])
			require.NoError(t, convErr)
			nums = append(nums, n)
		}
		sort.Ints(nums)

		require.Len(t, nums, len(usageLogInsertArgTypes),
			"static VALUES list #%d must bind exactly one placeholder per arg type", i+1)
		for want, got := range nums {
			require.Equal(t, want+1, got,
				"static VALUES list #%d must be a contiguous $1..$%d run", i+1, len(usageLogInsertArgTypes))
		}
	}
}

// TestUsageLogInsertColumnsMatchArgTypes pins the INSERT column list to the arg-type
// table. Together with the placeholder test above this closes the loop
// 列数 == 占位符数 == argTypes 数，which is the invariant every upstream merge
// that touches usage_logs has to preserve.
func TestUsageLogInsertColumnsMatchArgTypes(t *testing.T) {
	src, err := os.ReadFile("usage_log_repo_insert.go")
	require.NoError(t, err, "read insert source")

	m := regexp.MustCompile(`(?s)INSERT INTO usage_logs \(\n(.*?)\n\t*\) `).FindStringSubmatch(string(src))
	require.Len(t, m, 2, "expected an INSERT INTO usage_logs column list")

	cols := regexp.MustCompile(`[\s,]+`).Split(strings.TrimSpace(m[1]), -1)
	kept := cols[:0]
	for _, c := range cols {
		if c != "" {
			kept = append(kept, c)
		}
	}
	require.Len(t, kept, len(usageLogInsertArgTypes),
		"INSERT column list must have exactly one column per arg type")
}
