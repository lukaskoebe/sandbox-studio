package gateway

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/proxy"

	"github.com/lukaskoebe/sandbox-studio/internal/policy"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

type fakePolicy struct {
	mu    sync.Mutex
	asked []policy.Request
	rules map[string]store.Rule // by host
}

func (p *fakePolicy) Decide(_ context.Context, req policy.Request) (store.Rule, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.asked = append(p.asked, req)
	if r, ok := p.rules[req.Host]; ok {
		return r, nil
	}
	return store.Rule{}, policy.ErrUndecided
}

type fakeNames map[netip.Addr]string

func (n fakeNames) Lookup(ip netip.Addr) (string, bool) { name, ok := n[ip]; return name, ok }

type harness struct {
	gw     *Gateway
	addr   string
	policy *fakePolicy
	mu     sync.Mutex
	dialed []string
}

// newHarness runs a gateway whose upstream dials all reach an in-process server that
// sends banner on connect (if any) and then echoes.
func newHarness(t *testing.T, banner string) *harness {
	t.Helper()
	h := &harness{policy: &fakePolicy{rules: map[string]store.Rule{
		"allowed.example": {ID: "r1", Action: store.ActionAllow},
		"github.com":      {ID: "r2", Action: store.ActionAllow},
		"denied.example":  {ID: "r3", Action: store.ActionDeny},
		"203.0.113.9":     {ID: "r4", Action: store.ActionAllow},
	}}}
	h.gw = &Gateway{
		Key:    []byte("key"),
		Policy: h.policy,
		Sandbox: func(_ context.Context, id string) (store.Sandbox, error) {
			if id != "sb1" {
				return store.Sandbox{}, store.ErrNotFound
			}
			return store.Sandbox{ID: "sb1", EnvironmentID: "env", Name: "dev"}, nil
		},
		names: func(string) Names { return fakeNames{netip.MustParseAddr("192.0.2.7"): "github.com"} },
		Log:   slog.New(slog.DiscardHandler),
		Conns: &ConnLog{},
		Dial: func(_ context.Context, _, addr string) (net.Conn, error) {
			h.mu.Lock()
			h.dialed = append(h.dialed, addr)
			h.mu.Unlock()
			client, server := net.Pipe()
			go func() {
				defer server.Close()
				server.Write([]byte(banner))
				io.Copy(server, server)
			}()
			return client, nil
		},
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go h.gw.Serve(l)
	h.addr = l.Addr().String()
	return h
}

func (h *harness) dial(t *testing.T, password, target string) (net.Conn, error) {
	t.Helper()
	d, err := proxy.SOCKS5("tcp", h.addr, &proxy.Auth{User: "sb1", Password: password}, proxy.Direct)
	if err != nil {
		t.Fatal(err)
	}
	c, err := d.Dial("tcp", target)
	if c != nil {
		c.SetDeadline(time.Now().Add(5 * time.Second))
	}
	return c, err
}

func (h *harness) lastDial() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.dialed) == 0 {
		return ""
	}
	return h.dialed[len(h.dialed)-1]
}

func clientHello(t *testing.T, serverName string) []byte {
	t.Helper()
	a, b := net.Pipe()
	go tls.Client(a, &tls.Config{ServerName: serverName}).Handshake()
	defer a.Close()
	buf := make([]byte, peekSize)
	n, err := b.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	return buf[:n]
}

func TestAuthentication(t *testing.T) {
	h := newHarness(t, "")
	if _, err := h.dial(t, "wrong", "192.0.2.1:443"); err == nil {
		t.Fatal("wrong password accepted")
	}
	if c, err := h.dial(t, Password([]byte("key"), "sb1"), "192.0.2.1:443"); err != nil {
		t.Fatalf("right password refused: %v", err)
	} else {
		c.Close()
	}
}

func TestTLSIsNamedBySNIAndDialedByName(t *testing.T) {
	h := newHarness(t, "")
	c, err := h.dial(t, Password([]byte("key"), "sb1"), "192.0.2.1:443")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	hello := clientHello(t, "Allowed.Example")
	c.Write(hello)
	echo := make([]byte, len(hello))
	if _, err := io.ReadFull(c, echo); err != nil || string(echo) != string(hello) {
		t.Fatalf("ClientHello not replayed upstream: %v", err)
	}
	if got := h.lastDial(); got != "allowed.example:443" {
		t.Fatalf("dialed %q, want the SNI name", got)
	}
}

