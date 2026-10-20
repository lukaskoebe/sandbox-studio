package gitreview

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/dnsproxy"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/teammates"
)

// Forge kinds. forgejo, github and gitlab have an API (default branch, pull or merge requests);
// https is any host speaking git over HTTPS with a token; ssh is any host speaking git over SSH
// with a key Studio generated; local is a folder of repositories on this computer (local.go).
const (
	KindForgejo = "forgejo"
	KindGitHub  = "github"
	KindGitLab  = "gitlab"
	KindHTTPS   = "https"
	KindSSH     = "ssh"
	KindLocal   = "local"
)

// hasAPI reports whether a kind has an API adapter, and so repository paths and PRs.
func hasAPI(kind string) bool { return kind == KindForgejo || kind == KindGitHub || kind == KindGitLab }

// badOwner reports whether owner can't be the owner of a repository on a forge of kind. An API
// forge needs one; Forgejo and GitHub take a single segment, GitLab any path (subgroups).
func badOwner(kind, owner string) bool {
	switch kind {
	case KindForgejo, KindGitHub:
		return owner == "" || strings.Contains(owner, "/")
	case KindGitLab:
		return owner == ""
	}
	return false
}

// errNoAPI is what API calls answer for kinds without one.
var errNoAPI = errors.New("this kind of forge has no API")

// ErrInvalid marks a forge the user described wrongly.
var ErrInvalid = errors.New("invalid forge")

// Adapter is what Studio needs from a forge beyond plain git over HTTPS.
type Adapter interface {
	// GitAuth is the basic-auth pair git requests to the forge carry.
	GitAuth(token string) (user, password string)
	// Check verifies that the token works and returns the account it belongs to.
	Check(ctx context.Context, token string) (Account, error)
	// DefaultBranch returns a repository's default branch.
	DefaultBranch(ctx context.Context, token, owner, repo string) (string, error)
	// OpenPullRequest proposes merging head into base and returns the PR's web URL.
	OpenPullRequest(ctx context.Context, token, owner, repo string, pr PullRequest) (string, error)
	// OpenPullRequestOf returns the web URL of the open pull request from head, a branch of
	// the repository itself, or "" if there is none.
	OpenPullRequestOf(ctx context.Context, token, owner, repo, head string) (string, error)
}

// PullRequest is the pull request a push proposes, and what a git.pr approval opens.
type PullRequest struct {
	Head  string `json:"head" doc:"Branch with the changes"`
	Base  string `json:"base" doc:"Branch to merge into"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

// Account is whom a forge token belongs to.
type Account struct {
	User    string
	Expires time.Time // zero if the forge didn't say
}

// adapter returns the adapter for a forge reached over HTTPS, talking through client.
func adapter(f store.Forge, client *http.Client) (Adapter, error) {
	switch f.Kind {
	case KindForgejo:
		return &Forgejo{BaseURL: f.BaseURL, Client: client}, nil
	case KindGitHub:
		return &GitHub{BaseURL: f.BaseURL, Client: client}, nil
	case KindGitLab:
		return &GitLab{BaseURL: f.BaseURL, Client: client}, nil
	case KindHTTPS:
		return Plain{User: f.Username}, nil
	case KindSSH, KindLocal:
		return nil, fmt.Errorf("%w: %s forges have no HTTPS adapter", ErrInvalid, f.Kind)
	}
	return nil, fmt.Errorf("%w: unknown kind %q", ErrInvalid, f.Kind)
}

var (
	forgeName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	userName  = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,63}$`)
)

// ForgeURL is a forge's normalized base URL.
type ForgeURL struct {
	Base string // https://host[:port][/path], ssh://user@host[:port], or a folder (local)
	Host string // without port
	Port int    // ssh only
	User string // https and ssh: the basic-auth or SSH user
}

