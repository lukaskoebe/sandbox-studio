package gateway

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"time"

	"golang.org/x/net/proxy"

	"github.com/lukaskoebe/sandbox-studio/internal/ca"
	"github.com/lukaskoebe/sandbox-studio/internal/caddyrule"
	"github.com/lukaskoebe/sandbox-studio/internal/secrets"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

type plainSealer struct{}

func (plainSealer) Seal(b, _ []byte) []byte            { return b }
func (plainSealer) Unseal(b, _ []byte) ([]byte, error) { return b, nil }

type fakeSecrets []secrets.Binding

func (f fakeSecrets) Bindings(context.Context, string) ([]secrets.Binding, error) { return f, nil }

const (
	apiPlaceholder    = "studio-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	apiValue          = "sk-live-1234567890"
	pathPlaceholder   = "studio-cccccccccccccccccccccccccccccccc"
	pathValue         = "tok/en with space"
	shortPlaceholder  = "studio-ffffffffffffffffffffffffffffffff"
	shortValue        = "Q!7"
	otherPlaceholder  = "studio-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	headerValue       = "header-secret-value"
	plainPlaceholder  = "studio-dddddddddddddddddddddddddddddddd"
	headerPlaceholder = "studio-eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
)

var testBindings = fakeSecrets{
	{Name: "API_KEY", Placeholder: apiPlaceholder, Hosts: []string{"example.com", "h1.example"}, Value: []byte(apiValue)},
	{Name: "PATH_KEY", Placeholder: pathPlaceholder, Hosts: []string{"example.com"}, Value: []byte(pathValue)},
	{Name: "SHORT_KEY", Placeholder: shortPlaceholder, Hosts: []string{"short.example"}, Value: []byte(shortValue)},
	{Name: "OTHER_KEY", Placeholder: otherPlaceholder, Hosts: []string{"other.example"}, Value: []byte("other-secret-value")},
	{Name: "HEADER_KEY", Placeholder: headerPlaceholder, Hosts: []string{"*.proxied.example", "proxied.example"}, Value: []byte(headerValue)},
	{Name: "PLAIN_KEY", Placeholder: plainPlaceholder, Hosts: []string{"plain.example"}, Value: []byte("plain-secret-value")},
}

type seen struct {
	Host, Path, Query, Proto string
	Header                   http.Header
}

type interceptHarness struct {
	*harness
	envCA *x509.CertPool // what sandboxes of the environment trust
	upCA  *x509.CertPool // what the upstream servers' certificates chain to

	mu   sync.Mutex
	seen []seen
}

