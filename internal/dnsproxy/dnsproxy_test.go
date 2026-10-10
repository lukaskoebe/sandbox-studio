package dnsproxy

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

const wwwName = "www.example.test."

// fakeAnswer is the upstream used by the tests: www.example.test is a CNAME to a CDN
// host with an A and AAAA record; every other name is NXDOMAIN.
func fakeAnswer(q []byte) []byte {
	var p dnsmessage.Parser
	h, err := p.Start(q)
	if err != nil {
		return nil
	}
	question, err := p.Question()
	if err != nil {
		return nil
	}
	hdr := dnsmessage.Header{ID: h.ID, Response: true, Authoritative: true, RecursionAvailable: true}
	if question.Name.String() != wwwName {
		hdr.RCode = dnsmessage.RCodeNameError
	}
	b := dnsmessage.NewBuilder(nil, hdr)
	b.StartQuestions()
	b.Question(question)
	if question.Name.String() == wwwName {
		edge := dnsmessage.MustNewName("edge.cdn.test.")
		b.StartAnswers()
		b.CNAMEResource(dnsmessage.ResourceHeader{Name: question.Name, Class: dnsmessage.ClassINET, TTL: 30},
			dnsmessage.CNAMEResource{CNAME: edge})
		b.AResource(dnsmessage.ResourceHeader{Name: edge, Class: dnsmessage.ClassINET, TTL: 30},
			dnsmessage.AResource{A: netip.MustParseAddr("192.0.2.10").As4()})
		b.AAAAResource(dnsmessage.ResourceHeader{Name: edge, Class: dnsmessage.ClassINET, TTL: 30},
			dnsmessage.AAAAResource{AAAA: netip.MustParseAddr("2001:db8::10").As16()})
	}
	msg, err := b.Finish()
	if err != nil {
		panic(err)
	}
	return msg
}

// fakeUpstream answers queries with its answer function and remembers every query it got.
type fakeUpstream struct {
	addr string

	mu   sync.Mutex
	seen [][]byte
}

func (u *fakeUpstream) queries() [][]byte {
	u.mu.Lock()
	defer u.mu.Unlock()
	return slices.Clone(u.seen)
}

// startUpstream serves answer on UDP and TCP sharing one port.
func startUpstream(t *testing.T, answer func(q []byte) []byte) *fakeUpstream {
	t.Helper()
	pc, ln, err := listenPair("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pc.Close()
		ln.Close()
	})
	u := &fakeUpstream{addr: pc.LocalAddr().String()}
	seen := func(q []byte) {
		u.mu.Lock()
		defer u.mu.Unlock()
		u.seen = append(u.seen, bytes.Clone(q))
	}
	go func() {
		buf := make([]byte, maxMessage)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			seen(buf[:n])
			pc.WriteTo(answer(buf[:n]), addr)
		}
	}()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				for {
					q, err := readFrame(c)
					if err != nil {
						return
					}
					seen(q)
					if err := writeFrame(c, answer(q)); err != nil {
						return
					}
				}
			}()
		}
	}()
	return u
}

// deadUpstream returns an address with nothing listening on it.
func deadUpstream(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

// listen starts a server on a random loopback port and closes it when the test ends.
func listen(t *testing.T, upstreams ...string) *Server {
	t.Helper()
	srv, err := Listen("127.0.0.1:0", upstreams, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	return srv
}

func (s *Server) tcpConns() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}

func buildQuery(id uint16, name string, typ dnsmessage.Type) []byte {
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: id, RecursionDesired: true})
	b.StartQuestions()
	b.Question(dnsmessage.Question{Name: dnsmessage.MustNewName(name), Type: typ, Class: dnsmessage.ClassINET})
	msg, err := b.Finish()
	if err != nil {
		panic(err)
	}
	return msg
}

func udpExchange(t *testing.T, addr string, q []byte) []byte {
	t.Helper()
	c, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Write(q); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, maxMessage)
	n, err := c.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	return buf[:n]
}

