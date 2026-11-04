package dnsproxy

import (
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// answer is one record of a reply, as far as the tests look at it.
type answer struct {
	typ  dnsmessage.Type
	name string
	addr netip.Addr // A and AAAA records only
}

func parseReply(t *testing.T, msg []byte) (dnsmessage.Header, []answer) {
	t.Helper()
	var p dnsmessage.Parser
	h, err := p.Start(msg)
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	if err := p.SkipAllQuestions(); err != nil {
		t.Fatalf("reply questions: %v", err)
	}
	var out []answer
	for {
		rh, err := p.AnswerHeader()
		if err == dnsmessage.ErrSectionDone {
			return h, out
		}
		if err != nil {
			t.Fatalf("reply answers: %v", err)
		}
		a := answer{typ: rh.Type, name: rh.Name.String()}
		switch rh.Type {
		case dnsmessage.TypeA:
			r, _ := p.AResource()
			a.addr = netip.AddrFrom4(r.A)
		case dnsmessage.TypeAAAA:
			r, _ := p.AAAAResource()
			a.addr = netip.AddrFrom16(r.AAAA)
		default:
			if err := p.SkipAnswer(); err != nil {
				t.Fatalf("reply answer: %v", err)
			}
		}
		out = append(out, a)
	}
}

func TestDenyAnswersNXDOMAIN(t *testing.T) {
	up := startUpstream(t, fakeAnswer)
	srv := listen(t, up.addr)
	srv.SetHooks(Hooks{Deny: func(name string) bool { return name == "www.example.test" }})

	h, answers := parseReply(t, udpExchange(t, srv.Addr(), buildQuery(0x2345, wwwName, dnsmessage.TypeA)))
	if h.ID != 0x2345 || h.RCode != dnsmessage.RCodeNameError || len(answers) != 0 {
		t.Fatalf("denied name: %+v %v", h, answers)
	}
	if n := len(up.queries()); n != 0 {
		t.Fatalf("a denied name was forwarded %d times", n)
	}
	// Names that aren't denied are still forwarded.
	udpExchange(t, srv.Addr(), buildQuery(0x2346, "other.test.", dnsmessage.TypeA))
	if n := len(up.queries()); n != 1 {
		t.Fatalf("other names: %d forwarded queries, want 1", n)
	}
}

func TestVirtualizeAnswersWithVirtualAddresses(t *testing.T) {
	up := startUpstream(t, fakeAnswer)
	srv := listen(t, up.addr)
	srv.SetHooks(Hooks{Virtualize: func(name string) bool { return name == "www.example.test" }})

	h, answers := parseReply(t, udpExchange(t, srv.Addr(), buildQuery(0x3456, wwwName, dnsmessage.TypeA)))
	if h.RCode != dnsmessage.RCodeSuccess || len(answers) != 1 {
		t.Fatalf("A reply: %+v %+v", h, answers)
	}
	a := answers[0]
	if a.typ != dnsmessage.TypeA || a.name != wwwName || a.addr == netip.MustParseAddr("192.0.2.10") {
		t.Fatalf("A record: %+v", a)
	}
	if !netip.MustParsePrefix("198.18.0.0/15").Contains(a.addr) || a.addr == VirtualAddr {
		t.Fatalf("%s is not a fresh virtual address", a.addr)
	}
	if name, ok := srv.Names.Lookup(a.addr); !ok || name != "www.example.test" {
		t.Fatalf("virtual address names %q, %v", name, ok)
	}

	// The same name gets the same address again; an AAAA question gets no answers at all.
	_, again := parseReply(t, udpExchange(t, srv.Addr(), buildQuery(0x3457, wwwName, dnsmessage.TypeA)))
	if len(again) != 1 || again[0].addr != a.addr {
		t.Fatalf("second A reply: %+v, want %s", again, a.addr)
	}
	h, answers = parseReply(t, udpExchange(t, srv.Addr(), buildQuery(0x3458, wwwName, dnsmessage.TypeAAAA)))
	if h.RCode != dnsmessage.RCodeSuccess || len(answers) != 0 {
		t.Fatalf("AAAA reply: %+v %+v", h, answers)
	}
}

func TestVirtualizeLeavesOtherRepliesAlone(t *testing.T) {
	up := startUpstream(t, fakeAnswer)
	srv := listen(t, up.addr)
	srv.SetHooks(Hooks{Virtualize: func(string) bool { return true }})

	// A name without addresses keeps its reply as the upstream sent it.
	q := buildQuery(0x4567, "missing.test.", dnsmessage.TypeA)
	if got, want := udpExchange(t, srv.Addr(), q), fakeAnswer(q); string(got) != string(want) {
		t.Fatalf("NXDOMAIN reply changed")
	}
	// Questions for other records keep their replies, whatever they hold.
	q = buildQuery(0x4568, wwwName, dnsmessage.TypeCNAME)
	if got, want := udpExchange(t, srv.Addr(), q), fakeAnswer(q); string(got) != string(want) {
		t.Fatalf("CNAME reply changed")
	}
}

func TestVirtualAddressesArePerNameAndAddress(t *testing.T) {
	var v virtualAddrs
	first := v.addr("git.company.local", netip.MustParseAddr("10.0.0.5"))
	if again := v.addr("git.company.local", netip.MustParseAddr("10.0.0.5")); again != first {
		t.Fatalf("%s then %s for the same name and address", first, again)
	}
	seen := map[netip.Addr]bool{first: true}
	for _, c := range []struct{ name, ip string }{
		{"git.company.local", "10.0.0.6"},
		{"ssh.company.local", "10.0.0.5"},
		{"ssh.company.local", "10.0.0.6"},
	} {
		a := v.addr(c.name, netip.MustParseAddr(c.ip))
		if seen[a] || a == VirtualAddr || a == HostAddr || !netip.MustParsePrefix("198.18.0.0/15").Contains(a) || strings.HasSuffix(a.String(), ".0") {
			t.Fatalf("%s for %s %s: not a fresh virtual address", a, c.name, c.ip)
		}
		seen[a] = true
	}
}

func TestVirtualizedNamesSkipFallbacks(t *testing.T) {
	up := startUpstream(t, fakeAnswer)
	saved := fallbackUpstreams
	fallbackUpstreams = []string{up.addr}
	t.Cleanup(func() { fallbackUpstreams = saved })
	srv := listen(t, up.addr)
	srv.SetHooks(Hooks{Virtualize: func(name string) bool { return name == "www.example.test" }})

	h, _ := parseReply(t, udpExchange(t, srv.Addr(), buildQuery(0x4567, wwwName, dnsmessage.TypeA)))
	if h.RCode != dnsmessage.RCodeServerFailure {
		t.Fatalf("private host through a fallback: %+v", h)
	}
	if n := len(up.queries()); n != 0 {
		t.Fatalf("a private host was sent to a fallback resolver %d times", n)
	}
}

// A withheld name reaches no resolver: A gets a virtual address that names it, and nothing else
// gets an answer.
func TestWithheldNamesStayHere(t *testing.T) {
	const secret = "c2vjcmv0.attacker.com."
	up := startUpstream(t, fakeAnswer)
	srv := listen(t, up.addr)
	var told []string
	srv.SetHooks(Hooks{
		Withhold: func(name string) bool { return name != "allowed.com" },
		Withheld: func(name, kind string) { told = append(told, name+" "+kind) },
	})

	h, answers := parseReply(t, udpExchange(t, srv.Addr(), buildQuery(0x5001, secret, dnsmessage.TypeA)))
	if h.ID != 0x5001 || h.RCode != dnsmessage.RCodeSuccess || len(answers) != 1 || answers[0].typ != dnsmessage.TypeA || answers[0].name != secret {
		t.Fatalf("A reply: %+v %+v", h, answers)
	}
	addr := answers[0].addr
	if !netip.MustParsePrefix("198.18.0.0/15").Contains(addr) || addr == VirtualAddr || addr == HostAddr {
		t.Fatalf("%s is not a fresh virtual address", addr)
	}
	if name, ok := srv.Names.Lookup(addr); !ok || name != "c2vjcmv0.attacker.com" {
		t.Fatalf("virtual address names %q, %v", name, ok)
	}
	if _, again := parseReply(t, udpExchange(t, srv.Addr(), buildQuery(0x5002, secret, dnsmessage.TypeA))); len(again) != 1 || again[0].addr != addr {
		t.Fatalf("second A reply: %+v, want %s", again, addr)
	}

	for _, typ := range []dnsmessage.Type{dnsmessage.TypeAAAA, dnsmessage.TypeHTTPS, dnsmessage.TypeTXT, dnsmessage.TypeMX, dnsmessage.TypeNS, dnsmessage.TypeCNAME, dnsmessage.TypeSRV} {
		h, answers := parseReply(t, udpExchange(t, srv.Addr(), buildQuery(0x5003, secret, typ)))
		if h.RCode != dnsmessage.RCodeSuccess || len(answers) != 0 {
			t.Errorf("%s reply: %+v %+v", typ, h, answers)
		}
	}
	// Only the questions no connection follows from are worth telling about.
	if got, want := strings.Join(told, ","), "c2vjcmv0.attacker.com TXT,c2vjcmv0.attacker.com MX,c2vjcmv0.attacker.com NS,c2vjcmv0.attacker.com CNAME,c2vjcmv0.attacker.com SRV"; got != want {
		t.Errorf("told %q, want %q", got, want)
	}

	// Names that reach no public host fail at once.
	for _, name := range []string{"metadata.google.internal.", "nas.local.", "nas.", "foo.localhost.", "nothing.invalid.", "github.com.lan."} {
		h, answers := parseReply(t, udpExchange(t, srv.Addr(), buildQuery(0x5004, name, dnsmessage.TypeA)))
		if h.RCode != dnsmessage.RCodeNameError || len(answers) != 0 {
			t.Errorf("%s: %+v %+v", name, h, answers)
		}
	}
	if n := len(up.queries()); n != 0 {
		t.Fatalf("%d withheld queries were forwarded", n)
	}

	// A name that isn't withheld is forwarded, as is a private host's, whatever Withhold says.
	udpExchange(t, srv.Addr(), buildQuery(0x5005, "allowed.com.", dnsmessage.TypeTXT))
	if n := len(up.queries()); n != 1 {
		t.Fatalf("an allowed name: %d forwarded queries, want 1", n)
	}
	srv.SetHooks(Hooks{Withhold: func(string) bool { return true }, Virtualize: func(string) bool { return true }})
	udpExchange(t, srv.Addr(), buildQuery(0x5006, "git.company.local.", dnsmessage.TypeA))
	if n := len(up.queries()); n != 2 {
		t.Fatalf("a private host: %d forwarded queries, want 2", n)
	}
}

// A client may keep a withheld name's address long past its TTL, and it must still name the host.
func TestWithheldAddressOutlivesItsTTL(t *testing.T) {
	up := startUpstream(t, fakeAnswer)
	srv := listen(t, up.addr)
	srv.SetHooks(Hooks{Withhold: func(string) bool { return true }})
	_, answers := parseReply(t, udpExchange(t, srv.Addr(), buildQuery(0x5101, "app.attacker.com.", dnsmessage.TypeA)))
	if len(answers) != 1 {
		t.Fatalf("A reply: %+v", answers)
	}
	saved := now
	t.Cleanup(func() { now = saved })
	later := saved().Add(7 * 24 * time.Hour)
	now = func() time.Time { return later }
	if name, ok := srv.Names.Lookup(answers[0].addr); !ok || name != "app.attacker.com" {
		t.Fatalf("a week later the address names %q, %v", name, ok)
	}
}

// A sandbox picks the names it asks for, so what they cost is bounded.
func TestVirtualAddressesAreBounded(t *testing.T) {
	var v virtualAddrs
	for i := range maxNames + 10 {
		a := v.addr("n"+strconv.Itoa(i)+".attacker.com", netip.Addr{})
		if a == VirtualAddr || a == HostAddr {
			t.Fatalf("handed out %s", a)
		}
	}
	if n := len(v.by); n > maxNames {
		t.Fatalf("%d names remembered", n)
	}
}