// newInterceptHarness runs a gateway with a CA and secrets in front of an HTTPS upstream
// (port 443) and a plain HTTP one (any other port) that echo requests back.
func newInterceptHarness(t *testing.T) *interceptHarness {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	env, err := st.CreateEnvironment(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	upEnv, err := st.CreateEnvironment(ctx, "upstream")
	if err != nil {
		t.Fatal(err)
	}
	authority := &ca.Authority{Store: st, Sealer: plainSealer{}}
	h := &interceptHarness{envCA: caPool(t, authority, env.ID), upCA: caPool(t, authority, upEnv.ID)}

	echo := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		h.seen = append(h.seen, seen{Host: r.Host, Path: r.URL.EscapedPath(), Query: r.URL.RawQuery, Proto: r.Proto, Header: r.Header.Clone()})
		h.mu.Unlock()
		if r.URL.Path == "/upgrade" {
			w.Header().Set("Connection", "Upgrade")
			w.Header().Set("Upgrade", "websocket")
			w.WriteHeader(http.StatusSwitchingProtocols)
			c, rw, err := http.NewResponseController(w).Hijack()
			if err != nil {
				return
			}
			defer c.Close()
			rw.Flush()
			io.Copy(c, rw) // echoes until the client hangs up
			return
		}
		w.Header().Set("X-Echo-Auth", r.Header.Get("Authorization"))
		if r.URL.Path == "/trailer" {
			w.Header().Add("Trailer", "X-Echo-Secret")
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, "response body")
			w.Header().Set("X-Echo-Secret", r.Header.Get("Authorization"))
			return
		}
		var out io.Writer = w
		if r.URL.Path == "/gzip" && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			w.Header().Set("Content-Encoding", "gzip")
			gz := gzip.NewWriter(w)
			defer gz.Close()
			out = gz
		}
		var lines []string
		for k, v := range r.Header {
			lines = append(lines, k+": "+strings.Join(v, ","))
		}
		sort.Strings(lines)
		fmt.Fprintf(out, "%s %s?%s\n%s\n", r.Method, r.URL.EscapedPath(), r.URL.RawQuery, strings.Join(lines, "\n"))
	})
	tlsUp := httptest.NewUnstartedServer(echo)
	tlsUp.EnableHTTP2 = true
	tlsUp.TLS = &tls.Config{GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		return authority.Leaf(hello.Context(), upEnv.ID, hello.ServerName)
	}}
	tlsUp.StartTLS()
	t.Cleanup(tlsUp.Close)
	h1Up := httptest.NewUnstartedServer(echo)
	h1Up.TLS = &tls.Config{
		NextProtos: []string{"http/1.1"},
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			return authority.Leaf(hello.Context(), upEnv.ID, hello.ServerName)
		},
	}
	h1Up.StartTLS()
	t.Cleanup(h1Up.Close)
	plainUp := httptest.NewServer(echo)
	t.Cleanup(plainUp.Close)

	h.harness = &harness{policy: &fakePolicy{rules: map[string]store.Rule{
		"example.com":        {ID: "r1", Action: store.ActionAllow},
		"h1.example":         {ID: "r13", Action: store.ActionAllow},
		"plain.example":      {ID: "r2", Action: store.ActionAllow},
		"denied.example":     {ID: "r3", Action: store.ActionDeny},
		"splice.example":     {ID: "r4", Action: store.ActionAllow},
		"proxied.example":    {ID: "r5", Action: store.ActionProxy, Config: store.RuleConfig{Headers: map[string]string{"X-Api-Key": "Bearer {secret.HEADER_KEY}", "X-Static": "v1"}}},
		"badref.example":     {ID: "r6", Action: store.ActionProxy, Config: store.RuleConfig{Headers: map[string]string{"X-Api-Key": "{secret.API_KEY}"}}},
		"other.example":      {ID: "r7", Action: store.ActionAllow},
		"short.example":      {ID: "r11", Action: store.ActionAllow},
		"upgradable.example": {ID: "r12", Action: store.ActionProxy},
		"caddy.example":      {ID: "r8", Action: store.ActionCaddy, Config: store.RuleConfig{Caddyfile: caddyRule}},
		"caddy-unbound.example": {ID: "r9", Action: store.ActionCaddy, Config: store.RuleConfig{
			Caddyfile: "reverse_proxy https://example.com {\n  header_up X-Api-Key {secret.OTHER_KEY}\n}",
		}},
		"caddy-broken.example": {ID: "r10", Action: store.ActionCaddy, Config: store.RuleConfig{Caddyfile: "respond {env.HOME}"}},
	}}}
	h.gw = &Gateway{
		Key:    []byte("key"),
		Policy: h.policy,
		Sandbox: func(_ context.Context, id string) (store.Sandbox, error) {
			return store.Sandbox{ID: "sb1", EnvironmentID: env.ID, Name: "dev"}, nil
		},
		names:   func(string) Names { return fakeNames{} },
		Log:     slog.New(slog.DiscardHandler),
		Conns:   &ConnLog{},
		CA:      authority,
		Secrets: testBindings,
		Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			h.harness.mu.Lock()
			h.dialed = append(h.dialed, addr)
			h.harness.mu.Unlock()
			up := plainUp.Listener.Addr().String()
			if strings.HasPrefix(addr, "h1.example:") {
				up = h1Up.Listener.Addr().String()
			} else if strings.HasSuffix(addr, ":443") {
				up = tlsUp.Listener.Addr().String()
			}
			return (&net.Dialer{}).DialContext(ctx, network, up)
		},
		upstreamRoots: h.upCA,
		Caddy:         sharedCaddy(),
		Virtual: []VirtualHost{{Name: "echo.studio.internal", Serve: func(sb store.Sandbox) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, "virtual %s %s %s", sb.ID, sb.EnvironmentID, r.URL.Path)
			})
		}}},
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go h.gw.Serve(l)
	h.addr = l.Addr().String()
	return h
}

