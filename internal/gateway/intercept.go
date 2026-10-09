package gateway

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/policy"
	"github.com/lukaskoebe/sandbox-studio/internal/secrets"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// An intercepted connection is terminated by the gateway: TLS with a certificate from the
// environment's CA, then HTTP. Each request is checked and rewritten, and sent on over a
// new connection to the same host. Proxy rules intercept, and so does any connection to a
// host a secret is bound to, since only then can its placeholder be swapped for the value.
//
// Placeholders are swapped in request headers, the path and the query, where credentials
// travel, and never in bodies: an upstream may store a body and hand it back, value and all.
// For the same reason the values of the host's secrets are masked in responses.

// interception serves the requests of one intercepted connection.
type interception struct {
	host    string
	tls     bool              // the sandbox spoke TLS; requests go upstream over TLS too
	headers map[string]string // set on every request; values may reference {secret.NAME}
	bound   []secrets.Binding // secrets that may be sent to host
	unbound []secrets.Binding
	masked  [][]byte // values to mask in responses
	proxy   *httputil.ReverseProxy
	log     *slog.Logger

	mu      sync.Mutex
	problem string // the last refused request, for the connection log
}

// boundTo splits an environment's secrets by whether they may be sent to host.
func boundTo(all []secrets.Binding, host string) (bound, unbound []secrets.Binding) {
	for _, b := range all {
		ok := false
		for _, p := range b.Hosts {
			if policy.Covers(p, host) {
				ok = true
				break
			}
		}
		if ok {
			bound = append(bound, b)
		} else {
			unbound = append(unbound, b)
		}
	}
	return bound, unbound
}

// Values shorter than this are not masked in responses: they would mask innocent text, and
// a value that short is no credential worth hiding.
const minMasked = 8

// intercept serves the sandbox's requests on c until it hangs up. It returns the bytes
// sent and received on c and the last refused request, if any.
func (g *Gateway) intercept(c net.Conn, br *bufio.Reader, envID, host string, port int, isTLS bool, rule store.Rule, all []secrets.Binding) (sent, received int64, problem string) {
	ic := &interception{host: host, tls: isTLS, headers: rule.Config.Headers, log: g.Log}
	ic.bound, ic.unbound = boundTo(all, host)
	for _, b := range ic.bound {
		if len(b.Value) >= minMasked {
			ic.masked = append(ic.masked, b.Value)
		}
	}

	dial := g.Dial
	if dial == nil {
		dial = publicDialer.DialContext
	}
	transport := &http.Transport{
		// Every request goes to host, whatever address the sandbox connected to.
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dial(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
		},
		TLSClientConfig:     &tls.Config{ServerName: host, RootCAs: g.upstreamRoots},
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: 15 * time.Second,
		IdleConnTimeout:     90 * time.Second,
	}
	defer transport.CloseIdleConnections()
	scheme := "http"
	if isTLS {
		scheme = "https"
	}
	ic.proxy = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme, pr.Out.URL.Host = scheme, net.JoinHostPort(host, strconv.Itoa(port))
		},
		Transport:      transport,
		FlushInterval:  -1, // streamed responses (server-sent events) pass through as they come
		ModifyResponse: ic.maskResponse,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if errors.Is(err, context.Canceled) { // the sandbox went away
				return
			}
			ic.refuse(w, http.StatusBadGateway, fmt.Sprintf("Sandbox Studio could not complete the request to %s: %s", host, ic.mask(err.Error())))
		},
		ErrorLog: slog.NewLogLogger(g.Log.Handler(), slog.LevelDebug),
	}

	cc := &countingConn{Conn: c, r: br}
	var conn net.Conn = cc
	if isTLS {
		// Shaking hands here rather than in the HTTP server tells why it failed.
		tc := tls.Server(cc, g.serverTLS(envID, host))
		tc.SetDeadline(time.Now().Add(15 * time.Second))
		if err := tc.Handshake(); err != nil {
			return cc.sent.Load(), cc.received.Load(), handshakeProblem(err)
		}
		tc.SetDeadline(time.Time{})
		conn = tc
	}
	serveHTTP(conn, ic, g.Log, time.Minute)
	ic.mu.Lock()
	defer ic.mu.Unlock()
	return cc.sent.Load(), cc.received.Load(), ic.problem
}