func TestServerFirstProtocolUsesDNSName(t *testing.T) {
	h := newHarness(t, "SSH-2.0-test\r\n")
	start := time.Now()
	c, err := h.dial(t, Password([]byte("key"), "sb1"), "192.0.2.7:22")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil || line != "SSH-2.0-test\r\n" {
		t.Fatalf("banner: %q %v", line, err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("server-first connection waited %v", time.Since(start))
	}
	if got := h.lastDial(); got != "github.com:22" {
		t.Fatalf("dialed %q", got)
	}
}

func TestHTTPRefusals(t *testing.T) {
	h := newHarness(t, "")
	for host, want := range map[string]string{
		"denied.example":  "blocked by a Sandbox Studio network rule",
		"unknown.example": "waiting for approval",
	} {
		c, err := h.dial(t, Password([]byte("key"), "sb1"), "192.0.2.1:80")
		if err != nil {
			t.Fatal(err)
		}
		c.Write([]byte("GET / HTTP/1.1\r\nHost: " + host + ":80\r\nUser-Agent: test\r\n\r\n"))
		resp, _ := io.ReadAll(c)
		c.Close()
		if !strings.HasPrefix(string(resp), "HTTP/1.1 403 ") || !strings.Contains(string(resp), want) {
			t.Errorf("%s: got %q", host, resp)
		}
	}
	log := h.gw.Conns.List("sb1")
	if len(log) != 2 || log[0].Host != "unknown.example" || log[0].Verdict != VerdictUndecided || log[1].Verdict != VerdictDenied || log[1].Source != "http" {
		t.Fatalf("connection log: %+v", log)
	}
}

func TestIPWithoutNameAndDomainTargets(t *testing.T) {
	h := newHarness(t, "hi")
	c, err := h.dial(t, Password([]byte("key"), "sb1"), "203.0.113.9:5432")
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 2)
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatal(err)
	}
	c.Close()
	if got := h.lastDial(); got != "203.0.113.9:5432" {
		t.Fatalf("dialed %q", got)
	}
	// A SOCKS domain target (as curl --socks5-hostname sends) names the connection too.
	c, err = h.dial(t, Password([]byte("key"), "sb1"), "github.com:9418")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatal(err)
	}
	c.Close()
	if got := h.lastDial(); got != "github.com:9418" {
		t.Fatalf("dialed %q", got)
	}
	h.policy.mu.Lock()
	defer h.policy.mu.Unlock()
	if last := h.policy.asked[len(h.policy.asked)-1]; last.SandboxName != "dev" || last.EnvironmentID != "env" || last.Port != 9418 {
		t.Fatalf("policy request: %+v", last)
	}
}

func TestSniffHelpers(t *testing.T) {
	if got := serverName(clientHello(t, "registry.npmjs.org")[5:]); got != "registry.npmjs.org" {
		t.Errorf("SNI: %q", got)
	}
	// A hello cut short (one record of a larger message) still names the server.
	if got := serverName(clientHello(t, "example.com")[5:300]); got != "example.com" {
		t.Errorf("truncated hello: %q", got)
	}
	if got := serverName([]byte{1, 0, 0, 200, 3, 3}); got != "" {
		t.Errorf("truncated hello: %q", got)
	}
	for head, want := range map[string]string{
		"GET / HTTP/1.1\r\nHost: example.com\r\n\r\n":          "example.com",
		"GET / HTTP/1.1\r\nhost:  Example.com:8080 \r\n\r\n":   "Example.com",
		"GET / HTTP/1.1\r\nHost: [2001:db8::1]:80\r\n\r\n":     "2001:db8::1",
		"GET / HTTP/1.1\r\nX: y\r\n\r\nHost: smuggled.example": "",
	} {
		if got := httpHost([]byte(head)); got != want {
			t.Errorf("%q: got %q, want %q", head, got, want)
		}
	}
}

func TestPublicAddresses(t *testing.T) {
	for addr, want := range map[string]bool{
		"1.1.1.1": true, "2606:4700::1111": true,
		"127.0.0.1": false, "::1": false, "10.1.2.3": false, "192.168.178.1": false, "169.254.169.254": false,
		"100.100.1.1": false, "198.18.0.1": false, "0.0.0.0": false, "::ffff:127.0.0.1": false, "fd00::1": false,
		"224.0.0.1": false, "255.255.255.255": false,
	} {
		if got := public(netip.MustParseAddr(addr)); got != want {
			t.Errorf("%s: got %v", addr, got)
		}
	}
	_, err := publicDialer.DialContext(context.Background(), "tcp", "127.0.0.1:1")
	if err == nil || !strings.Contains(err.Error(), "non-public") {
		t.Fatalf("loopback dial: %v", err)
	}
	var opErr *net.OpError
	if !errors.As(err, &opErr) {
		t.Fatalf("unexpected error type %T", err)
	}
}