func caPool(t *testing.T, a *ca.Authority, envID string) *x509.CertPool {
	t.Helper()
	pemBytes, err := a.CertPEM(context.Background(), envID)
	if err != nil {
		t.Fatal(err)
	}
	p := x509.NewCertPool()
	p.AppendCertsFromPEM(pemBytes)
	return p
}

// client is a sandbox's HTTP client: it reaches everything through the gateway and trusts
// roots.
func (h *interceptHarness) client(t *testing.T, roots *x509.CertPool) *http.Client {
	t.Helper()
	d, err := proxy.SOCKS5("tcp", h.addr, &proxy.Auth{User: "sb1", Password: Password([]byte("key"), "sb1")}, proxy.Direct)
	if err != nil {
		t.Fatal(err)
	}
	tr := &http.Transport{
		DialContext:        d.(proxy.ContextDialer).DialContext,
		TLSClientConfig:    &tls.Config{RootCAs: roots},
		ForceAttemptHTTP2:  true,
		DisableCompression: true,
	}
	t.Cleanup(tr.CloseIdleConnections)
	return &http.Client{Transport: tr, Timeout: 10 * time.Second}
}

func (h *interceptHarness) lastSeen(t *testing.T) seen {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.seen) == 0 {
		t.Fatal("no request reached the upstream")
	}
	return h.seen[len(h.seen)-1]
}

func (h *interceptHarness) requests() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.seen)
}

func do(t *testing.T, c *http.Client, req *http.Request) (*http.Response, string) {
	t.Helper()
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(body)
}

func get(t *testing.T, c *http.Client, url string, header ...string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	return do(t, c, req)
}

func TestInterceptSubstitutesPlaceholders(t *testing.T) {
	h := newInterceptHarness(t)
	c := h.client(t, h.envCA)

	resp, body := get(t, c, "https://example.com/v1/"+pathPlaceholder+"/x?key="+apiPlaceholder+"&p="+pathPlaceholder+"&q=1",
		"Authorization", "Bearer "+apiPlaceholder, "X-Body-Like", "unrelated studio-text")
	if resp.StatusCode != 200 || resp.ProtoMajor != 2 {
		t.Fatalf("status %d over %s: %s", resp.StatusCode, resp.Proto, body)
	}
	up := h.lastSeen(t)
	if up.Path != "/v1/tok%2Fen%20with%20space/x" {
		t.Errorf("upstream path %q", up.Path)
	}
	if up.Query != "key="+apiValue+"&p=tok%2Fen+with+space&q=1" {
		t.Errorf("upstream query %q", up.Query)
	}
	if got := up.Header.Get("Authorization"); got != "Bearer "+apiValue {
		t.Errorf("upstream Authorization %q", got)
	}
	if up.Host != "example.com" || up.Proto != "HTTP/2.0" {
		t.Errorf("upstream saw host %q over %s", up.Host, up.Proto)
	}
	// The echo carries the values back; the sandbox must not see them.
	if strings.Contains(body, apiValue) || strings.Contains(body, pathValue) || strings.Contains(resp.Header.Get("X-Echo-Auth"), apiValue) {
		t.Errorf("response leaks a value: %q %q", body, resp.Header.Get("X-Echo-Auth"))
	}
	if !strings.Contains(body, "Bearer "+strings.Repeat("*", len(apiValue))) {
		t.Errorf("response not masked: %q", body)
	}

	req, _ := http.NewRequest("GET", "https://example.com/basic", nil)
	req.SetBasicAuth("user", apiPlaceholder)
	do(t, c, req)
	if user, pass, _ := (&http.Request{Header: h.lastSeen(t).Header}).BasicAuth(); user != "user" || pass != apiValue {
		t.Errorf("basic auth reached upstream as %q:%q", user, pass)
	}

	c.CloseIdleConnections()
	waitFor(t, func() bool {
		log := h.gw.Conns.List("sb1")
		return len(log) == 1 && log[0].Intercepted && log[0].Verdict == VerdictAllowed && log[0].Error == ""
	}, func() string { return fmt.Sprintf("%+v", h.gw.Conns.List("sb1")) })
}