// expectNoUDPReply sends payload and checks the resolver stays silent.
func expectNoUDPReply(t *testing.T, addr string, payload []byte) {
	t.Helper()
	c, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Write(payload); err != nil {
		t.Fatal(err)
	}
	c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if n, err := c.Read(make([]byte, maxMessage)); err == nil {
		t.Fatalf("got a %d-byte reply; want none", n)
	}
}

// expectTCPClose sends payload as one TCP frame and checks the resolver hangs up without replying.
func expectTCPClose(t *testing.T, addr string, payload []byte) {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	if err := writeFrame(c, payload); err != nil {
		t.Fatal(err)
	}
	reply, err := readFrame(c)
	switch {
	case err == nil:
		t.Fatalf("got a %d-byte reply; want the connection closed", len(reply))
	case errors.Is(err, os.ErrDeadlineExceeded):
		t.Fatal("connection left open")
	}
}

func checkServfail(t *testing.T, resp []byte, id uint16) {
	t.Helper()
	var p dnsmessage.Parser
	h, err := p.Start(resp)
	if err != nil {
		t.Fatal(err)
	}
	if h.ID != id || !h.Response || h.RCode != dnsmessage.RCodeServerFailure {
		t.Fatalf("header = %+v; want ID %#x, response, SERVFAIL", h, id)
	}
	qs, err := p.AllQuestions()
	if err != nil || len(qs) != 1 || qs[0].Name.String() != wwwName {
		t.Fatalf("question not echoed: %v, %v", qs, err)
	}
}

func assertNames(t *testing.T, n *Names) {
	t.Helper()
	for _, s := range []string{"192.0.2.10", "2001:db8::10"} {
		got, ok := n.Lookup(netip.MustParseAddr(s))
		if !ok || got != "www.example.test" {
			t.Errorf("Lookup(%s) = %q, %v; want www.example.test", s, got, ok)
		}
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for condition")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestUDPForwardsAndRecordsNames(t *testing.T) {
	up := startUpstream(t, fakeAnswer)
	srv := listen(t, up.addr)
	// Listen sets Names; nothing in this test does.
	if srv.Names == nil {
		t.Fatal("Listen did not set Names")
	}
	q := buildQuery(0x1234, wwwName, dnsmessage.TypeA)

	if got := udpExchange(t, srv.Addr(), q); !bytes.Equal(got, fakeAnswer(q)) {
		t.Fatalf("response differs from upstream answer")
	}
	assertNames(t, srv.Names)
}

func TestTCPTwoQueriesOnOneConnection(t *testing.T) {
	srv := listen(t, startUpstream(t, fakeAnswer).addr)
	c, err := net.Dial("tcp", srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))

	for _, typ := range []dnsmessage.Type{dnsmessage.TypeA, dnsmessage.TypeAAAA} {
		q := buildQuery(0x4321, wwwName, typ)
		if err := writeFrame(c, q); err != nil {
			t.Fatal(err)
		}
		got, err := readFrame(c)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, fakeAnswer(q)) {
			t.Fatalf("type %v: response differs from upstream answer", typ)
		}
	}
	assertNames(t, srv.Names)
}

func TestFallsBackToNextUpstream(t *testing.T) {
	saved := upstreamTimeout
	upstreamTimeout = 500 * time.Millisecond
	t.Cleanup(func() { upstreamTimeout = saved })

	srv := listen(t, deadUpstream(t), startUpstream(t, fakeAnswer).addr)
	q := buildQuery(7, wwwName, dnsmessage.TypeA)

	if got := udpExchange(t, srv.Addr(), q); !bytes.Equal(got, fakeAnswer(q)) {
		t.Fatalf("UDP: response differs from upstream answer")
	}
	assertNames(t, srv.Names)

	c, err := net.Dial("tcp", srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	if err := writeFrame(c, q); err != nil {
		t.Fatal(err)
	}
	got, err := readFrame(c)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, fakeAnswer(q)) {
		t.Fatalf("TCP: response differs from upstream answer")
	}
}

