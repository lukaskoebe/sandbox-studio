package api

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// Guard rejects requests that could come from a web page instead of the local user:
//
//   - a Host other than a loopback name, which is how DNS-rebinding attacks arrive;
//   - unsafe methods whose Origin is a different site, e.g. a form on a preview page.
func Guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !localHost(r.Host) {
			http.Error(w, "unknown host", http.StatusMisdirectedRequest)
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if o := r.Header.Get("Origin"); o != "" && !sameHost(o, r.Host) {
				http.Error(w, "cross-origin request refused", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func localHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func sameHost(origin, host string) bool {
	u, err := url.Parse(origin)
	return err == nil && strings.EqualFold(u.Host, host)
}
