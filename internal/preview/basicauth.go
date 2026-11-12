package preview

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// Logins keeps the HTTP basic auth sign-ins the user gave in Studio's dialog, by sandbox and
// port, in memory only: they last until Studio stops. The proxy adds a preview's sign-in to
// each request that carries none of its own. A nil Logins keeps none.
type Logins struct {
	mu sync.Mutex
	m  map[loginKey]login
}

type loginKey struct {
	id   string
	port int
}

type login struct{ user, pass string }

// NewLogins returns an empty Logins.
func NewLogins() *Logins { return &Logins{m: map[loginKey]login{}} }

// Set keeps the sign-in for the preview of port in sandbox id.
func (l *Logins) Set(id string, port int, user, pass string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.m[loginKey{id, port}] = login{user, pass}
}

// Forget drops the preview's sign-in.
func (l *Logins) Forget(id string, port int) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.m, loginKey{id, port})
}

func (l *Logins) get(id string, port int) (login, bool) {
	if l == nil {
		return login{}, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	c, ok := l.m[loginKey{id, port}]
	return c, ok
}

// forgetIf drops the preview's sign-in if it is still c, which the app just refused.
func (l *Logins) forgetIf(id string, port int, c login) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.m[loginKey{id, port}] == c {
		delete(l.m, loginKey{id, port})
	}
}

// BasicAuthMessage is the type of the message the sign-in page posts to the frame's parent.
const BasicAuthMessage = "studio-preview-basic-auth"

// basicRealm is the realm of a Basic challenge among a response's WWW-Authenticate values, and
// whether there is one.
func basicRealm(values []string) (string, bool) {
	for _, v := range values {
		scheme, params, _ := strings.Cut(strings.TrimSpace(v), " ")
		if !strings.EqualFold(scheme, "Basic") {
			continue
		}
		for _, p := range strings.Split(params, ",") {
			k, val, ok := strings.Cut(strings.TrimSpace(p), "=")
			if ok && strings.EqualFold(strings.TrimSpace(k), "realm") {
				return strings.Trim(strings.TrimSpace(val), `"`), true
			}
		}
		return "", true
	}
	return "", false
}

// askInStudio answers a framed page's Basic challenge with a page that asks Studio, the frame's
// parent, for the sign-in, instead of the browser's own dialog, which would cover all of Studio.
// failed says the sign-in Studio sent was refused. The message also names the page, which Studio
// opens again once signed in. A page loaded in a tab of its own keeps the browser's dialog.
func askInStudio(resp *http.Response, failed bool) bool {
	if resp.StatusCode != http.StatusUnauthorized || !isNavigation(resp.Request) || resp.Request.Header.Get("Sec-Fetch-Dest") != "iframe" {
		return false
	}
	realm, ok := basicRealm(resp.Header.Values("WWW-Authenticate"))
	if !ok {
		return false
	}
	msg, _ := json.Marshal(map[string]any{"type": BasicAuthMessage, "realm": realm, "failed": failed})
	body := fmt.Appendf(nil, `<!doctype html><meta charset="utf-8"><title>Sign-in needed</title>
<body style="font:15px system-ui;margin:3rem;color:#444"><h1 style="font-size:1.2rem">This preview asks for a sign-in</h1>
<p>Sign in from Studio, or reload the preview to be asked again.</p>
<script>var m=%s;m.path=location.pathname+location.search;parent.postMessage(m,"*")</script>`, msg)
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	for _, h := range []string{"WWW-Authenticate", "Content-Encoding", "Content-Security-Policy", "Content-Security-Policy-Report-Only", "X-Frame-Options"} {
		resp.Header.Del(h)
	}
	resp.Header.Set("Content-Type", "text/html; charset=utf-8")
	resp.Header.Set("Content-Length", fmt.Sprint(len(body)))
	resp.Header.Set("Cache-Control", "no-store")
	return true
}