func TestMismatchedReplyFallsBackToNextUpstream(t *testing.T) {
	cases := map[string]func(q []byte) []byte{
		"wrong ID": func(q []byte) []byte {
			r := bytes.Clone(fakeAnswer(q))
			r[0] ^= 0xff
			return r
		},
		"query echoed back": func(q []byte) []byte { return q },
		"other question": func(q []byte) []byte {
			return fakeAnswer(buildQuery(binary.BigEndian.Uint16(q), "other.test.", dnsmessage.TypeA))
		},
	}
	for name, answer := range cases {
		t.Run(name, func(t *testing.T) {
			bad := startUpstream(t, answer)
			good := startUpstream(t, fakeAnswer)
			srv := listen(t, bad.addr, good.addr)
			q := buildQuery(0x2468, wwwName, dnsmessage.TypeA)

			if got := udpExchange(t, srv.Addr(), q); !bytes.Equal(got, fakeAnswer(q)) {
				t.Fatal("mismatched reply was relayed instead of falling back")
			}
			if n := len(bad.queries()); n != 1 {
				t.Fatalf("bad upstream got %d queries; want 1", n)
			}
			if n := len(good.queries()); n != 1 {
				t.Fatalf("good upstream got %d queries; want 1", n)
			}
		})
	}
}

func TestMismatchedReplyIsNotRecorded(t *testing.T) {
	wrongID := func(q []byte) []byte {
		r := bytes.Clone(fakeAnswer(q))
		r[0] ^= 0xff
		return r
	}
	srv := listen(t, startUpstream(t, wrongID).addr)
	q := buildQuery(0x2468, wwwName, dnsmessage.TypeA)

	checkServfail(t, udpExchange(t, srv.Addr(), q), 0x2468)
	if _, ok := srv.Names.Lookup(netip.MustParseAddr("192.0.2.10")); ok {
		t.Error("names from a mismatched reply were recorded")
	}
}

func TestAllUpstreamsDownReturnsServfail(t *testing.T) {
	saved := upstreamTimeout
	upstreamTimeout = 500 * time.Millisecond
	t.Cleanup(func() { upstreamTimeout = saved })

	srv := listen(t, deadUpstream(t), deadUpstream(t))
	q := buildQuery(0xBEEF, wwwName, dnsmessage.TypeA)
	checkServfail(t, udpExchange(t, srv.Addr(), q), 0xBEEF)
}

func TestMalformedQueriesAreNeverForwarded(t *testing.T) {
	up := startUpstream(t, fakeAnswer)
	srv := listen(t, up.addr)
	good := buildQuery(0x0101, wwwName, dnsmessage.TypeA)

	responseBit := bytes.Clone(good)
	responseBit[2] |= 0x80 // QR
	notQuery := bytes.Clone(good)
	notQuery[2] |= 1 << 3 // OpCode 1 (IQUERY)
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: 0x0102, RecursionDesired: true})
	b.StartQuestions()
	b.Question(dnsmessage.Question{Name: dnsmessage.MustNewName(wwwName), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET})
	b.Question(dnsmessage.Question{Name: dnsmessage.MustNewName("other.test."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET})
	twoQuestions, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	noQuestions := []byte{0x01, 0x03, 0x01, 0x00, 0, 0, 0, 0, 0, 0, 0, 0}

	cases := map[string][]byte{
		"garbage":       {0xde, 0xad, 0xbe, 0xef, 0x01},
		"response bit":  responseBit,
		"opcode 1":      notQuery,
		"two questions": twoQuestions,
		"no questions":  noQuestions,
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			expectNoUDPReply(t, srv.Addr(), payload)
			expectTCPClose(t, srv.Addr(), payload)
		})
	}
	if got := up.queries(); len(got) != 0 {
		t.Fatalf("upstream received %d malformed queries", len(got))
	}

	// The resolver still serves, and the upstream saw only the well-formed query.
	if got := udpExchange(t, srv.Addr(), good); !bytes.Equal(got, fakeAnswer(good)) {
		t.Fatal("well-formed query not answered after malformed ones")
	}
	if got := up.queries(); len(got) != 1 || !bytes.Equal(got[0], good) {
		t.Fatalf("upstream queries = %d; want only the well-formed one", len(got))
	}
}

