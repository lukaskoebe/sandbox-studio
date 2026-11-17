package webauth

import (
	"net"
	"strings"
)

// DevTools hosts are devtools-<token>.localhost:<studio-port>. The token is a grant the
// browser broker issued when the user opened DevTools for a page of a teammate's browser
// (internal/browser/devtools.go); the host is DevTools' own origin, so the frontend Chromium
// serves from the browser VM never runs with Studio's. The middleware lets these hosts
// through without a cookie, and the broker refuses a token it didn't issue.
const devToolsPrefix = "devtools-"

// DevToolsTokenLen is the length of a DevTools token: 128 bits in lowercase hex.
const DevToolsTokenLen = 32

// DevToolsHost is the host, with port, that serves the DevTools of a grant's token.
func DevToolsHost(token, port string) string {
	return net.JoinHostPort(devToolsPrefix+token+".localhost", port)
}

// DevToolsToken is the token of a DevTools host, if hostport is one.
func DevToolsToken(hostport string) (string, bool) {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	label, ok := strings.CutSuffix(strings.ToLower(host), ".localhost")
	if !ok {
		return "", false
	}
	token, ok := strings.CutPrefix(label, devToolsPrefix)
	if !ok || len(token) != DevToolsTokenLen {
		return "", false
	}
	for _, c := range token {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", false
		}
	}
	return token, true
}
