package browser

import (
	"encoding/json"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Snapshots come from agent-browser 0.39's `snapshot` (cli/src/native/snapshot.rs,
// render_tree): one line per accessibility node,
//
//	{indent}- {role} {JSON-quoted name} [level=N, checked=…, expanded=…, selected, disabled, required, ref=eN, url=…] {cursor kind [hints]}: {value}
//
// where every part after the role is optional. The value is the node's AX value, appended
// raw when it is not empty and differs from the name; a multi-line value (a textarea)
// continues on the following lines. The snapshot does not say whether an input is a
// password field or what its autocomplete is, so the broker asks the page for the type and
// autocomplete attributes of every valued element (Field) and blanks what it must here.

// RedactedValue replaces a blanked value.
const RedactedValue = "[redacted]"

// Field is what the broker learned about one element with a value.
type Field struct {
	Ref          string
	Role, Name   string
	Type         string // the input's type attribute, lowercased
	Autocomplete string // its autocomplete attribute, lowercased
	// Probed is false when the attributes could not be read; the value is then blanked.
	Probed bool
	// Credential marks a field Studio filled from the vault.
	Credential bool
}

// sensitiveAutocomplete are the autocomplete tokens whose fields are never shown.
var sensitiveAutocomplete = []string{"current-password", "new-password", "one-time-code"}

// Sensitive reports whether a field's value must be blanked: a password input, a field
// whose autocomplete names a password, a one-time code or payment card data (cc-*), a field
// Studio filled with a credential, one a sensitive pattern covers, or one whose attributes
// could not be read (fail closed).
func (f Field) Sensitive(origin string, patterns []store.BrowserPattern) bool {
	if !f.Probed || f.Credential || f.Type == "password" {
		return true
	}
	// autocomplete is a list of tokens, such as "section-login username" or "shipping cc-number".
	for _, tok := range strings.Fields(f.Autocomplete) {
		if strings.HasPrefix(tok, "cc-") {
			return true
		}
		for _, s := range sensitiveAutocomplete {
			if tok == s {
				return true
			}
		}
	}
	for _, p := range patterns {
		if p.Verdict == store.BrowserSensitive && Matches(p, Request{Action: p.Action, Origin: origin, Role: f.Role, Label: f.Name}) {
			return true
		}
	}
	return false
}

// Line is one parsed snapshot line.
type Line struct {
	Indent int    // spaces before "- "
	Role   string // empty for a continuation line
	Name   string
	Ref    string // eN, if the node has a ref
	head   string // the line up to its value
	Value  string // the raw value, if any
}

var refAttr = regexp.MustCompile(`(?:\[|, )ref=(e[0-9]+)(?:,|\])`)

// ParseLine parses one snapshot line. A line that is not a node line (it continues the
// previous node's multi-line value) has an empty Role.
func ParseLine(s string) Line {
	trimmed := strings.TrimLeft(s, " ")
	indent := len(s) - len(trimmed)
	if !strings.HasPrefix(trimmed, "- ") || indent%2 != 0 {
		return Line{Value: s}
	}
	rest := trimmed[2:]
	role, after, _ := strings.Cut(rest, " ")
	if strings.HasSuffix(role, ":") {
		// "- text: Hello": a role without name or attributes, then the value.
		role = strings.TrimSuffix(role, ":")
		after = ":" + strings.TrimPrefix(rest, role+":")
	}
	if role == "" {
		return Line{Value: s}
	}
	l := Line{Indent: indent, Role: role}
	headLen := len(s) - len(after)
	if strings.HasPrefix(after, `"`) {
		if q, err := strconv.QuotedPrefix(after); err == nil {
			var name string
			if json.Unmarshal([]byte(q), &name) == nil {
				l.Name = name
			}
			headLen += len(q)
			after = after[len(q):]
		}
	}
	// The first ": " after the name starts the value: attributes and cursor hints hold
	// no ": " (a url attribute holds "://"). verify (live) against pages with odd URLs.
	if i := strings.Index(after, ": "); i >= 0 {
		l.head = s[:headLen+i]
		l.Value = after[i+2:]
	} else if strings.HasPrefix(after, ":") && strings.TrimSpace(after) == ":" {
		l.head = s[:headLen]
	} else {
		l.head = s
	}
	if m := refAttr.FindStringSubmatch(l.head); m != nil {
		l.Ref = m[1]
	}
	return l
}

// textRoles are the roles of elements that hold typed text.
var textRoles = []string{"textbox", "searchbox", "combobox", "spinbutton"}

// Probes returns the lines whose element attributes the broker must read before showing a
// snapshot: those with a ref and a value, and text fields with children (whose text may
// sit in a child node).
func Probes(snapshot string) []Line {
	lines := strings.Split(snapshot, "\n")
	var out []Line
	for i, s := range lines {
		l := ParseLine(s)
		if l.Role == "" || l.Ref == "" {
			continue
		}
		hasChild := false
		if i+1 < len(lines) {
			n := ParseLine(lines[i+1])
			hasChild = n.Role != "" && n.Indent > l.Indent
		}
		if l.Value != "" || hasChild && slices.Contains(textRoles, l.Role) {
			out = append(out, l)
		}
	}
	return out
}

// Redact blanks the values of sensitive fields in a snapshot. fields holds what the
// broker read for each probed ref (Probes); a valued line whose ref is missing from it is
// blanked too. The continuation lines of a blanked value and the descendants of a
// sensitive node (Chromium may expose an input's text as a StaticText child; verify
// (live)) are dropped.
func Redact(snapshot, origin string, fields map[string]Field, patterns []store.BrowserPattern) string {
	lines := strings.Split(snapshot, "\n")
	out := make([]string, 0, len(lines))
	redacting := false // inside a sensitive node
	redactIndent := 0
	for _, s := range lines {
		l := ParseLine(s)
		if l.Role == "" {
			// A continuation line belongs to the last node.
			if !redacting {
				out = append(out, s)
			}
			continue
		}
		if redacting && l.Indent > redactIndent {
			continue
		}
		redacting = false
		if l.Ref == "" {
			// No ref: static text or a heading, not a form field.
			out = append(out, s)
			continue
		}
		f, known := fields[l.Ref]
		switch {
		case known && f.Sensitive(origin, patterns), !known && l.Value != "":
			redacting, redactIndent = true, l.Indent
			if l.Value != "" {
				s = l.head + ": " + RedactedValue
			}
		}
		out = append(out, s)
	}
	return strings.Join(out, "\n")
}

// Scrub replaces every occurrence of the given secrets in s. The broker runs every text it
// returns to an agent, logs or records through it, so a credential Studio filled cannot
// come back even if the page echoes it somewhere a snapshot or get_text shows.
func Scrub(s string, secrets []string) string {
	for _, v := range secrets {
		if len(v) >= minScrub {
			s = strings.ReplaceAll(s, v, RedactedValue)
		}
	}
	return s
}

// minScrub is the shortest secret Scrub replaces; shorter ones would blank ordinary text.
const minScrub = 4