// CheckForge validates the name, kind, base URL and username of a new forge and returns
// the normalized base URL. Hosts must be public names or addresses, for SSH as for HTTPS.
// For kind ssh the base URL is ssh://user@host[:port] or scp-style user@host: (the user
// defaults to git, the port to 22); username is ignored. For kind local it is a folder on
// this computer (CheckLocalPath). For kind https the username defaults to git; the other
// kinds have none.
func CheckForge(name, kind, baseURL, username string) (ForgeURL, error) {
	if !forgeName.MatchString(name) {
		return ForgeURL{}, fmt.Errorf("%w: the name must be 1 to 32 lower-case letters, digits and dashes, starting with a letter or digit", ErrInvalid)
	}
	switch kind {
	case KindForgejo, KindGitHub, KindGitLab, KindHTTPS:
	case KindSSH:
		return ParseSSHURL(baseURL)
	case KindLocal:
		return CheckLocalPath(baseURL)
	default:
		return ForgeURL{}, fmt.Errorf("%w: unknown kind %q", ErrInvalid, kind)
	}
	base, host, err := publicBase(baseURL)
	if err != nil {
		return ForgeURL{}, err
	}
	u := ForgeURL{Base: base, Host: host}
	if kind == KindHTTPS {
		u.User = strings.TrimSpace(username)
		if u.User == "" {
			u.User = "git"
		}
		if !userName.MatchString(u.User) {
			return ForgeURL{}, fmt.Errorf("%w: the username may only contain letters, digits, dots, dashes and underscores", ErrInvalid)
		}
	}
	return u, nil
}

func publicBase(raw string) (string, string, error) {
	base, host, err := teammates.BaseURL(raw)
	if err != nil {
		return "", "", fmt.Errorf("%w: %s", ErrInvalid, strings.TrimPrefix(err.Error(), teammates.ErrInvalid.Error()+": "))
	}
	if dnsproxy.IsVirtual(host) {
		return "", "", fmt.Errorf("%w: the base URL must name a public host", ErrInvalid)
	}
	return base, host, nil
}

// ParseSSHURL normalizes an SSH forge address: ssh://user@host[:port][/], user@host[:] or
// host, to ssh://user@host[:port] with the port only when it isn't 22.
func ParseSSHURL(raw string) (ForgeURL, error) {
	raw = strings.TrimSpace(raw)
	bad := func(why string) (ForgeURL, error) {
		return ForgeURL{}, fmt.Errorf("%w: %s", ErrInvalid, why)
	}
	if len(raw) > teammates.MaxBaseURLLen {
		return bad("the address is too long")
	}
	var user, hostport string
	if rest, ok := strings.CutPrefix(raw, "ssh://"); ok {
		rest = strings.TrimSuffix(rest, "/")
		if strings.ContainsAny(rest, "/?#") {
			return bad("the address must not contain a path; give the repository path in the remote URL")
		}
		user, hostport, ok = strings.Cut(rest, "@")
		if !ok {
			user, hostport = "git", rest
		}
	} else {
		if strings.Contains(raw, "://") {
			return bad("use ssh://user@host[:port] or user@host")
		}
		rest, path, _ := strings.Cut(raw, ":")
		if path != "" {
			return bad("the address must not contain a path; give the repository path in the remote URL")
		}
		var ok bool
		user, hostport, ok = strings.Cut(rest, "@")
		if !ok {
			user, hostport = "git", rest
		}
	}
	if !userName.MatchString(user) {
		return bad("the user may only contain letters, digits, dots, dashes and underscores")
	}
	if hostport == "" || strings.ContainsAny(hostport, "/?#@ ") {
		return bad("invalid host")
	}
	// The same host rules as an HTTPS base URL: a public name or address.
	base, host, err := publicBase("https://" + hostport)
	if err != nil {
		return ForgeURL{}, err
	}
	port := 22
	if u, err := url.Parse(base); err == nil && u.Port() != "" {
		p, err := strconv.Atoi(u.Port())
		if err != nil || p < 1 || p > 65535 {
			return bad("the port must be between 1 and 65535")
		}
		port = p
	}
	addr := host
	if strings.Contains(host, ":") {
		addr = "[" + host + "]"
	}
	if port != 22 {
		addr += ":" + strconv.Itoa(port)
	}
	return ForgeURL{Base: "ssh://" + user + "@" + addr, Host: host, Port: port, User: user}, nil
}

