package dnsproxy

import (
	"net/netip"
	"strings"
	"testing"

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
			p.SkipAnswer()
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
		if seen[a] || a == VirtualAddr || !netip.MustParsePrefix("198.18.0.0/15").Contains(a) || strings.HasSuffix(a.String(), ".0") {
			t.Fatalf("%s for %s %s: not a fresh virtual address", a, c.name, c.ip)
		}
		seen[a] = true
	}
}
