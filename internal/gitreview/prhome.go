package gitreview

import (
	"context"
	"net/url"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// prHome is where a repository's pull requests are: a forge with them, and the repository's
// path there.
type prHome struct {
	forge store.Forge
	repo  string
}

// prHomes finds where the pull requests of repo on f are, when f is a forge without them, such
// as a folder on the user's computer: the forges with pull requests that the sandbox's clones of
// it also have as remotes, through Studio's git remote, and, for a local forge, those the
// folder's repository itself has as remotes. forges are the environment's.
func prHomes(ctx context.Context, f store.Forge, repo string, clones []agentproto.GitRepo, forges []store.Forge) []prHome {
	var out []prHome
	add := func(c store.Forge, path string) {
		if !slices.ContainsFunc(out, func(h prHome) bool { return h.forge.ID == c.ID && h.repo == path }) {
			out = append(out, prHome{c, path})
		}
	}
	for _, cl := range clones {
		if !slices.ContainsFunc(cl.Remotes, func(r agentproto.GitRemote) bool { return r.Forge == f.Name && r.Repository == repo }) {
			continue
		}
		for _, r := range cl.Remotes {
			i := slices.IndexFunc(forges, func(c store.Forge) bool { return c.Name == r.Forge })
			if i >= 0 && HasAPI(forges[i]) {
				add(forges[i], r.Repository)
			}
		}
	}
	if f.Kind == KindLocal {
		if owner, name, ok := splitPath(repo); ok {
			if dir, err := localRepo(target{forge: f, owner: owner, repo: name}); err == nil {
				for _, raw := range folderRemotes(ctx, dir) {
					if c, path, ok := remoteOn(forges, raw); ok {
						add(c, path)
					}
				}
			}
		}
	}
	return out
}

// folderRemotes are the fetch URLs of the remotes of the repository at dir, on this computer,
// as git reads them (with the user's url.<base>.insteadOf).
func folderRemotes(ctx context.Context, dir string) []string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "remote", "-v")
	cmd.Env = gitEnv("")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var urls []string
	for line := range strings.Lines(string(out)) {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[2] == "(fetch)" {
			urls = append(urls, fields[1])
		}
	}
	return urls
}

// remoteAddr splits a git remote URL into its host, its port ("" for the scheme's own), the
// repository's path and whether it goes over SSH: https://host/owner/repo.git,
// ssh://git@host:2222/owner/repo.git or git@host:owner/repo.git. ok is false for anything
// else, such as a path on this computer.
func remoteAddr(raw string) (host, port, path string, ssh, ok bool) {
	raw = strings.TrimSpace(raw)
	if scheme, _, found := strings.Cut(raw, "://"); found {
		switch scheme {
		case "https", "http", "ssh", "git+ssh", "ssh+git":
		default:
			return "", "", "", false, false
		}
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" {
			return "", "", "", false, false
		}
		ssh = strings.Contains(scheme, "ssh")
		port = u.Port()
		if ssh && port == "22" || !ssh && (port == "443" || port == "80") {
			port = ""
		}
		return strings.ToLower(u.Hostname()), port, u.Path, ssh, true
	}
	// scp-like, [user@]host:path, which git reads so only when no slash comes before the colon.
	hostpart, p, found := strings.Cut(raw, ":")
	if !found || p == "" || hostpart == "" || strings.ContainsAny(hostpart, `/\`) {
		return "", "", "", false, false
	}
	if _, h, at := strings.Cut(hostpart, "@"); at {
		hostpart = h
	}
	return strings.ToLower(strings.Trim(hostpart, "[]")), "", p, true, true
}

// remoteOn finds the forge with pull requests among forges that raw, a remote URL of a
// repository on this computer, points at, and the repository's path there: an HTTPS URL under
// a forge's base URL, or an SSH one to its host; for an ssh forge with an API token, an SSH URL
// to its address or an HTTPS one to its API's host.
func remoteOn(forges []store.Forge, raw string) (store.Forge, string, bool) {
	host, port, p, ssh, ok := remoteAddr(raw)
	if !ok {
		return store.Forge{}, "", false
	}
	p = strings.TrimSuffix(strings.Trim(p, "/"), ".git")
	under := func(base string) (string, bool) {
		b, err := url.Parse(base)
		if err != nil || !strings.EqualFold(b.Hostname(), host) {
			return "", false
		}
		if ssh {
			return p, true // the web path of a forge under one isn't the SSH path
		}
		if b.Port() != port {
			return "", false
		}
		if pre := strings.Trim(b.Path, "/"); pre != "" {
			return strings.CutPrefix(p, pre+"/")
		}
		return p, true
	}
	for _, f := range forges {
		api, ok := apiForge(f)
		if !ok {
			continue
		}
		path, ok := "", false
		if f.Kind == KindSSH {
			if !ssh {
				path, ok = under(api.BaseURL)
			} else if u, err := ParseSSHURL(f.BaseURL); err == nil {
				fp := ""
				if u.Port != 22 {
					fp = strconv.Itoa(u.Port)
				}
				path, ok = p, strings.EqualFold(u.Host, host) && fp == port
			}
		} else {
			path, ok = under(f.BaseURL)
		}
		if !ok {
			continue
		}
		if clean, _, err := CheckRepoPath(api.Kind, path); err == nil {
			return f, clean, true
		}
	}
	return store.Forge{}, "", false
}

// sandboxClones are the repositories in the sandbox's workspace and their remotes through
// Studio, as the guest reports them, or none when Studio can't ask it.
func (s *Service) sandboxClones(ctx context.Context, sb store.Sandbox) []agentproto.GitRepo {
	f := s.forgeState()
	f.mu.Lock()
	g := f.guest
	f.mu.Unlock()
	if g == nil {
		return nil
	}
	if _, ok := g.Connected(sb.ID); !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	reply, err := g.Files(ctx, sb.ID, agentproto.FilesRequest{Op: agentproto.FilesGitRemotes})
	if err != nil {
		return nil
	}
	return reply.Repos
}

// pullRequestsOf turns the forge and repository a pull request tool got into where that
// repository's pull requests are, when the forge has none, such as the folder on the user's
// computer the sandbox cloned it from (the `studio` command names the forge of origin): the one
// forge with pull requests that prHomes finds. It leaves them as they are when it finds none, for
// reviewForge to say why, and asks for the forge when it finds several.
func (s *Service) pullRequestsOf(ctx context.Context, sb store.Sandbox, forge, repository string) (string, string, error) {
	if forge == "" || repository == "" {
		return forge, repository, nil
	}
	f, err := s.Store.ForgeByName(ctx, sb.EnvironmentID, forge)
	if err != nil || HasAPI(f) {
		return forge, repository, nil // reviewForge says what's wrong with a forge that isn't there
	}
	forges, err := s.Store.Forges(ctx, sb.EnvironmentID)
	if err != nil {
		return "", "", err
	}
	homes := prHomes(ctx, f, strings.Trim(repository, "/"), s.sandboxClones(ctx, sb), forges)
	switch len(homes) {
	case 0:
		return forge, repository, nil
	case 1:
		return homes[0].forge.Name, homes[0].repo, nil
	}
	var names []string
	for _, h := range homes {
		names = append(names, h.repo+" on "+h.forge.Name)
	}
	return "", "", integrations.ToolError("%s; %s is also %s: give the forge (--forge) and repository of the one you mean",
		noAPI(f), repository, strings.Join(names, ", and "))
}
