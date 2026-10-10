package browser

import (
	"errors"
	"net"
	"net/url"
	"slices"
	"strings"

	"github.com/lukaskoebe/sandbox-studio/internal/policy"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Verdict is what the policy decides for one browser action.
type Verdict int

const (
	Deny Verdict = iota
	Ask
	Allow
)

func (v Verdict) String() string { return [...]string{"deny", "ask", "allow"}[v] }

// Request is one browser action as the policy sees it.
type Request struct {
	Action string // a broker action: open, click, fill, ...; or an agent-browser command
	Origin string // scheme://host[:port] of the page, or of the target for open
	Role   string // accessibility role of the element, if any
	Label  string // its accessible name
	// Sensitive marks a password-like target field (see Field.Sensitive).
	Sensitive bool
}

// denied are agent-browser capabilities no agent gets, whatever the patterns say: script
// evaluation, page rewriting, network interception and recording, and reading cookies,
// storage or saved state. The broker exposes no tool for them; they are listed so the
// policy refuses them even if one were added.
var denied = []string{"eval", "addscript", "addinitscript", "addstyle", "setcontent", "route", "unroute",
	"network", "har", "cookies", "storage", "state", "credentials", "set", "get_html", "cdp"}

// autoAllowed actions only read the page or move around it.
var autoAllowed = []string{"snapshot", "scroll", "get_text", "get_value", "screenshot", "wait", "back"}

// asked actions change the page; they need an allow pattern for the origin or an approval.
var asked = []string{"click", "fill", "type", "press", "select", "fill_credential"}

// alwaysAsked actions move files across the browser VM's boundary; no pattern allows them.
var alwaysAsked = []string{"upload", "download"}

// Decide applies the fixed rules and the persona's patterns to an action. The order:
// the deny list and sensitive get_value, deny patterns, uploads and downloads (always
// asked), read-only actions, allow patterns, open (the gateway still gates the network),
// then ask for page changes and deny anything unknown. Sensitive patterns do not decide
// actions; they mark fields for redaction.
func Decide(r Request, patterns []store.BrowserPattern) (Verdict, string) {
	switch {
	case slices.Contains(denied, r.Action):
		return Deny, r.Action + " is never available to agents"
	case r.Action == "get_value" && r.Sensitive:
		return Deny, "reading a password-like field is never available to agents"
	}
	for _, p := range patterns {
		if p.Verdict == store.BrowserDeny && Matches(p, r) {
			return Deny, "a browser rule denies " + r.Action + " here"
		}
	}
	switch {
	case slices.Contains(alwaysAsked, r.Action):
		return Ask, ""
	case slices.Contains(autoAllowed, r.Action):
		return Allow, ""
	}
	for _, p := range patterns {
		if p.Verdict == store.BrowserAllow && Matches(p, r) {
			return Allow, ""
		}
	}
	switch {
	case r.Action == "open":
		return Allow, ""
	case slices.Contains(asked, r.Action):
		return Ask, ""
	}
	return Deny, "unknown browser action " + r.Action
}

// Matches reports whether a pattern covers an action. Action * matches every action; an
// empty role or label matches any element.
func Matches(p store.BrowserPattern, r Request) bool {
	if p.Action != "*" && p.Action != r.Action {
		return false
	}
	if !OriginMatches(p.Origin, r.Origin) {
		return false
	}
	if p.Role != "" && !strings.EqualFold(p.Role, r.Role) {
		return false
	}
	return p.Label == "" || glob(strings.ToLower(p.Label), strings.ToLower(r.Label))
}

// OriginMatches matches an origin against a pattern: * matches any; otherwise the pattern
// is [scheme://]host[:port], the host may start with *. and a missing scheme or port
// matches any.
func OriginMatches(pattern, origin string) bool {
	if pattern == "*" {
		return true
	}
	ps, ph, pp := splitOrigin(pattern)
	os, oh, op := splitOrigin(origin)
	if oh == "" || ph == "" {
		return false
	}
	if ps != "" && ps != os || pp != "" && pp != op {
		return false
	}
	return policy.Covers(ph, oh)
}

func splitOrigin(o string) (scheme, host, port string) {
	o = strings.ToLower(strings.TrimSpace(o))
	if i := strings.Index(o, "://"); i >= 0 {
		scheme, o = o[:i], o[i+3:]
	}
	o, _, _ = strings.Cut(o, "/")
	if h, p, err := net.SplitHostPort(o); err == nil {
		return scheme, strings.Trim(h, "[]"), p
	}
	return scheme, strings.Trim(o, "[]"), ""
}

// Origin returns scheme://host[:port] of an http(s) URL, or "" for anything else.
func Origin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + strings.ToLower(u.Host)
}

// ErrBadPattern rejects a pattern that could not match anything sensible.
var ErrBadPattern = errors.New("invalid browser pattern")

// CheckPattern validates and normalizes a pattern before it is stored.
func CheckPattern(p store.BrowserPattern) (store.BrowserPattern, error) {
	p.Action = strings.TrimSpace(strings.ToLower(p.Action))
	p.Origin = strings.TrimSpace(strings.ToLower(p.Origin))
	p.Role = strings.TrimSpace(p.Role)
	p.Label = strings.TrimSpace(p.Label)
	if p.Verdict != store.BrowserAllow && p.Verdict != store.BrowserDeny && p.Verdict != store.BrowserSensitive {
		return p, ErrBadPattern
	}
	if p.Action == "" || len(p.Action) > 32 || len(p.Role) > 64 || len(p.Label) > 200 {
		return p, ErrBadPattern
	}
	if p.Verdict == store.BrowserSensitive {
		p.Action = "*" // a field is sensitive for every action
		if p.Role == "" && p.Label == "" {
			return p, ErrBadPattern
		}
	}
	if p.Origin != "*" {
		s, h, _ := splitOrigin(p.Origin)
		if s != "" && s != "http" && s != "https" {
			return p, ErrBadPattern
		}
		if _, err := policy.ValidPattern(h); err != nil || h == "*" {
			return p, ErrBadPattern
		}
	}
	return p, nil
}

// glob matches s against a pattern where * stands for any run of characters.
func glob(pattern, s string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == s
	}
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	for _, mid := range parts[1 : len(parts)-1] {
		i := strings.Index(s, mid)
		if i < 0 {
			return false
		}
		s = s[i+len(mid):]
	}
	return strings.HasSuffix(s, parts[len(parts)-1])
}
