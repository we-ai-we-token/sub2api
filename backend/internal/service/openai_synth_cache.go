package service

// 本文件实现"隐藏/扣减自动注入 Codex instructions 产生的缓存 token"功能
// （功能开关 openai_synth_cache_hidden_enabled，见 SettingKeyOpenAISynthCacheHidden）。
//
// 背景：当客户端未提供 instructions 时，网关会自动注入一整份 Codex base 系统提示词
// （defaultCodexSynthInstructions / applyInstructions）。这段固定前缀被 OpenAI 的
// prompt caching 命中后，客户端仅发一句 "hi" 也会在 usage 里看到数千 cached_tokens。
// 开关开启后，网关把这段"我方注入"的提示词贡献的 input/cache token，从计费与用量记录
// （RecordUsage，唯一扣减真相源）以及回传给客户端的响应 usage 中扣除。

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// openAISynthInstructionsTokensKey 是 gin.Context 里承载"本次请求注入的 Codex
// instructions token 数 N"的键。仅当开关开启且确实发生注入时 >0。
const openAISynthInstructionsTokensKey = "openai_synth_instructions_tokens"

// setOpenAISynthInstructionsTokens 在转发阶段（ctx 存活）记录注入 token 数 N。
func setOpenAISynthInstructionsTokens(c *gin.Context, n int) {
	if c == nil || n <= 0 {
		return
	}
	c.Set(openAISynthInstructionsTokensKey, n)
}

// openAISynthInstructionsTokensFromContext 读取本次请求的注入 token 数（缺省 0）。
func openAISynthInstructionsTokensFromContext(c *gin.Context) int {
	if c == nil {
		return 0
	}
	if v, ok := c.Get(openAISynthInstructionsTokensKey); ok {
		if n, ok := v.(int); ok && n > 0 {
			return n
		}
	}
	return 0
}

// isDefaultCodexSynthInstructions 判断 finalInstr 是否恰好等于我方默认合成的 Codex base
// 提示词（按可能的模型名各生成一份候选比对）。相等即表示"客户端未提供 instructions，我方
// 注入了整份 base prompt"（hi/探活场景）；客户端自有 system 内容提升到 instructions 时不相等，
// 从而不会被误扣。model 候选覆盖上游归一化模型与注入时的请求模型（二者通常映射到同一份 prompt）。
func isDefaultCodexSynthInstructions(finalInstr string, models ...string) bool {
	finalInstr = strings.TrimSpace(finalInstr)
	if finalInstr == "" {
		return false
	}
	seen := make(map[string]struct{}, len(models))
	for _, m := range models {
		if _, ok := seen[m]; ok {
			continue
		}
		seen[m] = struct{}{}
		if finalInstr == strings.TrimSpace(defaultCodexSynthInstructions(m)) {
			return true
		}
	}
	return false
}

