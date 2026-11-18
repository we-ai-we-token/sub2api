package service

import "encoding/json"

// geminiFirstUserPartsSeedNormalizeMaxBytes 是第一条 user parts 做 JSON 归一化的体积上限。
// 纯文本对话的首条 parts 远小于它；超过它的基本都带 inlineData 参考图。
const geminiFirstUserPartsSeedNormalizeMaxBytes = 256 << 10

// geminiFirstUserPartsSeed 返回第一条 user parts 写进粘性会话哈希的种子串（二开）。
//
// 上游 v0.2.23（b6c21cd5e）的 GenerateGeminiContentSessionHash 对这段 parts 调
// normalizeCompatSeedJSON：整段 json.Unmarshal 成 any 再 json.Marshal 回来，为的是让同一内容的
// 不同 JSON 排版得到同一个哈希。纯文本没问题，但二开的 Gemini 原生生图
// （/v1beta/models/...:generateContent）首条 parts 里是整张 base64 参考图，请求体平均约 2MB、
// p99 约 20MB：RawMessage 拷贝、解码、编码、转回 string，每个请求在选号之前同步多分配约 4 倍
// 请求体大小的内存，还不在 request_body_memory_budget 的记账内。生图并发的天花板本来就是内存。
//
// 体积超过上限时直接用原始字节当种子，零拷贝。代价是同一张图换一种 JSON 排版会得到不同的
// 哈希——同一个客户端发同一张图排版不会变，而且生图请求之间本来就没有可复用的前缀缓存，
// 粘性落空没有损失。上限以内的行为与上游完全一致。
func geminiFirstUserPartsSeed(raw string) string {
	if len(raw) > geminiFirstUserPartsSeedNormalizeMaxBytes {
		return raw
	}
	return normalizeCompatSeedJSON(json.RawMessage(raw))
}