// upstreamDescription says how Studio reaches a repository of f once a push is approved.
func upstreamDescription(f store.Forge, path string) string {
	if f.Kind == KindLocal {
		return "to " + filepath.Join(f.BaseURL, filepath.FromSlash(path)) + " on this computer"
	}
	if f.Kind == KindSSH {
		d := "over SSH to " + f.BaseURL + "/" + path
		if f.HostKeyFingerprint != "" {
			d += ", host key " + f.HostKeyFingerprint
		}
		return d
	}
	return "over HTTPS to " + f.BaseURL + "/" + path + ".git"
}

// Plain is the adapter of a forge without an API (kind https): basic auth for git only.
type Plain struct{ User string }

func (p Plain) GitAuth(token string) (string, string) { return p.User, token }

func (Plain) Check(context.Context, string) (Account, error) { return Account{}, errNoAPI }

func (Plain) DefaultBranch(context.Context, string, string, string) (string, error) {
	return "", errNoAPI
}

func (Plain) OpenPullRequest(context.Context, string, string, string, PullRequest) (string, error) {
	return "", errNoAPI
}

func (Plain) OpenPullRequestOf(context.Context, string, string, string, string) (string, error) {
	return "", errNoAPI
}

// --- Forgejo -------------------------------------------------------------------------------

// Forgejo talks to a Forgejo (or Gitea) instance's API, /api/v1 under BaseURL.
type Forgejo struct {
	BaseURL string
	Client  *http.Client
}

func (f *Forgejo) GitAuth(token string) (string, string) { return "sandbox-studio", token }

func (f *Forgejo) Check(ctx context.Context, token string) (Account, error) {
	var user struct {
		Login string `json:"login"`
	}
	if err := f.call(ctx, token, http.MethodGet, "/user", nil, &user); err != nil {
		return Account{}, err
	}
	return Account{User: user.Login}, nil
}

func (f *Forgejo) DefaultBranch(ctx context.Context, token, owner, repo string) (string, error) {
	var r struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := f.call(ctx, token, http.MethodGet, "/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(repo), nil, &r); err != nil {
		return "", err
	}
	return r.DefaultBranch, nil
}

