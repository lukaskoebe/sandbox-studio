// Package preview serves http://<port>-<sandbox-id>.localhost:<studio-port>/ by proxying to
// that port inside the sandbox over the guest-agent channel. Each preview is its own
// origin, so a page in one sandbox can't read another's or Studio's.
package preview

import (
	"context"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/webauth"
)

// DialFunc connects to a loopback port inside a sandbox.
type DialFunc func(ctx context.Context, sandboxID string, port int) (net.Conn, error)

// Parse extracts the sandbox ID and port from a preview Host header.
func Parse(hostport string) (id string, port int, ok bool) {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	label, ok := strings.CutSuffix(strings.ToLower(host), ".localhost")
	if !ok || strings.Contains(label, ".") {
		return "", 0, false
	}
	p, id, ok := strings.Cut(label, "-")
	if !ok || id == "" {
		return "", 0, false
	}
	port, err := strconv.Atoi(p)
	if err != nil || port <= 0 || port > 65535 {
		return "", 0, false
	}
	return id, port, true
}

// Route sends preview hosts to a proxy and everything else to next.
func Route(dial DialFunc, next http.Handler) http.Handler {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			id, port, _ := Parse(pr.In.Host)
			// The upstream "host" is only a key for dialing and connection pooling.
			pr.SetURL(&url.URL{Scheme: "http", Host: net.JoinHostPort(id, strconv.Itoa(port))})
			pr.Out.Host = pr.In.Host
			stripStudioCookies(pr.Out.Header)
			pr.SetXForwarded()
		},
		ModifyResponse: func(resp *http.Response) error {
			filterGuestCookies(resp.Header)
			return nil
		},
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
				id, p, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}
				port, _ := strconv.Atoi(p)
				return dial(ctx, id, port)
			},
			MaxIdleConnsPerHost: 8,
			IdleConnTimeout:     30 * time.Second,
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			_, port, _ := Parse(r.Host)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>Preview unavailable</title>
<body style="font:15px system-ui;margin:3rem;color:#444"><h1 style="font-size:1.2rem">Nothing answered on port %d</h1>
<p>Start a server in the sandbox that listens on port %d, then reload.</p><p style="color:#999">%s</p>`,
				port, port, html.EscapeString(err.Error()))
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, _, ok := Parse(r.Host); ok {
			proxy.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// stripStudioCookies keeps preview authentication credentials away from the guest while
// leaving the preview application's own cookies intact.
func stripStudioCookies(header http.Header) {
	values := header.Values("Cookie")
	if len(values) == 0 {
		return
	}
	header.Del("Cookie")
	for _, value := range values {
		parts := strings.Split(value, ";")
		kept := parts[:0]
		for _, part := range parts {
			cookieName, _, ok := strings.Cut(strings.TrimSpace(part), "=")
			cookieName = strings.TrimSpace(cookieName)
			if ok && cookieName != webauth.SessionCookie && cookieName != webauth.PreviewCookie {
				kept = append(kept, part)
			}
		}
		if len(kept) > 0 {
			header.Add("Cookie", strings.Join(kept, ";"))
		}
	}
}

// filterGuestCookies prevents guest responses from setting Studio's authentication cookies
// or cookies scoped to sibling preview/Studio hosts. Host-only application cookies pass
// through unchanged.
func filterGuestCookies(header http.Header) {
	values := header.Values("Set-Cookie")
	if len(values) == 0 {
		return
	}
	header.Del("Set-Cookie")
	for _, value := range values {
		if !dropGuestCookie(value) {
			header.Add("Set-Cookie", value)
		}
	}
}

func dropGuestCookie(value string) bool {
	parts := strings.Split(value, ";")
	name, _, hasValue := strings.Cut(strings.TrimSpace(parts[0]), "=")
	name = strings.TrimSpace(name)
	if hasValue && (name == webauth.SessionCookie || name == webauth.PreviewCookie) {
		return true
	}
	for _, part := range parts[1:] {
		attribute, _, _ := strings.Cut(strings.TrimSpace(part), "=")
		if strings.EqualFold(strings.TrimSpace(attribute), "domain") {
			return true
		}
	}
	return false
}
