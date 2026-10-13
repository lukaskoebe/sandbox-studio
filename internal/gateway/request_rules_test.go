package gateway

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/policy"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// requestPolicy denies the requests whose path starts with /secret, and decides the rest as
// fakePolicy does.
type requestPolicy struct{ *fakePolicy }

func (p requestPolicy) Decide(ctx context.Context, req policy.Request) (store.Rule, error) {
	if req.HTTP != nil && strings.HasPrefix(req.HTTP.Path, "/secret") {
		return store.Rule{ID: "no-secret", Action: store.ActionDeny}, nil
	}
	return p.fakePolicy.Decide(ctx, req)
}

// A rule that names a request's method or path makes the requests of its host checkable: an agent's
// connection that a host rule allows is then terminated, and a browser's requests are checked too.
func TestRequestRulesCheckTheRequests(t *testing.T) {
	h := newBrowserHarness(t)
	h.gw.Policy = requestPolicy{h.policy}
	h.policy.mu.Lock()
	h.policy.terminates = map[string]bool{"splice.example": true}
	h.policy.mu.Unlock()

	agent := h.client(t, h.envCA)
	resp, body := get(t, agent, "https://splice.example/secret/x")
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(body, "blocks GET /secret/x") {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if resp, body = get(t, agent, "https://splice.example/open"); resp.StatusCode != http.StatusOK {
		t.Fatalf("an allowed request: status %d: %s", resp.StatusCode, body)
	}
	log := h.gw.Conns.List("sb1")
	if len(log) == 0 {
		t.Fatal("no connection logged")
	}
	for _, c := range log {
		if !c.Intercepted {
			t.Errorf("a connection a request rule names was spliced: %+v", c)
		}
	}

	before := h.requests()
	browser := h.clientAs(t, "vm1", h.envCA)
	if resp, body = get(t, browser, "https://splice.example/secret/y"); resp.StatusCode != http.StatusForbidden || !strings.Contains(body, "blocks GET /secret/y") {
		t.Fatalf("a browser's refused request: status %d: %s", resp.StatusCode, body)
	}
	if n := h.requests(); n != before {
		t.Fatalf("a refused browser request reached the upstream (%d -> %d)", before, n)
	}
}
