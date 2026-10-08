// Spike host daemon for S2 (DNS), S3 (vsock channel), S4 (CA/MITM) and S6 (hold-and-ask).
//
// It runs a SOCKS5 gateway, a DNS server and a vsock host socket, and logs every decision.
// Sandboxes are created separately with the msb CLI and pointed at these listeners.
package main

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
	"golang.org/x/net/dns/dnsmessage"
)

var (
	socksAddr = flag.String("socks", "127.0.0.1:1080", "SOCKS5 listen address")
	dnsAddr   = flag.String("dns", "127.0.0.1:5353", "DNS listen address (UDP+TCP)")
	upstream  = flag.String("upstream-dns", "1.1.1.1:53", "upstream resolver")
	vsockPath = flag.String("vsock", "", "unix socket path for the guest agent channel")
	caDir     = flag.String("ca", "", "directory holding ca.crt/ca.key (created if missing)")
	deny      = flag.String("deny", "", "comma-separated names to deny")
	hold      = flag.String("hold", "", "comma-separated names to hold (never answer)")
	mitm      = flag.String("mitm", "", "comma-separated names to terminate TLS for")
	rebind    = flag.String("rebind", "", "name=ip pairs answered locally, e.g. svc.studio.internal=198.18.0.1")
)

type gateway struct {
	deny, hold, mitm map[string]bool
	local            map[string]net.IP
	ca               *tls.Certificate
	caCert           *x509.Certificate
	names            sync.Map // ip string -> name, from DNS answers
	certs            sync.Map // name -> *tls.Certificate
}

func main() {
	flag.Parse()
	g := &gateway{deny: set(*deny), hold: set(*hold), mitm: set(*mitm), local: map[string]net.IP{}}
	for _, kv := range strings.Split(*rebind, ",") {
		if name, ip, ok := strings.Cut(kv, "="); ok {
			g.local[name] = net.ParseIP(ip)
		}
	}
	if *caDir != "" {
		if err := g.loadCA(*caDir); err != nil {
			log.Fatal(err)
		}
	}
	go g.serveDNS()
	if *vsockPath != "" {
		go serveVsock(*vsockPath)
	}
	ln, err := net.Listen("tcp", *socksAddr)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("socks5 on %s, dns on %s", ln.Addr(), *dnsAddr)
	for {
		c, err := ln.Accept()
		if err != nil {
			log.Fatal(err)
		}
		go g.handle(c)
	}
}

func set(csv string) map[string]bool {
	m := map[string]bool{}
	for _, s := range strings.Split(csv, ",") {
		if s = strings.TrimSpace(s); s != "" {
			m[s] = true
		}
	}
	return m
}

// --- SOCKS5 ---------------------------------------------------------------------------

func (g *gateway) handle(c net.Conn) {
	defer c.Close()
	start := time.Now()
	br := bufio.NewReader(c)
	user, host, port, err := socksHandshake(c, br)
	if err != nil {
		log.Printf("[socks] handshake: %v", err)
		return
	}
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	name, tlsHello := sniff(br)
	c.SetReadDeadline(time.Time{})
	via := "sni/host"
	if name == "" {
		if v, ok := g.names.Load(host); ok {
			name, via = v.(string), "dns-map"
		}
	}
	verdict := "allow"
	switch {
	case g.deny[name]:
		verdict = "deny"
	case g.hold[name]:
		verdict = "hold"
	case g.mitm[name] && tlsHello && g.ca != nil:
		verdict = "mitm"
	}
	log.Printf("[socks] sandbox=%s dst=%s:%d name=%q via=%s -> %s", user, host, port, name, via, verdict)
	switch verdict {
	case "deny":
		return
	case "hold":
		_, err := io.Copy(io.Discard, br) // returns when the client gives up and closes
		log.Printf("[hold] sandbox=%s name=%q client gave up after %s (%v)", user, name, time.Since(start).Round(time.Second), err)
		return
	case "mitm":
		g.serveMITM(&peekConn{Conn: c, r: br}, name, host, port)
		return
	}
	up, err := net.DialTimeout("tcp", net.JoinHostPort(host, fmt.Sprint(port)), 10*time.Second)
	if err != nil {
		return
	}
	defer up.Close()
	go io.Copy(up, br)
	io.Copy(c, up)
}

