package service

import (
	"context"
	"strings"
)

// IsRetryableOpenAIImagesTransportError 判断一个「没有 HTTP 状态码的传输层错误」
// 是否可以安全地换账号重试。
//
// 判据只有一条：**上游能否已经收到并处理了这次请求**。能，就绝不重试——生图请求
// 一旦打到上游，即使我们没拿到响应，上游照样出图照样计费，重试就是双倍成本。
//
// 因此白名单只收两类：
//
//  1. net/http: TLS handshake timeout —— 到上游的 TLS 都没建成，请求一个字节都没发出去。
//  2. write 方向的连接中断（connection reset by peer / broken pipe）—— 请求体正在
//     上传途中被掐断，上游拿到的是不完整的请求，不会出图。实测这类错误的对端
//     地址是代理 IP（core -> 代理 这一跳），与上游无关。
//
// 明确排除（这些都可能意味着上游已经出图）：
//   - unexpected EOF：请求已发完、响应阶段断的
//   - read 方向的中断：同上，请求已经发出去了
//   - context deadline exceeded / i/o timeout：无法从字符串区分是建连阶段还是
//     等响应阶段超时，前者安全后者不安全，所以整体不收
//
// 有 HTTP 状态码的错误不走这里，由既有的 UpstreamFailoverError 分支处理。
func IsRetryableOpenAIImagesTransportError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if msg == "" {
		return false
	}

	// read 方向一票否决，放在最前面：即使同一条消息里同时出现 write 和 read
	// 字样（例如未来某种包装错误），也宁可不重试。
	if strings.Contains(msg, "read tcp ") || strings.Contains(msg, ": read:") {
		return false
	}
	if strings.Contains(msg, "unexpected eof") {
		return false
	}

	if strings.Contains(msg, "tls handshake timeout") {
		return true
	}

	if strings.Contains(msg, "write tcp ") || strings.Contains(msg, ": write:") {
		return strings.Contains(msg, "connection reset by peer") ||
			strings.Contains(msg, "broken pipe")
	}

	return false
}

// ShouldFailoverOpenAIImagesTransportError 合并「系统设置是否开启」与「该错误是否
// 可安全重试」两个判断，供 handler 调用。
//
// settingService 为 nil 时返回 false（保持改动前的行为）——大量网关单测构造的
// service 没有 settingService。
func (s *OpenAIGatewayService) ShouldFailoverOpenAIImagesTransportError(ctx context.Context, err error) bool {
	if s == nil || s.settingService == nil {
		return false
	}
	if !IsRetryableOpenAIImagesTransportError(err) {
		return false
	}
	return s.settingService.IsOpenAIImagesTransportFailoverEnabled(ctx)
}
