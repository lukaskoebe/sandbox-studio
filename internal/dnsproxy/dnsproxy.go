// Package dnsproxy is the per-sandbox DNS resolver. Each sandbox's DNS goes to its own
// loopback port, so Studio knows which sandbox asked and can map addresses back to names.
package dnsproxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

const (
	maxNames       = 4096
	maxMessage     = 65535
	maxUDPInFlight = 256
	maxTCPConns    = 64
	tcpIdleTimeout = 10 * time.Second
	// Clients keep connections and caches well beyond a record's TTL, so answers are
	// remembered for at least this long.
	minNameTTL  = time.Hour
	retryDelay  = 10 * time.Millisecond
	opCodeQuery = dnsmessage.OpCode(0)
)

var (
	now               = time.Now
	upstreamTimeout   = 2 * time.Second
	resolvConfPath    = "/etc/resolv.conf"
	fallbackUpstreams = []string{"1.1.1.1:53", "9.9.9.9:53"}

	errMismatch = errors.New("upstream reply does not match the query")
)

// Names remembers which DNS name each answered address belongs to, for one sandbox.
// It is safe for concurrent use. The zero value is ready to use.
type Names struct {
	mu sync.Mutex
	m  map[netip.Addr]entry
}

type entry struct {
	name    string
	expires time.Time
}

// Lookup returns the name the sandbox looked up that resolved to ip.
func (n *Names) Lookup(ip netip.Addr) (string, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	e, ok := n.m[ip.Unmap()]
	if !ok || !now().Before(e.expires) {
		return "", false
	}
	return e.name, true
}

// put records that name resolved to ip, refreshing any earlier entry for ip.
func (n *Names) put(ip netip.Addr, name string, ttl uint32) {
	t := now()
	// Dual-stack sockets can hand back ::ffff:a.b.c.d, which must match the plain IPv4 key.
	ip = ip.Unmap()
	exp := t.Add(max(time.Duration(ttl)*time.Second, minNameTTL))

	n.mu.Lock()
	defer n.mu.Unlock()
	if n.m == nil {
		n.m = make(map[netip.Addr]entry)
	}
	if _, ok := n.m[ip]; !ok && len(n.m) >= maxNames {
		n.evict(t)
	}
	n.m[ip] = entry{name: name, expires: exp}
}

// evict makes room for one entry: expired entries go first, then the one expiring soonest.
func (n *Names) evict(t time.Time) {
	for k, e := range n.m {
		if !t.Before(e.expires) {
			delete(n.m, k)
		}
	}
	if len(n.m) < maxNames {
		return
	}
	var victim netip.Addr
	var victimExp time.Time
	for k, e := range n.m {
		if !victim.IsValid() || e.expires.Before(victimExp) {
			victim, victimExp = k, e.expires
		}
	}
	delete(n.m, victim)
}

// Server answers DNS queries on one loopback address over UDP and TCP by forwarding them.
type Server struct {
	// Names is set by Listen; read-only.
	Names *Names

	log       *slog.Logger
	upstreams []string
	udp       net.PacketConn
	tcp       net.Listener
	udpSem    chan struct{} // one slot per UDP query being resolved
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup

	mu     sync.Mutex
	closed bool
	conns  map[net.Conn]struct{}
}

// Listen starts a resolver on addr (e.g. "127.0.0.1:17100"), UDP and TCP on the same port,
// forwarding to upstreams ("ip:port") in order. log may be nil (use slog.New(slog.DiscardHandler)).
func Listen(addr string, upstreams []string, log *slog.Logger) (*Server, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	pc, ln, err := listenPair(addr)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		Names:     &Names{},
		log:       log,
		upstreams: slices.Clone(upstreams),
		udp:       pc,
		tcp:       ln,
		udpSem:    make(chan struct{}, maxUDPInFlight),
		ctx:       ctx,
		cancel:    cancel,
		conns:     make(map[net.Conn]struct{}),
	}
	s.wg.Add(2)
	go s.serveUDP()
	go s.serveTCP()
	return s, nil
}

// listenPair binds UDP and TCP on one port, since clients reach the resolver at one ip:port
// over both transports. With port 0 the kernel picks the UDP port, which can already be busy
// for TCP, so another port is tried.
func listenPair(addr string) (net.PacketConn, net.Listener, error) {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, nil, err
	}
	for attempt := 1; ; attempt++ {
		pc, err := net.ListenPacket("udp", addr)
		if err != nil {
			return nil, nil, err
		}
		ln, err := net.Listen("tcp", pc.LocalAddr().String())
		if err == nil {
			return pc, ln, nil
		}
		pc.Close()
		if port != "0" || attempt == 10 {
			return nil, nil, err
		}
	}
}

