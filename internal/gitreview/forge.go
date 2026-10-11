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
	"regexp"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/dnsproxy"
	"github.com/lukaskoebe/sandbox-studio/internal/personas"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Forge kinds.
const (
	KindForgejo = "forgejo"
	KindGitHub  = "github"
)

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
}

// PullRequest is what a git.pr approval proposes.
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

// adapter returns the adapter for a forge, talking through client.
func adapter(f store.Forge, client *http.Client) (Adapter, error) {
	switch f.Kind {
	case KindForgejo:
		return &Forgejo{BaseURL: f.BaseURL, Client: client}, nil
	case KindGitHub:
		return &GitHub{BaseURL: f.BaseURL, Client: client}, nil
	}
	return nil, fmt.Errorf("%w: unknown kind %q", ErrInvalid, f.Kind)
}

var forgeName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// CheckForge validates the name, kind and base URL of a new forge and returns the
// normalized base URL and its host.
func CheckForge(name, kind, baseURL string) (string, string, error) {
	if !forgeName.MatchString(name) {
		return "", "", fmt.Errorf("%w: the name must be 1 to 32 lower-case letters, digits and dashes, starting with a letter or digit", ErrInvalid)
	}
	switch kind {
	case KindForgejo, KindGitHub:
	default:
		return "", "", fmt.Errorf("%w: unknown kind %q", ErrInvalid, kind)
	}
	base, host, err := personas.BaseURL(baseURL)
	if err != nil {
		return "", "", fmt.Errorf("%w: %s", ErrInvalid, strings.TrimPrefix(err.Error(), personas.ErrInvalid.Error()+": "))
	}
	if dnsproxy.IsVirtual(host) {
		return "", "", fmt.Errorf("%w: the base URL must name a public host", ErrInvalid)
	}
	return base, host, nil
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

func (g *GitHub) call(ctx context.Context, token, method, path string, in, out any) (http.Header, error) {
	return callJSON(ctx, g.Client, "Bearer "+token, token, method, g.api()+path,
		map[string]string{"Accept": "application/vnd.github+json", "X-GitHub-Api-Version": GitHubAPIVersion}, in, out)
}

// mask removes token from text shown to anyone.
func mask(text, token string) string {
	if token == "" {
		return text
	}
	return strings.ReplaceAll(text, token, "[token]")
}