func (f *Forgejo) OpenPullRequest(ctx context.Context, token, owner, repo string, pr PullRequest) (string, error) {
	var out struct {
		HTMLURL string `json:"html_url"`
		Number  int    `json:"number"`
	}
	err := f.call(ctx, token, http.MethodPost, "/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(repo)+"/pulls", pr, &out)
	if err != nil {
		return "", err
	}
	if out.HTMLURL == "" {
		return fmt.Sprintf("#%d", out.Number), nil
	}
	return out.HTMLURL, nil
}

// OpenPullRequestOf looks through the 50 most recently updated open pull requests, since the
// API filters them by base branch but not by head.
func (f *Forgejo) OpenPullRequestOf(ctx context.Context, token, owner, repo, head string) (string, error) {
	var out []struct {
		HTMLURL string `json:"html_url"`
		Number  int    `json:"number"`
		Head    struct {
			Ref    string `json:"ref"`
			RepoID int64  `json:"repo_id"`
		} `json:"head"`
		Base struct {
			RepoID int64 `json:"repo_id"`
		} `json:"base"`
	}
	err := f.call(ctx, token, http.MethodGet, "/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(repo)+"/pulls?state=open&sort=recentupdate&limit=50", nil, &out)
	if err != nil {
		return "", err
	}
	for _, p := range out {
		if p.Head.Ref == head && p.Head.RepoID == p.Base.RepoID {
			return prLink(p.HTMLURL, "#", p.Number), nil
		}
	}
	return "", nil
}

// prLink is a pull request's web URL, or its number when the forge gave none.
func prLink(webURL, sign string, n int) string {
	if webURL == "" {
		return fmt.Sprintf("%s%d", sign, n)
	}
	return webURL
}

func (f *Forgejo) call(ctx context.Context, token, method, path string, in, out any) error {
	_, err := callJSON(ctx, f.Client, "token "+token, token, method, f.BaseURL+"/api/v1"+path, nil, in, out)
	return err
}

// callJSON sends one API request with the given Authorization and decodes the JSON
// answer into out. Errors carry the forge's message, never the token.
func callJSON(ctx context.Context, client *http.Client, auth, token, method, url string, headers map[string]string, in, out any) (http.Header, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New(mask(err.Error(), token))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusMovedPermanently || resp.StatusCode == http.StatusTemporaryRedirect || resp.StatusCode == http.StatusPermanentRedirect {
		return nil, fmt.Errorf("the repository moved to %s; update the remote", mask(resp.Header.Get("Location"), token))
	}
	if resp.StatusCode/100 != 2 {
		var e struct {
			Message string `json:"message"`
			Errors  []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		msg := strings.TrimSpace(string(data))
		if json.Unmarshal(data, &e) == nil && e.Message != "" {
			msg = e.Message
			if len(e.Errors) > 0 && e.Errors[0].Message != "" {
				msg += ": " + e.Errors[0].Message
			}
		}
		if len(msg) > 300 {
			msg = msg[:300] + "..."
		}
		return nil, fmt.Errorf("the forge answered %s: %s", resp.Status, mask(msg, token))
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return nil, fmt.Errorf("the forge's answer: %w", err)
		}
	}
	return resp.Header, nil
}

// --- GitHub ---------------------------------------------------------------------------------

// GitHubAPIVersion is the REST API version Studio asks for.
const GitHubAPIVersion = "2022-11-28"

// GitHub talks to the REST API of github.com (api.github.com) or a GitHub Enterprise
// Server (/api/v3 under BaseURL) with a fine-grained personal access token.
type GitHub struct {
	BaseURL string
	Client  *http.Client
}

func (g *GitHub) api() string {
	if g.BaseURL == "https://github.com" {
		return "https://api.github.com"
	}
	return g.BaseURL + "/api/v3"
}

func (g *GitHub) GitAuth(token string) (string, string) { return "x-access-token", token }

func (g *GitHub) Check(ctx context.Context, token string) (Account, error) {
	var user struct {
		Login string `json:"login"`
	}
	h, err := g.call(ctx, token, http.MethodGet, "/user", nil, &user)
	if err != nil {
		return Account{}, err
	}
	a := Account{User: user.Login}
	// "2026-10-31 12:00:00 UTC" or "... +0100"; absent for tokens without expiry.
	if v := h.Get("GitHub-Authentication-Token-Expiration"); v != "" {
		for _, layout := range []string{"2006-01-02 15:04:05 MST", "2006-01-02 15:04:05 -0700"} {
			if t, err := time.Parse(layout, v); err == nil {
				a.Expires = t
				break
			}
		}
	}
	return a, nil
}

func (g *GitHub) DefaultBranch(ctx context.Context, token, owner, repo string) (string, error) {
	var r struct {
		DefaultBranch string `json:"default_branch"`
	}
	if _, err := g.call(ctx, token, http.MethodGet, "/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(repo), nil, &r); err != nil {
		return "", err
	}
	return r.DefaultBranch, nil
}

func (g *GitHub) OpenPullRequest(ctx context.Context, token, owner, repo string, pr PullRequest) (string, error) {
	var out struct {
		HTMLURL string `json:"html_url"`
		Number  int    `json:"number"`
	}
	if _, err := g.call(ctx, token, http.MethodPost, "/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(repo)+"/pulls", pr, &out); err != nil {
		return "", err
	}
	if out.HTMLURL == "" {
		return fmt.Sprintf("#%d", out.Number), nil
	}
	return out.HTMLURL, nil
}

func (g *GitHub) OpenPullRequestOf(ctx context.Context, token, owner, repo, head string) (string, error) {
	var out []struct {
		HTMLURL string `json:"html_url"`
		Number  int    `json:"number"`
	}
	q := url.Values{"state": {"open"}, "head": {owner + ":" + head}}
	if _, err := g.call(ctx, token, http.MethodGet, "/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(repo)+"/pulls?"+q.Encode(), nil, &out); err != nil {
		return "", err
	}
	if len(out) == 0 {
		return "", nil
	}
	return prLink(out[0].HTMLURL, "#", out[0].Number), nil
}

func (g *GitHub) call(ctx context.Context, token, method, path string, in, out any) (http.Header, error) {
	return callJSON(ctx, g.Client, "Bearer "+token, token, method, g.api()+path,
		map[string]string{"Accept": "application/vnd.github+json", "X-GitHub-Api-Version": GitHubAPIVersion}, in, out)
}

// --- GitLab ---------------------------------------------------------------------------------

// GitLab talks to the REST API v4 of gitlab.com or a self-hosted GitLab, under BaseURL. Git
// over HTTPS takes oauth2 as the user with the token, which every kind of GitLab token accepts.
type GitLab struct {
	BaseURL string
	Client  *http.Client
}

func (g *GitLab) GitAuth(token string) (string, string) { return "oauth2", token }

func (g *GitLab) Check(ctx context.Context, token string) (Account, error) {
	var user struct {
		Username string `json:"username"`
	}
	if err := g.call(ctx, token, http.MethodGet, "/user", nil, &user); err != nil {
		return Account{}, err
	}
	return Account{User: user.Username}, nil
}

// gitlabProject is a project's path as GitLab's API takes it: the whole path, slashes escaped.
func gitlabProject(owner, repo string) string {
	if owner == "" {
		return url.PathEscape(repo)
	}
	return url.PathEscape(owner + "/" + repo)
}

func (g *GitLab) DefaultBranch(ctx context.Context, token, owner, repo string) (string, error) {
	var p struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := g.call(ctx, token, http.MethodGet, "/projects/"+gitlabProject(owner, repo), nil, &p); err != nil {
		return "", err
	}
	return p.DefaultBranch, nil
}

// OpenPullRequest opens a merge request, GitLab's pull request, from head into base.
func (g *GitLab) OpenPullRequest(ctx context.Context, token, owner, repo string, pr PullRequest) (string, error) {
	in := struct {
		Source      string `json:"source_branch"`
		Target      string `json:"target_branch"`
		Title       string `json:"title"`
		Description string `json:"description"`
	}{pr.Head, pr.Base, pr.Title, pr.Body}
	var out struct {
		WebURL string `json:"web_url"`
		IID    int    `json:"iid"`
	}
	if err := g.call(ctx, token, http.MethodPost, "/projects/"+gitlabProject(owner, repo)+"/merge_requests", in, &out); err != nil {
		return "", err
	}
	if out.WebURL == "" {
		return fmt.Sprintf("!%d", out.IID), nil
	}
	return out.WebURL, nil
}

func (g *GitLab) OpenPullRequestOf(ctx context.Context, token, owner, repo, head string) (string, error) {
	var out []struct {
		WebURL          string `json:"web_url"`
		IID             int    `json:"iid"`
		SourceProjectID int64  `json:"source_project_id"`
		TargetProjectID int64  `json:"target_project_id"`
	}
	q := url.Values{"state": {"opened"}, "source_branch": {head}}
	if err := g.call(ctx, token, http.MethodGet, "/projects/"+gitlabProject(owner, repo)+"/merge_requests?"+q.Encode(), nil, &out); err != nil {
		return "", err
	}
	for _, mr := range out {
		if mr.SourceProjectID == mr.TargetProjectID {
			return prLink(mr.WebURL, "!", mr.IID), nil
		}
	}
	return "", nil
}

func (g *GitLab) call(ctx context.Context, token, method, path string, in, out any) error {
	_, err := callJSON(ctx, g.Client, "Bearer "+token, token, method, g.BaseURL+"/api/v4"+path, nil, in, out)
	return err
}

// mask removes token from text shown to anyone.
func mask(text, token string) string {
	if token == "" {
		return text
	}
	return strings.ReplaceAll(text, token, "[token]")
}
