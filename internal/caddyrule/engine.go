package caddyrule

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp/reverseproxy"
	_ "github.com/caddyserver/caddy/v2/modules/filestorage"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Engine runs the Caddy rules in an embedded Caddy: one server per rule, without listeners
// or admin endpoint. The gateway hands it the requests of a rule's intercepted connections
// through Transport.
//
// Caddy's state is global, so a process has one Engine.
type Engine struct {
	Dir string // Caddy's storage, which Caddy needs even without certificates to manage
	// Dial connects to upstreams. It must refuse what the gateway refuses.
	Dial    func(ctx context.Context, network, addr string) (net.Conn, error)
	RootCAs *x509.CertPool // verifies upstreams; nil uses the system's roots

	mu      sync.Mutex
	rules   map[string]*loadedRule // by rule ID
	servers atomic.Pointer[map[string]*caddyhttp.Server]
}

type loadedRule struct {
	source   string
	compiled Compiled
	used     time.Time
}

// active is the Engine the modules below take their dialer from.
var active atomic.Pointer[Engine]

// Rules that weren't used for this long are unloaded with the next change.
const unusedRule = 24 * time.Hour

// Ensure loads the current version of a Caddy rule, unless it already runs, and returns it
// compiled.
func (e *Engine) Ensure(rule store.Rule) (Compiled, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if r, ok := e.rules[rule.ID]; ok && r.source == rule.Config.Caddyfile {
		r.used = time.Now()
		return r.compiled, nil
	}
	compiled, err := Compile(rule.Config.Caddyfile)
	if err != nil {
		return Compiled{}, err
	}
	next := map[string]*loadedRule{rule.ID: {source: rule.Config.Caddyfile, compiled: compiled, used: time.Now()}}
	for id, r := range e.rules {
		if id != rule.ID && time.Since(r.used) < unusedRule {
			next[id] = r
		}
	}
	if err := e.load(next); err != nil {
		return Compiled{}, err
	}
	e.rules = next
	return compiled, nil
}

// Check compiles src and has Caddy provision it without running it, which catches what
// only Caddy checks, such as a regular expression that doesn't compile.
func (e *Engine) Check(src string) (Compiled, error) {
	compiled, err := Compile(src)
	if err != nil {
		return Compiled{}, err
	}
	// Provisioning sets some of Caddy's globals, as loading does.
	e.mu.Lock()
	defer e.mu.Unlock()
	e.activate()
	cfg, err := e.config(map[string]*loadedRule{"check": {compiled: compiled}})
	if err != nil {
		return Compiled{}, err
	}
	var parsed caddy.Config
	if err := json.Unmarshal(cfg, &parsed); err != nil {
		return Compiled{}, err
	}
	if err := caddy.Validate(&parsed); err != nil {
		return Compiled{}, errors.New(strings.TrimPrefix(err.Error(), "loading new config: "))
	}
	return compiled, nil
}

func (e *Engine) activate() {
	if old := active.Swap(e); old != nil && old != e {
		panic("caddyrule: a second Engine")
	}
}

func (e *Engine) load(rules map[string]*loadedRule) error {
	e.activate()
	cfg, err := e.config(rules)
	if err != nil {
		return err
	}
	if err := caddy.Load(cfg, false); err != nil {
		return err
	}
	app, err := caddy.ActiveContext().App("http")
	if err != nil {
		return err
	}
	servers := app.(*caddyhttp.App).Servers
	e.servers.Store(&servers)
	return nil
}

