package gateway

import (
	"bufio"
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
	"sort"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"time"

	"golang.org/x/net/proxy"

	"github.com/lukaskoebe/sandbox-studio/internal/ca"
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
	otherPlaceholder  = "studio-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	headerValue       = "header-secret-value"
	plainPlaceholder  = "studio-dddddddddddddddddddddddddddddddd"
	headerPlaceholder = "studio-eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
)

var testBindings = fakeSecrets{
	{Name: "API_KEY", Placeholder: apiPlaceholder, Hosts: []string{"example.com"}, Value: []byte(apiValue)},
	{Name: "PATH_KEY", Placeholder: pathPlaceholder, Hosts: []string{"example.com"}, Value: []byte(pathValue)},
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
	plainUp := httptest.NewServer(echo)
	t.Cleanup(plainUp.Close)

	h.harness = &harness{policy: &fakePolicy{rules: map[string]store.Rule{
		"example.com":     {ID: "r1", Action: store.ActionAllow},
		"plain.example":   {ID: "r2", Action: store.ActionAllow},
		"denied.example":  {ID: "r3", Action: store.ActionDeny},
		"splice.example":  {ID: "r4", Action: store.ActionAllow},
		"proxied.example": {ID: "r5", Action: store.ActionProxy, Config: store.RuleConfig{Headers: map[string]string{"X-Api-Key": "Bearer {secret.HEADER_KEY}", "X-Static": "v1"}}},
		"badref.example":  {ID: "r6", Action: store.ActionProxy, Config: store.RuleConfig{Headers: map[string]string{"X-Api-Key": "{secret.API_KEY}"}}},
		"other.example":   {ID: "r7", Action: store.ActionAllow},
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
			if strings.HasSuffix(addr, ":443") {
				up = tlsUp.Listener.Addr().String()
			}
			return (&net.Dialer{}).DialContext(ctx, network, up)
		},
		upstreamRoots: h.upCA,
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

func TestInterceptUpgrades(t *testing.T) {
	h := newInterceptHarness(t)
	d, _ := proxy.SOCKS5("tcp", h.addr, &proxy.Auth{User: "sb1", Password: Password([]byte("key"), "sb1")}, proxy.Direct)
	raw, err := d.Dial("tcp", "example.com:443")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	raw.SetDeadline(time.Now().Add(5 * time.Second))
	c := tls.Client(raw, &tls.Config{ServerName: "example.com", RootCAs: h.envCA, NextProtos: []string{"http/1.1"}})
	fmt.Fprintf(c, "GET /upgrade HTTP/1.1\r\nHost: example.com\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
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
	values := [][]byte{[]byte("sk-live-1234567890"), []byte("abcabcab")}
	in := "x sk-live-1234567890 y sk-live-12 abcabcabcab sk-live-1234567890"
	want := "x ****************** y sk-live-12 ********cab ******************"
	for name, r := range map[string]io.Reader{
		"whole":     strings.NewReader(in),
		"bytewise":  iotest.OneByteReader(strings.NewReader(in)),
		"half-read": iotest.HalfReader(strings.NewReader(in)),
	} {
		got, err := io.ReadAll(&masker{r: io.NopCloser(r), values: values})
		if err != nil || string(got) != want {
			t.Errorf("%s: %q %v", name, got, err)
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
