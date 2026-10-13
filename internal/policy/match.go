package policy

import (
	"errors"
	"net"
	"path"
	"slices"
	"strings"

	"golang.org/x/net/publicsuffix"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// ErrInvalidPattern is returned for host patterns rules can't use.
var ErrInvalidPattern = errors.New("host patterns are a name (example.com), a wildcard (*.example.com), an IP address, or *")

// Match returns the rule that decides a connection from sandboxID, owned by teammateID
// (empty for an unowned sandbox), to host:port. Rules that name a method or path don't decide
// connections; see matchRule.
func Match(rules []store.Rule, sandboxID, teammateID, host string, port int) (store.Rule, bool) {
	return matchRule(rules, sandboxID, teammateID, host, port, nil)
}

// matchRule returns the rule that decides a connection from sandboxID, owned by teammateID, to
// host:port. req is the request on a terminated connection, nil for a connection itself: a
// rule that names a method or path decides only the requests it covers.
//
// An environment-wide deny always wins, so it works as a fence the sandbox can't
// override. Otherwise sandbox rules come before teammate rules, teammate rules before
// environment rules, and within a scope the most specific host pattern wins, then rules
// that name ports, then rules that name a request, then deny over allow. Teammate rules
// apply only to that teammate's sandboxes.
func matchRule(rules []store.Rule, sandboxID, teammateID, host string, port int, req *HTTPRequest) (store.Rule, bool) {
	host = Normalize(host)
	var best store.Rule
	var bestRank rank
	found := false
	for _, r := range rules {
		if !appliesTo(r, sandboxID, teammateID) {
			continue
		}
		if len(r.Ports) > 0 && !slices.Contains(r.Ports, port) {
			continue
		}
		specificity := hostSpecificity(r.Host, host)
		if specificity < 0 {
			continue
		}
		if !requestFits(r, req) {
			continue
		}
		rk := rank{
			fence:       r.SandboxID == "" && r.TeammateID == "" && r.Action == store.ActionDeny,
			scope:       scopeOf(r),
			specificity: specificity,
			ports:       len(r.Ports) > 0,
			request:     r.Method != "" || r.PathPrefix != "",
			deny:        r.Action == store.ActionDeny,
		}
		if !found || rk.better(bestRank) {
			best, bestRank, found = r, rk, true
		}
	}
	return best, found
}

// requestFits reports whether a rule covers req. A rule with no method or path covers every
// request; one with them covers the requests on a terminated connection that match. An allow
// rule covers only a path the upstream reads as the path matched: one with dot segments or
// doubled slashes is refused, so a rule for /repos/* can't be walked out of with /repos/../x.
// A deny rule matches the cleaned path, so it can't be walked around either.
func requestFits(r store.Rule, req *HTTPRequest) bool {
	if r.Method == "" && r.PathPrefix == "" {
		return true
	}
	if req == nil || (r.Method != "" && r.Method != req.Method) {
		return false
	}
	if r.PathPrefix == "" {
		return true
	}
	clean := cleanPath(req.Path)
	if r.Action == store.ActionAllow && clean != req.Path {
		return false
	}
	return strings.HasPrefix(clean, strings.TrimSuffix(r.PathPrefix, "*"))
}

// cleanPath resolves a request path the way the upstream is likely to: dot segments and doubled
// slashes go, and a trailing slash stays.
func cleanPath(p string) string {
	if p == "" {
		return "/"
	}
	c := path.Clean(p)
	if strings.HasSuffix(p, "/") && c != "/" {
		c += "/"
	}
	return c
}

// appliesTo reports whether rule r covers the sandbox sandboxID, owned by teammateID (if any).
func appliesTo(r store.Rule, sandboxID, teammateID string) bool {
	return (r.SandboxID == "" || r.SandboxID == sandboxID) && (r.TeammateID == "" || r.TeammateID == teammateID)
}

type rank struct {
	fence       bool
	scope       int // 2 sandbox, 1 teammate, 0 environment
	specificity int
	ports, deny bool
	request     bool // names a method or path
}

func scopeOf(r store.Rule) int {
	switch {
	case r.SandboxID != "":
		return 2
	case r.TeammateID != "":
		return 1
	}
	return 0
}

func (a rank) better(b rank) bool {
	switch {
	case a.fence != b.fence:
		return a.fence
	case a.scope != b.scope:
		return a.scope > b.scope
	case a.specificity != b.specificity:
		return a.specificity > b.specificity
	case a.ports != b.ports:
		return a.ports
	case a.request != b.request:
		return a.request
	}
	return a.deny && !b.deny
}

// hostSpecificity scores how closely pattern matches host, or returns -1 if it doesn't.
// "*.example.com" also matches example.com itself.
func hostSpecificity(pattern, host string) int {
	switch {
	case pattern == "*":
		return 0
	case strings.HasPrefix(pattern, "*."):
		suffix := pattern[2:]
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return 1 + len(suffix)
		}
	case pattern == host:
		return 1 << 16
	}
	return -1
}

