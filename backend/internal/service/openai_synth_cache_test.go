package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/tidwall/gjson"
)

// TestDeductSynthInstructionsFromUsage 覆盖热/冷缓存与 clamp、cache_creation 溢出。
func TestDeductSynthInstructionsFromUsage(t *testing.T) {
	const n = 4376
	cases := []struct {
		name                                            string
		in                                              OpenAIUsage
		wantInput, wantCacheRead, wantCacheCreate, want int // want = actualInput
	}{
		{
			// 热缓存：注入前缀已被缓存，input 与 cache_read 同步降 N，actualInput 不变。
			name:            "hot_cache",
			in:              OpenAIUsage{InputTokens: 4500, CacheReadInputTokens: 4400},
			wantInput:       124,
			wantCacheRead:   24,
			wantCacheCreate: 0,
			want:            100,
		},
		{
			// 冷缓存：无 cache_read，input 降 N，actualInput 降 N。
			name:            "cold_cache",
			in:              OpenAIUsage{InputTokens: 4400, CacheReadInputTokens: 0},
			wantInput:       24,
			wantCacheRead:   0,
			wantCacheCreate: 0,
			want:            24,
		},
		{
			// N 超过 input：全部 clamp 到 0，不出现负数。
			name:            "clamp_small_input",
			in:              OpenAIUsage{InputTokens: 100, CacheReadInputTokens: 0},
			wantInput:       0,
			wantCacheRead:   0,
			wantCacheCreate: 0,
			want:            0,
		},
		{
			// cache_read 不足，余量从 cache_creation 扣。
			name:            "spill_to_cache_creation",
			in:              OpenAIUsage{InputTokens: 5000, CacheReadInputTokens: 100, CacheCreationInputTokens: 5000},
			wantInput:       624,
			wantCacheRead:   0,
			wantCacheCreate: 724,
			want:            -100, // 624 - 0 - 724 = -100（原始口径本就为 5000-100-5000）
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := tc.in
			deductSynthInstructionsFromUsage(&u, n)
			if u.InputTokens != tc.wantInput {
				t.Errorf("InputTokens = %d, want %d", u.InputTokens, tc.wantInput)
			}
			if u.CacheReadInputTokens != tc.wantCacheRead {
				t.Errorf("CacheReadInputTokens = %d, want %d", u.CacheReadInputTokens, tc.wantCacheRead)
			}
			if u.CacheCreationInputTokens != tc.wantCacheCreate {
				t.Errorf("CacheCreationInputTokens = %d, want %d", u.CacheCreationInputTokens, tc.wantCacheCreate)
			}
			got := u.InputTokens - u.CacheReadInputTokens - u.CacheCreationInputTokens
			if got != tc.want {
				t.Errorf("actualInput = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestDeductSynthInstructionsFromUsage_KeepsCacheWriteTTLBuckets 钉住上游 v0.2.23 的不变量：
// OpenAI 缓存写入的 30m / 12h 两桶互斥且穷尽（30m + 12h == 聚合值）。扣减只动聚合值的话，
// openAICacheCreationTTLCost 会判明细无效、整笔回退到短 TTL 价，usage_logs 的分桶列还会把
// 本该隐藏的注入 token 数漏出来。
func TestDeductSynthInstructionsFromUsage_KeepsCacheWriteTTLBuckets(t *testing.T) {
	const n = 4000
	longPrice := 2.5
	cases := []struct {
		name                         string
		in                           OpenAIUsage
		wantCreate, want30m, want12h int
		wantTTLCostOK                bool
		wantTTLCost                  float64 // shortPrice=1.25, longPrice=2.5
	}{
		{
			// 冷缓存、全部写入 12h：客户自己的 100 个 token 仍按 12h 价计。
			name:          "cold_cache_all_12h",
			in:            OpenAIUsage{InputTokens: 4200, CacheCreationInputTokens: 4100, CacheCreation12hInputTokens: 4100},
			wantCreate:    100,
			want30m:       0,
			want12h:       100,
			wantTTLCostOK: true,
			wantTTLCost:   100 * 2.5,
		},
		{
			name:          "cold_cache_all_30m",
			in:            OpenAIUsage{InputTokens: 4200, CacheCreationInputTokens: 4100, CacheCreation30mInputTokens: 4100},
			wantCreate:    100,
			want30m:       100,
			want12h:       0,
			wantTTLCostOK: true,
			wantTTLCost:   100 * 1.25,
		},
		{
			// 两桶都有：先扣 12h，再扣 30m。
			name:          "mixed_buckets_deduct_12h_first",
			in:            OpenAIUsage{InputTokens: 6000, CacheCreationInputTokens: 5000, CacheCreation30mInputTokens: 2000, CacheCreation12hInputTokens: 3000},
			wantCreate:    1000,
			want30m:       1000,
			want12h:       0,
			wantTTLCostOK: true,
			wantTTLCost:   1000 * 1.25,
		},
		{
			// 热缓存先吃掉一部分，余量才落到缓存写入。
			name:          "cache_read_absorbs_part",
			in:            OpenAIUsage{InputTokens: 9000, CacheReadInputTokens: 3000, CacheCreationInputTokens: 5000, CacheCreation12hInputTokens: 5000},
			wantCreate:    4000,
			want30m:       0,
			want12h:       4000,
			wantTTLCostOK: true,
			wantTTLCost:   4000 * 2.5,
		},
		{
			// 注入部分把缓存写入扣光：聚合归零，分桶也必须归零。
			name:          "aggregate_drained",
			in:            OpenAIUsage{InputTokens: 4000, CacheCreationInputTokens: 3000, CacheCreation12hInputTokens: 3000},
			wantCreate:    0,
			want30m:       0,
			want12h:       0,
			wantTTLCostOK: false,
		},
		{
			// 上游没给明细（解析侧已清零）：不凭空造分桶，计费继续走聚合价。
			name:          "no_breakdown_untouched",
			in:            OpenAIUsage{InputTokens: 4200, CacheCreationInputTokens: 4100},
			wantCreate:    100,
			want30m:       0,
			want12h:       0,
			wantTTLCostOK: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := tc.in
			deductSynthInstructionsFromUsage(&u, n)
			if u.CacheCreationInputTokens != tc.wantCreate || u.CacheCreation30mInputTokens != tc.want30m || u.CacheCreation12hInputTokens != tc.want12h {
				t.Fatalf("create/30m/12h = %d/%d/%d, want %d/%d/%d",
					u.CacheCreationInputTokens, u.CacheCreation30mInputTokens, u.CacheCreation12hInputTokens,
					tc.wantCreate, tc.want30m, tc.want12h)
			}
			// 用上游真实的计费判定验证，而不是在测试里复述一遍不变量。
			cost, ok := openAICacheCreationTTLCost(u.billingUsageTokens(), 1.25, &longPrice)
			if ok != tc.wantTTLCostOK {
				t.Fatalf("openAICacheCreationTTLCost ok = %v, want %v (tokens=%+v)", ok, tc.wantTTLCostOK, u.billingUsageTokens())
			}
			if ok && cost != tc.wantTTLCost {
				t.Fatalf("openAICacheCreationTTLCost = %v, want %v", cost, tc.wantTTLCost)
			}
		})
	}
}

// 5m / 1h 分桶不要求穷尽，但聚合值归零时 billingUsageTokens 会拿它们当聚合值，
// 所以两桶之和不得超过扣减后的聚合值。
func TestDeductSynthInstructionsFromUsage_Trims5m1hBucketsToAggregate(t *testing.T) {
	u := OpenAIUsage{InputTokens: 4000, CacheCreationInputTokens: 3000, CacheCreation5mTokens: 1000, CacheCreation1hTokens: 2000}
	deductSynthInstructionsFromUsage(&u, 4000)
	if u.CacheCreationInputTokens != 0 || u.CacheCreation5mTokens != 0 || u.CacheCreation1hTokens != 0 {
		t.Fatalf("create/5m/1h = %d/%d/%d, want 0/0/0", u.CacheCreationInputTokens, u.CacheCreation5mTokens, u.CacheCreation1hTokens)
	}
	if got := u.billingUsageTokens().CacheCreationTokens; got != 0 {
		t.Fatalf("billing CacheCreationTokens = %d, want 0 (hidden tokens must not come back via 5m/1h)", got)
	}

	// 部分明细（余量归 5m）且扣减后仍装得下：不动。
	u = OpenAIUsage{InputTokens: 9000, CacheCreationInputTokens: 8000, CacheCreation1hTokens: 1000}
	deductSynthInstructionsFromUsage(&u, 4000)
	if u.CacheCreationInputTokens != 4000 || u.CacheCreation5mTokens != 0 || u.CacheCreation1hTokens != 1000 {
		t.Fatalf("create/5m/1h = %d/%d/%d, want 4000/0/1000", u.CacheCreationInputTokens, u.CacheCreation5mTokens, u.CacheCreation1hTokens)
	}
}

// TestDeductSynthInstructionsFromUsage_NoOp 确认 N<=0 或 nil 时不改动。
func TestDeductSynthInstructionsFromUsage_NoOp(t *testing.T) {
	u := OpenAIUsage{InputTokens: 100, CacheReadInputTokens: 50}
	deductSynthInstructionsFromUsage(&u, 0)
	if u.InputTokens != 100 || u.CacheReadInputTokens != 50 {
		t.Fatalf("unexpected mutation on n=0: %+v", u)
	}
	deductSynthInstructionsFromUsage(nil, 10) // must not panic
}

// TestCountOpenAISynthInstructionsTokens 确认对四份 base 模板均能算出合理的正 token 数，
// 且与 isDefaultCodexSynthInstructions 判定一致。
func TestCountOpenAISynthInstructionsTokens(t *testing.T) {
	models := []struct {
		model  string
		minTok int
	}{
		{"gpt-5.5", 3000},
		{"gpt-5.2", 3000},
		{"gpt-5.1", 3000},
		{"gpt-5.3-codex", 1000},
	}
	for _, m := range models {
		instr := defaultCodexSynthInstructions(m.model)
		n := countOpenAISynthInstructionsTokens(m.model, instr)
		if n < m.minTok {
			t.Errorf("model %s: token count %d < expected min %d", m.model, n, m.minTok)
		}
		if !isDefaultCodexSynthInstructions(instr, m.model) {
			t.Errorf("model %s: injected default not recognized by isDefaultCodexSynthInstructions", m.model)
		}
	}
	if countOpenAISynthInstructionsTokens("gpt-5.5", "  ") != 0 {
		t.Error("empty text should count 0")
	}
}

// TestIsDefaultCodexSynthInstructions 确认客户端自有内容不被误判为注入。
func TestIsDefaultCodexSynthInstructions(t *testing.T) {
	if isDefaultCodexSynthInstructions("You are my custom assistant.", "gpt-5.5", "gpt-5") {
		t.Error("client-provided instructions must not be treated as our synth default")
	}
	if isDefaultCodexSynthInstructions("", "gpt-5.5") {
		t.Error("empty must be false")
	}
}

// TestHideSynthCacheInUsageObject_ResponsePrefix 验证 response.usage 改写：cached / input /
// total 同步下调，output 与其它字段不变。
func TestHideSynthCacheInUsageObject_ResponsePrefix(t *testing.T) {
	const n = 4376
	in := `{"type":"response.completed","sequence_number":9,"response":{"id":"resp_1","model":"gpt-5","usage":{"input_tokens":4500,"output_tokens":50,"total_tokens":4550,"input_tokens_details":{"cached_tokens":4400}}}}`
	out := hideSynthCacheInUsageObject([]byte(in), "response.usage", n)

	if got := gjson.GetBytes(out, "response.usage.input_tokens_details.cached_tokens").Int(); got != 24 {
		t.Errorf("cached_tokens = %d, want 24", got)
	}
	if got := gjson.GetBytes(out, "response.usage.input_tokens").Int(); got != 124 {
		t.Errorf("input_tokens = %d, want 124", got)
	}
	if got := gjson.GetBytes(out, "response.usage.total_tokens").Int(); got != 174 {
		t.Errorf("total_tokens = %d, want 174", got)
	}
	if got := gjson.GetBytes(out, "response.usage.output_tokens").Int(); got != 50 {
		t.Errorf("output_tokens = %d, want 50 (unchanged)", got)
	}
	if got := gjson.GetBytes(out, "response.id").String(); got != "resp_1" {
		t.Errorf("response.id = %q, want resp_1 (unchanged)", got)
	}
	if got := gjson.GetBytes(out, "type").String(); got != "response.completed" {
		t.Errorf("type = %q, want response.completed", got)
	}
}

// TestHideSynthCacheInUsageObject_ChatPromptDetails 验证 chat 形态字段别名（prompt_tokens_details）。
func TestHideSynthCacheInUsageObject_ChatPromptDetails(t *testing.T) {
	const n = 100
	in := `{"usage":{"prompt_tokens":200,"completion_tokens":10,"total_tokens":210,"prompt_tokens_details":{"cached_tokens":180}}}`
	out := hideSynthCacheInUsageObject([]byte(in), "usage", n)
	if got := gjson.GetBytes(out, "usage.prompt_tokens_details.cached_tokens").Int(); got != 80 {
		t.Errorf("cached_tokens = %d, want 80", got)
	}
	if got := gjson.GetBytes(out, "usage.prompt_tokens").Int(); got != 100 {
		t.Errorf("prompt_tokens = %d, want 100", got)
	}
	if got := gjson.GetBytes(out, "usage.total_tokens").Int(); got != 110 {
		t.Errorf("total_tokens = %d, want 110", got)
	}
}

// TestHideSynthCacheInUsageObject_NoOp 确认 N<=0 或字段缺失时原样返回。
func TestHideSynthCacheInUsageObject_NoOp(t *testing.T) {
	in := `{"response":{"usage":{"input_tokens":10}}}`
	if got := string(hideSynthCacheInUsageObject([]byte(in), "response.usage", 0)); got != in {
		t.Errorf("n=0 should be no-op, got %s", got)
	}
}

// TestHideSynthCacheInSSELine 验证 SSE 行改写保留 "data: " 前缀且只改 usage。
func TestHideSynthCacheInSSELine(t *testing.T) {
	const n = 4376
	line := `data: {"type":"response.completed","response":{"usage":{"input_tokens":4500,"total_tokens":4550,"input_tokens_details":{"cached_tokens":4400}}}}`
	out := hideSynthCacheInSSELine(line, n)
	if out == line {
		t.Fatal("expected rewrite")
	}
	if got := gjson.Get(out[len("data: "):], "response.usage.input_tokens_details.cached_tokens").Int(); got != 24 {
		t.Errorf("cached_tokens = %d, want 24", got)
	}
	// 非 data 行原样返回。
	if got := hideSynthCacheInSSELine("event: response.completed", n); got != "event: response.completed" {
		t.Errorf("non-data line mutated: %q", got)
	}
}

// TestIsOpenAIResponsesTerminalUsageEvent 确认终态事件判定（排除 failed）。
func TestIsOpenAIResponsesTerminalUsageEvent(t *testing.T) {
	for _, e := range []string{"response.completed", "response.done", "response.incomplete", "response.cancelled", "response.canceled"} {
		if !isOpenAIResponsesTerminalUsageEvent(e) {
			t.Errorf("%s should be terminal usage event", e)
		}
	}
	for _, e := range []string{"response.failed", "response.output_text.delta", ""} {
		if isOpenAIResponsesTerminalUsageEvent(e) {
			t.Errorf("%s should NOT be terminal usage event", e)
		}
	}
}

// TestDeductSynthInstructionsFromChatUsage 验证 chat usage 结构扣减与 total 重算。
func TestDeductSynthInstructionsFromChatUsage(t *testing.T) {
	u := &apicompat.ChatUsage{
		PromptTokens:        200,
		CompletionTokens:    10,
		TotalTokens:         210,
		PromptTokensDetails: &apicompat.ChatTokenDetails{CachedTokens: 180},
	}
	deductSynthInstructionsFromChatUsage(u, 100)
	if u.PromptTokens != 100 {
		t.Errorf("PromptTokens = %d, want 100", u.PromptTokens)
	}
	if u.PromptTokensDetails.CachedTokens != 80 {
		t.Errorf("CachedTokens = %d, want 80", u.PromptTokensDetails.CachedTokens)
	}
	if u.TotalTokens != 110 {
		t.Errorf("TotalTokens = %d, want 110 (prompt+completion)", u.TotalTokens)
	}
	deductSynthInstructionsFromChatUsage(nil, 10) // must not panic
}
