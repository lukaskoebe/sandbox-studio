package reports

import (
	"net"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// Name is a word Studio knows identifies the user, and what stands in for it in a report.
type Name struct {
	Text  string // as it may appear, matched ignoring case
	Label string // such as computer or teammate, shown as <computer>
}

// minName is the shortest known name that is replaced: a teammate called "Al" would take
// every "al" in the text with it.
const minName = 3

var (
	secretPattern = regexp.MustCompile(`\b(?:` +
		`sk-[A-Za-z0-9_-]{16,}` + // Anthropic and OpenAI keys
		`|gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}` +
		`|glpat-[A-Za-z0-9_-]{16,}` +
		`|xox[abprs]-[A-Za-z0-9-]{10,}` +
		`|AKIA[0-9A-Z]{16}` +
		`|eyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}` + // a JWT
		`)`)
	// A long run of letters and digits mixing both cases and digits reads as a token; commit
	// hashes are lowercase hex, so they stay.
	tokenPattern = regexp.MustCompile(`\b[A-Za-z0-9+/_-]{32,}={0,2}`)
	emailPattern = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
	urlPattern   = regexp.MustCompile(`\b(?:https?|ssh|git)://[^\s<>"'()\[\]{}` + "`" + `]+`)
	scpPattern   = regexp.MustCompile(`\b[A-Za-z0-9._-]+@([A-Za-z0-9.-]+\.[A-Za-z]{2,}):[A-Za-z0-9._/-]+`)
	ipv4Pattern  = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}(?:/\d{1,2})?\b`)
	// IPv6: at least three groups and a "::", or all eight; times such as 12:30:45 have neither.
	ipv6Pattern = regexp.MustCompile(`(?i)\b(?:[0-9a-f]{1,4}:){1,7}:(?:[0-9a-f]{1,4}(?::[0-9a-f]{1,4})*)?\b|\b(?:[0-9a-f]{1,4}:){7}[0-9a-f]{1,4}\b`)
	homePattern = regexp.MustCompile(`(/home/|/Users/|[A-Za-z]:\\Users\\|[A-Za-z]:/Users/)([^/\\\s:'"]+)`)
	workspace   = regexp.MustCompile(`/workspace/([^/\s:'"]+)`)
	// A host under a suffix for private use names a machine on someone's network, never a
	// public service: git.corp.internal, nas.local, printer.lan.
	privateHostPattern = regexp.MustCompile(`(?i)\b(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)+(?:internal|local|localdomain|lan|home|corp|intranet)\b`)
)

// publicHosts keep their name in a report, and registries their path too: they say which
// service misbehaved without saying whose project it was.
var publicHosts = map[string]bool{
	"github.com": false, "gitlab.com": false, "codeberg.org": false, "bitbucket.org": false,
	"api.github.com": false, "raw.githubusercontent.com": false, "objects.githubusercontent.com": false,
	"registry.npmjs.org": true, "www.npmjs.com": true, "pypi.org": true, "files.pythonhosted.org": true,
	"proxy.golang.org": true, "sum.golang.org": true, "crates.io": true, "static.crates.io": true,
	"rubygems.org": true, "repo.maven.apache.org": true, "deb.debian.org": true, "security.debian.org": true,
	"archive.ubuntu.com": true, "registry-1.docker.io": true, "auth.docker.io": true, "docker.io": true,
	"ghcr.io": true, "mise.jdx.dev": true, "mise-versions.jdx.dev": true, "nodejs.org": true,
	"api.anthropic.com": true, "claude.ai": true, "console.anthropic.com": true, "api.openai.com": true,
	"chatgpt.com": true, "docs.anthropic.com": true,
}

// Anonymize takes out of text what could tell who sent it: the names Studio knows (the
// computer's, the teammate's, repositories and hosts), email addresses, IP addresses, the
// user part of home folders, the names of projects under /workspace, hosts that aren't
// public services, and secrets. It returns the text and how many things it replaced.
func Anonymize(text string, names []Name) (string, int) {
	n := 0
	sub := func(re *regexp.Regexp, s string, f func(m string) string) string {
		return re.ReplaceAllStringFunc(s, func(m string) string {
			r := f(m)
			if r != m {
				n++
			}
			return r
		})
	}
	text = sub(secretPattern, text, func(string) string { return "<secret>" })
	text = sub(tokenPattern, text, func(m string) string {
		if looksLikeToken(m) {
			return "<secret>"
		}
		return m
	})
	text = sub(urlPattern, text, anonymizeURL)
	text = sub(scpPattern, text, func(m string) string {
		host := strings.ToLower(scpPattern.FindStringSubmatch(m)[1])
		if _, ok := publicHosts[host]; !ok {
			host = "<host>"
		}
		return "git@" + host + ":<repository>"
	})
	text = privateHosts(text, &n)
	text = sub(emailPattern, text, func(m string) string {
		if strings.HasPrefix(m, "git@") {
			return m // an SSH remote's user, kept above
		}
		return "<email>"
	})
	text = sub(ipv4Pattern, text, func(m string) string {
		ip := net.ParseIP(strings.Split(m, "/")[0])
		if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
			return m
		}
		return "<ip>"
	})
	text = sub(ipv6Pattern, text, func(m string) string {
		ip := net.ParseIP(m)
		if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
			return m
		}
		return "<ip>"
	})
	text = sub(homePattern, text, func(m string) string {
		g := homePattern.FindStringSubmatch(m)
		if g[2] == "agent" || g[2] == "<user>" {
			return m
		}
		return g[1] + "<user>"
	})
	text = sub(workspace, text, func(m string) string {
		if strings.HasPrefix(m, "/workspace/<") {
			return m
		}
		return "/workspace/<project>"
	})
	// Longest first, so a repository named after its computer is replaced whole.
	names = slices.Clone(names)
	slices.SortFunc(names, func(a, b Name) int { return utf8.RuneCountInString(b.Text) - utf8.RuneCountInString(a.Text) })
	for _, nm := range names {
		t := strings.TrimSpace(nm.Text)
		if utf8.RuneCountInString(t) < minName || strings.EqualFold(t, "agent") {
			continue
		}
		// Not inside a label already put in, as <computer> would be for a computer named so.
		re := regexp.MustCompile(`(?i)(^|[^\pL\pN_<])` + regexp.QuoteMeta(t) + `($|[^\pL\pN_>])`)
		label := "<" + nm.Label + ">"
		for {
			next := re.ReplaceAllString(text, "${1}"+strings.ReplaceAll(label, "$", "$$")+"${2}")
			if next == text {
				break
			}
			n += strings.Count(next, label) - strings.Count(text, label)
			text = next
		}
	}
	return text, n
}

