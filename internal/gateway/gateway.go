// Package gateway is the SOCKS5 endpoint every sandbox connection goes through,
// including those of containers inside the sandbox. It names the destination (TLS SNI,
// HTTP Host, or the sandbox's own DNS answers), asks the policy what to do, and then
// connects, refuses or holds the connection until the user decides.
package gateway

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/policy"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/secrets"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Gateway serves SOCKS5 for sandboxes. Each sandbox authenticates with its ID as the
// user name and a password derived from Key, which only Studio and microsandbox know.
type Gateway struct {
	Addr   string // where sandboxes' VMs reach the gateway
	Key    []byte
	Policy interface {
		Decide(context.Context, policy.Request) (store.Rule, error)
	}
	Sandbox   func(ctx context.Context, id string) (store.Sandbox, error)
	Resolvers *Resolvers
	Conns     *ConnLog
	Log       *slog.Logger
	// Dial connects upstream; nil uses a dialer that refuses non-public addresses.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
	// CA issues the certificates presented to sandboxes on intercepted connections.
	CA interface {
		Leaf(ctx context.Context, envID, host string) (*tls.Certificate, error)
	}
	Secrets interface {
		Bindings(ctx context.Context, envID string) ([]secrets.Binding, error)
	}

	names         func(sandboxID string) Names // replaces Resolvers.Names in tests
	upstreamRoots *x509.CertPool               // replaces the system roots in tests
}

// ErrNoNetwork is returned for sandboxes created before Studio routed their traffic.
var ErrNoNetwork = errors.New("this sandbox was created before network rules existed; recreate it")

// Attach starts a sandbox's resolver and makes its gateway password available to
// microsandbox, which reads it from Studio's environment when the VM starts.
func (g *Gateway) Attach(sb store.Sandbox) (runtime.Egress, error) {
	if sb.DNSPort == 0 {
		return runtime.Egress{}, ErrNoNetwork
	}
	if err := g.Resolvers.Start(sb.ID, sb.DNSPort); err != nil {
		return runtime.Egress{}, fmt.Errorf("sandbox resolver: %w", err)
	}
	env := PasswordEnv(sb.ID)
	if err := os.Setenv(env, Password(g.Key, sb.ID)); err != nil {
		return runtime.Egress{}, err
	}
	return runtime.Egress{
		Nameserver:  net.JoinHostPort("127.0.0.1", strconv.Itoa(sb.DNSPort)),
		Proxy:       g.Addr,
		User:        sb.ID,
		PasswordEnv: env,
	}, nil
}

// Detach stops the resolver of a deleted sandbox and forgets its connections.
func (g *Gateway) Detach(sandboxID string) {
	g.Resolvers.Stop(sandboxID)
	os.Unsetenv(PasswordEnv(sandboxID))
	g.Conns.Forget(sandboxID)
}

// Password returns the SOCKS5 password of a sandbox.
func Password(key []byte, sandboxID string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte("socks5:" + sandboxID))
	return hex.EncodeToString(m.Sum(nil))
}

// PasswordEnv names the environment variable microsandbox reads a sandbox's SOCKS5
// password from. It must be set in Studio's environment whenever a sandbox is created or
// started.
func PasswordEnv(sandboxID string) string { return "STUDIO_GW_" + strings.ToUpper(sandboxID) }

// Serve accepts connections until l is closed.
func (g *Gateway) Serve(l net.Listener) error {
	for {
		c, err := l.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go g.handle(c)
	}
}

// Sniff timeouts: web ports always speak first; on other ports a server may speak first
// (SSH, SMTP), so wait only briefly, and barely at all when DNS already gave a name.
const (
	sniffWeb   = 5 * time.Second
	sniffNamed = 300 * time.Millisecond
	sniffOther = time.Second
)

