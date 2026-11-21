package gitreview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// KindResolve is the approval a teammate's ask to resolve review threads raises: Studio
// resolves them only once the user approves (docs/git-review.md#resolve-review-threads).
const KindResolve = "git.resolve"

// maxResolve bounds the threads one ask resolves.
const maxResolve = 20

// ThreadResolver resolves a pull request's review threads. GitHub and GitLab have one;
// Forgejo and Gitea have no API for it.
type ThreadResolver interface {
	// ResolveThread marks the thread, as ReadPullRequest names it, resolved.
	ResolveThread(ctx context.Context, token, owner, repo string, number int, thread string) error
}

// DraftThread is a review thread a teammate asks to resolve, as it was when it asked.
type DraftThread struct {
	ID      string `json:"id"`
	Path    string `json:"path"`
	Line    int    `json:"line,omitempty"`
	Side    string `json:"side" enum:"new,old"`
	Author  string `json:"author" doc:"Who started the thread"`
	Body    string `json:"body" doc:"The thread's first comment, cut at 2,000 bytes"`
	Replies int    `json:"replies,omitempty" doc:"How many replies follow it"`
	Done    bool   `json:"done,omitempty" doc:"Resolved, once approved"`
}

// ResolveDraft is the payload of a git.resolve approval: the pull request and its threads.
// Once decided, it also holds what became of them.
type ResolveDraft struct {
	Teammate  string        `json:"teammate,omitempty" doc:"The teammate owning the sandbox; empty for unowned sandboxes"`
	Sandbox   string        `json:"sandbox" doc:"The sandbox's name"`
	Forge     string        `json:"forge"`
	ForgeID   string        `json:"forgeId"`
	ForgeHost string        `json:"forgeHost" doc:"The forge's host"`
	Owner     string        `json:"owner"`
	Repo      string        `json:"repo"`
	Number    int           `json:"number"`
	Title     string        `json:"title" doc:"The pull request's title"`
	URL       string        `json:"url,omitempty" doc:"The pull request's web URL"`
	Account   string        `json:"account,omitempty" doc:"The forge account that resolves them: the user's token's"`
	Threads   []DraftThread `json:"threads"`
	// IdempotencyKey is the agent's key for the ask: asking again with it answers with this one.
	IdempotencyKey string `json:"idempotencyKey,omitempty" doc:"The agent's key for this ask, so that asking again doesn't ask twice"`
	State          string `json:"state,omitempty" enum:"resolved,discarded,failed" doc:"Empty until it is decided and, once approved, done"`
	Result         string `json:"result,omitempty" doc:"Why resolving failed, thread by thread"`
	Note           string `json:"note,omitempty" doc:"The user's note"`
}

// Resolve states.
const (
	ResolveDone      = "resolved"
	ResolveDiscarded = "discarded"
	ResolveFailed    = "failed"
)

