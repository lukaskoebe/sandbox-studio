package gateway

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/proxy"

	"github.com/lukaskoebe/sandbox-studio/internal/policy"
	"github.com/lukaskoebe/sandbox-studio/internal/secrets"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// newBrowserHarness is newInterceptHarness with a browser VM, vm1, besides the agent sandbox sb1.
func newBrowserHarness(t *testing.T) *interceptHarness {
	t.Helper()
	h := newInterceptHarness(t)
	agent := h.gw.Sandbox
	h.gw.Sandbox = func(ctx context.Context, id string) (store.Sandbox, error) {
		if id == "vm1" {
			return store.Sandbox{ID: "vm1", EnvironmentID: h.envID, Name: "browser-dev", Kind: store.SandboxKindBrowser}, nil
		}
		return agent(ctx, id)
	}
	return h
}

// The browser VM's HTTPS is terminated even where a rule would splice it for an agent: the
// browser speaks HTTP/2 with the gateway, which checks each request, and it trusts the
// environment's CA only.
func TestBrowserVMHTTPSIsTerminated(t *testing.T) {
	h := newBrowserHarness(t)
	resp, body := get(t, h.clientAs(t, "vm1", h.envCA), "https://splice.example/v1?x=1")
	if resp.StatusCode != 200 || resp.ProtoMajor != 2 {
		t.Fatalf("status %d over %s: %s", resp.StatusCode, resp.Proto, body)
	}
	if up := h.lastSeen(t); up.Host != "splice.example" || up.Proto != "HTTP/2.0" {
		t.Errorf("upstream saw host %q over %s", up.Host, up.Proto)
	}
	log := h.gw.Conns.List("vm1")
	if len(log) != 1 || !log[0].Intercepted || (log[0].Verdict != VerdictOpen && log[0].Verdict != VerdictAllowed) {
		t.Fatalf("connection log: %+v", log)
	}

	// The same rule splices an agent sandbox's connection: that one still talks TLS to the
	// upstream itself, so it trusts the upstream's roots, not the environment's.
	get(t, h.client(t, h.upCA), "https://splice.example/")
	if log := h.gw.Conns.List("sb1"); len(log) != 1 || log[0].Intercepted {
		t.Fatalf("an agent's connection was intercepted: %+v", log)
	}
}

// The browser trusts the environment's CA and nothing that signed the upstream: a browser that
// only trusted the upstream's roots refuses the gateway's certificate, so verification stays on
// on both sides.
func TestBrowserVMTrustsOnlyTheEnvironmentCA(t *testing.T) {
	h := newBrowserHarness(t)
	if _, err := h.clientAs(t, "vm1", h.upCA).Get("https://splice.example/"); err == nil {
		t.Fatal("a client that does not trust the environment's CA accepted the gateway's certificate")
	}
}

// Upstream certificates are verified as for agents: one that no root signs fails the request
// with a readable refusal, and the upstream sees nothing.
func TestBrowserVMRefusesUpstreamWithoutValidCertificate(t *testing.T) {
	h := newBrowserHarness(t)
	self := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "reached")
	}))
	t.Cleanup(self.Close)
	h.policy.mu.Lock()
	h.policy.rules["selfsigned.example"] = store.Rule{ID: "r20", Action: store.ActionAllow}
	h.policy.mu.Unlock()
	dial := h.gw.Dial
	h.gw.Dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if strings.HasPrefix(addr, "selfsigned.example:") {
			return (&net.Dialer{}).DialContext(ctx, network, self.Listener.Addr().String())
		}
		return dial(ctx, network, addr)
	}

	resp, body := get(t, h.clientAs(t, "vm1", h.envCA), "https://selfsigned.example/")
	if resp.StatusCode != http.StatusBadGateway || resp.Header.Get("X-Sandbox-Studio") != "refused" {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "certificate") || strings.Contains(body, "reached") {
		t.Fatalf("refusal does not say why: %q", body)
	}
}

