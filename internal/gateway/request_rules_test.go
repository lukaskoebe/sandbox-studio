package gateway

import (
	"bufio"
	"context"
	"io"
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

// A connection whose requests can't be read doesn't get past a request rule by not looking like
// web traffic: it never reaches the upstream. Other hosts' connections go on as before.
func TestRequestRulesRefuseWhatTheyCantRead(t *testing.T) {
	for name, first := range map[string]string{
		"a leading line break": "\r\nGET /secret HTTP/1.1\r\nHost: github.com\r\n\r\n",
		"an unusual method":    "PROPFIND /secret HTTP/1.1\r\nHost: github.com\r\n\r\n",
		"not web at all":       "SSH-2.0-client\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, "")
			h.policy.mu.Lock()
			h.policy.terminates = map[string]bool{"github.com": true}
			h.policy.mu.Unlock()
			c, err := h.dial(t, Password([]byte("key"), "sb1"), "192.0.2.7:443")
			if err == nil {
				defer c.Close()
				_, _ = c.Write([]byte(first))
				if _, err := io.ReadAll(c); err != nil {
					t.Logf("read: %v", err)
				}
			}
			if got := h.lastDial(); got != "" {
				t.Fatalf("reached the upstream at %s", got)
			}
		})
	}

	h := newHarness(t, "")
	c, err := h.dial(t, Password([]byte("key"), "sb1"), "192.0.2.7:22")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("SSH-2.0-client\r\n")); err != nil {
		t.Fatal(err)
	}
	if line, err := bufio.NewReader(c).ReadString('\n'); err != nil || line != "SSH-2.0-client\r\n" {
		t.Fatalf("another host's connection: %q, %v", line, err)
	}
}

// curatedPolicy answers a request as the curated list does: a read passes, a write comes back from
// the hold unanswered.
type curatedPolicy struct {
	*fakePolicy
	curated []policy.Request
}

func (p *curatedPolicy) Decide(ctx context.Context, req policy.Request) (store.Rule, error) {
	if req.HTTP == nil {
		return p.fakePolicy.Decide(ctx, req)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.curated = append(p.curated, req)
	if req.HTTP.Method == http.MethodGet {
		return store.Rule{Action: store.ActionAllow}, nil
	}
	return store.Rule{}, policy.ErrUndecided
}

// A connection the curated list allowed to a read-only host is terminated. A read reaches the
// upstream. A write that nobody has answered doesn't, and it says what it is waiting for.
func TestCuratedHostsPassReadsAndHoldWrites(t *testing.T) {
	h := newBrowserHarness(t)
	pol := &curatedPolicy{fakePolicy: h.policy}
	h.gw.Policy = pol
	h.policy.mu.Lock()
	h.policy.rules["curated.example"] = store.Rule{Action: store.ActionAllow, Origin: store.OriginCurated}
	h.policy.terminates = map[string]bool{"curated.example": true}
	h.policy.mu.Unlock()

	agent := h.client(t, h.envCA)
	if resp, body := get(t, agent, "https://curated.example/o/r"); resp.StatusCode != http.StatusOK {
		t.Fatalf("a read: status %d: %s", resp.StatusCode, body)
	}
	before := h.requests()
	resp, err := agent.Post("https://curated.example/o/r.git/git-receive-pack", "application/octet-stream", strings.NewReader("pack"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(body), "waiting for approval") {
		t.Fatalf("a write: status %d: %s", resp.StatusCode, body)
	}
	if n := h.requests(); n != before {
		t.Fatalf("a held write reached the upstream (%d -> %d)", before, n)
	}
	for _, req := range pol.curated {
		if !req.Curated || !req.RulesOnly {
			t.Errorf("request decided without saying it is the list's: %+v", req)
		}
	}
}