// countOpenAISynthInstructionsTokens 用与 count_tokens 一致的 tiktoken 编码器
// （o200k_base / cl100k_base，按模型选择）估算注入 instructions 的 token 数。
// 失败或空串返回 0（安全：0 表示不扣减，行为回退到现状）。
func countOpenAISynthInstructionsTokens(model, text string) int {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	codec, err := openAIInputTokensCodecForModel(model)
	if err != nil || codec == nil {
		return 0
	}
	n, err := codec.Count(text)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// deductSynthInstructionsFromUsage 从 usage 中扣除注入 instructions 贡献的 N 个 token。
//
// 计费口径 actualInput = InputTokens - CacheRead - CacheCreation。为避免"只清 cached
// 反而抬高 actualInput、按全价计费"，这里同时从 cached / cache_creation / input 扣减：
//   - 优先从 CacheRead 扣（热缓存：注入前缀已被缓存）
//   - 不足部分从 CacheCreation 扣
//   - InputTokens 始终扣 min(N, InputTokens)（注入前缀是总输入的一部分）
//
// 热缓存下 InputTokens 与 CacheRead 同步降 N → actualInput 不变、缓存计费降 N；
// 冷缓存下 CacheRead 无可扣、InputTokens 降 N → actualInput 降 N。两种情况客户都只为
// 自己的内容付费。所有字段 clamp 到 >=0。
func deductSynthInstructionsFromUsage(usage *OpenAIUsage, n int) {
	if usage == nil || n <= 0 {
		return
	}
	dCache := minInt(n, usage.CacheReadInputTokens)
	usage.CacheReadInputTokens -= dCache
	rem := n - dCache
	if rem > 0 {
		dCreate := minInt(rem, usage.CacheCreationInputTokens)
		usage.CacheCreationInputTokens -= dCreate
	}
	usage.InputTokens -= minInt(n, usage.InputTokens)
}

// deductSynthInstructionsFromChatUsage 在 Chat Completions 的 usage 结构上扣减 N（客户端侧），
// 与 deductSynthInstructionsFromUsage 口径一致：cached_tokens 与 prompt_tokens 同步下调，
// total 重算为 prompt + completion。用于 chat 缓冲/流式响应写出前的就地改写。
func deductSynthInstructionsFromChatUsage(u *apicompat.ChatUsage, n int) {
	if u == nil || n <= 0 {
		return
	}
	if u.PromptTokensDetails != nil {
		u.PromptTokensDetails.CachedTokens -= minInt(n, u.PromptTokensDetails.CachedTokens)
	}
	u.PromptTokens -= minInt(n, u.PromptTokens)
	u.TotalTokens = u.PromptTokens + u.CompletionTokens
}

// openAISynthUsageCachedTokenPaths 覆盖 usage 里 cached token 的所有别名键
// （Responses / Chat Completions / Anthropic 兼容三种形态），用于改写客户端响应。
// 与 openAICacheReadTokensFromUsage 的读取优先级保持一致。
var openAISynthUsageCachedTokenPaths = []string{
	"input_tokens_details.cached_tokens",
	"prompt_tokens_details.cached_tokens",
	"cache_read_input_tokens",
	"cache_read_tokens",
	"cached_tokens",
}

// openAISynthUsageInputTokenPaths 覆盖 usage 里总输入 token 的别名键。
var openAISynthUsageInputTokenPaths = []string{
	"input_tokens",
	"prompt_tokens",
}

// hideSynthCacheInUsageObject 在一个 usage JSON 对象（原始字节）上按 N 扣减 cached / input
// token，返回改写后的新副本；原字节不被修改（调用方须保证不污染计费用的 dataBytes）。
// prefix 为 usage 对象在外层 JSON 中的路径前缀（如 "response.usage" / "usage"）；为空表示
// data 本身即 usage 对象。仅改写存在且为数值的字段，其余结构原样保留。
func hideSynthCacheInUsageObject(data []byte, prefix string, n int) []byte {
	if n <= 0 || len(data) == 0 {
		return data
	}
	out := data
	join := func(field string) string {
		if prefix == "" {
			return field
		}
		return prefix + "." + field
	}
	// cached：按别名依次扣减。
	remaining := n
	for _, field := range openAISynthUsageCachedTokenPaths {
		if remaining <= 0 {
			break
		}
		path := join(field)
		res := gjson.GetBytes(out, path)
		if !res.Exists() || res.Type != gjson.Number {
			continue
		}
		cur := int(res.Int())
		if cur <= 0 {
			continue
		}
		d := minInt(remaining, cur)
		if updated, err := sjson.SetBytes(out, path, cur-d); err == nil {
			out = updated
			remaining -= d
		}
	}
	// input/prompt：扣减 min(N, cur)，与 cached 扣减量共同保证 actualInput 口径正确。
	// 记录实际从 input 端扣掉的量，用于同步下调 total_tokens，避免客户端看到 total 与明细不一致。
	inputDelta := 0
	for _, field := range openAISynthUsageInputTokenPaths {
		path := join(field)
		res := gjson.GetBytes(out, path)
		if !res.Exists() || res.Type != gjson.Number {
			continue
		}
		cur := int(res.Int())
		if cur <= 0 {
			continue
		}
		d := minInt(n, cur)
		if updated, err := sjson.SetBytes(out, path, cur-d); err == nil {
			out = updated
			if d > inputDelta {
				inputDelta = d
			}
		}
	}
	// total_tokens 同步下调 input 端扣减量（若存在），保持 total = input + output 的一致性。
	if inputDelta > 0 {
		totalPath := join("total_tokens")
		if res := gjson.GetBytes(out, totalPath); res.Exists() && res.Type == gjson.Number {
			cur := int(res.Int())
			d := minInt(inputDelta, cur)
			if updated, err := sjson.SetBytes(out, totalPath, cur-d); err == nil {
				out = updated
			}
		}
	}
	return out
}

// hideSynthCacheInSSELine 在一条 "data: {json}" SSE 行上就地改写 usage（扣减 N），返回新行；
// 直接作用于已经过全部客户端侧转换（模型名替换等）的 line 字符串，避免从 dataBytes 重建导致
// 丢失这些转换。仅当 line 以 "data: " 开头时处理，其余原样返回。usage 位于 response.usage。
func hideSynthCacheInSSELine(line string, n int) string {
	const prefix = "data: "
	if n <= 0 || !strings.HasPrefix(line, prefix) {
		return line
	}
	payload := line[len(prefix):]
	rewritten := hideSynthCacheInUsageObject([]byte(payload), "response.usage", n)
	if len(rewritten) == len(payload) && string(rewritten) == payload {
		return line
	}
	return prefix + string(rewritten)
}

// isOpenAIResponsesTerminalUsageEvent 判断 SSE 事件类型是否为携带最终 usage 的终态事件
// （与 parseSSEUsageBytes 的口径一致，但排除 response.failed —— 其 usage 已由
// sanitizeOpenAIResponseFailedEventForClient 删除，无需再改写）。
func isOpenAIResponsesTerminalUsageEvent(eventType string) bool {
	switch strings.TrimSpace(eventType) {
	case "response.completed", "response.done", "response.incomplete",
		"response.cancelled", "response.canceled":
		return true
	default:
		return false
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