func TestUDPInFlightCapDropsQueries(t *testing.T) {
	up := startUpstream(t, fakeAnswer)
	srv := listen(t, up.addr)
	// Fill every slot, as if maxUDPInFlight queries were already being resolved.
	for range maxUDPInFlight {
		srv.udpSem <- struct{}{}
	}
	q := buildQuery(0x5511, wwwName, dnsmessage.TypeA)
	expectNoUDPReply(t, srv.Addr(), q)
	if n := len(up.queries()); n != 0 {
		t.Fatalf("query was forwarded while saturated (%d upstream queries)", n)
	}

	for range maxUDPInFlight {
		<-srv.udpSem
	}
	if got := udpExchange(t, srv.Addr(), q); !bytes.Equal(got, fakeAnswer(q)) {
		t.Fatal("query not answered after slots were freed")
	}
}

func TestTCPConnectionCap(t *testing.T) {
	srv := listen(t, startUpstream(t, fakeAnswer).addr)
	var held []net.Conn
	t.Cleanup(func() {
		for _, c := range held {
			c.Close()
		}
	})
	for range maxTCPConns {
		c, err := net.Dial("tcp", srv.Addr())
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, c)
	}
	waitFor(t, func() bool { return srv.tcpConns() == maxTCPConns })

	extra, err := net.Dial("tcp", srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer extra.Close()
	extra.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err = extra.Read(make([]byte, 1))
	switch {
	case err == nil:
		t.Fatal("connection beyond the cap stayed open")
	case errors.Is(err, os.ErrDeadlineExceeded):
		t.Fatal("connection beyond the cap was not closed")
	}
}

func TestNamesExpiry(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cur := base
	saved := now
	now = func() time.Time { return cur }
	t.Cleanup(func() { now = saved })

	var n Names
	n.put(netip.MustParseAddr("192.0.2.1"), "short.test", 30)
	n.put(netip.MustParseAddr("192.0.2.2"), "long.test", 7200)

	cur = base.Add(59 * time.Minute)
	if got, ok := n.Lookup(netip.MustParseAddr("192.0.2.1")); !ok || got != "short.test" {
		t.Errorf("TTL below the floor: Lookup = %q, %v; want short.test (floor is one hour)", got, ok)
	}
	cur = base.Add(61 * time.Minute)
	if _, ok := n.Lookup(netip.MustParseAddr("192.0.2.1")); ok {
		t.Errorf("entry still returned after its expiry")
	}
	cur = base.Add(90 * time.Minute)
	if _, ok := n.Lookup(netip.MustParseAddr("192.0.2.2")); !ok {
		t.Errorf("TTL above the floor expired early")
	}
}

func TestNamesCapEvictsEarliestExpiry(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cur := base
	saved := now
	now = func() time.Time { return cur }
	t.Cleanup(func() { now = saved })

	ip := func(i int) netip.Addr {
		return netip.AddrFrom4([4]byte{10, byte(i >> 16), byte(i >> 8), byte(i)})
	}
	var n Names
	// A one-day TTL keeps every entry live, and staggered inserts stagger expiries.
	for i := range maxNames {
		cur = base.Add(time.Duration(i) * time.Second)
		n.put(ip(i), fmt.Sprintf("h%d.test", i), 86400)
	}
	cur = base.Add(maxNames * time.Second)
	n.put(ip(maxNames), "new.test", 86400)

	if len(n.m) != maxNames {
		t.Fatalf("len = %d; want %d", len(n.m), maxNames)
	}
	if _, ok := n.Lookup(ip(0)); ok {
		t.Errorf("earliest-expiring entry survived eviction")
	}
	if _, ok := n.Lookup(ip(1)); !ok {
		t.Errorf("second entry was evicted instead of the earliest")
	}
	if got, ok := n.Lookup(ip(maxNames)); !ok || got != "new.test" {
		t.Errorf("new entry missing: %q, %v", got, ok)
	}
}