// Addr returns the listening address.
func (s *Server) Addr() string {
	return s.tcp.Addr().String()
}

// Close stops both listeners and waits for in-flight queries to finish.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	for c := range s.conns {
		c.Close()
	}
	s.mu.Unlock()

	// Cancelling aborts upstream exchanges in flight instead of waiting out their timeouts.
	s.cancel()
	err := errors.Join(s.udp.Close(), s.tcp.Close())
	s.wg.Wait()
	return err
}

func (s *Server) serveUDP() {
	defer s.wg.Done()
	buf := make([]byte, maxMessage)
	for {
		n, addr, err := s.udp.ReadFrom(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			s.log.Debug("udp read", "err", err)
			time.Sleep(retryDelay)
			continue
		}
		q, ok := parseQuery(bytes.Clone(buf[:n]))
		if !ok {
			continue
		}
		select {
		case s.udpSem <- struct{}{}:
		default:
			// Saturated: dropping keeps the read loop moving, and DNS clients retry.
			continue
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer func() { <-s.udpSem }()
			if resp := s.resolve("udp", q); resp != nil {
				// The client may already be gone; there is nothing to do about that.
				s.udp.WriteTo(resp, addr)
			}
		}()
	}
}

func (s *Server) serveTCP() {
	defer s.wg.Done()
	for {
		c, err := s.tcp.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			s.log.Debug("tcp accept", "err", err)
			time.Sleep(retryDelay)
			continue
		}
		s.mu.Lock()
		switch {
		case s.closed:
			s.mu.Unlock()
			c.Close()
			return
		case len(s.conns) >= maxTCPConns:
			s.mu.Unlock()
			c.Close()
			continue
		}
		s.conns[c] = struct{}{}
		s.wg.Add(1)
		s.mu.Unlock()
		go s.serveConn(c)
	}
}

// serveConn answers queries from one TCP client until it goes quiet, disconnects or sends
// something that isn't a query.
func (s *Server) serveConn(c net.Conn) {
	defer func() {
		s.mu.Lock()
		delete(s.conns, c)
		s.mu.Unlock()
		c.Close()
		s.wg.Done()
	}()
	for {
		c.SetReadDeadline(time.Now().Add(tcpIdleTimeout))
		raw, err := readFrame(c)
		if err != nil {
			return
		}
		q, ok := parseQuery(raw)
		if !ok {
			return
		}
		resp := s.resolve("tcp", q)
		if resp == nil {
			return
		}
		c.SetWriteDeadline(time.Now().Add(tcpIdleTimeout))
		if err := writeFrame(c, resp); err != nil {
			return
		}
	}
}

// resolve forwards q to the upstreams in order and returns the first reply that answers it,
// after recording the reply's names so the sandbox can't connect before the mapping exists.
// If no upstream answers, it returns a SERVFAIL.
func (s *Server) resolve(network string, q query) []byte {
	for _, up := range s.upstreams {
		resp, err := s.exchange(network, q.raw, up)
		if err == nil && !answers(resp, q) {
			err = errMismatch
		}
		if err != nil {
			s.log.Debug("upstream failed", "upstream", up, "network", network, "err", err)
			continue
		}
		s.record(resp)
		return resp
	}
	return servfail(q)
}

// exchange sends q to one upstream and returns its reply. It gives up after upstreamTimeout,
// or at once when the server is closing.
func (s *Server) exchange(network string, q []byte, upstream string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(s.ctx, upstreamTimeout)
	defer cancel()
	var d net.Dialer
	c, err := d.DialContext(ctx, network, upstream)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	if dl, ok := ctx.Deadline(); ok {
		c.SetDeadline(dl)
	}
	// Reads don't observe ctx, so cancellation is delivered as an already-passed deadline.
	stop := context.AfterFunc(ctx, func() { c.SetDeadline(time.Now()) })
	defer stop()

	if network == "udp" {
		if _, err := c.Write(q); err != nil {
			return nil, err
		}
		buf := make([]byte, maxMessage)
		n, err := c.Read(buf)
		if err != nil {
			return nil, err
		}
		return buf[:n], nil
	}
	if err := writeFrame(c, q); err != nil {
		return nil, err
	}
	return readFrame(c)
}

