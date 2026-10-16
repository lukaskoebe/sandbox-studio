package gateway

import (
	"context"
	"encoding/binary"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func TestValidPrivateHost(t *testing.T) {
	ok := []struct {
		in, host string
		ports    []int
	}{
		{"git.company.local", "git.company.local", []int{}},
		{" GIT.Company.Local. ", "git.company.local", []int{}},
		{"*.company.local", "*.company.local", []int{}},
		{"*.corp.example.net", "*.corp.example.net", []int{}},
		{"192.168.178.20", "192.168.178.20", []int{}},
		{"::ffff:10.0.0.5", "10.0.0.5", []int{}},
		{"192.168.178.7/24", "192.168.178.0/24", []int{}},
		{"100.64.0.0/10", "100.64.0.0/10", []int{}},
		{"10.0.0.5/32", "10.0.0.5", []int{}},
	}
	for _, c := range ok {
		got, err := ValidPrivateHost(store.PrivateHost{Host: c.in})
		if err != nil || got.Host != c.host || !reflect.DeepEqual(got.Ports, c.ports) {
			t.Errorf("%q: got %+v, %v", c.in, got, err)
		}
	}
	got, err := ValidPrivateHost(store.PrivateHost{Host: "git.company.local", Ports: []int{8443, 22, 8443}})
	if err != nil || !reflect.DeepEqual(got.Ports, []int{22, 8443}) {
		t.Errorf("ports: got %+v, %v", got, err)
	}

	refused := []struct {
		name string
		in   store.PrivateHost
	}{
		{"any host", store.PrivateHost{Host: "*"}},
		{"public IPv4 address", store.PrivateHost{Host: "8.8.8.8"}},
		{"loopback address", store.PrivateHost{Host: "127.0.0.1"}},
		{"link-local address", store.PrivateHost{Host: "169.254.169.254"}},
		{"Studio's virtual address", store.PrivateHost{Host: "198.18.0.1"}},
		{"IPv6 address", store.PrivateHost{Host: "fd00::5"}},
		{"range wider than a private range", store.PrivateHost{Host: "192.0.0.0/8"}},
		{"public range", store.PrivateHost{Host: "8.8.8.0/24"}},
		{"IPv6 range", store.PrivateHost{Host: "fd00::/8"}},
		{"wildcard over a top-level domain", store.PrivateHost{Host: "*.com"}},
		{"wildcard over a local top-level domain", store.PrivateHost{Host: "*.local"}},
		{"wildcard over a public suffix", store.PrivateHost{Host: "*.co.uk"}},
		{"invalid name", store.PrivateHost{Host: "git company.local"}},
		{"port 0", store.PrivateHost{Host: "git.company.local", Ports: []int{0}}},
		{"port above 65535", store.PrivateHost{Host: "git.company.local", Ports: []int{65536}}},
	}
	for _, c := range refused {
		if _, err := ValidPrivateHost(c.in); err == nil {
			t.Errorf("%s (%q) accepted", c.name, c.in.Host)
		}
	}
}

func TestListedNames(t *testing.T) {
	exact := []store.PrivateHost{{Host: "git.company.local"}}
	wild := []store.PrivateHost{{Host: "*.corp.local", Ports: []int{22}}}
	cases := []struct {
		name string
		list []store.PrivateHost
		host string
		port int
		want bool
	}{
		{"exact name on default port 443", exact, "git.company.local", 443, true},
		{"exact name on default port 80", exact, "git.company.local", 80, true},
		{"exact name, case and trailing dot", exact, "GIT.company.local.", 443, true},
		{"exact name on another port", exact, "git.company.local", 22, false},
		{"other name", exact, "other.company.local", 443, false},
		{"parent of an exact name", exact, "company.local", 443, false},
		{"wildcard covers the domain itself", wild, "corp.local", 22, true},
		{"wildcard covers subdomains", wild, "ssh.corp.local", 22, true},
		{"wildcard on a port it doesn't list", wild, "ssh.corp.local", 443, false},
		{"wildcard does not cover other domains", wild, "corp.local.evil.example", 22, false},
		{"listed address", []store.PrivateHost{{Host: "10.0.0.5", Ports: []int{22}}}, "10.0.0.5", 22, true},
		{"listed address on another port", []store.PrivateHost{{Host: "10.0.0.5", Ports: []int{22}}}, "10.0.0.5", 443, false},
		{"other address", []store.PrivateHost{{Host: "10.0.0.5"}}, "10.0.0.6", 443, false},
		{"address in a listed range", []store.PrivateHost{{Host: "192.168.178.0/24"}}, "192.168.178.20", 80, true},
		{"address outside a listed range", []store.PrivateHost{{Host: "192.168.178.0/24"}}, "192.168.179.20", 80, false},
		{"a name never lists an address", []store.PrivateHost{{Host: "*.corp.local"}}, "10.0.0.5", 443, false},
		{"an address never lists a name", []store.PrivateHost{{Host: "10.0.0.0/8"}}, "git.corp.local", 443, false},
		{"no list", nil, "git.company.local", 443, false},
	}
	for _, c := range cases {
		if got := listed(c.list, c.host, c.port); got != c.want {
			t.Errorf("%s: listed = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestDialAddresses is the dial guard's table: which addresses a connection may reach, for a
// name on the private hosts list and for one that isn't.
func TestDialAddresses(t *testing.T) {
	const studioPort = 7879
	reserved := []uint16{studioPort}
	cases := []struct {
		addr             string
		listed, unlisted bool // allowed for a listed name, and for a name that isn't listed
	}{
		{"8.8.8.8", true, true},             // public
		{"172.32.0.1", true, true},          // just outside 172.16/12, so public
		{"10.1.2.3", true, false},           // RFC 1918
		{"172.16.0.1", true, false},         // RFC 1918
		{"192.168.1.1", true, false},        // RFC 1918
		{"100.64.0.1", true, false},         // carrier-grade NAT, Tailscale
		{"fd00::1", true, false},            // unique local
		{"::ffff:10.1.2.3", true, false},    // IPv4-mapped private
		{"127.0.0.1", false, false},         // loopback
		{"::1", false, false},               // loopback
		{"169.254.169.254", false, false},   // link-local: the cloud metadata address
		{"fe80::1", false, false},           // link-local
		{"198.18.0.1", false, false},        // Studio's virtual services
		{"0.0.0.0", false, false},           // unspecified
		{"224.0.0.1", false, false},         // multicast
		{"240.0.0.1", false, false},         // reserved
		{"203.0.113.9", false, false},       // documentation
		{"2002:0a01:0203::1", false, false}, // 6to4, which can carry private IPv4
	}
	for _, c := range cases {
		addr := netip.MustParseAddr(c.addr)
		hostport := netip.AddrPortFrom(addr, 443).String()
		if err := checkPrivate(reserved)("tcp", hostport, nil); (err == nil) != c.listed {
			t.Errorf("%s for a listed name: err = %v, want allowed %v", c.addr, err, c.listed)
		}
		if err := checkPublic("tcp", hostport, nil); (err == nil) != c.unlisted {
			t.Errorf("%s for a name that isn't listed: err = %v, want allowed %v", c.addr, err, c.unlisted)
		}
	}
	if err := checkPrivate(reserved)("tcp", netip.AddrPortFrom(netip.MustParseAddr("10.1.2.3"), studioPort).String(), nil); err == nil {
		t.Error("a private host reached Studio's own port")
	}
}

// fakeDNS returns a resolver that answers from records alone: no packet leaves the test.
func fakeDNS(records map[string][]netip.Addr) *net.Resolver {
	return &net.Resolver{
		PreferGo: true,
		Dial: func(context.Context, string, string) (net.Conn, error) {
			client, server := net.Pipe()
			go serveFakeDNS(server, records)
			return client, nil
		},
	}
}

// serveFakeDNS answers DNS queries over one stream, as a resolver over TCP does.
func serveFakeDNS(c net.Conn, records map[string][]netip.Addr) {
	defer c.Close()
	for {
		var size [2]byte
		if _, err := io.ReadFull(c, size[:]); err != nil {
			return
		}
		raw := make([]byte, binary.BigEndian.Uint16(size[:]))
		if _, err := io.ReadFull(c, raw); err != nil {
			return
		}
		var p dnsmessage.Parser
		h, err := p.Start(raw)
		if err != nil {
			return
		}
		q, err := p.Question()
		if err != nil {
			return
		}
		name := strings.TrimSuffix(strings.ToLower(q.Name.String()), ".")
		addrs, known := records[name]
		header := dnsmessage.Header{ID: h.ID, Response: true, RecursionAvailable: true}
		if !known {
			header.RCode = dnsmessage.RCodeNameError
		}
		b := dnsmessage.NewBuilder(nil, header)
		if b.StartQuestions() != nil || b.Question(q) != nil || b.StartAnswers() != nil {
			return
		}
		for _, a := range addrs {
			hdr := dnsmessage.ResourceHeader{Name: q.Name, Class: dnsmessage.ClassINET, TTL: 60}
			switch {
			case q.Type == dnsmessage.TypeA && a.Is4():
				if b.AResource(hdr, dnsmessage.AResource{A: a.As4()}) != nil {
					return
				}
			case q.Type == dnsmessage.TypeAAAA && a.Is6():
				if b.AAAAResource(hdr, dnsmessage.AAAAResource{AAAA: a.As16()}) != nil {
					return
				}
			}
		}
		msg, err := b.Finish()
		if err != nil {
			return
		}
		out := append(binary.BigEndian.AppendUint16(nil, uint16(len(msg))), msg...)
		if _, err := c.Write(out); err != nil {
			return
		}
	}
}

// TestDialGuardResolvesNames covers the dial path end to end with a fake resolver: the
// addresses a name resolves to are checked when the gateway dials it, and every refusal here
// happens before a connection is made.
func TestDialGuardResolvesNames(t *testing.T) {
	records := map[string][]netip.Addr{
		"rebind.example":       {netip.MustParseAddr("10.0.0.5")},  // not listed: DNS rebinding
		"git.company.local":    {netip.MustParseAddr("127.0.0.1")}, // listed, but loopback
		"meta.company.local":   {netip.MustParseAddr("169.254.169.254")},
		"ssh.company.local":    {netip.MustParseAddr("10.0.0.5")}, // listed on 22 only
		"studio.company.local": {netip.MustParseAddr("10.0.0.5")},
	}
	g := &Gateway{
		Addr:     "127.0.0.1:7879",
		Log:      slog.New(slog.DiscardHandler),
		resolver: fakeDNS(records),
		Own:      []string{"127.0.0.1:7878"},
		Conns:    &ConnLog{},
		PrivateHosts: func(_ context.Context, envID string) ([]store.PrivateHost, error) {
			if envID != "env" {
				return nil, nil
			}
			return []store.PrivateHost{
				{Host: "git.company.local"},
				{Host: "meta.company.local"},
				{Host: "ssh.company.local", Ports: []int{22}},
				{Host: "studio.company.local", Ports: []int{7878}},
			}, nil
		},
	}
	cases := []struct {
		addr, want string // want is a fragment of the refusal, or "" for no refusal before a connection
	}{
		{"rebind.example:443", "non-public address 10.0.0.5"},
		{"git.company.local:443", "not a public or private address"},
		{"meta.company.local:443", "not a public or private address"},
		{"ssh.company.local:443", "non-public address 10.0.0.5"}, // port 443 isn't listed
		{"studio.company.local:7878", "Studio's own port"},
		{"10.0.0.5:443", "non-public address 10.0.0.5"}, // a name never lists the address it resolves to
		{"127.0.0.1:443", "non-public address 127.0.0.1"},
		{"169.254.169.254:80", "non-public address 169.254.169.254"},
	}
	for _, c := range cases {
		conn, err := g.guardedDialer("env", nil)(context.Background(), "tcp", c.addr)
		if conn != nil {
			conn.Close()
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("dial %s: err = %v, want a refusal containing %q", c.addr, err, c.want)
		}
	}
}

func TestCheckPrivateRefusesHostAddresses(t *testing.T) {
	saved := interfaceAddrs
	interfaceAddrs = func() ([]net.Addr, error) {
		return []net.Addr{&net.IPNet{IP: net.ParseIP("192.168.1.5"), Mask: net.CIDRMask(24, 32)}}, nil
	}
	t.Cleanup(func() { interfaceAddrs = saved })
	check := checkPrivate(nil)
	if err := check("tcp", "192.168.1.5:3000", nil); err == nil {
		t.Fatal("a private host reached the host's own address")
	}
	if err := check("tcp", "192.168.1.6:443", nil); err != nil {
		t.Fatalf("another private address: %v", err)
	}
}

func TestDialGuardReachesListedAddressesButNotTheHosts(t *testing.T) {
	saved := interfaceAddrs
	interfaceAddrs = func() ([]net.Addr, error) {
		return []net.Addr{&net.IPNet{IP: net.ParseIP("192.168.1.5"), Mask: net.CIDRMask(24, 32)}}, nil
	}
	t.Cleanup(func() { interfaceAddrs = saved })
	g := &Gateway{
		Addr: "127.0.0.1:7879", Log: slog.New(slog.DiscardHandler), Conns: &ConnLog{},
		PrivateHosts: func(context.Context, string) ([]store.PrivateHost, error) {
			return []store.PrivateHost{{Host: "192.168.1.0/24", Ports: []int{22}}}, nil
		},
	}
	for addr, want := range map[string]string{
		"192.168.1.5:22":  "an address of this computer", // listed, but the host's own
		"192.168.1.6:443": "non-public address",          // listed on 22 only
		"192.168.2.6:22":  "non-public address",          // not listed
	} {
		conn, err := g.guardedDialer("env", nil)(context.Background(), "tcp", addr)
		if conn != nil {
			conn.Close()
		}
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("dial %s: err = %v, want a refusal containing %q", addr, err, want)
		}
	}
}

func TestDialGuardReachesOpenHostPorts(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			_, _ = c.Write([]byte("SSH-2.0-test\r\n"))
			c.Close()
		}
	}()
	port := l.Addr().(*net.TCPAddr).Port
	g := &Gateway{
		Addr: "127.0.0.1:7879", Log: slog.New(slog.DiscardHandler), Conns: &ConnLog{},
		HostPorts: func(_ context.Context, envID string) ([]int, error) {
			if envID != "env" {
				return nil, nil
			}
			return []int{port, 7879}, nil
		},
	}
	conn, err := g.guardedDialer("env", nil)(context.Background(), "tcp", net.JoinHostPort("Host.Studio.Internal", strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("dial an open port: %v", err)
	}
	banner, _ := io.ReadAll(conn)
	conn.Close()
	if string(banner) != "SSH-2.0-test\r\n" {
		t.Fatalf("read %q", banner)
	}
	for _, c := range []struct {
		env, addr, want string
	}{
		{"other", net.JoinHostPort("host.studio.internal", strconv.Itoa(port)), "not open to sandboxes"},
		{"env", "host.studio.internal:2222", "not open to sandboxes"},
		{"env", "host.studio.internal:7879", "Studio's own port"},
	} {
		conn, err := g.guardedDialer(c.env, nil)(context.Background(), "tcp", c.addr)
		if conn != nil {
			conn.Close()
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s dials %s: err = %v, want a refusal containing %q", c.env, c.addr, err, c.want)
		}
	}
}