// Covers reports whether pattern matches host.
func Covers(pattern, host string) bool { return hostSpecificity(pattern, Normalize(host)) >= 0 }

// Normalize lowercases a host name and drops a trailing dot.
func Normalize(host string) string { return strings.TrimSuffix(strings.ToLower(host), ".") }

// ValidPattern checks and normalizes a rule's host pattern.
func ValidPattern(pattern string) (string, error) {
	p := Normalize(strings.TrimSpace(pattern))
	if p == "*" && strings.TrimSpace(pattern) == "*" || net.ParseIP(p) != nil {
		return p, nil
	}
	if !validName(strings.TrimPrefix(p, "*.")) {
		return "", ErrInvalidPattern
	}
	return p, nil
}

func validName(name string) bool {
	if name == "" || len(name) > 253 {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' && c != '_' {
				return false
			}
		}
	}
	return true
}

// DefaultPorts are the ports a rule created from an approval for port covers: web
// traffic gets both 80 and 443, anything else exactly the requested port.
func DefaultPorts(port int) []int {
	if port == 80 || port == 443 {
		return []int{80, 443}
	}
	return []int{port}
}

// Suggestions are the host patterns offered when approving a connection to host: the
// name itself and its registrable domain as a wildcard (never a public suffix such as
// *.github.io).
func Suggestions(host string) []string {
	host = Normalize(host)
	if net.ParseIP(host) != nil {
		return []string{host}
	}
	out := []string{host}
	if domain, err := publicsuffix.EffectiveTLDPlusOne(host); err == nil {
		out = append(out, "*."+domain)
	}
	return out
}

// Site is the registrable domain of host, the unit a browser's site approvals use. An IP
// address, or a name with no known public suffix, is its own site.
func Site(host string) string {
	host = Normalize(host)
	if net.ParseIP(host) != nil {
		return host
	}
	if domain, err := publicsuffix.EffectiveTLDPlusOne(host); err == nil {
		return domain
	}
	return host
}

// The levels of host pattern a teammate's approvals remember: the requested host itself, or
// the wildcard of its registrable domain.
const (
	LevelExact    = "exact"
	LevelWildcard = "wildcard"
)

// PatternLevel names the level of pattern for a request to host: LevelExact for host itself,
// LevelWildcard for the wildcard that Suggestions offers, and "" for any other pattern.
func PatternLevel(pattern, host string) string {
	pattern = Normalize(pattern)
	switch {
	case pattern == Normalize(host):
		return LevelExact
	case strings.HasPrefix(pattern, "*.") && slices.Contains(Suggestions(host), pattern):
		return LevelWildcard
	}
	return ""
}

// Methods a request-level rule can name.
var methods = []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}

// ErrInvalidRequest is returned for a request-level rule that names no method or path it can
// match.
var ErrInvalidRequest = errors.New("a rule's method is GET, HEAD, POST, PUT, PATCH, DELETE or OPTIONS, and its path a prefix ending in *, such as /repos/*")

// ValidMethod checks a rule's method: empty for any method, else a standard method.
func ValidMethod(method string) (string, error) {
	if method == "" || slices.Contains(methods, method) {
		return method, nil
	}
	return "", ErrInvalidRequest
}

// ValidPathPrefix checks a rule's path: empty for any path, else a path starting with / and
// ending in *, which matches the paths below the prefix before it. It has no query, fragment,
// whitespace, dot segments or doubled slashes.
func ValidPathPrefix(prefix string) (string, error) {
	if prefix == "" {
		return "", nil
	}
	valid := len(prefix) <= 512 && prefix[0] == '/' && strings.HasSuffix(prefix, "*") &&
		strings.Count(prefix, "*") == 1 && !strings.Contains(prefix, "..") && !strings.Contains(prefix, "//")
	for _, c := range prefix {
		if c <= ' ' || c == 0x7f || c == '?' || c == '#' || c == '\\' {
			valid = false
		}
	}
	if !valid {
		return "", ErrInvalidRequest
	}
	return prefix, nil
}

// PathSuggestion is the path prefix an allow for a request offers: its first directory, such as
// /repos/* for /repos/x/y. A request one level deep offers none.
func PathSuggestion(p string) string {
	if cleanPath(p) != p {
		return "" // an allow can't cover a path the upstream reads differently
	}
	first, _, ok := strings.Cut(strings.TrimPrefix(p, "/"), "/")
	if !ok || first == "" {
		return ""
	}
	return "/" + first + "/*"
}
