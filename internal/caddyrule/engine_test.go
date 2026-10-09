package caddyrule

import (
	"bufio"
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Caddy's state is global, so the tests share an Engine. Upstreams are httptest TLS
// servers, whose certificate is for example.com; testDial connects example.com:443 to the
// current one and refuses everything else, like the gateway's dialer would refuse a
// private address.
var (
	testEngine *Engine
	dialMu     sync.Mutex
	dialTo     string // the current upstream's address
)

func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "caddyrule-home")
	if err != nil {
		panic(err)
	}
	for _, v := range []string{"HOME", "XDG_DATA_HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "AppData", "LocalAppData"} {
		os.Setenv(v, home)
	}
	dir, err := os.MkdirTemp("", "caddyrule-data")
	if err != nil {
		panic(err)
	}
	cert := httptest.NewTLSServer(http.NotFoundHandler())
	roots := x509.NewCertPool()
	roots.AddCert(cert.Certificate())
	cert.Close()
	testEngine = &Engine{Dir: dir, Dial: testDial, RootCAs: roots}

	code := m.Run()
	if entries, _ := os.ReadDir(home); len(entries) > 0 {
		fmt.Printf("Caddy wrote to the home directory: %v\n", entries)
		code = 1
	}
	os.RemoveAll(home)
	os.RemoveAll(dir)
	os.Exit(code)
}

func testDial(ctx context.Context, network, addr string) (net.Conn, error) {
	dialMu.Lock()
	to := dialTo
	dialMu.Unlock()
	if addr != "example.com:443" || to == "" {
		return nil, errors.New("refused")
	}
	var d net.Dialer
	return d.DialContext(ctx, network, to)
}

func upstream(t *testing.T, h http.HandlerFunc) {
	srv := httptest.NewTLSServer(h)
	dialMu.Lock()
	dialTo = srv.Listener.Addr().String()
	dialMu.Unlock()
	t.Cleanup(func() {
		dialMu.Lock()
		dialTo = ""
		dialMu.Unlock()
		srv.Close()
	})
}

// load runs src as rule id and returns a transport for it, as the gateway would get one.
func load(t *testing.T, id, src string, values map[string]string) http.RoundTripper {
	t.Helper()
	c, err := testEngine.Ensure(store.Rule{ID: id, Config: store.RuleConfig{Caddyfile: src}})
	if err != nil {
		t.Fatal(err)
	}
	return testEngine.Transport(id, c, values, true)
}

// send makes a request the way the gateway's reverse proxy hands it over.
func send(t *testing.T, tr http.RoundTripper, method, target string, header http.Header) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, "https://example.com"+target, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body)
}

