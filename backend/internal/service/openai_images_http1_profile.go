package service

import "context"

// openAIImagesUpstreamProfile 返回生图请求应当挂的上游 transport profile。
//
// 系统设置 openai_images_force_http1 打开时返回 HTTPUpstreamProfileOpenAIImages，
// 它在 repository 层固定解析成 upstreamProtocolModeOpenAIH1；关闭时返回
// HTTPUpstreamProfileOpenAI，也就是改动前的行为。
//
// 每个请求都实时读一次（SettingService 内部是 atomic.Value + 60s TTL 的零锁热路径，
// 保存设置时还会主动刷缓存），因此翻开关无需重启：协议模式变了 buildCacheKey 的
// "|proto:" 后缀就变了，下一个请求直接落到一条新的 HTTP/1.1 连接池，老的 HTTP/2
// 连接池既不会被关闭也不会被复用，在飞请求完全不受影响。
//
// s / s.settingService 为 nil 时回落到原行为——大量网关单测构造的 service 没有
// settingService，直接解引用会 panic。
func (s *OpenAIGatewayService) openAIImagesUpstreamProfile(ctx context.Context) HTTPUpstreamProfile {
	if s == nil || s.settingService == nil {
		return HTTPUpstreamProfileOpenAI
	}
	if s.settingService.IsOpenAIImagesForceHTTP1Enabled(ctx) {
		return HTTPUpstreamProfileOpenAIImages
	}
	return HTTPUpstreamProfileOpenAI
}

// openAIImagesUpstreamProfile 是后台「测试账号」生图探针用的同名助手，让探针走的
// 传输链路跟线上生图一致——否则开关打开后，探针仍在 HTTP/2 上验证，通过了也不
// 说明真实链路可用。
func (s *AccountTestService) openAIImagesUpstreamProfile(ctx context.Context) HTTPUpstreamProfile {
	if s == nil || s.settingService == nil {
		return HTTPUpstreamProfileOpenAI
	}
	if s.settingService.IsOpenAIImagesForceHTTP1Enabled(ctx) {
		return HTTPUpstreamProfileOpenAIImages
	}
	return HTTPUpstreamProfileOpenAI
}
