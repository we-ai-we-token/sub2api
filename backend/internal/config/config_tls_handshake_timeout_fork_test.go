package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadDefaultTLSHandshakeTimeout(t *testing.T) {
	resetViperWithJWTSecret(t)
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, 10, cfg.Gateway.TLSHandshakeTimeoutSeconds)
}

func TestLoadTLSHandshakeTimeoutFromEnv(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("GATEWAY_TLS_HANDSHAKE_TIMEOUT_SECONDS", "30")
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, 30, cfg.Gateway.TLSHandshakeTimeoutSeconds)
}
