package gitreview

import (
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/teammates"
)

// RepoURL is a forge described by one of its repositories' URLs, as pasted into the Add
// forge dialog.
type RepoURL struct {
	Kind    string `json:"kind" enum:"forgejo,github,gitlab,https,ssh,local"`
	BaseURL string `json:"baseUrl" doc:"The forge's base URL, SSH address for kind ssh, or folder for kind local"`
	Name    string `json:"name" doc:"A short name for the forge, not yet taken in the environment"`
	Path    string `json:"path" doc:"The repository's path on the forge, without .git"`
	Auth    string `json:"auth,omitempty" enum:"agent,key,none" doc:"Kind ssh: the host's SSH agent"`
	APIKind string `json:"apiKind,omitempty" enum:"forgejo,github,gitlab" doc:"Kind ssh: the API the host is known to have, for pull requests with a token"`
	APIURL  string `json:"apiUrl,omitempty" doc:"Kind ssh with apiKind: the API's HTTPS URL"`
}

// apiHosts are hosts with an API Studio speaks, so their HTTPS URLs get that kind.
var apiHosts = map[string]string{"github.com": KindGitHub, "codeberg.org": KindForgejo, "gitlab.com": KindGitLab}

// sshAPIHosts are the SSH hosts of apiHosts, by the HTTPS host of their API.
var sshAPIHosts = map[string]string{"github.com": "github.com", "ssh.github.com": "github.com",
	"codeberg.org": "codeberg.org", "gitlab.com": "gitlab.com", "altssh.gitlab.com": "gitlab.com"}

// ParseRepoURL reads a repository URL: git@host:owner/repo.git, ssh://git@host:port/owner/repo.git
// or https://host/owner/repo(.git). SSH URLs make an ssh forge signing with the agent, also
// for github.com; HTTPS URLs make a github or forgejo forge for hosts with that API, else
// an https one. Name is the host's main label, such as github, and not checked for
// uniqueness. A path on this computer, /home/me/git/repo, ~/git/repo or file:///..., makes
// a local forge for the folder the repository is in, named local.
func ParseRepoURL(raw string) (RepoURL, error) {
	return parseRepoURL(raw, nil)
}

// parseRepoURL is ParseRepoURL with known, more hosts by the API they have: an HTTPS host
// gets that kind, an SSH host that API on https:// and its name.
func parseRepoURL(raw string, known map[string]string) (RepoURL, error) {
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
		k, ok := apiHosts[host]
		if !ok {
			k, ok = known[host]
		}
		if ok {
			r.Kind = k
			if k == KindGitLab {
				// GitLab paths may have subgroups; "/-/" starts the project's own pages, such as
				// /-/tree/main or /-/merge_requests.
				path, _, _ = strings.Cut(u.Path, "/-/")
			} else if parts := strings.Split(strings.Trim(path, "/"), "/"); len(parts) > 2 {
				// Browser URLs such as /owner/repo/tree/main name the repository too.
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
	case strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "~") || strings.HasPrefix(raw, "file://") || filepath.VolumeName(raw) != "":
		return parseLocalRepo(raw)
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
	if badOwner(r.Kind, owner) {
		return bad("repositories on this host are owner/repo")
	}
	host := r.BaseURL
	if r.Kind == KindSSH {
		u, _ := ParseSSHURL(r.BaseURL)
		host = u.Host
		if web, ok := sshAPIHosts[host]; ok {
			r.APIKind, r.APIURL = apiHosts[web], "https://"+web
		} else if k, ok := known[host]; ok {
			r.APIKind, r.APIURL = k, "https://"+sshWebHost(host)
		}
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

// parseLocalRepo makes a local forge from a repository's path on this computer: the folder
// it is in, and its name without .git.
func parseLocalRepo(raw string) (RepoURL, error) {
	u, err := CheckLocalPath(raw)
	if err != nil {
		return RepoURL{}, err
	}
	repo := strings.TrimSuffix(filepath.Base(u.Base), ".git")
	if _, _, ok := splitPath(repo); !ok {
		return RepoURL{}, fmt.Errorf("%w: the repository's folder name must be letters, digits, dots, dashes and underscores", ErrInvalid)
	}
	dir, err := CheckLocalPath(filepath.Dir(u.Base))
	if err != nil {
		return RepoURL{}, err
	}
	return RepoURL{Kind: KindLocal, BaseURL: dir.Base, Name: "local", Path: repo, Auth: store.ForgeAuthNone}, nil
}
