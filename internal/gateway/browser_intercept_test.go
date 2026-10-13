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
	"testing"
	"time"

	"golang.org/x/net/proxy"

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
		io.WriteString(w, "reached")
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
	c.Write([]byte("ping"))
	buf := make([]byte, 4)
	if _, err := io.ReadFull(br, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("echo %q %v", buf, err)
	}
}
