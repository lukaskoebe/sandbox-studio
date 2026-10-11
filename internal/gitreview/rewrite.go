package gitreview

import (
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Rewrite maps the real URLs of a forge's repositories to the virtual remote, as git's
// url.<URL>.insteadOf in the sandbox's managed git config. A sandbox that clones
// git@github.com:o/r or https://github.com/o/r thus goes through Studio.
type Rewrite struct {
	URL       string   // https://git.studio.internal/<forge>/
	InsteadOf []string // URL prefixes git replaces with URL
}

// Rewrites returns the rewrites for an environment's forges: the base URL's HTTPS form for
// the HTTPS kinds, plus the scp and ssh:// forms with user git (for an ssh forge its own
// user and port; scp only on port 22). When forges share a host, the oldest gets the rules
// and the others are reached through their explicit virtual URL.
func Rewrites(forges []store.Forge) []Rewrite {
	forges = slices.Clone(forges)
	slices.SortStableFunc(forges, func(a, b store.Forge) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	var out []Rewrite
	claimed := map[string]bool{}
	for _, f := range forges {
		host, rules := rewriteRules(f)
		if host == "" || claimed[host] {
			continue
		}
		claimed[host] = true
		out = append(out, Rewrite{URL: "https://" + Host + "/" + f.Name + "/", InsteadOf: rules})
	}
	slices.SortFunc(out, func(a, b Rewrite) int { return strings.Compare(a.URL, b.URL) })
	return out
}

func rewriteRules(f store.Forge) (string, []string) {
	if f.Kind == KindSSH {
		u, err := ParseSSHURL(f.BaseURL)
		if err != nil {
			return "", nil
		}
		host := bracket(u.Host)
		rules := []string{"ssh://" + u.User + "@" + host + portSuffix(u.Port) + "/"}
		if u.Port == 22 {
			rules = append(rules, u.User+"@"+host+":")
		}
		return u.Host, rules
	}
	u, err := url.Parse(f.BaseURL)
	if err != nil || u.Hostname() == "" {
		return "", nil
	}
	host := bracket(u.Hostname())
	return u.Hostname(), []string{f.BaseURL + "/", "git@" + host + ":", "ssh://git@" + host + "/"}
}

func bracket(host string) string {
	if strings.Contains(host, ":") {
		return "[" + host + "]"
	}
	return host
}

func portSuffix(port int) string {
	if port == 22 || port == 0 {
		return ""
	}
	return ":" + strconv.Itoa(port)
}