// WebSockets pass through the terminated connection, as they do for proxy rules.
func TestBrowserVMUpgradesWebSockets(t *testing.T) {
	h := newBrowserHarness(t)
	d, err := proxy.SOCKS5("tcp", h.addr, &proxy.Auth{User: "vm1", Password: Password([]byte("key"), "vm1")}, proxy.Direct)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := d.Dial("tcp", "splice.example:443")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	raw.SetDeadline(time.Now().Add(5 * time.Second))
	c := tls.Client(raw, &tls.Config{ServerName: "splice.example", RootCAs: h.envCA, NextProtos: []string{"http/1.1"}})
	fmt.Fprintf(c, "GET /upgrade HTTP/1.1\r\nHost: splice.example\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, nil)
	if err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade: %v %v", resp, err)
	}
	if _, err := c.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(br, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("echo %q %v", buf, err)
	}
}

// A browser's connection that no rule covers is not held: each request is asked for with what
// the browser said about it, and is refused until the user decides. The query stays out of it.
func TestBrowserRequestsAreAskedOneByOne(t *testing.T) {
	h := newBrowserHarness(t)
	c := h.clientAs(t, "vm1", h.envCA)
	req, _ := http.NewRequest("GET", "https://ask.example/guides/a?token=abc", nil)
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Referer", "https://docs.example.com/index?q=1")
	resp, body := do(t, c, req)
	if resp.StatusCode != http.StatusForbidden || resp.Header.Get("X-Sandbox-Studio") != "refused" || !strings.Contains(body, "waiting for approval") {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	// A subresource on the same connection is asked for as well.
	resp, body = get(t, c, "https://ask.example/static/app.js")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("subresource status %d: %s", resp.StatusCode, body)
	}
	if n := h.requests(); n != 0 {
		t.Fatalf("%d refused requests reached the upstream", n)
	}

	h.policy.mu.Lock()
	defer h.policy.mu.Unlock()
	var asked []*policy.HTTPRequest
	for _, r := range h.policy.asked {
		if r.HTTP != nil {
			asked = append(asked, r.HTTP)
		}
		if r.HTTP == nil && (!r.Deferred || r.Host != "ask.example") {
			t.Errorf("connection asked for as %+v", r)
		}
	}
	if len(asked) != 2 {
		t.Fatalf("requests asked for: %+v", asked)
	}
	nav := asked[0]
	if nav.Method != "GET" || nav.Path != "/guides/a" || !nav.Navigation || nav.Site != "ask.example" || nav.Initiator != "https://docs.example.com" {
		t.Errorf("navigation %+v", nav)
	}
	if strings.Contains(nav.Path+nav.Initiator, "token") || strings.Contains(nav.Path+nav.Initiator, "q=1") {
		t.Errorf("the query reached the approval: %+v", nav)
	}
	if sub := asked[1]; sub.Navigation || sub.Path != "/static/app.js" || sub.Site != "" {
		t.Errorf("subresource %+v", sub)
	}
}