func TestInterceptRefusesMisplacedSecrets(t *testing.T) {
	h := newInterceptHarness(t)
	c := h.client(t, h.envCA)
	for _, tc := range []struct {
		name, url, header string
		code              int
		want              string
	}{
		{"unbound placeholder", "https://example.com/?k=" + otherPlaceholder, "", 403, "OTHER_KEY, which Sandbox Studio only sends to other.example, not to example.com"},
		{"unbound in a header", "https://other.example/", apiPlaceholder, 403, "API_KEY"},
		{"plaintext", "http://plain.example:8080/", plainPlaceholder, 403, "only sends the secret PLAIN_KEY over HTTPS"},
		{"rule header with an unbound secret", "https://badref.example/", "", 403, "secret API_KEY, which is not bound to badref.example"},
	} {
		before := h.requests()
		resp, body := get(t, c, tc.url, "X-Key", tc.header)
		if resp.StatusCode != tc.code || !strings.Contains(body, tc.want) || resp.Header.Get("X-Sandbox-Studio") != "refused" {
			t.Errorf("%s: %d %q", tc.name, resp.StatusCode, body)
		}
		if h.requests() != before {
			t.Errorf("%s: the request reached the upstream", tc.name)
		}
	}

	// Without a placeholder, plain HTTP to a host with secrets passes.
	if resp, body := get(t, c, "http://plain.example:8080/ok"); resp.StatusCode != 200 {
		t.Errorf("plain request: %d %q", resp.StatusCode, body)
	}

	// The certificate is for example.com; a request for another host on that connection
	// would reach whatever the upstream serves under that name.
	req, _ := http.NewRequest("GET", "https://example.com/", nil)
	req.Host = "other.example"
	before := h.requests()
	if resp, body := do(t, c, req); resp.StatusCode != http.StatusMisdirectedRequest {
		t.Errorf("fronted request: %d %q", resp.StatusCode, body)
	}
	if h.requests() != before {
		t.Error("the fronted request reached the upstream")
	}
}

func TestProxyRuleHeaders(t *testing.T) {
	h := newInterceptHarness(t)
	c := h.client(t, h.envCA)
	resp, body := get(t, c, "https://proxied.example/", "X-Static", "from the sandbox")
	if resp.StatusCode != 200 {
		t.Fatalf("%d %q", resp.StatusCode, body)
	}
	up := h.lastSeen(t)
	if up.Header.Get("X-Api-Key") != "Bearer "+headerValue || up.Header.Get("X-Static") != "v1" || len(up.Header.Values("X-Static")) != 1 {
		t.Errorf("upstream headers %v", up.Header)
	}
	if strings.Contains(body, headerValue) {
		t.Errorf("response leaks the header value: %q", body)
	}
}

func TestInterceptMasksShortSecrets(t *testing.T) {
	h := newInterceptHarness(t)
	c := h.client(t, h.envCA)
	resp, body := get(t, c, "https://short.example/", "Authorization", shortPlaceholder)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %q", resp.StatusCode, body)
	}
	if got := h.lastSeen(t).Header.Get("Authorization"); got != shortValue {
		t.Errorf("upstream Authorization %q", got)
	}
	masked := strings.Repeat("*", len(shortValue))
	if strings.Contains(body, shortValue) || !strings.Contains(body, "Authorization: "+masked) {
		t.Errorf("short value not masked in body: %q", body)
	}
	if got := resp.Header.Get("X-Echo-Auth"); got != masked {
		t.Errorf("short value not masked in response header: %q", got)
	}
}