func (e *Engine) config(rules map[string]*loadedRule) ([]byte, error) {
	servers := map[string]any{}
	for id, r := range rules {
		servers[id] = map[string]any{
			"routes":          r.compiled.Routes,
			"automatic_https": map[string]any{"disable": true},
		}
	}
	return json.Marshal(map[string]any{
		"admin":   map[string]any{"disabled": true, "config": map[string]any{"persist": false}},
		"logging": map[string]any{"logs": map[string]any{"default": map[string]any{"writer": map[string]any{"output": "discard"}}}},
		"storage": map[string]any{"module": "file_system", "root": e.Dir},
		"apps": map[string]any{
			// Storage cleaning is what writes to Caddy's data directory in the user's home.
			"tls":  map[string]any{"disable_storage_clean": true, "disable_storage_check": true},
			"http": map[string]any{"servers": servers},
		},
	})
}

func (e *Engine) server(ruleID string) *caddyhttp.Server {
	if servers := e.servers.Load(); servers != nil {
		return (*servers)[ruleID]
	}
	return nil
}

// Transport sends requests through a loaded rule. values holds the values of the secrets
// the rule references, by name; each is only sent to the hosts c.Secrets lists for it, even
// if the rule changes in between. isTLS tells the rule the sandbox spoke TLS.
func (e *Engine) Transport(ruleID string, c Compiled, values map[string]string, isTLS bool) http.RoundTripper {
	return &transport{engine: e, ruleID: ruleID, secrets: &ruleSecrets{values: values, hosts: c.Secrets}, tls: isTLS}
}

type secretsKey struct{}

// ruleSecrets travels with a request through Caddy to the transport, which is the only
// place secrets are filled in.
type ruleSecrets struct {
	values map[string]string
	hosts  map[string][]string
}

// fill replaces the markers in a header value for a request to host.
func (s *ruleSecrets) fill(v, host string) (string, error) {
	var err error
	out := markerRef.ReplaceAllStringFunc(v, func(m string) string {
		name := markerRef.FindStringSubmatch(m)[1]
		value, ok := s.values[name]
		if !ok || !slices.Contains(s.hosts[name], host) {
			err = fmt.Errorf("the rule may not send secret %s to %s", name, host)
		}
		return value
	})
	return out, err
}

// transport runs a request through a rule's Caddy server as if it had arrived on one of its
// connections, and streams the response back.
type transport struct {
	engine  *Engine
	ruleID  string
	secrets *ruleSecrets
	tls     bool
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	srv := t.engine.server(t.ruleID)
	if srv == nil {
		return nil, errors.New("the Caddy rule is not loaded")
	}
	in := req.Clone(context.WithValue(req.Context(), secretsKey{}, t.secrets))
	in.URL.Scheme, in.URL.Host = "", ""
	in.RequestURI = in.URL.RequestURI()
	in.RemoteAddr = "0.0.0.0:0"
	if t.tls {
		in.TLS = &tls.ConnectionState{HandshakeComplete: true, ServerName: hostOnly(req.Host)}
	}

	pr, pw := io.Pipe()
	w := &pipeResponse{header: http.Header{}, body: pw, ready: make(chan struct{})}
	go func() {
		defer func() {
			if p := recover(); p != nil {
				w.WriteHeader(http.StatusInternalServerError)
				pw.CloseWithError(fmt.Errorf("caddy rule: %v", p))
				return
			}
			w.WriteHeader(http.StatusOK) // nothing written: an empty response, as net/http sends
			pw.Close()
		}()
		srv.ServeHTTP(w, in)
	}()
	select {
	case <-w.ready:
	case <-req.Context().Done():
		pr.Close()
		return nil, req.Context().Err()
	}
	resp := &http.Response{
		Status:        strconv.Itoa(w.code) + " " + http.StatusText(w.code),
		StatusCode:    w.code,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        w.sent,
		Body:          pr,
		ContentLength: -1,
		Request:       req,
	}
	if n, err := strconv.ParseInt(w.sent.Get("Content-Length"), 10, 64); err == nil && n >= 0 {
		resp.ContentLength = n
	}
	return resp, nil
}

