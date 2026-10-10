package policy

import (
	"errors"
	"net"
	"slices"
	"strings"

	"golang.org/x/net/publicsuffix"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// ErrInvalidPattern is returned for host patterns rules can't use.
var ErrInvalidPattern = errors.New("host patterns are a name (example.com), a wildcard (*.example.com), an IP address, or *")

// Match returns the rule that decides a connection from sandboxID, owned by personaID
// (empty for an unowned sandbox), to host:port.
//
// An environment-wide deny always wins, so it works as a fence the sandbox can't
// override. Otherwise sandbox rules come before persona rules, persona rules before
// environment rules, and within a scope the most specific host pattern wins, then rules
// that name ports, then deny over allow. Persona rules apply only to that persona's
// sandboxes.
func Match(rules []store.Rule, sandboxID, personaID, host string, port int) (store.Rule, bool) {
	host = Normalize(host)
	var best store.Rule
	var bestRank rank
	found := false
	for _, r := range rules {
		if r.SandboxID != "" && r.SandboxID != sandboxID {
			continue
		}
		if r.PersonaID != "" && r.PersonaID != personaID {
			continue
		}
		if len(r.Ports) > 0 && !slices.Contains(r.Ports, port) {
			continue
		}
		specificity := hostSpecificity(r.Host, host)
		if specificity < 0 {
			continue
		}
		rk := rank{
			fence:       r.SandboxID == "" && r.PersonaID == "" && r.Action == store.ActionDeny,
			scope:       scopeOf(r),
			specificity: specificity,
			ports:       len(r.Ports) > 0,
			deny:        r.Action == store.ActionDeny,
		}
		if !found || rk.better(bestRank) {
			best, bestRank, found = r, rk, true
		}
	}
	return best, found
}

type rank struct {
	fence       bool
	scope       int // 2 sandbox, 1 persona, 0 environment
	specificity int
	ports, deny bool
}

func scopeOf(r store.Rule) int {
	switch {
	case r.SandboxID != "":
		return 2
	case r.PersonaID != "":
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
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
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