func TestInterceptSuppressesSecretResponseTrailers(t *testing.T) {
	h := newInterceptHarness(t)
	c := h.client(t, h.envCA)
	resp, body := get(t, c, "https://h1.example/trailer", "Authorization", apiPlaceholder)
	if resp.StatusCode != http.StatusOK || strings.Contains(body, apiValue) {
		t.Fatalf("%d %q", resp.StatusCode, body)
	}
	if up := h.lastSeen(t); up.Proto != "HTTP/1.1" {
		t.Fatalf("trailer regression did not use HTTP/1 upstream: %s", up.Proto)
	}
	if got := resp.Trailer.Get("X-Echo-Secret"); got != "" {
		t.Errorf("secret response trailer was forwarded: %q", got)
	}
	if len(resp.Trailer) != 0 || resp.Header.Get("Trailer") != "" {
		t.Errorf("trailers were not suppressed: header %v trailers %v", resp.Header, resp.Trailer)
	}
}

func TestInterceptMasksCompressedResponses(t *testing.T) {
	h := newInterceptHarness(t)
	c := h.client(t, h.envCA)
	resp, body := get(t, c, "https://example.com/gzip", "Authorization", apiPlaceholder, "Accept-Encoding", "gzip")
	if resp.StatusCode != 200 || resp.Header.Get("Content-Encoding") != "" {
		t.Fatalf("%d %v", resp.StatusCode, resp.Header)
	}
	if strings.Contains(body, apiValue) || !strings.Contains(body, strings.Repeat("*", len(apiValue))) {
		t.Errorf("body %q", body)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestInterceptRefusesEncodedResponseWhenMasking(t *testing.T) {
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	if _, err := io.WriteString(gz, "upstream echoed "+apiValue); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	ic := &interception{
		host:   "example.com",
		masked: [][]byte{[]byte(apiValue)},
		log:    slog.New(slog.DiscardHandler),
	}
	ic.proxy = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = "https"
			pr.Out.URL.Host = "example.com"
		},
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode:    http.StatusOK,
				Header:        http.Header{"Content-Encoding": []string{"gzip"}},
				Body:          io.NopCloser(bytes.NewReader(compressed.Bytes())),
				ContentLength: int64(compressed.Len()),
				Request:       r,
			}, nil
		}),
		ModifyResponse: ic.maskResponse,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			ic.refuse(w, http.StatusBadGateway, err.Error())
		},
	}
	rec := httptest.NewRecorder()
	ic.proxy.ServeHTTP(rec, httptest.NewRequest("GET", "http://example.com/", nil))
	if rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), apiValue) {
		t.Fatalf("encoded response was not safely refused: %d %q", rec.Code, rec.Body.String())
	}
}

func TestInterceptUpgrades(t *testing.T) {
	h := newInterceptHarness(t)
	d, _ := proxy.SOCKS5("tcp", h.addr, &proxy.Auth{User: "sb1", Password: Password([]byte("key"), "sb1")}, proxy.Direct)
	raw, err := d.Dial("tcp", "upgradable.example:443")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	raw.SetDeadline(time.Now().Add(5 * time.Second))
	c := tls.Client(raw, &tls.Config{ServerName: "upgradable.example", RootCAs: h.envCA, NextProtos: []string{"http/1.1"}})
	fmt.Fprintf(c, "GET /upgrade HTTP/1.1\r\nHost: upgradable.example\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, nil)
	if err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade: %v %v", resp, err)
	}
	c.Write([]byte("ping"))
	buf := make([]byte, 4)
	if _, err := io.ReadFull(br, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("echo %q %v", buf, err)
	}
}

