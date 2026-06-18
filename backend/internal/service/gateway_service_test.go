package service

import "testing"

func TestExtractUpstreamErrorCodeTopLevel(t *testing.T) {
	body := []byte(`{"code":"unsupported_parameter","message":"Streaming is only supported with n=1."}`)
	if got := extractUpstreamErrorCode(body); got != "unsupported_parameter" {
		t.Fatalf("extractUpstreamErrorCode = %q, want unsupported_parameter", got)
	}
	// 既有 error.code 行为不回归
	nested := []byte(`{"error":{"code":"server_error","message":"x"}}`)
	if got := extractUpstreamErrorCode(nested); got != "server_error" {
		t.Fatalf("extractUpstreamErrorCode(nested) = %q, want server_error", got)
	}
}