func (ic *interception) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// The policy approved host, and the requests go there. A Host header naming another site
	// would otherwise reach it through a shared front end (domain fronting).
	if name := hostOnly(r.Host); name != "" && name != ic.host {
		ic.refuse(w, http.StatusMisdirectedRequest, fmt.Sprintf("This connection is to %s. Open a new connection for %s.", ic.host, name))
		return
	}
	out := r.Clone(r.Context())
	if out.Host == "" {
		out.Host = ic.host
	}
	if err := ic.rewrite(out); err != nil {
		ic.refuse(w, http.StatusForbidden, err.Error())
		return
	}
	ic.proxy.ServeHTTP(w, out)
}

// rewrite swaps placeholders for values and sets the rule's headers.
func (ic *interception) rewrite(r *http.Request) error {
	for name, values := range r.Header {
		for i, v := range values {
			var err error
			if strings.EqualFold(name, "Authorization") && len(v) > 6 && strings.EqualFold(v[:6], "Basic ") {
				v, err = ic.substituteBasic(v)
			} else {
				v, err = ic.substitute(v, "the "+name+" header", nil)
			}
			if err != nil {
				return err
			}
			values[i] = v
		}
	}
	if path := r.URL.EscapedPath(); path != "" {
		escaped, err := ic.substitute(path, "the URL path", url.PathEscape)
		if err != nil {
			return err
		}
		if escaped != path {
			r.URL.RawPath = escaped
			if r.URL.Path, err = url.PathUnescape(escaped); err != nil {
				return err
			}
		}
	}
	query, err := ic.substitute(r.URL.RawQuery, "the URL query", url.QueryEscape)
	if err != nil {
		return err
	}
	r.URL.RawQuery = query
	for name, tmpl := range ic.headers {
		v, err := ic.expand(name, tmpl)
		if err != nil {
			return err
		}
		r.Header.Set(name, v)
	}
	if len(ic.masked) > 0 {
		// Without an Accept-Encoding of the client's, the transport asks for gzip and
		// decompresses it, so responses can be masked.
		r.Header.Del("Accept-Encoding")
	}
	return nil
}

// substitute swaps the placeholders in s for their values, escaped by escape if it isn't
// nil. A placeholder of a secret that isn't bound to the host is refused, since sending it
// shows the sandbox expects it to work there.
func (ic *interception) substitute(s, where string, escape func(string) string) (string, error) {
	if !strings.Contains(s, placeholderPrefix) {
		return s, nil
	}
	for _, b := range ic.unbound {
		if strings.Contains(s, b.Placeholder) {
			return "", fmt.Errorf("%s holds the placeholder of the secret %s, which Sandbox Studio only sends to %s, not to %s. Bind the secret to %s in Sandbox Studio if it belongs there.",
				where, b.Name, strings.Join(b.Hosts, ", "), ic.host, ic.host)
		}
	}
	for _, b := range ic.bound {
		if !strings.Contains(s, b.Placeholder) {
			continue
		}
		if !ic.tls {
			return "", plaintextError(b.Name, ic.host)
		}
		v := string(b.Value)
		if escape != nil {
			v = escape(v)
		}
		s = strings.ReplaceAll(s, b.Placeholder, v)
	}
	return s, nil
}

// placeholderPrefix starts every placeholder, which saves searching strings without one.
const placeholderPrefix = "studio-"

// substituteBasic swaps placeholders inside HTTP basic credentials, which are base64.
func (ic *interception) substituteBasic(header string) (string, error) {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(header[6:]))
	if err != nil {
		return ic.substitute(header, "the Authorization header", nil)
	}
	creds, err := ic.substitute(string(decoded), "the Authorization header", nil)
	if err != nil || creds == string(decoded) {
		return header, err
	}
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(creds)), nil
}