func TestInterceptRefusesUpgradesWhileMaskingSecrets(t *testing.T) {
	h := newInterceptHarness(t)
	for _, tc := range []struct {
		host, authorization string
	}{
		{"example.com", "Bearer " + apiPlaceholder},
		{"short.example", shortPlaceholder},
	} {
		t.Run(tc.host, func(t *testing.T) {
			d, _ := proxy.SOCKS5("tcp", h.addr, &proxy.Auth{User: "sb1", Password: Password([]byte("key"), "sb1")}, proxy.Direct)
			raw, err := d.Dial("tcp", net.JoinHostPort(tc.host, "443"))
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			raw.SetDeadline(time.Now().Add(5 * time.Second))
			c := tls.Client(raw, &tls.Config{ServerName: tc.host, RootCAs: h.envCA, NextProtos: []string{"http/1.1"}})
			fmt.Fprintf(c, "GET /upgrade HTTP/1.1\r\nHost: %s\r\nAuthorization: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n", tc.host, tc.authorization)
			resp, err := http.ReadResponse(bufio.NewReader(c), nil)
			if err != nil {
				t.Fatalf("upgrade response: %v", err)
			}
			if resp.StatusCode != http.StatusBadGateway || resp.Header.Get("X-Sandbox-Studio") != "refused" {
				t.Fatalf("upgrade was not refused: %d", resp.StatusCode)
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(body), apiValue) || strings.Contains(string(body), shortValue) {
				t.Errorf("refusal leaked a secret: %q", body)
			}
		})
	}
}

func TestTLSRefusalsAreReadable(t *testing.T) {
	h := newInterceptHarness(t)
	c := h.client(t, h.envCA)
	for host, want := range map[string]string{
		"denied.example":  "blocked by a Sandbox Studio network rule",
		"unknown.example": "waiting for approval",
	} {
		resp, body := get(t, c, "https://"+host+"/")
		if resp.StatusCode != 403 || !strings.Contains(body, want) {
			t.Errorf("%s: %d %q", host, resp.StatusCode, body)
		}
	}
}

func TestAllowedHostsWithoutSecretsAreSpliced(t *testing.T) {
	h := newInterceptHarness(t)
	// Spliced, the sandbox talks TLS with the upstream itself.
	resp, body := get(t, h.client(t, h.upCA), "https://splice.example/")
	if resp.StatusCode != 200 {
		t.Fatalf("%d %q", resp.StatusCode, body)
	}
	if log := h.gw.Conns.List("sb1"); len(log) != 1 || log[0].Intercepted {
		t.Fatalf("%+v", log)
	}
}

func TestUntrustedCAIsLogged(t *testing.T) {
	h := newInterceptHarness(t)
	if _, err := h.client(t, h.upCA).Get("https://example.com/"); err == nil {
		t.Fatal("a sandbox without the environment's CA accepted its certificate")
	}
	waitFor(t, func() bool {
		log := h.gw.Conns.List("sb1")
		return len(log) == 1 && strings.Contains(log[0].Error, "does not trust")
	}, func() string { return fmt.Sprintf("%+v", h.gw.Conns.List("sb1")) })
}

func TestMasker(t *testing.T) {
	tests := []struct {
		name   string
		values [][]byte
		in     string
		want   string
	}{
		{
			name:   "overlapping matches",
			values: [][]byte{[]byte("sk-live-1234567890"), []byte("abcabcab")},
			in:     "x sk-live-1234567890 y sk-live-12 abcabcabcab sk-live-1234567890",
			want:   "x ****************** y sk-live-12 *********** ******************",
		},
		{
			name:   "longer overlap split across reads",
			values: [][]byte{[]byte("abcdefgh"), []byte("bcdefghXYZ")},
			in:     "abcdefghXYZ",
			want:   "***********",
		},
	}
	readers := map[string]func(string) io.Reader{
		"whole":     func(s string) io.Reader { return strings.NewReader(s) },
		"bytewise":  func(s string) io.Reader { return iotest.OneByteReader(strings.NewReader(s)) },
		"half-read": func(s string) io.Reader { return iotest.HalfReader(strings.NewReader(s)) },
	}
	for _, tc := range tests {
		if got := (&interception{masked: tc.values}).mask(tc.in); got != tc.want {
			t.Errorf("%s string mask: %q want %q", tc.name, got, tc.want)
		}
		for name, newReader := range readers {
			got, err := io.ReadAll(&masker{r: io.NopCloser(newReader(tc.in)), values: tc.values})
			if err != nil || string(got) != tc.want {
				t.Errorf("%s %s: %q %v", tc.name, name, got, err)
			}
		}
	}
}