func TestNamesCapPrefersExpiredEntries(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cur := base
	saved := now
	now = func() time.Time { return cur }
	t.Cleanup(func() { now = saved })

	var n Names
	for i := range maxNames {
		n.put(netip.AddrFrom4([4]byte{10, byte(i >> 16), byte(i >> 8), byte(i)}), "old.test", 0)
	}
	cur = base.Add(2 * time.Hour)
	n.put(netip.MustParseAddr("192.0.2.1"), "new.test", 0)
	if len(n.m) != 1 {
		t.Fatalf("len = %d after inserting into a full map of expired entries; want 1", len(n.m))
	}
}

func TestSystemUpstreams(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resolv.conf")
	conf := `# managed by the network manager
; legacy comment
search lan
nameserver 192.168.1.1
nameserver fe80::1%eth0
nameserver 1.1.1.1 # same as a fallback
nameserver 2001:db8::53
`
	if err := os.WriteFile(path, []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	saved := resolvConfPath
	resolvConfPath = path
	t.Cleanup(func() { resolvConfPath = saved })

	got := SystemUpstreams()
	want := []string{"192.168.1.1:53", "[fe80::1%eth0]:53", "1.1.1.1:53", "[2001:db8::53]:53", "9.9.9.9:53"}
	if !slices.Equal(got, want) {
		t.Errorf("SystemUpstreams() = %q; want %q", got, want)
	}

	resolvConfPath = filepath.Join(t.TempDir(), "missing")
	got = SystemUpstreams()
	want = []string{"1.1.1.1:53", "9.9.9.9:53"}
	if !slices.Equal(got, want) {
		t.Errorf("without resolv.conf: SystemUpstreams() = %q; want %q", got, want)
	}
}

func TestCloseReleasesPortAndOpenConnections(t *testing.T) {
	srv, err := Listen("127.0.0.1:0", []string{startUpstream(t, fakeAnswer).addr}, nil)
	if err != nil {
		t.Fatal(err)
	}
	addr := srv.Addr()

	// An idle client connection must not keep Close waiting.
	idle, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer idle.Close()

	done := make(chan error, 1)
	go func() { done <- srv.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return")
	}
	if err := srv.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		t.Fatalf("UDP port not released: %v", err)
	}
	pc.Close()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("TCP port not released: %v", err)
	}
	ln.Close()
}

func TestVirtualNamesAreAnsweredLocally(t *testing.T) {
	up := startUpstream(t, fakeAnswer)
	srv := listen(t, up.addr)
	for _, typ := range []dnsmessage.Type{dnsmessage.TypeA, dnsmessage.TypeAAAA} {
		resp := udpExchange(t, srv.Addr(), buildQuery(0x77, "Git.Studio.Internal.", typ))
		var p dnsmessage.Parser
		h, err := p.Start(resp)
		if err != nil || h.ID != 0x77 || !h.Response || h.RCode != dnsmessage.RCodeSuccess {
			t.Fatalf("type %v: header %+v, %v", typ, h, err)
		}
		p.SkipAllQuestions()
		answers, err := p.AllAnswers()
		if err != nil {
			t.Fatal(err)
		}
		want := 0
		if typ == dnsmessage.TypeA {
			want = 1
		}
		if len(answers) != want {
			t.Fatalf("type %v: %d answers", typ, len(answers))
		}
		if want == 1 {
			if a := answers[0].Body.(*dnsmessage.AResource).A; netip.AddrFrom4(a) != VirtualAddr {
				t.Fatalf("A = %v", a)
			}
		}
	}
	if name, ok := srv.Names.Lookup(VirtualAddr); !ok || name != "git.studio.internal" {
		t.Fatalf("Lookup = %q, %v", name, ok)
	}
	if n := len(up.queries()); n != 0 {
		t.Fatalf("%d queries went upstream", n)
	}
}
