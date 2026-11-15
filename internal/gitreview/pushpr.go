package gitreview

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// pushPRFresh is how long a pushed branch's pull request, as read from its forge, is answered
// again before the forge is asked anew: the Changes rail asks every few seconds per row.
const pushPRFresh = time.Minute

// PushPR is what became of the pull request of a push Studio made, as the Changes rail shows it.
type PushPR struct {
	// Found is whether the push has a pull request Studio could read. Without one, Note says why.
	Found    bool   `json:"found"`
	Number   int    `json:"number,omitempty"`
	URL      string `json:"url,omitempty"`
	State    string `json:"state,omitempty" enum:"open,merged,closed" doc:"Its state on the forge"`
	Checks   string `json:"checks,omitempty" enum:"none,pending,success,failure" doc:"The sum of its head commit's CI checks; empty when they couldn't be read"`
	Comments int    `json:"comments" doc:"Its conversation comments, line comments and reviews with words"`
	Note     string `json:"note,omitempty"`
}

type pushPRCache struct {
	mu sync.Mutex
	m  map[string]pushPRCached
}

type pushPRCached struct {
	at time.Time
	pr PushPR
}

var prNumber = regexp.MustCompile(`/(\d+)/?$`)

// PushPullRequest reads the pull request of the push a git.push or git.pr approval is about,
// from the pull request Studio opened or else the open one from the pushed branch. Answers are
// kept for a minute per approval.
func (s *Service) PushPullRequest(ctx context.Context, envID, approvalID string) (PushPR, error) {
	key := envID + "/" + approvalID
	s.pushPRs.mu.Lock()
	if c, ok := s.pushPRs.m[key]; ok && time.Since(c.at) < pushPRFresh {
		s.pushPRs.mu.Unlock()
		return c.pr, nil
	}
	s.pushPRs.mu.Unlock()

	pr, err := s.pushPullRequest(ctx, envID, approvalID)
	if err != nil {
		return pr, err
	}
	s.pushPRs.mu.Lock()
	if s.pushPRs.m == nil || len(s.pushPRs.m) > 500 {
		s.pushPRs.m = map[string]pushPRCached{}
	}
	s.pushPRs.m[key] = pushPRCached{at: time.Now(), pr: pr}
	s.pushPRs.mu.Unlock()
	return pr, nil
}

func (s *Service) pushPullRequest(ctx context.Context, envID, approvalID string) (PushPR, error) {
	p, err := s.Store.GitPushByApproval(ctx, envID, approvalID)
	if err != nil {
		return PushPR{}, err
	}
	if p.State != store.PushPushed {
		return PushPR{Note: "It wasn't pushed."}, nil
	}
	branch := strings.TrimPrefix(p.Ref, "refs/heads/")
	f, err := s.Store.Forge(ctx, envID, p.ForgeID)
	if errors.Is(err, store.ErrNotFound) {
		return PushPR{Note: "Its forge is gone."}, nil
	}
	if err != nil {
		return PushPR{}, err
	}
	api, ok := apiForge(f)
	if !ok {
		return PushPR{Note: noAPI(f)}, nil
	}
	ad, err := adapter(api, s.httpClient())
	if err != nil {
		return PushPR{Note: err.Error()}, nil
	}
	rv, ok := ad.(Reviewer)
	if !ok {
		return PushPR{Note: fmt.Sprintf("Studio can't read %s's pull requests.", f.Name)}, nil
	}
	token, err := s.token(ctx, api)
	if err != nil {
		return PushPR{}, fmt.Errorf("the forge's token: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	number := 0
	if m := prNumber.FindStringSubmatch(p.PRURL); m != nil {
		number, _ = strconv.Atoi(m[1])
	}
	if number == 0 {
		if branch == "" || branch == p.DefaultBranch {
			return PushPR{Note: "It went straight to " + branch + "."}, nil
		}
		prs, err := rv.ListPullRequests(ctx, token, p.Owner, p.Repo, "all", maxPullRequests)
		if err != nil {
			return PushPR{Note: mask(err.Error(), token)}, nil
		}
		for _, pr := range prs {
			if pr.Head == branch {
				number = pr.Number
				break
			}
		}
		if number == 0 {
			return PushPR{Note: "No pull request is from " + branch + "."}, nil
		}
	}
	info, err := rv.ReadPullRequest(ctx, token, p.Owner, p.Repo, number)
	if err != nil {
		return PushPR{Note: mask(err.Error(), token)}, nil
	}
	out := PushPR{Found: true, Number: number, URL: info.URL, State: info.State, Comments: len(info.Comments) + len(info.LineComments)}
	if out.URL == "" {
		out.URL = p.PRURL
	}
	if info.Merged {
		out.State = "merged"
	}
	for _, r := range info.Reviews {
		if strings.TrimSpace(r.Body) != "" {
			out.Comments++
		}
	}
	if cr, ok := rv.(CheckReader); ok {
		if checks, err := cr.PullRequestChecks(ctx, token, p.Owner, p.Repo, number); err == nil {
			out.Checks = checks.State
		}
	}
	return out, nil
}