func (g *Gateway) handle(c net.Conn) {
	defer c.Close()
	br := bufio.NewReaderSize(c, peekSize)
	c.SetDeadline(time.Now().Add(10 * time.Second))
	sb, target, err := g.handshake(c, br)
	if err != nil {
		g.Log.Debug("socks handshake", "remote", c.RemoteAddr(), "err", err)
		return
	}
	c.SetDeadline(time.Time{})

	host, source := target.domain, "socks"
	if host == "" {
		host, source = target.addr.String(), "ip"
		if names := g.lookupNames(sb.ID); names != nil {
			if name, ok := names.Lookup(target.addr); ok && validHost(name) {
				host, source = name, "dns"
			}
		}
	}
	timeout := sniffOther
	switch {
	case target.port == 80 || target.port == 443:
		timeout = sniffWeb
	case source != "ip":
		timeout = sniffNamed
	}
	name, proto := sniff(c, br, timeout)
	if name = policy.Normalize(name); validHost(name) {
		host, source = name, proto
	}
	port := int(target.port)
	entry := g.Conns.add(sb.ID, Conn{Host: host, Port: port, Address: target.String(), Source: source})
	verdict, ruleID, problem := VerdictFailed, "", ""
	var sent, received int64
	defer func() { g.Conns.finish(entry, verdict, ruleID, problem, sent, received) }()

	rule, err := g.Policy.Decide(context.Background(), policy.Request{
		EnvironmentID: sb.EnvironmentID, SandboxID: sb.ID, SandboxName: sb.Name, Host: host, Port: port,
	})
	ruleID = rule.ID
	switch {
	case errors.Is(err, policy.ErrUndecided):
		verdict = VerdictUndecided
		g.refuse(c, br, sb.EnvironmentID, host, proto, fmt.Sprintf("Connections to %s:%d are waiting for approval in Sandbox Studio. Retry once the user has allowed them.", host, port))
		return
	case errors.Is(err, policy.ErrDismissed):
		verdict = VerdictDenied
		g.refuse(c, br, sb.EnvironmentID, host, proto, fmt.Sprintf("The user dismissed the request to connect to %s:%d.", host, port))
		return
	case err != nil:
		problem = err.Error()
		g.Log.Error("network policy", "sandbox", sb.ID, "host", host, "err", err)
		return
	case rule.Action == store.ActionDeny:
		verdict = VerdictDenied
		g.refuse(c, br, sb.EnvironmentID, host, proto, fmt.Sprintf("Connections to %s:%d are blocked by a Sandbox Studio network rule.", host, port))
		return
	case rule.Action == store.ActionCaddy:
		problem = "caddy rules are not supported yet"
		return
	}

	all, err := g.bindings(sb.EnvironmentID)
	if err != nil {
		problem = err.Error()
		g.Log.Error("secrets", "sandbox", sb.ID, "err", err)
		return
	}
	web := proto == "tls" || proto == "http"
	if bound, _ := boundTo(all, host); rule.Action == store.ActionProxy || len(bound) > 0 && web {
		if !web {
			problem = "proxy rules only handle HTTP and TLS connections"
			return
		}
		if proto == "tls" && g.CA == nil {
			problem = "no CA to intercept TLS with"
			return
		}
		g.Conns.opened(entry, ruleID, true)
		verdict = VerdictAllowed
		sent, received, problem = g.intercept(c, br, sb.EnvironmentID, host, port, proto == "tls", rule, all)
		return
	}

	// Dial the name, not the address the sandbox picked: a sandbox can't get a connection
	// to one host approved by naming another in its SNI or Host header.
	dial := g.Dial
	if dial == nil {
		dial = publicDialer.DialContext
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	up, err := dial(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	cancel()
	if err != nil {
		problem = err.Error()
		return
	}
	defer up.Close()
	g.Conns.opened(entry, ruleID, false)
	verdict = VerdictAllowed
	sent, received = splice(c, br, up)
}

// Names maps addresses a sandbox resolved back to the names it asked for.
type Names interface {
	Lookup(netip.Addr) (string, bool)
}

func (g *Gateway) lookupNames(sandboxID string) Names {
	if g.names != nil {
		return g.names(sandboxID)
	}
	return g.Resolvers.Names(sandboxID)
}

// splice copies both ways until both directions are done, passing on half-closes.
// Bytes the gateway peeked at are still in br and go upstream first.
func splice(c net.Conn, br io.Reader, up net.Conn) (sent, received int64) {
	done := make(chan int64)
	go func() {
		n, _ := io.Copy(up, br)
		closeWrite(up)
		done <- n
	}()
	received, _ = io.Copy(c, up)
	closeWrite(c)
	return <-done, received
}

func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		cw.CloseWrite()
	} else {
		c.Close()
	}
}

func (g *Gateway) bindings(envID string) ([]secrets.Binding, error) {
	if g.Secrets == nil {
		return nil, nil
	}
	return g.Secrets.Bindings(context.Background(), envID)
}

// refuse tells an HTTP or TLS client why its request failed. Other protocols only see the
// connection close.
func (g *Gateway) refuse(c net.Conn, br *bufio.Reader, envID, host, proto, msg string) {
	if proto == "tls" && g.CA != nil {
		g.refuseTLS(c, br, envID, host, msg)
		return
	}
	if proto != "http" {
		return
	}
	msg += "\n"
	fmt.Fprintf(c, "HTTP/1.1 403 Forbidden\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Length: %d\r\nConnection: close\r\nX-Sandbox-Studio: refused\r\n\r\n%s", len(msg), msg)
}

// --- SOCKS5 (RFC 1928, RFC 1929) -------------------------------------------------------

type socksTarget struct {
	domain string     // set for ATYP domain
	addr   netip.Addr // set for ATYP IPv4/IPv6
	port   uint16
}