func TestBoundTo(t *testing.T) {
	bound, unbound := boundTo(testBindings, "api.proxied.example")
	if len(bound) != 1 || bound[0].Name != "HEADER_KEY" || len(unbound) != len(testBindings)-1 {
		t.Fatalf("bound %v", bound)
	}
	if hostOnly("[2001:db8::1]:443") != "2001:db8::1" || hostOnly("Example.COM.:8443") != "example.com" {
		t.Fatal("hostOnly")
	}
}

func waitFor(t *testing.T, cond func() bool, state func() string) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); !cond(); {
		if time.Now().After(deadline) {
			t.Fatalf("condition not met: %s", state())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// --- caddy rules ---------------------------------------------------------------------------

// caddyRule passes GET and POST on to example.com with the API key, and refuses the rest.
const caddyRule = `@ok method GET POST
handle @ok {
  reverse_proxy https://example.com {
    header_up Host example.com
    header_up X-Api-Key "Bearer {secret.API_KEY}"
  }
}
respond "read only" 403
`

// Caddy's state is global, so the tests share a Caddy engine, and its upstream: a TLS
// server for example.com (httptest's certificate) that echoes requests.
var (
	caddyOnce   sync.Once
	caddyEngine *caddyrule.Engine
	caddyMu     sync.Mutex
	caddySeen   []seen
)

func sharedCaddy() *caddyrule.Engine {
	caddyOnce.Do(func() {
		up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			caddyMu.Lock()
			caddySeen = append(caddySeen, seen{Host: r.Host, Path: r.URL.Path, Header: r.Header.Clone()})
			caddyMu.Unlock()
			fmt.Fprintf(w, "%s %s %q\nX-Api-Key: %s\n", r.Method, r.URL.Path, body, r.Header.Get("X-Api-Key"))
		}))
		roots := x509.NewCertPool()
		roots.AddCert(up.Certificate())
		caddyEngine = &caddyrule.Engine{
			Dir:     filepath.Join(os.TempDir(), "sandbox-studio-gateway-test-caddy"), // never written
			RootCAs: roots,
			Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
				if addr != "example.com:443" {
					return nil, fmt.Errorf("refusing to connect to %s", addr)
				}
				return (&net.Dialer{}).DialContext(ctx, network, up.Listener.Addr().String())
			},
		}
	})
	return caddyEngine
}

func caddyRequests() []seen {
	caddyMu.Lock()
	defer caddyMu.Unlock()
	return slices.Clone(caddySeen)
}

