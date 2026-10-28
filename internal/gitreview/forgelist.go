package gitreview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Bounds of a pull request list.
const (
	defaultPullRequests = 30
	maxPullRequests     = 100
)

// PRSummary is a pull request as git_pull_request_list answers it.
type PRSummary struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	Author    string    `json:"author"`
	State     string    `json:"state" doc:"open or closed"`
	Draft     bool      `json:"draft,omitempty"`
	Merged    bool      `json:"merged,omitempty"`
	Base      string    `json:"base" doc:"The branch it would merge into"`
	Head      string    `json:"head" doc:"Its branch"`
	Reviewers []string  `json:"requestedReviewers,omitempty" doc:"The accounts asked to review it"`
	Labels    []string  `json:"labels,omitempty"`
	Updated   time.Time `json:"updated"`
	URL       string    `json:"url,omitempty"`
}

// listPullRequests lists a repository's pull requests in state (open, closed or all), most
// recently updated first. Forgejo and GitHub share the shape but not the sort's name or the
// page parameter.
func listPullRequests(ctx context.Context, call restAPI, github bool, token, owner, repo, state string, limit int) ([]PRSummary, error) {
	q := url.Values{"state": {state}}
	if github {
		q.Set("sort", "updated")
		q.Set("direction", "desc")
		q.Set("per_page", strconv.Itoa(limit))
	} else {
		q.Set("sort", "recentupdate")
		q.Set("limit", strconv.Itoa(limit))
	}
	type login struct {
		Login string `json:"login"`
	}
	var list []struct {
		Number    int        `json:"number"`
		Title     string     `json:"title"`
		State     string     `json:"state"`
		Draft     bool       `json:"draft"`
		Merged    bool       `json:"merged"`
		MergedAt  *time.Time `json:"merged_at"`
		HTMLURL   string     `json:"html_url"`
		User      login      `json:"user"`
		UpdatedAt time.Time  `json:"updated_at"`
		Base      struct {
			Ref string `json:"ref"`
		} `json:"base"`
		Head struct {
			Ref string `json:"ref"`
		} `json:"head"`
		Reviewers []login `json:"requested_reviewers"`
		Labels    []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	path := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/pulls?" + q.Encode()
	if err := call(ctx, token, http.MethodGet, path, nil, &list); err != nil {
		return nil, err
	}
	out := []PRSummary{}
	for _, pr := range list[:min(len(list), limit)] {
		s := PRSummary{Number: pr.Number, Title: pr.Title, Author: pr.User.Login, State: pr.State, Draft: pr.Draft,
			Merged: pr.Merged || pr.MergedAt != nil, Base: pr.Base.Ref, Head: pr.Head.Ref, Updated: pr.UpdatedAt, URL: pr.HTMLURL}
		for _, r := range pr.Reviewers {
			s.Reviewers = append(s.Reviewers, r.Login)
		}
		for _, l := range pr.Labels {
			s.Labels = append(s.Labels, l.Name)
		}
		out = append(out, s)
	}
	return out, nil
}

func (f *Forgejo) ListPullRequests(ctx context.Context, token, owner, repo, state string, limit int) ([]PRSummary, error) {
	return listPullRequests(ctx, f.call, false, token, owner, repo, state, limit)
}

func (g *GitHub) ListPullRequests(ctx context.Context, token, owner, repo, state string, limit int) ([]PRSummary, error) {
	return listPullRequests(ctx, g.rest, true, token, owner, repo, state, limit)
}

func (s *Service) toolPullRequestList(ctx context.Context, sb store.Sandbox, params json.RawMessage) (any, error) {
	var in struct {
		Forge      string `json:"forge"`
		Repository string `json:"repository"`
		State      string `json:"state"`
		Limit      int    `json:"limit"`
	}
	if err := decodeArgs(params, &in); err != nil {
		return nil, err
	}
	switch in.State {
	case "":
		in.State = "open"
	case "open", "closed", "all":
	default:
		return nil, integrations.ToolError("state is open, closed or all, not %q", in.State)
	}
	if in.Limit <= 0 || in.Limit > maxPullRequests {
		in.Limit = defaultPullRequests
	}
	f, rv, err := s.reviewForge(ctx, sb.EnvironmentID, in.Forge)
	if err != nil {
		return nil, err
	}
	owner, repo, err := splitRepository(f.Kind, in.Repository)
	if err != nil {
		return nil, err
	}
	token, err := s.token(ctx, f)
	if err != nil {
		return nil, fmt.Errorf("the forge's token: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	list, err := rv.ListPullRequests(ctx, token, owner, repo, in.State, in.Limit)
	if err != nil {
		return nil, integrations.ToolError("%s", err)
	}
	return map[string]any{"forge": f.Name, "repository": owner + "/" + repo, "pullRequests": list}, nil
}

// forgeView is a forge as git_repositories shows it.
type forgeView struct {
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	Remote       string `json:"remote" doc:"The virtual remote's URL prefix: clone <remote><path>.git"`
	Reviews      bool   `json:"pullRequests" doc:"Whether the pull request tools work on this forge"`
	Repositories []Repo `json:"repositories,omitempty"`
	More         bool   `json:"more,omitempty" doc:"The forge lists more repositories than shown"`
	Note         string `json:"note,omitempty" doc:"Why the forge lists no repositories"`
}

func (s *Service) toolRepositories(ctx context.Context, sb store.Sandbox, params json.RawMessage) (any, error) {
	var in struct {
		Forge string `json:"forge"`
		Query string `json:"query"`
	}
	if err := decodeArgs(params, &in); err != nil {
		return nil, err
	}
	var forges []store.Forge
	if in.Forge != "" {
		f, err := s.Store.ForgeByName(ctx, sb.EnvironmentID, in.Forge)
		if errors.Is(err, store.ErrNotFound) {
			return nil, integrations.ToolError("this environment has no forge %q", in.Forge)
		}
		if err != nil {
			return nil, err
		}
		forges = []store.Forge{f}
	} else {
		var err error
		if forges, err = s.Store.Forges(ctx, sb.EnvironmentID); err != nil {
			return nil, err
		}
	}
	query := strings.ToLower(strings.TrimSpace(in.Query))
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	out := []forgeView{}
	for _, f := range forges {
		v := forgeView{Name: f.Name, Kind: f.Kind, Remote: "https://" + Host + "/" + f.Name + "/",
			Reviews: f.Kind == KindForgejo || f.Kind == KindGitHub}
		if f.Kind != KindLocal && !hasAPI(f.Kind) {
			v.Note = "a forge of kind " + f.Kind + " has no list; clone a repository by its path"
			out = append(out, v)
			continue
		}
		repos, err := s.Repositories(ctx, f)
		if err != nil {
			v.Note = "the forge didn't list its repositories: " + strings.TrimPrefix(err.Error(), ErrInvalid.Error()+": ")
			out = append(out, v)
			continue
		}
		for _, r := range repos {
			if query == "" || strings.Contains(strings.ToLower(r.Path+" "+r.Description), query) {
				v.Repositories = append(v.Repositories, r)
			}
		}
		v.More = len(repos) >= MaxRepositories
		out = append(out, v)
	}
	return map[string]any{"forges": out}, nil
}
