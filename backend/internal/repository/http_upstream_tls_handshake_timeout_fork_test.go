package repository

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// 二开：gateway.tls_handshake_timeout_seconds 让 TLS 握手超时可配。
// 独立成文件，不跟上游抢 http_upstream_test.go。

func (s *HTTPUpstreamSuite) TestTLSHandshakeTimeoutDefaultsTo10s() {
	svc := s.newService()
	entry, err := svc.getClientEntry("", 1, 1, service.HTTPUpstreamProfileDefault, false, false)
	require.NoError(s.T(), err)
	transport, ok := entry.client.Transport.(*http.Transport)
	require.True(s.T(), ok, "expected *http.Transport")
	require.Equal(s.T(), 10*time.Second, transport.TLSHandshakeTimeout)
}

func (s *HTTPUpstreamSuite) TestTLSHandshakeTimeoutHonorsConfig() {
	s.cfg.Gateway = config.GatewayConfig{TLSHandshakeTimeoutSeconds: 30}
	svc := s.newService()
	entry, err := svc.getClientEntry("", 1, 1, service.HTTPUpstreamProfileDefault, false, false)
	require.NoError(s.T(), err)
	transport, ok := entry.client.Transport.(*http.Transport)
	require.True(s.T(), ok, "expected *http.Transport")
	require.Equal(s.T(), 30*time.Second, transport.TLSHandshakeTimeout)
}

// 0 或负数回落到默认值，而不是退化成「无超时」——无超时会让坏链路上的请求
// 永远挂着，占住生图并发槽并推迟换号重试。
func (s *HTTPUpstreamSuite) TestTLSHandshakeTimeoutZeroFallsBackToDefault() {
	s.cfg.Gateway = config.GatewayConfig{TLSHandshakeTimeoutSeconds: 0}
	svc := s.newService()
	entry, err := svc.getClientEntry("", 1, 1, service.HTTPUpstreamProfileDefault, false, false)
	require.NoError(s.T(), err)
	transport, ok := entry.client.Transport.(*http.Transport)
	require.True(s.T(), ok, "expected *http.Transport")
	require.Equal(s.T(), 10*time.Second, transport.TLSHandshakeTimeout)

	require.Equal(s.T(), defaultUpstreamTLSHandshakeTimeout, resolveTLSHandshakeTimeout(poolSettings{}))
	require.Equal(s.T(), defaultUpstreamTLSHandshakeTimeout,
		resolveTLSHandshakeTimeout(poolSettings{tlsHandshakeTimeout: -time.Second}))
}

// 必须进 poolKey：否则改了配置之后，已缓存的客户端不会被重建，新值要等
// 客户端被淘汰才生效。
func TestBuildPoolKeyIncludesTLSHandshakeTimeout(t *testing.T) {
	a := poolSettings{tlsHandshakeTimeout: 10 * time.Second}
	b := poolSettings{tlsHandshakeTimeout: 30 * time.Second}
	require.NotEqual(t, buildPoolKey(a, upstreamProtocolModeDefault), buildPoolKey(b, upstreamProtocolModeDefault))
	require.Equal(t, buildPoolKey(a, upstreamProtocolModeDefault), buildPoolKey(a, upstreamProtocolModeDefault))
}