var secretRef = regexp.MustCompile(`\{secret\.([A-Za-z0-9_]+)\}`)

// expand resolves the {secret.NAME} references of a rule header.
func (ic *interception) expand(header, tmpl string) (string, error) {
	var err error
	v := secretRef.ReplaceAllStringFunc(tmpl, func(ref string) string {
		name := secretRef.FindStringSubmatch(ref)[1]
		if b, ok := named(ic.bound, name); ok {
			if ic.tls {
				return string(b.Value)
			}
			err = plaintextError(name, ic.host)
		} else if _, ok := named(ic.unbound, name); ok {
			err = fmt.Errorf("The Sandbox Studio rule for %s sets the %s header from the secret %s, which is not bound to %s.", ic.host, header, name, ic.host)
		} else {
			err = fmt.Errorf("The Sandbox Studio rule for %s sets the %s header from the secret %s, which does not exist.", ic.host, header, name)
		}
		return ""
	})
	return v, err
}

func named(list []secrets.Binding, name string) (secrets.Binding, bool) {
	for _, b := range list {
		if b.Name == name {
			return b, true
		}
	}
	return secrets.Binding{}, false
}

func plaintextError(name, host string) error {
	return fmt.Errorf("Sandbox Studio only sends the secret %s over HTTPS. Use https://%s.", name, host)
}

// refuse answers a request Studio did not pass on, and notes why for the connection log.
func (ic *interception) refuse(w http.ResponseWriter, code int, msg string) {
	ic.mu.Lock()
	ic.problem = msg
	ic.mu.Unlock()
	ic.log.Info("intercepted request failed", "host", ic.host, "status", code, "reason", msg)
	writeRefusal(w, code, msg)
}

func handshakeProblem(err error) string {
	msg := "TLS handshake: " + err.Error()
	if strings.Contains(msg, "unknown certificate authority") || strings.Contains(msg, "bad certificate") {
		msg += " (the sandbox does not trust the environment's Sandbox Studio CA)"
	}
	return msg
}

// --- masking responses -------------------------------------------------------------------

// mask hides the host's secret values in s.
func (ic *interception) mask(s string) string {
	for _, v := range ic.masked {
		s = strings.ReplaceAll(s, string(v), strings.Repeat("*", len(v)))
	}
	return s
}

// maskResponse hides the values of the host's secrets in a response, in case the upstream
// echoes a request back (error messages quoting a bad key, echo services). Masks have the
// value's length, so Content-Length stays right.
func (ic *interception) maskResponse(resp *http.Response) error {
	if len(ic.masked) == 0 {
		return nil
	}
	for _, values := range resp.Header {
		for i, v := range values {
			values[i] = ic.mask(v)
		}
	}
	enc := resp.Header.Get("Content-Encoding")
	if resp.StatusCode != http.StatusSwitchingProtocols && (enc == "" || enc == "identity") {
		resp.Body = &masker{r: resp.Body, values: ic.masked}
	}
	return nil
}

// masker masks values in a stream. It holds back only a tail that could be the start of a
// value, so streamed responses still arrive as they are sent.
type masker struct {
	r      io.ReadCloser
	values [][]byte
	buf    []byte // read, masked, not yet returned; buf[:ready] is final
	ready  int
	err    error
	chunk  [32 << 10]byte
}