func socksHandshake(c net.Conn, br *bufio.Reader) (user, host string, port uint16, err error) {
	hdr := make([]byte, 2)
	if _, err = io.ReadFull(br, hdr); err != nil || hdr[0] != 5 {
		return "", "", 0, fmt.Errorf("not socks5: %v", err)
	}
	io.ReadFull(br, make([]byte, hdr[1]))
	c.Write([]byte{5, 2})
	v := make([]byte, 2)
	io.ReadFull(br, v)
	u := make([]byte, v[1])
	io.ReadFull(br, u)
	pl := make([]byte, 1)
	io.ReadFull(br, pl)
	io.ReadFull(br, make([]byte, pl[0]))
	c.Write([]byte{1, 0})
	req := make([]byte, 4)
	if _, err = io.ReadFull(br, req); err != nil {
		return
	}
	if req[1] != 1 {
		c.Write([]byte{5, 7, 0, 1, 0, 0, 0, 0, 0, 0})
		return "", "", 0, fmt.Errorf("unsupported command %d", req[1])
	}
	switch req[3] {
	case 1:
		ip := make([]byte, 4)
		io.ReadFull(br, ip)
		host = net.IP(ip).String()
	case 4:
		ip := make([]byte, 16)
		io.ReadFull(br, ip)
		host = net.IP(ip).String()
	case 3:
		l := make([]byte, 1)
		io.ReadFull(br, l)
		d := make([]byte, l[0])
		io.ReadFull(br, d)
		host = string(d)
	}
	pb := make([]byte, 2)
	io.ReadFull(br, pb)
	c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
	return string(u), host, binary.BigEndian.Uint16(pb), nil
}

func sniff(br *bufio.Reader) (name string, isTLS bool) {
	first, err := br.Peek(1)
	if err != nil {
		return "", false
	}
	if first[0] != 0x16 {
		b, _ := br.Peek(br.Buffered())
		for _, line := range strings.Split(string(b), "\r\n") {
			if strings.HasPrefix(strings.ToLower(line), "host:") {
				h := strings.TrimSpace(line[5:])
				if hh, _, err := net.SplitHostPort(h); err == nil {
					h = hh
				}
				return h, false
			}
		}
		return "", false
	}
	h, err := br.Peek(5)
	if err != nil {
		return "", true
	}
	rec, err := br.Peek(5 + int(binary.BigEndian.Uint16(h[3:5])))
	if err != nil {
		return "", true
	}
	return parseSNI(rec[5:]), true
}

func parseSNI(b []byte) string {
	if len(b) < 4 || b[0] != 1 {
		return ""
	}
	p := 4 + 2 + 32
	if p >= len(b) {
		return ""
	}
	p += 1 + int(b[p])
	if p+2 > len(b) {
		return ""
	}
	p += 2 + int(binary.BigEndian.Uint16(b[p:]))
	if p >= len(b) {
		return ""
	}
	p += 1 + int(b[p])
	if p+2 > len(b) {
		return ""
	}
	end := p + 2 + int(binary.BigEndian.Uint16(b[p:]))
	p += 2
	for p+4 <= end && end <= len(b) {
		typ := binary.BigEndian.Uint16(b[p:])
		l := int(binary.BigEndian.Uint16(b[p+2:]))
		p += 4
		if typ == 0 && p+5 <= len(b) {
			nl := int(binary.BigEndian.Uint16(b[p+3:]))
			if p+5+nl <= len(b) {
				return string(b[p+5 : p+5+nl])
			}
		}
		p += l
	}
	return ""
}