func TestCaddyRule(t *testing.T) {
	h := newInterceptHarness(t)
	c := h.client(t, h.envCA)
	before := len(caddyRequests())

	resp, body := get(t, c, "https://caddy.example/repos", "X-Api-Key", "the sandbox's own")
	if resp.StatusCode != 200 {
		t.Fatalf("%d %q", resp.StatusCode, body)
	}
	seen := caddyRequests()
	if len(seen) != before+1 {
		t.Fatalf("the upstream got %d requests", len(seen)-before)
	}
	if up := seen[len(seen)-1]; up.Header.Get("X-Api-Key") != "Bearer "+apiValue || up.Host != "example.com" || up.Path != "/repos" {
		t.Errorf("upstream saw %+v", up)
	}
	// The echo carries the key back; the sandbox must not see it.
	if strings.Contains(body, apiValue) || !strings.Contains(body, "X-Api-Key: Bearer "+strings.Repeat("*", len(apiValue))) {
		t.Errorf("response not masked: %q", body)
	}

	req, _ := http.NewRequest("POST", "https://caddy.example/items", strings.NewReader("payload"))
	if resp, body := do(t, c, req); resp.StatusCode != 200 || !strings.Contains(body, `POST /items "payload"`) {
		t.Errorf("POST: %d %q", resp.StatusCode, body)
	}
	// The sandbox spoke plain HTTP; the rule still sends the key over HTTPS.
	if resp, body := get(t, c, "http://caddy.example/plain"); resp.StatusCode != 200 || strings.Contains(body, apiValue) {
		t.Errorf("plain HTTP: %d %q", resp.StatusCode, body)
	}

	before = len(caddyRequests())
	for _, tc := range []struct {
		name, method, url, header string
		code                      int
		want                      string
	}{
		{"refused by the rule", "DELETE", "https://caddy.example/items", "", 403, "read only"},
		{"a placeholder", "GET", "https://caddy.example/", apiPlaceholder, 403, "go through a Sandbox Studio Caddy rule"},
		{"a secret the rule may not send", "GET", "https://caddy-unbound.example/", "", 403, "sends the secret OTHER_KEY to example.com, which it is not bound to"},
		{"a broken rule", "GET", "https://caddy-broken.example/", "", 403, "Caddy rule for caddy-broken.example doesn't work: {env.*}"},
	} {
		req, _ := http.NewRequest(tc.method, tc.url, nil)
		req.Header.Set("X-Key", tc.header)
		if resp, body := do(t, c, req); resp.StatusCode != tc.code || !strings.Contains(body, tc.want) {
			t.Errorf("%s: %d %q", tc.name, resp.StatusCode, body)
		}
	}
	if n := len(caddyRequests()) - before; n != 0 {
		t.Errorf("%d refused requests reached the upstream", n)
	}

	c.CloseIdleConnections()
	waitFor(t, func() bool {
		var intercepted, broken bool
		for _, e := range h.gw.Conns.List("sb1") {
			switch e.Host {
			case "caddy.example":
				intercepted = e.Intercepted && e.RuleID == "r8"
			case "caddy-broken.example":
				broken = e.Verdict == VerdictFailed && strings.HasPrefix(e.Error, "caddy rule: ")
			}
		}
		return intercepted && broken
	}, func() string { return fmt.Sprintf("%+v", h.gw.Conns.List("sb1")) })
}

func TestVirtualHosts(t *testing.T) {
	h := newInterceptHarness(t)
	c := h.client(t, h.envCA)
	before := h.requests()

	resp, body := get(t, c, "https://echo.studio.internal/x")
	if resp.StatusCode != 200 || !strings.HasPrefix(body, "virtual sb1 ") || !strings.HasSuffix(body, " /x") {
		t.Fatalf("status %d: %q", resp.StatusCode, body)
	}
	resp, body = get(t, c, "https://other.studio.internal/x")
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(body, "no service") {
		t.Fatalf("unknown service: %d %q", resp.StatusCode, body)
	}
	resp, body = get(t, c, "http://echo.studio.internal/x")
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(body, "https://echo.studio.internal/") {
		t.Fatalf("plain HTTP: %d %q", resp.StatusCode, body)
	}
	if h.requests() != before {
		t.Fatal("a virtual host request went upstream")
	}
	h.harness.mu.Lock()
	defer h.harness.mu.Unlock()
	for _, d := range h.dialed {
		if strings.Contains(d, "studio.internal") {
			t.Fatalf("dialed %s", d)
		}
	}
	for _, r := range h.policy.asked {
		if strings.HasSuffix(r.Host, "studio.internal") {
			t.Fatalf("asked the policy about %s", r.Host)
		}
	}
}
