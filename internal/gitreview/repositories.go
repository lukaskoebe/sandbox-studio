package gitreview

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// MaxRepositories bounds a forge's repository list: enough to pick from, few enough for one
// answer.
const MaxRepositories = 300

// Repo is a repository a forge lists, for cloning into a sandbox.
type Repo struct {
	Path        string `json:"path" doc:"The repository's path on the forge, such as owner/repo; the remote URL is https://git.studio.internal/<forge>/<path>.git"`
	Description string `json:"description,omitempty"`
	Private     bool   `json:"private,omitempty"`
}

// Repositories lists the repositories of f that Studio's credential reaches: through the API
// of a forgejo, github or gitlab forge, or the repositories in a local forge's folder. Forges
// of kind https and ssh have no list.
func (s *Service) Repositories(ctx context.Context, f store.Forge) ([]Repo, error) {
	if f.Kind == KindLocal {
		return localRepositories(f.BaseURL)
	}
	if !hasAPI(f.Kind) {
		return nil, fmt.Errorf("%w: a forge of kind %s has no API to list its repositories", ErrInvalid, f.Kind)
	}
	ad, err := adapter(f, s.httpClient())
	if err != nil {
		return nil, err
	}
	token, err := s.token(ctx, f)
	if err != nil {
		return nil, fmt.Errorf("the forge's token: %w", err)
	}
	return ad.ListRepositories(ctx, token)
}

// CheckRepoPath checks a repository path on a forge of kind, as the virtual remote takes it,
// and returns it normalized along with its last segment, the repository's name.
func CheckRepoPath(kind, path string) (string, string, error) {
	owner, repo, ok := splitPath(strings.Trim(path, "/"))
	if !ok || badOwner(kind, owner) {
		return "", "", fmt.Errorf("%w: %q is not a repository path on this forge", ErrInvalid, path)
	}
	return target{owner: owner, repo: repo}.path(), repo, nil
}

// collect pages through a list until a page comes back short or MaxRepositories are in.
// fetch returns the repositories of one page, counted from 1, and how many entries it had.
// Paths the virtual remote can't serve are left out.
func collect(kind string, size int, fetch func(page int) ([]Repo, int, error)) ([]Repo, error) {
	out := []Repo{}
	for page := 1; len(out) < MaxRepositories; page++ {
		repos, n, err := fetch(page)
		if err != nil {
			return nil, err
		}
		for _, r := range repos {
			if p, _, err := CheckRepoPath(kind, r.Path); err == nil && len(out) < MaxRepositories {
				r.Path = p
				out = append(out, r)
			}
		}
		if n < size {
			break
		}
	}
	return out, nil
}

func (Plain) ListRepositories(context.Context, string) ([]Repo, error) { return nil, errNoAPI }

// ListRepositories pages through /user/repos, the repositories the token's user owns or is a
// member of.
func (f *Forgejo) ListRepositories(ctx context.Context, token string) ([]Repo, error) {
	const size = 50 // Forgejo's largest page by default
	return collect(KindForgejo, size, func(page int) ([]Repo, int, error) {
		var list []struct {
			FullName    string `json:"full_name"`
			Description string `json:"description"`
			Private     bool   `json:"private"`
		}
		q := url.Values{"page": {strconv.Itoa(page)}, "limit": {strconv.Itoa(size)}}
		if err := f.call(ctx, token, http.MethodGet, "/user/repos?"+q.Encode(), nil, &list); err != nil {
			return nil, 0, err
		}
		var out []Repo
		for _, r := range list {
			out = append(out, Repo{Path: r.FullName, Description: r.Description, Private: r.Private})
		}
		return out, len(list), nil
	})
}

// ListRepositories pages through /user/repos, most recently pushed first.
func (g *GitHub) ListRepositories(ctx context.Context, token string) ([]Repo, error) {
	const size = 100
	return collect(KindGitHub, size, func(page int) ([]Repo, int, error) {
		var list []struct {
			FullName    string `json:"full_name"`
			Description string `json:"description"`
			Private     bool   `json:"private"`
		}
		q := url.Values{"per_page": {strconv.Itoa(size)}, "sort": {"pushed"}, "page": {strconv.Itoa(page)}}
		if _, err := g.call(ctx, token, http.MethodGet, "/user/repos?"+q.Encode(), nil, &list); err != nil {
			return nil, 0, err
		}
		var out []Repo
		for _, r := range list {
			out = append(out, Repo{Path: r.FullName, Description: r.Description, Private: r.Private})
		}
		return out, len(list), nil
	})
}

// ListRepositories pages through the projects the token's user is a member of, most recently
// active first.
func (g *GitLab) ListRepositories(ctx context.Context, token string) ([]Repo, error) {
	const size = 100
	return collect(KindGitLab, size, func(page int) ([]Repo, int, error) {
		var list []struct {
			Path        string `json:"path_with_namespace"`
			Description string `json:"description"`
			Visibility  string `json:"visibility"`
		}
		q := url.Values{"membership": {"true"}, "simple": {"true"}, "order_by": {"last_activity_at"},
			"per_page": {strconv.Itoa(size)}, "page": {strconv.Itoa(page)}}
		if err := g.call(ctx, token, http.MethodGet, "/projects?"+q.Encode(), nil, &list); err != nil {
			return nil, 0, err
		}
		var out []Repo
		for _, r := range list {
			out = append(out, Repo{Path: r.Path, Description: r.Description, Private: r.Visibility == "private"})
		}
		return out, len(list), nil
	})
}

// localRepositories lists the repositories in a local forge's folder by name: its folders
// that are repositories, and those one level further down, as owner/repo. A bare repository
// <name>.git is listed as <name>, which the virtual remote finds again. Hidden folders are
// left out.
func localRepositories(dir string) ([]Repo, error) {
	if _, err := CheckLocalPath(dir); err != nil {
		return nil, err
	}
	out := []Repo{}
	var walk func(rel string, depth int) error
	walk = func(rel string, depth int) error {
		entries, err := os.ReadDir(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return err
		}
		for _, e := range entries {
			if len(out) >= MaxRepositories {
				return nil
			}
			name := e.Name()
			p := filepath.Join(dir, filepath.FromSlash(rel), name)
			if strings.HasPrefix(name, ".") || !isDir(p) {
				continue
			}
			path := strings.TrimPrefix(rel+"/"+name, "/")
			if isFile(filepath.Join(p, ".git")) || isFile(filepath.Join(p, "HEAD")) && isDir(filepath.Join(p, "objects")) {
				if clean, _, err := CheckRepoPath(KindLocal, strings.TrimSuffix(path, ".git")); err == nil {
					out = append(out, Repo{Path: clean})
				}
				continue
			}
			if depth == 0 {
				// A folder Studio can't read is not the folder the user picked; it is skipped.
				_ = walk(path, depth+1)
			}
		}
		return nil
	}
	if err := walk("", 0); err != nil {
		return nil, fmt.Errorf("%w: Studio cannot read %s: %v", ErrInvalid, dir, err)
	}
	return out, nil
}
