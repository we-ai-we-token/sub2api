package service

import (
	"net/http"
	"testing"

	pluginv1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
)

// 插件进程的 header key 大小写不受契约约束（plugin.proto 没规定，Rust/http crate 的
// HeaderName 恒为小写）。headersFromPlugin 一旦直接写 map 而不走 Add，http.Header.Get
// 就会因为 canonical 化而全部取空——且客户端透传不受影响（responseheaders 用 ToLower 匹配），
// 所以这类回归是静默的，只坏宿主内部逻辑。上游自带的运行时用例是 Go 假插件、key 天生 canonical，
// 覆盖不到这个形态，故在本 fork 独立成文件兜底。
func TestHeadersFromPluginCanonicalizesLowercaseKeys(t *testing.T) {
	out := headersFromPlugin(map[string]*pluginv1.HeaderValues{
		"x-request-id":        {Values: []string{"req-abc"}},
		"x-codex-turn-state":  {Values: []string{"turn-xyz"}},
		"content-type":        {Values: []string{"text/event-stream"}},
		"etag":                {Values: []string{`W/"v1"`}},
		"openai-organization": {Values: []string{"org-1"}},
	})

	// 宿主各处都是 resp.Header.Get(...) —— 注意小写参数同样会被 canonical 化后再查表，
	// 所以"传小写就能取到"是错觉，两种写法都必须过。
	for _, tc := range []struct {
		lookup string
		want   string
	}{
		{"x-request-id", "req-abc"},
		{"X-Request-Id", "req-abc"},
		{"x-codex-turn-state", "turn-xyz"},
		{"X-Codex-Turn-State", "turn-xyz"},
		{"Content-Type", "text/event-stream"},
		{"content-type", "text/event-stream"},
		{"ETag", `W/"v1"`},
		{"OpenAI-Organization", "org-1"},
	} {
		if got := out.Get(tc.lookup); got != tc.want {
			t.Fatalf("Get(%q) = %q, want %q（headersFromPlugin 未做 canonical 化）", tc.lookup, got, tc.want)
		}
	}
}

func TestHeadersFromPluginPreservesMultiValueOrderAndSkipsNil(t *testing.T) {
	out := headersFromPlugin(map[string]*pluginv1.HeaderValues{
		"set-cookie": {Values: []string{"a=1", "b=2"}},
		"x-empty":    nil,
		"x-none":     {Values: nil},
	})

	got := out.Values("Set-Cookie")
	if len(got) != 2 || got[0] != "a=1" || got[1] != "b=2" {
		t.Fatalf("Values(Set-Cookie) = %v, want [a=1 b=2]", got)
	}
	if _, ok := out[http.CanonicalHeaderKey("x-empty")]; ok {
		t.Fatal("nil HeaderValues 不应写入 header")
	}
	if got := out.Values("X-None"); len(got) != 0 {
		t.Fatalf("Values(X-None) = %v, want empty", got)
	}
}
