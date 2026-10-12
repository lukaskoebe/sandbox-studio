package gitreview

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/teammates"
)

// RepoURL is a forge described by one of its repositories' URLs, as pasted into the Add
// forge dialog.
type RepoURL struct {
	Kind    string `json:"kind" enum:"forgejo,github,https,ssh"`
	BaseURL string `json:"baseUrl" doc:"The forge's base URL, or SSH address for kind ssh"`
	Name    string `json:"name" doc:"A short name for the forge, not yet taken in the environment"`
	Path    string `json:"path" doc:"The repository's path on the forge, without .git"`
	Auth    string `json:"auth,omitempty" enum:"agent,key" doc:"Kind ssh: the host's SSH agent"`
}

// apiHosts are hosts with an API Studio speaks, so their HTTPS URLs get that kind.
var apiHosts = map[string]string{"github.com": KindGitHub, "codeberg.org": KindForgejo}

// ParseRepoURL reads a repository URL: git@host:owner/repo.git, ssh://git@host:port/owner/repo.git
// or https://host/owner/repo(.git). SSH URLs make an ssh forge signing with the agent, also
// for github.com; HTTPS URLs make a github or forgejo forge for hosts with that API, else
// an https one. Name is the host's main label, such as github, and not checked for
// uniqueness.
func ParseRepoURL(raw string) (RepoURL, error) {
	raw = strings.TrimSpace(raw)
	bad := func(why string) (RepoURL, error) {
		return RepoURL{}, fmt.Errorf("%w: %s", ErrInvalid, why)
	}
	if len(raw) > teammates.MaxBaseURLLen {
		return bad("the URL is too long")
	}
	var r RepoURL
	var path string
	switch {
	case strings.HasPrefix(raw, "https://"):
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return bad("not a repository URL")
		}
		base, host, err := publicBase("https://" + u.Host)
		if err != nil {
			return RepoURL{}, err
		}
		r.Kind, r.BaseURL, path = KindHTTPS, base, u.Path
		if k, ok := apiHosts[host]; ok {
			// Browser URLs such as /owner/repo/tree/main name the repository too.
			r.Kind = k
			if parts := strings.Split(strings.Trim(path, "/"), "/"); len(parts) > 2 {
				path = parts[0] + "/" + parts[1]
			}
		}
	case strings.HasPrefix(raw, "ssh://"):
		rest := strings.TrimPrefix(raw, "ssh://")
		hostport, p, ok := strings.Cut(rest, "/")
		if !ok {
			return bad("the URL has no repository path")
		}
		u, err := ParseSSHURL("ssh://" + hostport)
		if err != nil {
			return RepoURL{}, err
		}
		r.Kind, r.BaseURL, r.Auth, path = KindSSH, u.Base, store.ForgeAuthAgent, p
	case strings.Contains(raw, "://"):
		return bad("use an https:// or ssh:// URL, or git@host:path")
	default:
		// scp-like: [user@]host:path
		hostpart, p, ok := strings.Cut(raw, ":")
		if !ok || p == "" {
			return bad("not a repository URL; paste one like git@github.com:owner/repo.git")
		}
		if strings.HasPrefix(p, "/") {
			return bad("Studio reaches repositories by their path relative to the SSH user's home, not an absolute path")
		}
		u, err := ParseSSHURL(hostpart)
		if err != nil {
			return RepoURL{}, err
		}
		r.Kind, r.BaseURL, r.Auth, path = KindSSH, u.Base, store.ForgeAuthAgent, p
	}
	owner, repo, ok := splitPath(strings.Trim(path, "/"))
	if !ok {
		return bad("the repository path must be 1 to 8 segments of letters, digits, dots, dashes and underscores")
	}
	r.Path = repo
	if owner != "" {
		r.Path = owner + "/" + repo
	}
	if hasAPI(r.Kind) && (owner == "" || strings.Contains(owner, "/")) {
		return bad("repositories on this host are owner/repo")
	}
	host := r.BaseURL
	if r.Kind == KindSSH {
		u, _ := ParseSSHURL(r.BaseURL)
		host = u.Host
	} else if u, err := url.Parse(r.BaseURL); err == nil {
		host = u.Hostname()
	}
	r.Name = shortName(host)
	return r, nil
}

// shortName names a forge after its host: the label before the top-level domain, so
// github.com and git.example.org become github and example.
func shortName(host string) string {
	name := "git"
	if net.ParseIP(host) == nil {
		labels := strings.Split(host, ".")
		n := len(labels)
		switch {
		case n >= 3 && len(labels[n-2]) <= 2: // example.co.uk
			name = labels[n-3]
		case n >= 2:
			name = labels[n-2]
		default:
			name = labels[0]
		}
	}
	var b strings.Builder
	for _, c := range strings.ToLower(name) {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' && b.Len() > 0 {
			b.WriteRune(c)
		}
	}
	name = strings.TrimRight(b.String(), "-")
	if len(name) > 24 {
		name = strings.TrimRight(name[:24], "-")
	}
	if name == "" {
		return "git"
	}
	return name
}

// FreeName returns name, or name-2, name-3 and so on if a forge in taken has it.
func FreeName(name string, taken []store.Forge) string {
	used := map[string]bool{}
	for _, f := range taken {
		used[f.Name] = true
	}
	candidate := name
	for i := 2; used[candidate]; i++ {
		candidate = fmt.Sprintf("%s-%d", name, i)
	}
	return candidate
}