// ResolveDetail returns the draft of a git.resolve approval.
func ResolveDetail(a store.Approval) (*ResolveDraft, error) {
	var d ResolveDraft
	if err := json.Unmarshal(a.Payload, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

func (s *Service) toolPullRequestResolve(ctx context.Context, sb store.Sandbox, params json.RawMessage) (any, error) {
	var in struct {
		pullRequestArgs
		Threads []string `json:"threads"`
		Key     string   `json:"idempotency_key"`
	}
	if err := decodeArgs(params, &in); err != nil {
		return nil, err
	}
	key := strings.TrimSpace(in.Key)
	if key != "" {
		// Asked again, as after a timeout: the ask made with the key answers.
		a, err := s.Store.ApprovalByKey(ctx, sb.EnvironmentID, sb.ID, KindResolve, key)
		if err == nil {
			d, err := ResolveDetail(a)
			if err != nil {
				return nil, err
			}
			return resolveView(a, *d), nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
	}
	var ids []string
	for _, id := range in.Threads {
		if id = strings.TrimSpace(id); id != "" && !contains(ids, id) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, integrations.ToolError("give the threads to resolve: git_pull_request_read names each line comment's thread")
	}
	if len(ids) > maxResolve {
		return nil, integrations.ToolError("ask for at most %d threads at a time", maxResolve)
	}
	forge, repository, err := s.pullRequestsOf(ctx, sb, in.Forge, in.Repository)
	if err != nil {
		return nil, err
	}
	in.Forge, in.Repository = forge, repository
	f, rv, err := s.reviewForge(ctx, sb.EnvironmentID, in.Forge)
	if err != nil {
		return nil, err
	}
	if _, ok := rv.(ThreadResolver); !ok {
		return nil, integrations.ToolError("%s has no API to resolve review threads with; reply on the pull request with git_pull_request_comment instead", f.Name)
	}
	owner, repo, err := in.split(f.Kind)
	if err != nil {
		return nil, err
	}
	token, err := s.token(ctx, f)
	if err != nil {
		return nil, fmt.Errorf("the forge's token: %w", err)
	}
	rctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	// The threads must be this pull request's: the user approves what the card shows.
	info, err := rv.ReadPullRequest(rctx, token, owner, repo, in.Number)
	if err != nil {
		return nil, integrations.ToolError("%s", err)
	}
	byID := map[string]*DraftThread{}
	resolved := map[string]bool{}
	for _, c := range info.LineComments {
		if c.Thread == "" {
			continue
		}
		if t, ok := byID[c.Thread]; ok {
			t.Replies++
			continue
		}
		byID[c.Thread] = &DraftThread{ID: c.Thread, Path: c.Path, Line: c.Line, Side: c.Side, Author: c.Author, Body: truncateUTF8(strings.TrimSpace(c.Body), 2000)}
		resolved[c.Thread] = c.Resolved
	}
	var asked []DraftThread
	var already, unknown []string
	for _, id := range ids {
		switch t, ok := byID[id]; {
		case !ok:
			unknown = append(unknown, id)
		case resolved[id]:
			already = append(already, id)
		default:
			asked = append(asked, *t)
		}
	}
	if len(unknown) > 0 {
		if info.threadErr != nil {
			return nil, integrations.ToolError("the review threads of #%d couldn't be read: %v", in.Number, info.threadErr)
		}
		return nil, integrations.ToolError("#%d has no review thread %s; git_pull_request_read names each line comment's thread", in.Number, strings.Join(unknown, ", "))
	}
	pr := fmt.Sprintf("%s/%s#%d", owner, repo, in.Number)
	if len(asked) == 0 {
		return map[string]any{"status": "resolved", "forge": f.Name, "pullRequest": pr, "alreadyResolved": already}, nil
	}
	draft := ResolveDraft{Forge: f.Name, ForgeID: f.ID, ForgeHost: forgeHost(f), Owner: owner, Repo: repo, Number: in.Number, Sandbox: sb.Name,
		Title: info.Title, URL: info.URL, Threads: asked, IdempotencyKey: key}
	if ad, ok := rv.(Adapter); ok {
		if acct, err := ad.Check(rctx, token); err == nil {
			draft.Account = acct.User
		}
	}
	if sb.TeammateID != "" {
		if tm, err := s.Store.Teammate(ctx, sb.EnvironmentID, sb.TeammateID); err == nil {
			draft.Teammate = tm.Name
		}
	}
	payload, err := json.Marshal(draft)
	if err != nil {
		return nil, err
	}
	// One card per set of threads: an ask for others of the same pull request is a card of its
	// own, and asking for the same ones again counts on the card that waits.
	ids = make([]string, 0, len(asked))
	for _, t := range asked {
		ids = append(ids, t.ID)
	}
	slices.Sort(ids)
	a, created, err := s.Store.RequestApproval(ctx, store.Approval{EnvironmentID: sb.EnvironmentID, SandboxID: sb.ID, Kind: KindResolve,
		Subject: fmt.Sprintf("%s/%s %s", f.Name, pr, strings.Join(ids, ",")), Payload: payload})
	if err != nil {
		return nil, err
	}
	if !created { // the card that waits says what an Allow resolves
		if d, err := ResolveDetail(a); err == nil {
			draft = *d
		}
	}
	s.publish(sb.EnvironmentID, a.ID)
	v := resolveView(a, draft)
	if len(already) > 0 {
		v["alreadyResolved"] = already
	}
	return v, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// resolveView is an ask to resolve threads as git_pull_request_resolve and approval_status show it.
func resolveView(a store.Approval, d ResolveDraft) map[string]any {
	v := map[string]any{"forge": d.Forge, "pullRequest": fmt.Sprintf("%s/%s#%d", d.Owner, d.Repo, d.Number)}
	var asked, done []string
	for _, t := range d.Threads {
		asked = append(asked, t.ID)
		if t.Done {
			done = append(done, t.ID)
		}
	}
	v["threads"] = asked
	switch {
	case d.State == ResolveDone:
		v["status"] = "resolved"
	case d.State == ResolveFailed:
		v["status"], v["reason"] = "failed", d.Result
		if len(done) > 0 {
			v["resolved"] = done
		}
	case d.State == ResolveDiscarded:
		v["status"] = "discarded"
		if a.Status == store.StatusDismissed {
			v["status"] = "dismissed"
		}
		if d.Note != "" {
			v["note"] = d.Note
		}
	case a.Status == store.StatusPending:
		v["status"], v["approval"] = "awaiting approval", a.ID
	default:
		v["status"] = "resolving"
	}
	return v
}

// resolveOutcome is what became of an ask: resolved, discarded, or failed.
func (s *Service) resolveOutcome(ctx context.Context, a store.Approval) (map[string]any, bool, error) {
	d, err := ResolveDetail(a)
	if err != nil {
		return nil, false, err
	}
	if d.State == "" {
		return nil, false, nil
	}
	return resolveView(a, *d), true, nil
}

// decideResolve settles a git.resolve approval. Approving resolves the threads on the forge;
// denying leaves them, with the note.
func (s *Service) decideResolve(ctx context.Context, a store.Approval, d integrations.Decision) error {
	s.httpClient()
	st, err := status(d.Action)
	if err != nil {
		return err
	}
	draft, err := ResolveDetail(a)
	if err != nil {
		return err
	}
	if err := s.Store.DecideApproval(ctx, a.EnvironmentID, a.ID, st, ""); err != nil {
		return err
	}
	defer s.publish(a.EnvironmentID, a.ID)
	// Decided, the ask settles even if the user's request goes away.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()
	draft.Note = strings.TrimSpace(d.Note)
	if st != store.StatusApproved {
		draft.State = ResolveDiscarded
	} else if err := s.resolveThreads(ctx, a.EnvironmentID, draft); err != nil {
		s.Log.Info("git remote: approved resolve failed", "approval", a.ID, "err", err)
		draft.State, draft.Result = ResolveFailed, err.Error()
	} else {
		draft.State = ResolveDone
	}
	payload, err := json.Marshal(draft)
	if err != nil {
		return err
	}
	sctx, scancel := settleCtx(ctx)
	defer scancel()
	return s.Store.SetApprovalPayload(sctx, a.EnvironmentID, a.ID, payload)
}

// resolveThreads resolves the draft's threads with the user's token, marking each one done.
func (s *Service) resolveThreads(ctx context.Context, envID string, d *ResolveDraft) error {
	f, err := s.Store.Forge(ctx, envID, d.ForgeID)
	if err != nil {
		return errors.New("the forge was removed from Sandbox Studio")
	}
	f, ok := apiForge(f)
	if !ok {
		return errors.New(noAPI(f))
	}
	ad, err := adapter(f, s.httpClient())
	if err != nil {
		return err
	}
	r, ok := ad.(ThreadResolver)
	if !ok {
		return fmt.Errorf("%s has no API to resolve review threads with", f.Name)
	}
	token, err := s.token(ctx, f)
	if err != nil {
		return fmt.Errorf("the forge's token: %w", err)
	}
	var failed []string
	for i := range d.Threads {
		t := &d.Threads[i]
		if t.Done {
			continue
		}
		if err := r.ResolveThread(ctx, token, d.Owner, d.Repo, d.Number, t.ID); err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", threadPlace(*t), err))
			continue
		}
		t.Done = true
	}
	if len(failed) > 0 {
		return errors.New(strings.Join(failed, "; "))
	}
	return nil
}

// threadPlace is where a thread is, as path:line.
func threadPlace(t DraftThread) string {
	if t.Line > 0 {
		return fmt.Sprintf("%s:%d", t.Path, t.Line)
	}
	return t.Path
}

// threadState is the thread a line comment is in.
type threadState struct {
	id       string
	resolved bool
}

// setThreads gives each line comment its thread, by the comment's ID.
func setThreads(comments []PRLineComment, byComment map[int64]threadState) {
	for i := range comments {
		if t, ok := byComment[comments[i].id]; ok && comments[i].id != 0 {
			comments[i].Thread, comments[i].Resolved = t.id, t.resolved
		}
	}
}

// --- GitHub: review threads are GraphQL's only -------------------------------------------

func (g *GitHub) graphqlURL() string {
	if g.BaseURL == "https://github.com" {
		return "https://api.github.com/graphql"
	}
	return g.BaseURL + "/api/graphql"
}

// graphql runs a GraphQL query or mutation. GitHub answers its errors with 200 and an
// errors list, which become the error.
func (g *GitHub) graphql(ctx context.Context, token, query string, vars map[string]any, out any) error {
	var answer struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if _, err := callJSON(ctx, g.Client, "Bearer "+token, token, http.MethodPost, g.graphqlURL(), nil,
		map[string]any{"query": query, "variables": vars}, &answer); err != nil {
		return err
	}
	if len(answer.Errors) > 0 {
		var msgs []string
		for _, e := range answer.Errors {
			msgs = append(msgs, e.Message)
		}
		return fmt.Errorf("GitHub answered: %s", mask(truncate(strings.Join(msgs, "; "), 300), token))
	}
	if out == nil || len(answer.Data) == 0 {
		return nil
	}
	return json.Unmarshal(answer.Data, out)
}

const githubThreads = `query($owner: String!, $repo: String!, $number: Int!) {
  repository(owner: $owner, name: $repo) {
    pullRequest(number: $number) {
      reviewThreads(first: 100) {
        nodes { id isResolved comments(first: 50) { nodes { databaseId } } }
      }
    }
  }
}`

// threads gives each line comment its review thread.
func (g *GitHub) threads(ctx context.Context, token, owner, repo string, number int, comments []PRLineComment) error {
	var out struct {
		Repository *struct {
			PullRequest *struct {
				ReviewThreads struct {
					Nodes []struct {
						ID         string `json:"id"`
						IsResolved bool   `json:"isResolved"`
						Comments   struct {
							Nodes []struct {
								DatabaseID int64 `json:"databaseId"`
							} `json:"nodes"`
						} `json:"comments"`
					} `json:"nodes"`
				} `json:"reviewThreads"`
			} `json:"pullRequest"`
		} `json:"repository"`
	}
	if err := g.graphql(ctx, token, githubThreads, map[string]any{"owner": owner, "repo": repo, "number": number}, &out); err != nil {
		return err
	}
	if out.Repository == nil || out.Repository.PullRequest == nil {
		return errors.New("GitHub didn't find the pull request")
	}
	by := map[int64]threadState{}
	for _, t := range out.Repository.PullRequest.ReviewThreads.Nodes {
		for _, c := range t.Comments.Nodes {
			by[c.DatabaseID] = threadState{t.ID, t.IsResolved}
		}
	}
	setThreads(comments, by)
	return nil
}

const githubResolve = `mutation($id: ID!) { resolveReviewThread(input: {threadId: $id}) { thread { isResolved } } }`

func (g *GitHub) ResolveThread(ctx context.Context, token, owner, repo string, number int, thread string) error {
	var out struct {
		ResolveReviewThread struct {
			Thread struct {
				IsResolved bool `json:"isResolved"`
			} `json:"thread"`
		} `json:"resolveReviewThread"`
	}
	if err := g.graphql(ctx, token, githubResolve, map[string]any{"id": thread}, &out); err != nil {
		return err
	}
	if !out.ResolveReviewThread.Thread.IsResolved {
		return errors.New("GitHub left it unresolved")
	}
	return nil
}

// --- GitLab: a thread is a resolvable discussion --------------------------------------------

// threads gives each line comment its discussion.
func (g *GitLab) threads(ctx context.Context, token, mr string, comments []PRLineComment) error {
	var ds []struct {
		ID    string `json:"id"`
		Notes []struct {
			ID         int64 `json:"id"`
			Resolvable bool  `json:"resolvable"`
			Resolved   bool  `json:"resolved"`
		} `json:"notes"`
	}
	if _, err := g.get(ctx, token, mr+"/discussions?per_page=100", &ds, gitlabMaxAnswer); err != nil {
		return err
	}
	by := map[int64]threadState{}
	for _, d := range ds {
		resolvable, resolved := false, true
		for _, n := range d.Notes {
			if n.Resolvable {
				resolvable, resolved = true, resolved && n.Resolved
			}
		}
		if !resolvable {
			continue
		}
		for _, n := range d.Notes {
			by[n.ID] = threadState{d.ID, resolved}
		}
	}
	setThreads(comments, by)
	return nil
}

func (g *GitLab) ResolveThread(ctx context.Context, token, owner, repo string, number int, thread string) error {
	return g.call(ctx, token, http.MethodPut, g.mr(owner, repo, number)+"/discussions/"+url.PathEscape(thread), map[string]any{"resolved": true}, nil)
}