// pipeResponse is the ResponseWriter of a request handed to Caddy. The status and headers
// are final at WriteHeader; the body streams through the pipe as Caddy writes it.
type pipeResponse struct {
	header http.Header
	body   *io.PipeWriter
	once   sync.Once
	ready  chan struct{}
	code   int
	sent   http.Header
}

func (w *pipeResponse) Header() http.Header { return w.header }

func (w *pipeResponse) WriteHeader(code int) {
	if code >= 100 && code < 200 {
		return // informational responses are not passed on
	}
	w.once.Do(func() {
		w.code, w.sent = code, w.header.Clone()
		close(w.ready)
	})
}

func (w *pipeResponse) Write(p []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	return w.body.Write(p)
}

// Flush has nothing to do: the pipe hands each write over as it happens.
func (w *pipeResponse) Flush() {}

func hostOnly(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return hostport
}

// --- Secret markers and the transport ------------------------------------------------------

// Compile writes secret references as markers, which mean nothing to Caddy. The nonce keeps
// a sandbox from writing one itself.
var (
	markerNonce = rand.Text()
	markerRef   = regexp.MustCompile(`@studio-secret\.` + markerNonce + `\.([A-Za-z0-9_]+)@`)
)

func marker(name string) string { return "@studio-secret." + markerNonce + "." + name + "@" }

const transportProtocol = "studio"

func init() {
	caddy.RegisterModule(guardedTransport{})
	// Importing Caddy sends the standard logger's output to Caddy's log; it's Studio's.
	log.SetOutput(os.Stderr)
	log.SetFlags(log.LstdFlags)
}

// guardedTransport is Caddy's HTTP transport with the gateway's dialer and Studio's roots,
// which fills in the secrets of requests to the hosts they're bound to. Compile makes every
// reverse_proxy use it.
type guardedTransport struct {
	reverseproxy.HTTPTransport
}

func (guardedTransport) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.reverse_proxy.transport." + transportProtocol,
		New: func() caddy.Module { return new(guardedTransport) },
	}
}

func (t *guardedTransport) Provision(ctx caddy.Context) error {
	e := active.Load()
	if e == nil || e.Dial == nil {
		return errors.New("no Studio dialer for Caddy rules")
	}
	if err := t.HTTPTransport.Provision(ctx); err != nil {
		return err
	}
	t.Transport.Proxy = nil
	t.Transport.DialContext = e.Dial
	t.Transport.DialTLSContext = nil
	if t.Transport.TLSClientConfig != nil && e.RootCAs != nil {
		t.Transport.TLSClientConfig.RootCAs = e.RootCAs
	}
	return nil
}

func (t *guardedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !hasMarker(req.Header) {
		return t.HTTPTransport.RoundTrip(req)
	}
	t.SetScheme(req)
	secrets, _ := req.Context().Value(secretsKey{}).(*ruleSecrets)
	if secrets == nil || req.URL.Scheme != "https" || !t.TLSEnabled() {
		return nil, errors.New("secrets are only sent over HTTPS")
	}
	host := strings.ToLower(strings.TrimSuffix(hostOnly(req.URL.Host), "."))
	// Caddy reuses the request when it retries, so it has to keep the markers.
	out := req.Clone(req.Context())
	for _, vs := range out.Header {
		for i, v := range vs {
			filled, err := secrets.fill(v, host)
			if err != nil {
				return nil, err
			}
			vs[i] = filled
		}
	}
	// The gateway masks the values in responses, which it can only do in bodies it can read:
	// without an Accept-Encoding of the rule's, Go's transport asks for gzip and decompresses.
	out.Header.Del("Accept-Encoding")
	return t.HTTPTransport.RoundTrip(out)
}

func hasMarker(h http.Header) bool {
	for _, vs := range h {
		if slices.ContainsFunc(vs, markerRef.MatchString) {
			return true
		}
	}
	return false
}

var (
	_ caddy.Provisioner = (*guardedTransport)(nil)
	_ http.RoundTripper = (*guardedTransport)(nil)
)