func (m *masker) Read(p []byte) (int, error) {
	for m.ready == 0 {
		if m.err != nil {
			if len(m.buf) == 0 {
				return 0, m.err
			}
			m.ready = len(m.buf) // nothing more can complete a value
			break
		}
		n, err := m.r.Read(m.chunk[:])
		m.buf, m.err = append(m.buf, m.chunk[:n]...), err
		for _, v := range m.values {
			for i := 0; ; {
				j := bytes.Index(m.buf[i:], v)
				if j < 0 {
					break
				}
				copy(m.buf[i+j:], bytes.Repeat([]byte("*"), len(v)))
				i += j + len(v)
			}
		}
		m.ready = len(m.buf) - partialTail(m.buf, m.values)
	}
	n := copy(p, m.buf[:m.ready])
	m.buf, m.ready = m.buf[n:], m.ready-n
	if len(m.buf) == 0 {
		m.buf = nil
	}
	return n, nil
}

func (m *masker) Close() error { return m.r.Close() }

// partialTail returns the length of the longest suffix of b that a value starts with.
func partialTail(b []byte, values [][]byte) int {
	tail := 0
	for _, v := range values {
		for i := max(0, len(b)-len(v)+1); i < len(b)-tail; i++ {
			if b[i] == v[0] && bytes.HasPrefix(v, b[i:]) {
				tail = len(b) - i
				break
			}
		}
	}
	return tail
}

// --- serving -------------------------------------------------------------------------------

// serverTLS is the TLS configuration presented to a sandbox for host.
func (g *Gateway) serverTLS(envID, host string) *tls.Config {
	return &tls.Config{
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			return g.CA.Leaf(hello.Context(), envID, host)
		},
		NextProtos: []string{"h2", "http/1.1"},
	}
}

// refuseTLS completes the TLS handshake to answer the sandbox's first request with msg,
// which an agent can read, unlike a closed connection.
func (g *Gateway) refuseTLS(c net.Conn, br *bufio.Reader, envID, host, msg string) {
	c.SetDeadline(time.Now().Add(10 * time.Second))
	tc := tls.Server(&countingConn{Conn: c, r: br}, g.serverTLS(envID, host))
	serveHTTP(tc, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Connection", "close")
		writeRefusal(w, http.StatusForbidden, msg)
	}), g.Log, 10*time.Second)
}

func writeRefusal(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Sandbox-Studio", "refused")
	w.WriteHeader(code)
	io.WriteString(w, msg+"\n")
}

// serveHTTP serves HTTP/1.1 or HTTP/2 on c with h until the client hangs up or waits
// longer than timeout for a request.
func serveHTTP(c net.Conn, h http.Handler, log *slog.Logger, timeout time.Duration) {
	l := &singleListener{c: c, done: make(chan struct{})}
	var hijacked atomic.Bool
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h.ServeHTTP(w, r)
			if hijacked.Load() { // an upgraded connection (WebSocket) ends with its handler
				l.Close()
			}
		}),
		ReadHeaderTimeout: timeout,
		IdleTimeout:       2 * timeout,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelDebug),
		ConnState: func(_ net.Conn, s http.ConnState) {
			switch s {
			case http.StateHijacked:
				hijacked.Store(true)
			case http.StateClosed:
				l.Close()
			}
		},
	}
	srv.Serve(l)
}

// singleListener hands out one connection, then blocks until closed.
type singleListener struct {
	c    net.Conn
	once sync.Once
	done chan struct{}
	used atomic.Bool
}

func (l *singleListener) Accept() (net.Conn, error) {
	if !l.used.Swap(true) {
		return l.c, nil
	}
	<-l.done
	return nil, net.ErrClosed
}

func (l *singleListener) Close() error   { l.once.Do(func() { close(l.done) }); return nil }
func (l *singleListener) Addr() net.Addr { return l.c.LocalAddr() }

// countingConn reads through r (which holds the peeked bytes) and counts the traffic.
type countingConn struct {
	net.Conn
	r              io.Reader
	sent, received atomic.Int64
}

func (c *countingConn) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.sent.Add(int64(n))
	return n, err
}

func (c *countingConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.received.Add(int64(n))
	return n, err
}

// hostOnly returns the normalized host of a Host header.
func hostOnly(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		hostport = h
	}
	return policy.Normalize(strings.TrimSuffix(strings.TrimPrefix(hostport, "["), "]"))
}