// A host on the tracker list is refused to the browser, before anything reaches it, and the
// connection log says why.
func TestBrowserVMTrackersAreRefused(t *testing.T) {
	h := newBrowserHarness(t)
	h.policy.mu.Lock()
	h.policy.errs = map[string]error{"www.google-analytics.com": policy.ErrTracker}
	h.policy.mu.Unlock()
	resp, body := get(t, h.clientAs(t, "vm1", h.envCA), "https://www.google-analytics.com/collect")
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(body, "tracker list") {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if n := h.requests(); n != 0 {
		t.Fatalf("%d requests reached the upstream", n)
	}
	waitFor(t, func() bool {
		log := h.gw.Conns.List("vm1")
		return len(log) == 1 && log[0].Verdict == VerdictDenied && log[0].Error == "tracker"
	}, func() string { return fmt.Sprintf("%+v", h.gw.Conns.List("vm1")) })
}

// A teammate's browser login is sent as Basic credentials to the pages of its hosts: always
// while the user drives, and while an agent drives only when the login lets agents use it.
// Pages that still ask are reported, with the login refused or held back.
func TestBrowserLoginsAreSentAsBasicCredentials(t *testing.T) {
	h := newBrowserHarness(t)
	agent := h.gw.Sandbox
	h.gw.Sandbox = func(ctx context.Context, id string) (store.Sandbox, error) {
		if id == "vm1" {
			return store.Sandbox{ID: "vm1", EnvironmentID: h.envID, Name: "browser-dev", Kind: store.SandboxKindBrowser, TeammateID: "tm1"}, nil
		}
		return agent(ctx, id)
	}
	var mu sync.Mutex
	driving, password, mayUse := false, "correct-horse", false
	var asks []string
	h.gw.Driver = func(_ context.Context, sb store.Sandbox) store.Sandbox {
		mu.Lock()
		defer mu.Unlock()
		if driving {
			return store.Sandbox{ID: "sb1", EnvironmentID: h.envID, Name: "dev"}
		}
		return sb
	}
	h.gw.BrowserLogins = func(_ context.Context, envID, teammateID string) ([]secrets.BrowserLogin, error) {
		mu.Lock()
		defer mu.Unlock()
		if envID != h.envID || teammateID != "tm1" {
			t.Errorf("logins of %s %s", envID, teammateID)
		}
		return []secrets.BrowserLogin{
			{Name: "NO_USER", Hosts: []string{"splice.example"}, Value: []byte("nobody")},
			{Name: "STAGING", Hosts: []string{"splice.example"}, Username: "ada", Value: []byte(password), AgentMayUse: mayUse},
		}, nil
	}
	h.gw.SignIn = func(sb store.Sandbox, host string, ask *BasicAsk) {
		mu.Lock()
		defer mu.Unlock()
		if ask == nil {
			asks = append(asks, sb.ID+" "+host+" -")
		} else {
			asks = append(asks, fmt.Sprintf("%s %s %q %s %v", sb.ID, host, ask.Realm, ask.Login, ask.Refused))
		}
	}
	page := func(url string, auth string) (int, string) {
		t.Helper()
		// Each request on a new connection, which picks the login afresh.
		c := h.clientAs(t, "vm1", h.envCA)
		req, _ := http.NewRequest("GET", url, nil)
		req.Header.Set("Sec-Fetch-Dest", "document")
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, body := do(t, c, req)
		return resp.StatusCode, body
	}
	lastAsk := func() string {
		mu.Lock()
		defer mu.Unlock()
		if len(asks) == 0 {
			return ""
		}
		return asks[len(asks)-1]
	}

	if code, body := page("https://splice.example/private", ""); code != 200 || strings.Contains(body, "correct-horse") {
		t.Fatalf("the user's page: %d %s", code, body)
	}
	if got := h.lastSeen(t).Header.Get("Authorization"); got != "Basic YWRhOmNvcnJlY3QtaG9yc2U=" {
		t.Errorf("upstream saw %q", got)
	}
	if got := lastAsk(); got != "vm1 splice.example -" {
		t.Errorf("sign-in %q", got)
	}
	// A page's own credentials are left alone.
	page("https://splice.example/private", "Basic b3RoZXI6b3RoZXI=")
	if got := h.lastSeen(t).Header.Get("Authorization"); got != "Basic b3RoZXI6b3RoZXI=" {
		t.Errorf("upstream saw %q", got)
	}
	// Another host gets none.
	page("https://example.com/", "")
	if got := h.lastSeen(t).Header.Get("Authorization"); got != "" {
		t.Errorf("example.com saw %q", got)
	}

	mu.Lock()
	driving = true
	mu.Unlock()
	if code, _ := page("https://splice.example/private", ""); code != http.StatusUnauthorized {
		t.Fatalf("an agent's page: %d", code)
	}
	if got := h.lastSeen(t).Header.Get("Authorization"); got != "" {
		t.Errorf("the agent's page sent %q", got)
	}
	if got := lastAsk(); got != `vm1 splice.example "Staging" STAGING false` {
		t.Errorf("sign-in %q", got)
	}

	mu.Lock()
	mayUse, password = true, "wrong"
	mu.Unlock()
	if code, _ := page("https://splice.example/private", ""); code != http.StatusUnauthorized {
		t.Fatalf("a wrong password: %d", code)
	}
	if got := lastAsk(); got != `vm1 splice.example "Staging" STAGING true` {
		t.Errorf("sign-in %q", got)
	}

	mu.Lock()
	password = "correct-horse"
	mu.Unlock()
	if code, body := page("https://splice.example/private", ""); code != 200 || strings.Contains(body, "correct-horse") {
		t.Fatalf("an agent's page with a login it may use: %d %s", code, body)
	}
	// What a page loads doesn't count, only pages.
	n := len(asks)
	get(t, h.clientAs(t, "vm1", h.envCA), "https://splice.example/unauthorized")
	if len(asks) != n {
		t.Errorf("a subresource was reported: %v", asks[n:])
	}
}
