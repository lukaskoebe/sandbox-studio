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

	"github.com/lukaskoebe/sandbox-studio/internal/dnsproxy"
	"github.com/lukaskoebe/sandbox-studio/internal/personas"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Forge kinds.
const (
	KindForgejo = "forgejo"
	KindGitHub  = "github" // shares the interface; not implemented yet
)

// ErrInvalid marks a forge the user described wrongly.
var ErrInvalid = errors.New("invalid forge")

// ErrNotImplemented is returned for forge kinds Studio knows but can't drive yet.
var ErrNotImplemented = errors.New("not implemented yet")

// Adapter is what Studio needs from a forge beyond plain git over HTTPS.
type Adapter interface {
	// GitAuth is the basic-auth pair git requests to the forge carry.
	GitAuth(token string) (user, password string)
	// Check verifies that the token works and returns the user it belongs to.
	Check(ctx context.Context, token string) (string, error)
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

// adapter returns the adapter for a forge, talking through client.
func adapter(f store.Forge, client *http.Client) (Adapter, error) {
	switch f.Kind {
	case KindForgejo:
		return &Forgejo{BaseURL: f.BaseURL, Client: client}, nil
	case KindGitHub:
		return nil, fmt.Errorf("github forges are %w", ErrNotImplemented)
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
	case KindForgejo:
	case KindGitHub:
		return "", "", fmt.Errorf("github forges are %w", ErrNotImplemented)
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

func (f *Forgejo) Check(ctx context.Context, token string) (string, error) {
	var user struct {
		Login string `json:"login"`
	}
	if err := f.call(ctx, token, http.MethodGet, "/user", nil, &user); err != nil {
		return "", err
	}
	return user.Login, nil
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
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, f.BaseURL+"/api/v1"+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := f.Client.Do(req)
	if err != nil {
		return errors.New(mask(err.Error(), token))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		var e struct {
			Message string `json:"message"`
		}
		msg := strings.TrimSpace(string(data))
		if json.Unmarshal(data, &e) == nil && e.Message != "" {
			msg = e.Message
		}
		if len(msg) > 300 {
			msg = msg[:300] + "..."
		}
		return fmt.Errorf("the forge answered %s: %s", resp.Status, mask(msg, token))
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("the forge's answer: %w", err)
		}
	}
	return nil
}

// mask removes token from text shown to anyone.
func mask(text, token string) string {
	if token == "" {
		return text
	}
	return strings.ReplaceAll(text, token, "[token]")
}
