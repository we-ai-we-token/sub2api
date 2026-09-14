package repository

import (
	"net/http"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// 二开新增：生图强制 HTTP/1.1。挂哪个 profile 由 service 层按系统设置
// openai_images_force_http1 每请求决定，repository 层只负责把 profile 翻译成协议
// 模式。独立成文件，不跟上游抢 http_upstream_test.go。

// 生图 profile 必须固定解析成 openai_h1，且与文本的 openai_h2 分属不同的缓存桶
// ——这正是「翻开关不影响在飞请求」的机制基础。
func (s *HTTPUpstreamSuite) TestOpenAIImagesProfileForcesHTTP1AndSplitsPool() {
	s.cfg.Gateway = config.GatewayConfig{
		ResponseHeaderTimeout: 600,
		OpenAIHTTP2: config.GatewayOpenAIHTTP2Config{
			Enabled:                   true,
			AllowProxyFallbackToHTTP1: true,
		},
	}
	svc := s.newService()

	imageEntry, err := svc.getClientEntry("", 1, 1, service.HTTPUpstreamProfileOpenAIImages, false, false)
	require.NoError(s.T(), err)
	require.Equal(s.T(), upstreamProtocolModeOpenAIH1, imageEntry.protocolMode)
	imageTransport, ok := imageEntry.client.Transport.(*http.Transport)
	require.True(s.T(), ok, "expected *http.Transport")
	require.False(s.T(), imageTransport.ForceAttemptHTTP2, "images profile must not force HTTP/2")
	require.NotNil(s.T(), imageTransport.TLSNextProto, "HTTP/1 mode must disable automatic H2 negotiation")

	textEntry, err := svc.getClientEntry("", 1, 1, service.HTTPUpstreamProfileOpenAI, false, false)
	require.NoError(s.T(), err)
	require.Equal(s.T(), upstreamProtocolModeOpenAIH2, textEntry.protocolMode, "text routes must stay on HTTP/2")
	textTransport, ok := textEntry.client.Transport.(*http.Transport)
	require.True(s.T(), ok, "expected *http.Transport")
	require.True(s.T(), textTransport.ForceAttemptHTTP2)

	require.NotSame(s.T(), textEntry, imageEntry,
		"the two protocol modes must land in different cache buckets, so flipping never touches in-flight HTTP/2 requests")
}

// 降级协议不得改变响应头超时语义：生图仍要沿用 OpenAI profile 的不限/自定义值，
// 漏配会退回通用 600s，长时间出图会被截断。
func (s *HTTPUpstreamSuite) TestOpenAIImagesProfileKeepsOpenAIHeaderTimeout() {
	s.cfg.Gateway = config.GatewayConfig{
		ResponseHeaderTimeout: 600,
		OpenAIHTTP2:           config.GatewayOpenAIHTTP2Config{Enabled: true},
	}
	svc := s.newService()
	entry, err := svc.getClientEntry("", 1, 1, service.HTTPUpstreamProfileOpenAIImages, false, false)
	require.NoError(s.T(), err)
	transport, ok := entry.client.Transport.(*http.Transport)
	require.True(s.T(), ok, "expected *http.Transport")
	require.Equal(s.T(), time.Duration(0), transport.ResponseHeaderTimeout,
		"images must inherit the OpenAI header-timeout semantics, not the generic 600s")

	s.cfg.Gateway.OpenAIResponseHeaderTimeout = 1800
	svc2 := s.newService()
	entry2, err := svc2.getClientEntry("", 1, 1, service.HTTPUpstreamProfileOpenAIImages, false, false)
	require.NoError(s.T(), err)
	transport2, ok := entry2.client.Transport.(*http.Transport)
	require.True(s.T(), ok, "expected *http.Transport")
	require.Equal(s.T(), 1800*time.Second, transport2.ResponseHeaderTimeout)
}

// 全局 openai_http2.enabled=false 时文本也会走 h1，生图自然跟着。
func (s *HTTPUpstreamSuite) TestOpenAIImagesProfileUnaffectedByGlobalDisable() {
	s.cfg.Gateway = config.GatewayConfig{
		OpenAIHTTP2: config.GatewayOpenAIHTTP2Config{Enabled: false},
	}
	svc := s.newService()
	entry, err := svc.getClientEntry("", 1, 1, service.HTTPUpstreamProfileOpenAIImages, false, false)
	require.NoError(s.T(), err)
	require.Equal(s.T(), upstreamProtocolModeOpenAIH1, entry.protocolMode)
}

// per-proxy 的 H2->H1 自动回退记账：失败与成功两侧必须认同一组 profile，
// 只放宽一侧会让错误窗口永远清不掉、把代理钉死在 H1 十分钟。
func (s *HTTPUpstreamSuite) TestOpenAIHTTP2BookkeepingProfileSymmetry() {
	require.True(s.T(), isOpenAIHTTP2BookkeepingProfile(service.HTTPUpstreamProfileOpenAI))
	require.True(s.T(), isOpenAIHTTP2BookkeepingProfile(service.HTTPUpstreamProfileOpenAIImages))
	require.False(s.T(), isOpenAIHTTP2BookkeepingProfile(service.HTTPUpstreamProfileLongStream))
	require.False(s.T(), isOpenAIHTTP2BookkeepingProfile(service.HTTPUpstreamProfileDefault))
}
