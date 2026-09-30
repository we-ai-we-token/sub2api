package service

// inflightImageEstimateTiers 返回在途预留估算图片成本时要遍历的价格档（取其中最高价）。
//
// 上游只遍历 1K/2K/4K 尺寸档。二开的 OpenAI 分组启用按质量计费后，计费改按
// low/medium/high 查价、跳过分组尺寸档价（见 resolveOpenAIGroupImageBillingDecision），
// 估算必须同口径，否则质量价永远进不了估算：低质量被按默认 4K 价高估、误拒低余额
// 用户的并发请求，自定义高价又被低估。请求显式指定质量时只估那一档（xhigh/max 收敛
// 到 high，与计费一致）；未指定或 auto 时计费质量由上游响应决定，取三档最高价。
func inflightImageEstimateTiers(apiKey *APIKey, requestedQuality string) []string {
	if !isOpenAIGroupImageQualityBillingEnabled(apiKey) {
		return []string{ImageBillingSize1K, ImageBillingSize2K, ImageBillingSize4K}
	}
	if quality := NormalizeOpenAIImageQualityOrEmpty(requestedQuality); quality != "" {
		return []string{ImageQualityBillingTier(quality)}
	}
	return []string{OpenAIImageQualityLow, OpenAIImageQualityMedium, OpenAIImageQualityHigh}
}