func TestEngineGitReadOnly(t *testing.T) {
	type seen struct {
		method, uri string
		header      http.Header
	}
	var mu sync.Mutex
	var requests []seen
	upstream(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, seen{r.Method, r.RequestURI, r.Header.Clone()})
		mu.Unlock()
		w.Header().Set("Content-Length", "2")
		w.Write([]byte("ok"))
	})
	const basic = "cmVhZGVyOmZpeHR1cmUtdG9rZW4="
	tr := load(t, "git", strings.ReplaceAll(gitReadOnly, "git.example.com", "example.com"), map[string]string{"GIT_READ_BASIC": basic})

	allowed := [][2]string{
		{"GET", "/owner/repo.git/info/refs?service=git-upload-pack"},
		{"HEAD", "/owner/repo.git/info/refs?service=git-upload-pack"},
		{"POST", "/owner/repo.git/git-upload-pack"},
	}
	denied := [][2]string{
		{"GET", "/owner/repo.git/info/refs?service=git-receive-pack"},
		{"POST", "/owner/repo.git/git-receive-pack"},
		{"POST", "/api/v1/repos/owner/repo/pulls"},
		{"PUT", "/owner/repo.git/git-upload-pack"},
		{"DELETE", "/owner/repo.git/git-upload-pack"},
		{"POST", "/owner/repo.git/info/lfs/objects/batch"},
		{"GET", "/owner/repo.git/info/refs?service=git-upload-pack&service=git-receive-pack"},
		{"POST", "/owner/repo.git/git-upload-pack?service=git-receive-pack"},
		{"GET", "/owner/repo.git/git-upload-pack"},
		{"POST", "/owner/repo.git/git-upload-pack/extra"},
	}
	client := http.Header{"Authorization": {"Bearer CLIENT-TOKEN"}, "Cookie": {"session=CLIENT-SESSION"}}
	for _, a := range allowed {
		if code, body := send(t, tr, a[0], a[1], client); code != 200 || (a[0] != "HEAD" && body != "ok") {
			t.Errorf("%s %s: %d %q, want 200", a[0], a[1], code, body)
		}
	}
	for _, d := range denied {
		if code, body := send(t, tr, d[0], d[1], client); code != 403 || body != "Read only" {
			t.Errorf("%s %s: %d %q, want 403", d[0], d[1], code, body)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != len(allowed) {
		t.Fatalf("upstream got %d requests, want %d", len(requests), len(allowed))
	}
	for i, r := range requests {
		if r.method != allowed[i][0] || r.uri != allowed[i][1] {
			t.Errorf("upstream got %s %s, want %s %s", r.method, r.uri, allowed[i][0], allowed[i][1])
		}
		if got := r.header.Get("Authorization"); got != "Basic "+basic {
			t.Errorf("Authorization %q", got)
		}
		if r.header.Get("Cookie") != "" {
			t.Errorf("the cookie was passed on")
		}
	}
}

func TestEngineSecretChecks(t *testing.T) {
	hits := 0
	upstream(t, func(w http.ResponseWriter, r *http.Request) { hits++ })
	src := "reverse_proxy https://example.com {\n  header_up X-Key {secret.KEY}\n}"
	c, err := testEngine.Ensure(store.Rule{ID: "secret-checks", Config: store.RuleConfig{Caddyfile: src}})
	if err != nil {
		t.Fatal(err)
	}

	// The rule was changed to send the secret elsewhere after the connection was set up.
	elsewhere := Compiled{Secrets: map[string][]string{"KEY": {"other.example.com"}}}
	for name, tr := range map[string]http.RoundTripper{
		"other host":    testEngine.Transport("secret-checks", elsewhere, map[string]string{"KEY": "value-of-key"}, true),
		"no value":      testEngine.Transport("secret-checks", c, map[string]string{}, true),
		"no rule state": testEngine.Transport("secret-checks", c, nil, true),
	} {
		if code, _ := send(t, tr, "GET", "/", nil); code != http.StatusBadGateway {
			t.Errorf("%s: %d, want 502", name, code)
		}
	}
	if hits != 0 {
		t.Errorf("the upstream got %d requests", hits)
	}

	// Without a secret, nothing is checked.
	tr := load(t, "no-secret", "reverse_proxy https://example.com", nil)
	if code, _ := send(t, tr, "GET", "/", nil); code != 200 || hits != 1 {
		t.Errorf("%d, %d requests", code, hits)
	}
}

func TestEngineRefusedDial(t *testing.T) {
	tr := load(t, "refused", "reverse_proxy https://private.example.com", nil)
	if code, _ := send(t, tr, "GET", "/", nil); code != http.StatusBadGateway {
		t.Errorf("%d, want 502", code)
	}
}

func TestEngineReload(t *testing.T) {
	one := load(t, "reload-one", `respond "one"`, nil)
	two := load(t, "reload-two", `respond "two"`, nil)
	if _, body := send(t, one, "GET", "/", nil); body != "one" {
		t.Errorf("%q", body)
	}
	load(t, "reload-one", `respond "one, changed"`, nil)
	if _, body := send(t, one, "GET", "/", nil); body != "one, changed" {
		t.Errorf("%q", body)
	}
	if _, body := send(t, two, "GET", "/", nil); body != "two" {
		t.Errorf("the other rule: %q", body)
	}
	if _, err := testEngine.Ensure(store.Rule{ID: "reload-one", Config: store.RuleConfig{Caddyfile: "respond {env.HOME}"}}); err == nil {
		t.Errorf("a refused rule loaded")
	}
	if _, body := send(t, one, "GET", "/", nil); body != "one, changed" {
		t.Errorf("after a refused change: %q", body)
	}
}

func TestEngineStreams(t *testing.T) {
	release := make(chan struct{})
	upstream(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow-headers" {
			<-release
			return
		}
		w.Write([]byte("first\n"))
		w.(http.Flusher).Flush()
		<-release
		w.Write([]byte("second\n"))
	})
	defer close(release)
	tr := load(t, "streams", "reverse_proxy https://example.com", nil)

	req, _ := http.NewRequest("GET", "https://example.com/", nil)
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	first := make(chan string)
	go func() {
		line, _ := bufio.NewReader(resp.Body).ReadString('\n')
		first <- line
	}()
	select {
	case line := <-first:
		if line != "first\n" {
			t.Errorf("%q", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the response didn't stream")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	req, _ = http.NewRequestWithContext(ctx, "GET", "https://example.com/slow-headers", nil)
	if _, err := tr.RoundTrip(req); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("%v, want the deadline", err)
	}
}

func TestStandardLogger(t *testing.T) {
	if log.Writer() != os.Stderr || log.Flags() != log.LstdFlags {
		t.Errorf("importing Caddy took over the standard logger")
	}
}

func TestEngineCheck(t *testing.T) {
	if _, err := testEngine.Check("@r path_regexp ^(/x\nrespond @r 200"); err == nil || !strings.Contains(err.Error(), "regexp") {
		t.Errorf("%v, want a regexp error", err)
	}
	if _, err := testEngine.Check("respond {env.HOME}"); err == nil {
		t.Errorf("checked a refused rule")
	}
	if _, err := testEngine.Check(gitReadOnly); err != nil {
		t.Error(err)
	}
}