func (t socksTarget) String() string {
	if t.domain != "" {
		return net.JoinHostPort(t.domain, strconv.Itoa(int(t.port)))
	}
	return netip.AddrPortFrom(t.addr, t.port).String()
}

var errAuth = errors.New("authentication failed")

// handshake authenticates the sandbox and reads its CONNECT request. On success the
// client has been told the connection is open, so it starts sending.
func (g *Gateway) handshake(c net.Conn, br *bufio.Reader) (store.Sandbox, socksTarget, error) {
	var sb store.Sandbox
	var t socksTarget
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(br, hdr); err != nil || hdr[0] != 5 {
		return sb, t, fmt.Errorf("not SOCKS5: %v", err)
	}
	offered := make([]byte, hdr[1])
	if _, err := io.ReadFull(br, offered); err != nil {
		return sb, t, err
	}
	if !strings.ContainsRune(string(offered), 2) {
		c.Write([]byte{5, 0xff})
		return sb, t, errors.New("client does not offer user/password authentication")
	}
	c.Write([]byte{5, 2})

	user, pass, err := readCredentials(br)
	if err != nil {
		return sb, t, err
	}
	want := Password(g.Key, user)
	if !hmac.Equal([]byte(pass), []byte(want)) {
		c.Write([]byte{1, 1})
		return sb, t, errAuth
	}
	if sb, err = g.Sandbox(context.Background(), user); err != nil {
		c.Write([]byte{1, 1})
		return sb, t, fmt.Errorf("sandbox %q: %w", user, err)
	}
	c.Write([]byte{1, 0})

	req := make([]byte, 4)
	if _, err := io.ReadFull(br, req); err != nil {
		return sb, t, err
	}
	if req[0] != 5 || req[1] != 1 { // CONNECT only
		c.Write([]byte{5, 7, 0, 1, 0, 0, 0, 0, 0, 0})
		return sb, t, fmt.Errorf("unsupported command %d", req[1])
	}
	switch req[3] {
	case 1, 4:
		ip := make([]byte, map[byte]int{1: 4, 4: 16}[req[3]])
		if _, err := io.ReadFull(br, ip); err != nil {
			return sb, t, err
		}
		t.addr, _ = netip.AddrFromSlice(ip)
		t.addr = t.addr.Unmap()
	case 3:
		n, err := br.ReadByte()
		if err != nil {
			return sb, t, err
		}
		name := make([]byte, n)
		if _, err := io.ReadFull(br, name); err != nil {
			return sb, t, err
		}
		if t.domain = policy.Normalize(string(name)); !validHost(t.domain) {
			c.Write([]byte{5, 4, 0, 1, 0, 0, 0, 0, 0, 0})
			return sb, t, fmt.Errorf("invalid destination %q", name)
		}
		if ip, err := netip.ParseAddr(t.domain); err == nil {
			t.domain, t.addr = "", ip.Unmap()
		}
	default:
		c.Write([]byte{5, 8, 0, 1, 0, 0, 0, 0, 0, 0})
		return sb, t, fmt.Errorf("unsupported address type %d", req[3])
	}
	port := make([]byte, 2)
	if _, err := io.ReadFull(br, port); err != nil {
		return sb, t, err
	}
	t.port = binary.BigEndian.Uint16(port)
	_, err = c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
	return sb, t, err
}

func readCredentials(br *bufio.Reader) (user, pass string, err error) {
	read := func() (string, error) {
		n, err := br.ReadByte()
		if err != nil {
			return "", err
		}
		b := make([]byte, n)
		_, err = io.ReadFull(br, b)
		return string(b), err
	}
	if v, err := br.ReadByte(); err != nil || v != 1 {
		return "", "", fmt.Errorf("bad auth version: %v", err)
	}
	if user, err = read(); err != nil {
		return "", "", err
	}
	pass, err = read()
	return user, pass, err
}

// --- dialing ---------------------------------------------------------------------------

// publicDialer only connects to public addresses. Names are resolved on the host, so
// without this a sandbox could reach the host's loopback services (Studio's own API) or
// the LAN through any approved name that resolves there.
var publicDialer = &net.Dialer{
	Timeout: 15 * time.Second,
	Control: func(network, address string, _ syscall.RawConn) error {
		ap, err := netip.ParseAddrPort(address)
		if err != nil {
			return err
		}
		if !public(ap.Addr()) {
			return fmt.Errorf("refusing to connect to non-public address %s", ap.Addr())
		}
		return nil
	},
}

var nonPublic = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), // carrier-grade NAT, also Tailscale
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"), // benchmarking; Studio's virtual host services
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("2001:db8::/32"),
}

func public(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return false
	}
	for _, p := range nonPublic {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}