type peekConn struct {
	net.Conn
	r *bufio.Reader
}

func (p *peekConn) Read(b []byte) (int, error) { return p.r.Read(b) }

// --- MITM -----------------------------------------------------------------------------

func (g *gateway) serveMITM(c net.Conn, name, ip string, port uint16) {
	tc := tls.Server(c, &tls.Config{
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) { return g.leaf(hello.ServerName) },
		NextProtos:     []string{"http/1.1"},
	})
	if err := tc.Handshake(); err != nil {
		log.Printf("[mitm] %s: client handshake failed: %v", name, err)
		return
	}
	target := net.JoinHostPort(ip, fmt.Sprint(port))
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.Out.URL.Scheme, r.Out.URL.Host, r.Out.Host = "https", name, name
			r.Out.Header.Set("X-Studio-Spike", "mitm")
		},
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, target)
			},
			TLSClientConfig: &tls.Config{ServerName: name},
		},
		ModifyResponse: func(r *http.Response) error {
			log.Printf("[mitm] %s %s %s -> %d", name, r.Request.Method, r.Request.URL.Path, r.StatusCode)
			return nil
		},
	}
	srv := &http.Server{Handler: proxy, ReadHeaderTimeout: 10 * time.Second}
	srv.Serve(&oneConnListener{c: tc})
}

type oneConnListener struct {
	c    net.Conn
	once sync.Once
	done chan struct{}
}

func (l *oneConnListener) Accept() (net.Conn, error) {
	var c net.Conn
	l.once.Do(func() { c, l.done = l.c, make(chan struct{}) })
	if c != nil {
		return &closeNotify{Conn: c, done: l.done}, nil
	}
	<-l.done
	return nil, net.ErrClosed
}
func (l *oneConnListener) Close() error   { return nil }
func (l *oneConnListener) Addr() net.Addr { return l.c.LocalAddr() }

type closeNotify struct {
	net.Conn
	done chan struct{}
	once sync.Once
}

func (c *closeNotify) Close() error {
	c.once.Do(func() { close(c.done) })
	return c.Conn.Close()
}

func (g *gateway) loadCA(dir string) error {
	crt, key := filepath.Join(dir, "ca.crt"), filepath.Join(dir, "ca.key")
	if _, err := os.Stat(crt); errors.Is(err, os.ErrNotExist) {
		os.MkdirAll(dir, 0o700)
		k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		tmpl := &x509.Certificate{
			SerialNumber:          big.NewInt(time.Now().UnixNano()),
			Subject:               pkix.Name{CommonName: "Sandbox Studio spike CA"},
			NotBefore:             time.Now().Add(-time.Hour),
			NotAfter:              time.Now().AddDate(5, 0, 0),
			IsCA:                  true,
			BasicConstraintsValid: true,
			KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		}
		der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
		kb, _ := x509.MarshalECPrivateKey(k)
		os.WriteFile(crt, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
		os.WriteFile(key, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600)
	}
	pair, err := tls.LoadX509KeyPair(crt, key)
	if err != nil {
		return err
	}
	g.ca = &pair
	g.caCert, err = x509.ParseCertificate(pair.Certificate[0])
	return err
}

func (g *gateway) leaf(name string) (*tls.Certificate, error) {
	if c, ok := g.certs.Load(name); ok {
		return c.(*tls.Certificate), nil
	}
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: name},
		DNSNames:     []string{name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(0, 0, 30),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, g.caCert, &k.PublicKey, g.ca.PrivateKey)
	if err != nil {
		return nil, err
	}
	c := &tls.Certificate{Certificate: [][]byte{der, g.caCert.Raw}, PrivateKey: k}
	g.certs.Store(name, c)
	return c, nil
}

// --- DNS ------------------------------------------------------------------------------