// looksLikeToken is whether a long run of characters mixes upper case, lower case and digits,
// as keys do and words, paths and hex hashes don't.
func looksLikeToken(s string) bool {
	var upper, lower, digit bool
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
			upper = true
		case r >= 'a' && r <= 'z':
			lower = true
		case r >= '0' && r <= '9':
			digit = true
		}
	}
	return upper && lower && digit
}

// anonymizeURL keeps a URL's scheme, and its host where that is a public service or one of
// Studio's own; it drops credentials, query and fragment, and the path except on registries
// and Studio's hosts, since a path names the user's repository or page.
func anonymizeURL(raw string) string {
	trail := ""
	for strings.ContainsAny(raw[len(raw)-1:], ".,;:!?") {
		trail = raw[len(raw)-1:] + trail
		raw = raw[:len(raw)-1]
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "<url>" + trail
	}
	host := strings.ToLower(u.Hostname())
	keepPath, public := publicHosts[host]
	studio := host == "studio.internal" || strings.HasSuffix(host, ".studio.internal")
	local := host == "localhost" || host == "127.0.0.1" || host == "::1"
	out := u.Scheme + "://"
	switch {
	case studio, local, public:
		out += u.Host
	default:
		out += "<host>"
	}
	switch {
	case u.Path == "" || u.Path == "/":
		out += u.Path
	case host == "git.studio.internal":
		// git.studio.internal/<forge>/<owner>/<repo>.git
		out += "/<forge>/<repository>"
	case studio || local || keepPath:
		out += u.EscapedPath()
	default:
		out += "/<path>"
	}
	return out + trail
}

// privateHosts replaces the hosts under a suffix for private use that the URL and SSH remote
// rules left, but not Studio's own, nor a file name such as .env.local or settings.local.json.
func privateHosts(text string, n *int) string {
	var b strings.Builder
	last := 0
	for _, m := range privateHostPattern.FindAllStringIndex(text, -1) {
		host := strings.ToLower(text[m[0]:m[1]])
		before, after := text[:m[0]], text[m[1]:]
		file := strings.HasSuffix(before, ".") || len(after) > 1 && after[0] == '.' && isLetter(after[1])
		if file || host == "studio.internal" || strings.HasSuffix(host, ".studio.internal") || strings.Contains(host, "<") {
			continue
		}
		b.WriteString(text[last:m[0]])
		b.WriteString("<host>")
		last = m[1]
		*n++
	}
	b.WriteString(text[last:])
	return b.String()
}

func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