// record stores every A and AAAA answer in resp against the name the sandbox asked for.
// Owner names are ignored: behind a CNAME the address records sit under the target.
func (s *Server) record(resp []byte) {
	var p dnsmessage.Parser
	if _, err := p.Start(resp); err != nil {
		return
	}
	question, err := p.Question()
	if err != nil {
		return
	}
	name := strings.TrimSuffix(strings.ToLower(question.Name.String()), ".")
	if err := p.SkipAllQuestions(); err != nil {
		return
	}
	for {
		h, err := p.AnswerHeader()
		if err != nil {
			// ErrSectionDone at the end of the answers, or malformed input; keep what we have.
			return
		}
		switch h.Type {
		case dnsmessage.TypeA:
			r, err := p.AResource()
			if err != nil {
				return
			}
			s.Names.put(netip.AddrFrom4(r.A), name, h.TTL)
		case dnsmessage.TypeAAAA:
			r, err := p.AAAAResource()
			if err != nil {
				return
			}
			s.Names.put(netip.AddrFrom16(r.AAAA), name, h.TTL)
		default:
			if err := p.SkipAnswer(); err != nil {
				return
			}
		}
	}
}

// query is a standard query we are willing to forward: QR clear, OpCode 0, one question.
type query struct {
	raw      []byte
	header   dnsmessage.Header
	question dnsmessage.Question
}

// parseQuery checks raw is a query we forward. Anything else never leaves the resolver.
func parseQuery(raw []byte) (query, bool) {
	var p dnsmessage.Parser
	h, err := p.Start(raw)
	if err != nil || h.Response || h.OpCode != opCodeQuery {
		return query{}, false
	}
	qs, err := p.AllQuestions()
	if err != nil || len(qs) != 1 {
		return query{}, false
	}
	return query{raw: raw, header: h, question: qs[0]}, true
}

// answers reports whether resp is a reply to q: same ID, QR set and the same single question.
// The name comparison is case-insensitive, as DNS names are.
func answers(resp []byte, q query) bool {
	var p dnsmessage.Parser
	h, err := p.Start(resp)
	if err != nil || !h.Response || h.ID != q.header.ID {
		return false
	}
	qs, err := p.AllQuestions()
	if err != nil || len(qs) != 1 {
		return false
	}
	got, want := qs[0], q.question
	return got.Type == want.Type && got.Class == want.Class &&
		strings.EqualFold(got.Name.String(), want.Name.String())
}

// servfail builds a SERVFAIL reply to q, or returns nil if the reply can't be built.
func servfail(q query) []byte {
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{
		ID:                 q.header.ID,
		Response:           true,
		RecursionDesired:   q.header.RecursionDesired,
		RecursionAvailable: true,
		RCode:              dnsmessage.RCodeServerFailure,
	})
	if err := b.StartQuestions(); err != nil {
		return nil
	}
	if err := b.Question(q.question); err != nil {
		return nil
	}
	msg, err := b.Finish()
	if err != nil {
		return nil
	}
	return msg
}

// readFrame reads one DNS message in TCP framing (2-byte big-endian length prefix).
func readFrame(r io.Reader) ([]byte, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	msg := make([]byte, binary.BigEndian.Uint16(hdr[:]))
	if _, err := io.ReadFull(r, msg); err != nil {
		return nil, err
	}
	return msg, nil
}

// writeFrame writes one DNS message in TCP framing. msg must fit in 65535 bytes.
func writeFrame(w io.Writer, msg []byte) error {
	buf := make([]byte, 2+len(msg))
	binary.BigEndian.PutUint16(buf, uint16(len(msg)))
	copy(buf[2:], msg)
	_, err := w.Write(buf)
	return err
}

// SystemUpstreams returns the host's resolvers from /etc/resolv.conf (ignored where it
// doesn't exist, e.g. Windows) followed by the fallbacks 1.1.1.1:53 and 9.9.9.9:53.
func SystemUpstreams() []string {
	var out []string
	add := func(up string) {
		if !slices.Contains(out, up) {
			out = append(out, up)
		}
	}
	if f, err := os.Open(resolvConfPath); err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			if len(fields) < 2 || fields[0] != "nameserver" {
				continue
			}
			// The zone in "fe80::1%eth0" is kept: link-local nameservers need it to be dialled.
			ip, err := netip.ParseAddr(fields[1])
			if err != nil {
				continue
			}
			add(net.JoinHostPort(ip.String(), "53"))
		}
	}
	for _, up := range fallbackUpstreams {
		add(up)
	}
	return out
}