func (g *gateway) serveDNS() {
	pc, err := net.ListenPacket("udp", *dnsAddr)
	if err != nil {
		log.Fatal(err)
	}
	buf := make([]byte, 4096)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		q := append([]byte(nil), buf[:n]...)
		go func() {
			if resp := g.answer(q); resp != nil {
				pc.WriteTo(resp, addr)
			}
		}()
	}
}

func (g *gateway) answer(q []byte) []byte {
	var p dnsmessage.Parser
	h, err := p.Start(q)
	if err != nil {
		return nil
	}
	question, err := p.Question()
	if err != nil {
		return nil
	}
	name := strings.TrimSuffix(question.Name.String(), ".")
	if ip, ok := g.local[name]; ok && question.Type == dnsmessage.TypeA {
		log.Printf("[dns] %s %s -> local %s", question.Type, name, ip)
		b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: h.ID, Response: true, Authoritative: true})
		b.StartQuestions()
		b.Question(question)
		b.StartAnswers()
		var a [4]byte
		copy(a[:], ip.To4())
		b.AResource(dnsmessage.ResourceHeader{Name: question.Name, Class: dnsmessage.ClassINET, TTL: 30}, dnsmessage.AResource{A: a})
		out, _ := b.Finish()
		return out
	}
	if g.deny[name] {
		log.Printf("[dns] %s %s -> NXDOMAIN (denied)", question.Type, name)
		b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: h.ID, Response: true, RCode: dnsmessage.RCodeNameError})
		b.StartQuestions()
		b.Question(question)
		out, _ := b.Finish()
		return out
	}
	conn, err := net.Dial("udp", *upstream)
	if err != nil {
		return nil
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	conn.Write(q)
	resp := make([]byte, 4096)
	n, err := conn.Read(resp)
	if err != nil {
		return nil
	}
	resp = resp[:n]
	var rp dnsmessage.Parser
	if _, err := rp.Start(resp); err == nil {
		rp.SkipAllQuestions()
		var ips []string
		for {
			rh, err := rp.AnswerHeader()
			if err != nil {
				break
			}
			switch rh.Type {
			case dnsmessage.TypeA:
				r, _ := rp.AResource()
				ip := net.IP(r.A[:]).String()
				ips = append(ips, ip)
				g.names.Store(ip, name)
			case dnsmessage.TypeAAAA:
				r, _ := rp.AAAAResource()
				ip := net.IP(r.AAAA[:]).String()
				ips = append(ips, ip)
				g.names.Store(ip, name)
			default:
				rp.SkipAnswer()
			}
		}
		log.Printf("[dns] %s %s -> %v", question.Type, name, ips)
	}
	return resp
}

// --- vsock channel --------------------------------------------------------------------

func serveVsock(path string) {
	os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("[vsock] listening on %s", path)
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			sess, err := yamux.Server(c, nil)
			if err != nil {
				log.Printf("[vsock] yamux: %v", err)
				return
			}
			log.Printf("[vsock] guest connected")
			// Host -> guest: open a stream and expect an echo.
			go func() {
				st, err := sess.Open()
				if err != nil {
					log.Printf("[vsock] host open: %v", err)
					return
				}
				defer st.Close()
				t := time.Now()
				fmt.Fprintln(st, "ping from host")
				line, _ := bufio.NewReader(st).ReadString('\n')
				log.Printf("[vsock] host->guest echo %q in %s", strings.TrimSpace(line), time.Since(t))
			}()
			// Guest -> host: accept streams and reply.
			for {
				st, err := sess.Accept()
				if err != nil {
					log.Printf("[vsock] session closed: %v", err)
					return
				}
				go func() {
					defer st.Close()
					line, _ := bufio.NewReader(st).ReadString('\n')
					log.Printf("[vsock] guest->host %q", strings.TrimSpace(line))
					fmt.Fprintln(st, "hello guest, from host")
				}()
			}
		}()
	}
}
