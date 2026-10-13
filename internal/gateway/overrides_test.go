package gateway

import (
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// TestProxyRuleOverrides: a proxy rule sends its requests to its upstream. Secrets go only to the
// upstream they are bound to, the Host header is the sandbox's unless the rule replaces it, and
// the TLS server name is the upstream's unless the rule replaces that too.
func TestProxyRuleOverrides(t *testing.T) {
	h := newInterceptHarness(t)
	h.policy.rules["front.example"] = store.Rule{ID: "o1", Action: store.ActionProxy, Config: store.RuleConfig{Upstream: "upstream.example"}}
	h.policy.rules["renamed.example"] = store.Rule{ID: "o2", Action: store.ActionProxy, Config: store.RuleConfig{
		Upstream: "upstream.example:443", HostHeader: "api.internal.example", SNI: "sni.example",
	}}
	h.policy.rules["swap.example"] = store.Rule{ID: "o3", Action: store.ActionProxy, Config: store.RuleConfig{Upstream: "other.example"}}
	c := h.client(t, h.envCA)

	if resp, body := get(t, c, "https://front.example/echo"); resp.StatusCode != 200 {
		t.Fatalf("upstream override: %d %q", resp.StatusCode, body)
	}
	if got := h.lastSeen(t); got.Host != "front.example" {
		t.Errorf("upstream saw Host %q, want the sandbox's", got.Host)
	}
	h.mu.Lock()
	dialed := append([]string(nil), h.dialed...)
	h.mu.Unlock()
	if !strings.Contains(strings.Join(dialed, " "), "upstream.example:443") {
		t.Errorf("dialed %v, want the upstream", dialed)
	}

	if resp, body := get(t, c, "https://renamed.example/echo"); resp.StatusCode != 200 {
		t.Fatalf("Host header and SNI override: %d %q", resp.StatusCode, body)
	}
	if got := h.lastSeen(t); got.Host != "api.internal.example" {
		t.Errorf("upstream saw Host %q, want the rule's", got.Host)
	}

	// API_KEY is bound to example.com and h1.example, not to the upstream: its placeholder is refused.
	if resp, body := get(t, c, "https://swap.example/echo", "X-Api-Key", apiPlaceholder); resp.StatusCode != 403 || strings.Contains(body, apiValue) || !strings.Contains(body, "not to other.example") {
		t.Errorf("secret sent to an upstream it isn't bound to: %d %q", resp.StatusCode, body)
	}
	// OTHER_KEY is bound to other.example, so it is sent there.
	if resp, body := get(t, c, "https://swap.example/echo", "X-Api-Key", otherPlaceholder); resp.StatusCode != 200 {
		t.Fatalf("secret bound to the upstream: %d %q", resp.StatusCode, body)
	}
	if got := h.lastSeen(t); got.Header.Get("X-Api-Key") != "other-secret-value" {
		t.Errorf("upstream saw X-Api-Key %q, want the bound secret", got.Header.Get("X-Api-Key"))
	}
}
