package repository

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// 实测各 protocolMode 最终协商出的协议版本。
//
// 背景：gemini / claude 等平台没有设置 HTTPUpstreamProfile，落到
// upstreamProtocolModeDefault，而 buildUpstreamTransport 的 switch 对该模式没有
// 任何 case——既不设 ForceAttemptHTTP2 也不设 TLSNextProto。由于基础 transport
// 设了 DialContext，Go 的 onceSetNextProtoDefaults 不会自动启用 HTTP/2。
//
// 这个用例用同一套 TLS 配置、只改 protocolMode，靠对比证明协议版本确实由
// protocolMode 决定，而不是被测试环境带偏。
func TestUpstreamProtocolModeNegotiatedVersion(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.Proto)
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	clientTransport, ok := srv.Client().Transport.(*http.Transport)
	require.True(t, ok)
	tlsCfg := clientTransport.TLSClientConfig.Clone()

	probe := func(mode string) string {
		tr, err := buildUpstreamTransport(defaultPoolSettings(nil), nil, mode)
		require.NoError(t, err)
		tr.TLSClientConfig = tlsCfg.Clone()
		defer tr.CloseIdleConnections()

		resp, err := (&http.Client{Transport: tr}).Get(srv.URL)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, resp.Proto, string(body), "服务端看到的协议应与客户端一致")
		return resp.Proto
	}

	// gemini / claude 等未设 profile 的平台走这里
	require.Equal(t, "HTTP/1.1", probe(upstreamProtocolModeDefault),
		"default 模式不启用 HTTP/2：设了 DialContext 又没有 ForceAttemptHTTP2")

	// 对照组：显式开 h2 的两个模式确实协商出 HTTP/2
	require.Equal(t, "HTTP/2.0", probe(upstreamProtocolModeLongStreamH2))
	require.Equal(t, "HTTP/2.0", probe(upstreamProtocolModeOpenAIH2))

	// 对照组：显式关 h2
	require.Equal(t, "HTTP/1.1", probe(upstreamProtocolModeOpenAIH1))
	require.Equal(t, "HTTP/1.1", probe(upstreamProtocolModeOpenAIH1Fallback))

	// grok 模式同样没有 case，因此也是 HTTP/1.1
	require.Equal(t, "HTTP/1.1", probe(upstreamProtocolModeGrok))
}
