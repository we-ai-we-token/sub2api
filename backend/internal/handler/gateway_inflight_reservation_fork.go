package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/tidwall/gjson"
)

// geminiNativeInflightEstimate 二开：Gemini 原生 generateContent 的在途预留估算入参。
//
// 保留上游的 token 口径（渠道/分组按次价、按图价、token 价卡下它都与计费同口径），只修两处：
//  1. 生图模型（按映射后模型判定，与生图记录同口径）的参考图 base64 inlineData 不计入
//     BodyBytes。上游按 len(body)/4 折算输入 token，几 MB 参考图会把估算顶到 200k 输入
//     token 上限（pro 约 $0.50，实扣 $0.134–0.268），高并发中转 key 的余额落在区间内就被
//     误报余额不足；参考图实际只按几百个图像 token 计费。
//  2. Model 传映射前的请求模型：估算器自己解析渠道映射、按计费来源选价
//     （inflightBillingModelCandidates），与其他 handler 一致。
//
// 不改用图片口径（InflightEstimateImage）：上游该分支总拿默认图片单价与按次价取最大，
// 按次价低于官方价的 gemini 分组会被高估数倍，token 价卡分组更是高估几十倍。
func geminiNativeInflightEstimate(requestModel, mappedModel string, body []byte) service.InflightEstimateRequest {
	req := tokenInflightEstimate(requestModel, body)
	if service.IsGeminiImageGenerationModel(mappedModel) {
		req.BodyBytes -= geminiInlineDataBytes(body)
		if req.BodyBytes < 0 {
			req.BodyBytes = 0
		}
	}
	return req
}

// geminiInlineDataBytes 统计请求体 contents[].parts[] 里 inlineData / inline_data 的 base64 字节数。
func geminiInlineDataBytes(body []byte) int {
	total := 0
	gjson.GetBytes(body, "contents").ForEach(func(_, content gjson.Result) bool {
		content.Get("parts").ForEach(func(_, part gjson.Result) bool {
			for _, path := range []string{"inlineData.data", "inline_data.data"} {
				if v := part.Get(path); v.Exists() {
					total += len(v.Raw)
				}
			}
			return true
		})
		return true
	})
	return total
}
