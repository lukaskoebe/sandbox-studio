package gitreview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// The CI checks of a pull request's head commit (docs/git-review.md#ci-checks): GitHub's check
// runs and commit statuses, GitLab's pipeline jobs, and Forgejo's and Gitea's commit statuses,
// which their Actions report too.

// Check states, the same on every forge.
const (
	CheckPending   = "pending"
	CheckSuccess   = "success"
	CheckFailure   = "failure"
	CheckCancelled = "cancelled"
	CheckSkipped   = "skipped"
	CheckNeutral   = "neutral"
)

// The tail of a failed check's log that a read keeps.
const (
	checkLogLines = 40
	checkLogBytes = 6000
	// checkLogRead caps what is read of a log to find its end.
	checkLogRead = 32 << 20
	// checkLogsPerRead is how many failed checks' logs one read fetches.
	checkLogsPerRead = 3
)

// PRCheck is one CI check of a pull request's head commit.
type PRCheck struct {
	Name     string     `json:"name"`
	State    string     `json:"state" enum:"pending,success,failure,cancelled,skipped,neutral"`
	Summary  string     `json:"summary,omitempty" doc:"What the check said about itself"`
	URL      string     `json:"url,omitempty"`
	Started  *time.Time `json:"started,omitempty"`
	Finished *time.Time `json:"finished,omitempty"`
	Log      string     `json:"log,omitempty" doc:"The end of a failed check's log, as the CI wrote it"`

	// logID names the check's log on its forge: a GitHub Actions job or a GitLab job. Zero
	// when the forge has none to give, as for a commit status.
	logID int64
}

// PRChecks are the CI checks of a pull request's head commit, and their sum.
type PRChecks struct {
	Forge      string    `json:"forge"`
	Repository string    `json:"repository"`
	Number     int       `json:"number"`
	Head       string    `json:"head" doc:"The commit the checks ran on"`
	State      string    `json:"state" enum:"none,pending,success,failure" doc:"pending while one runs, then failure if one failed or was cancelled, else success; none without checks"`
	Checks     []PRCheck `json:"checks" nullable:"false"`
	Note       string    `json:"note,omitempty" doc:"What couldn't be read, as GitHub's check runs for a token without the Checks permission"`
}

// CheckReader reads the CI checks of pull requests.
type CheckReader interface {
	// PullRequestChecks returns the checks of the pull request's head commit, without logs.
	PullRequestChecks(ctx context.Context, token, owner, repo string, number int) (PRChecks, error)
	// CheckLog returns the end of a failed check's log, or "" when the forge keeps none.
	CheckLog(ctx context.Context, token, owner, repo string, c PRCheck) (string, error)
}

// sumChecks is the state of a set of checks: pending while one runs, then failure if one
// failed or was cancelled, success otherwise, and none without any.
func sumChecks(checks []PRCheck) string {
	if len(checks) == 0 {
		return "none"
	}
	state := CheckSuccess
	for _, c := range checks {
		switch c.State {
		case CheckPending:
			return CheckPending
		case CheckFailure, CheckCancelled:
			state = CheckFailure
		}
	}
	return state
}

// failed is whether a check's state counts against the pull request.
func (c PRCheck) failed() bool { return c.State == CheckFailure || c.State == CheckCancelled }

// statusState maps a commit status's state, on GitHub, Forgejo and Gitea, to a check's.
func statusState(s string) string {
	switch s {
	case "success":
		return CheckSuccess
	case "failure", "error":
		return CheckFailure
	case "warning":
		return CheckNeutral
	case "skipped":
		return CheckSkipped
	case "cancelled", "canceled":
		return CheckCancelled
	}
	return CheckPending
}

// commitStatus is one entry of a commit's combined status. GitHub names its state state,
// Forgejo and Gitea status.
type commitStatus struct {
	Context     string     `json:"context"`
	State       string     `json:"state"`
	Status      string     `json:"status"`
	Description string     `json:"description"`
	TargetURL   string     `json:"target_url"`
	CreatedAt   *time.Time `json:"created_at"`
	UpdatedAt   *time.Time `json:"updated_at"`
}

func (s commitStatus) check() PRCheck {
	state := s.State
	if s.Status != "" {
		state = s.Status
	}
	c := PRCheck{Name: s.Context, State: statusState(state), Summary: s.Description, URL: s.TargetURL, Started: s.CreatedAt}
	if c.State != CheckPending {
		c.Finished = s.UpdatedAt
	}
	return c
}

// pullHead is the head commit of a Forgejo or GitHub pull request.
func pullHead(ctx context.Context, call restAPI, token, owner, repo string, number int) (string, error) {
	var pr struct {
		Head struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	path := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/pulls/" + strconv.Itoa(number)
	if err := call(ctx, token, http.MethodGet, path, nil, &pr); err != nil {
		return "", err
	}
	if pr.Head.SHA == "" {
		return "", errors.New("the forge named no head commit for the pull request")
	}
	return pr.Head.SHA, nil
}

// combinedStatus lists the latest status of each context of a commit, on any of the three.
func combinedStatus(ctx context.Context, call restAPI, token, owner, repo, sha string) ([]PRCheck, error) {
	var out struct {
		Statuses []commitStatus `json:"statuses"`
	}
	path := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/commits/" + url.PathEscape(sha) + "/status?per_page=100&limit=50"
	if err := call(ctx, token, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	checks := make([]PRCheck, 0, len(out.Statuses))
	for _, s := range out.Statuses {
		checks = append(checks, s.check())
	}
	return checks, nil
}

// --- Forgejo and Gitea ----------------------------------------------------------------------

func (f *Forgejo) PullRequestChecks(ctx context.Context, token, owner, repo string, number int) (PRChecks, error) {
	head, err := pullHead(ctx, f.call, token, owner, repo, number)
	if err != nil {
		return PRChecks{}, err
	}
	checks, err := combinedStatus(ctx, f.call, token, owner, repo, head)
	if err != nil {
		return PRChecks{}, err
	}
	return PRChecks{Head: head, State: sumChecks(checks), Checks: checks}, nil
}

// CheckLog has nothing to read: a commit status only links to its run.
func (f *Forgejo) CheckLog(context.Context, string, string, string, PRCheck) (string, error) {
	return "", nil
}

// --- GitHub ---------------------------------------------------------------------------------

func (g *GitHub) PullRequestChecks(ctx context.Context, token, owner, repo string, number int) (PRChecks, error) {
	head, err := pullHead(ctx, g.rest, token, owner, repo, number)
	if err != nil {
		return PRChecks{}, err
	}
	var runs struct {
		CheckRuns []struct {
			ID          int64      `json:"id"`
			Name        string     `json:"name"`
			Status      string     `json:"status"`
			Conclusion  string     `json:"conclusion"`
			HTMLURL     string     `json:"html_url"`
			DetailsURL  string     `json:"details_url"`
			StartedAt   *time.Time `json:"started_at"`
			CompletedAt *time.Time `json:"completed_at"`
			Output      struct {
				Title string `json:"title"`
			} `json:"output"`
			App struct {
				Slug string `json:"slug"`
			} `json:"app"`
		} `json:"check_runs"`
	}
	base := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/commits/" + url.PathEscape(head)
	// A fine-grained token may read statuses but not check runs; the statuses still count.
	runsErr := g.rest(ctx, token, http.MethodGet, base+"/check-runs?per_page=100&filter=latest", nil, &runs)
	var checks []PRCheck
	for _, r := range runs.CheckRuns {
		c := PRCheck{Name: r.Name, State: githubCheckState(r.Status, r.Conclusion), Summary: r.Output.Title, URL: r.HTMLURL,
			Started: r.StartedAt, Finished: r.CompletedAt}
		if c.URL == "" {
			c.URL = r.DetailsURL
		}
		if r.Conclusion == "action_required" && c.Summary == "" {
			c.Summary = "action required"
		}
		// An Actions check run is its job, whose log the API serves.
		if r.App.Slug == "github-actions" {
			c.logID = r.ID
		}
		checks = append(checks, c)
	}
	statuses, err := combinedStatus(ctx, g.rest, token, owner, repo, head)
	if err != nil {
		if runsErr != nil {
			return PRChecks{}, runsErr
		}
		return PRChecks{}, err
	}
	checks = append(checks, statuses...)
	if checks == nil {
		checks = []PRCheck{}
	}
	out := PRChecks{Head: head, State: sumChecks(checks), Checks: checks}
	if runsErr != nil {
		out.Note = "only commit statuses: GitHub refused the check runs (" + runsErr.Error() + "); the token may lack the Checks permission"
	}
	return out, nil
}

// githubCheckState maps a check run's status and conclusion to a check's state.
func githubCheckState(status, conclusion string) string {
	if status != "completed" {
		return CheckPending
	}
	switch conclusion {
	case "success":
		return CheckSuccess
	case "cancelled":
		return CheckCancelled
	case "skipped":
		return CheckSkipped
	case "neutral", "stale":
		return CheckNeutral
	}
	// failure, timed_out, startup_failure, action_required.
	return CheckFailure
}

// CheckLog reads an Actions job's log. The API answers with a redirect to the log's storage,
// which is fetched without the token.
func (g *GitHub) CheckLog(ctx context.Context, token, owner, repo string, c PRCheck) (string, error) {
	if c.logID == 0 {
		return "", nil
	}
	u := g.api() + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/actions/jobs/" + strconv.FormatInt(c.logID, 10) + "/logs"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", GitHubAPIVersion)
	// The client follows no redirects; this one leads to the log's storage.
	resp, err := g.Client.Do(req)
	if err != nil {
		return "", errors.New(mask(err.Error(), token))
	}
	if resp.StatusCode/100 == 3 {
		loc, err := resp.Location()
		resp.Body.Close()
		if err != nil {
			return "", fmt.Errorf("the log's redirect: %w", err)
		}
		if loc.Scheme != "https" && loc.Scheme != "http" {
			return "", fmt.Errorf("the log's redirect goes to %s", loc.Scheme)
		}
		if req, err = http.NewRequestWithContext(ctx, http.MethodGet, loc.String(), nil); err != nil {
			return "", err
		}
		if resp, err = g.Client.Do(req); err != nil {
			return "", errors.New(mask(err.Error(), token))
		}
	}
	return readLogTail(resp, token)
}

// --- GitLab ---------------------------------------------------------------------------------

func (g *GitLab) PullRequestChecks(ctx context.Context, token, owner, repo string, number int) (PRChecks, error) {
	var mr struct {
		SHA string `json:"sha"`
	}
	if _, err := g.get(ctx, token, g.mr(owner, repo, number), &mr, gitlabMaxAnswer); err != nil {
		return PRChecks{}, err
	}
	var pipelines []struct {
		ID  int64  `json:"id"`
		SHA string `json:"sha"`
	}
	if _, err := g.get(ctx, token, g.mr(owner, repo, number)+"/pipelines?per_page=20", &pipelines, gitlabMaxAnswer); err != nil {
		return PRChecks{}, err
	}
	out := PRChecks{Head: mr.SHA, Checks: []PRCheck{}}
	// The newest pipeline of the head commit, which the list puts first.
	var pipeline int64
	for _, p := range pipelines {
		if p.SHA == mr.SHA {
			pipeline = p.ID
			break
		}
	}
	if pipeline == 0 {
		out.State = sumChecks(nil)
		return out, nil
	}
	var jobs []struct {
		ID            int64      `json:"id"`
		Name          string     `json:"name"`
		Stage         string     `json:"stage"`
		Status        string     `json:"status"`
		WebURL        string     `json:"web_url"`
		StartedAt     *time.Time `json:"started_at"`
		FinishedAt    *time.Time `json:"finished_at"`
		AllowFailure  bool       `json:"allow_failure"`
		FailureReason string     `json:"failure_reason"`
	}
	path := "/projects/" + gitlabProject(owner, repo) + "/pipelines/" + strconv.FormatInt(pipeline, 10) + "/jobs?per_page=100"
	if _, err := g.get(ctx, token, path, &jobs, gitlabMaxAnswer); err != nil {
		return PRChecks{}, err
	}
	for _, j := range jobs {
		c := PRCheck{Name: j.Stage + " / " + j.Name, State: gitlabJobState(j.Status, j.AllowFailure), URL: j.WebURL,
			Started: j.StartedAt, Finished: j.FinishedAt, logID: j.ID}
		if j.Stage == "" {
			c.Name = j.Name
		}
		if j.Status == "failed" {
			c.Summary = strings.ReplaceAll(j.FailureReason, "_", " ")
			if j.AllowFailure {
				c.Summary = strings.TrimSpace(c.Summary + " (allowed to fail)")
			}
		}
		if j.Status == "manual" {
			c.Summary = "waits to be started by hand"
		}
		out.Checks = append(out.Checks, c)
	}
	out.State = sumChecks(out.Checks)
	return out, nil
}

// gitlabJobState maps a pipeline job's status to a check's. A job allowed to fail doesn't
// fail the pull request, and one that waits to be started by hand doesn't hold it.
func gitlabJobState(status string, allowFailure bool) string {
	switch status {
	case "success":
		return CheckSuccess
	case "failed":
		if allowFailure {
			return CheckNeutral
		}
		return CheckFailure
	case "canceled", "canceling":
		return CheckCancelled
	case "skipped":
		return CheckSkipped
	case "manual":
		return CheckNeutral
	}
	// created, waiting_for_resource, preparing, pending, running, scheduled.
	return CheckPending
}

// CheckLog reads a job's trace.
func (g *GitLab) CheckLog(ctx context.Context, token, owner, repo string, c PRCheck) (string, error) {
	if c.logID == 0 {
		return "", nil
	}
	u := g.BaseURL + "/api/v4/projects/" + gitlabProject(owner, repo) + "/jobs/" + strconv.FormatInt(c.logID, 10) + "/trace"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := g.Client.Do(req)
	if err != nil {
		return "", errors.New(mask(err.Error(), token))
	}
	return readLogTail(resp, token)
}

// --- logs -----------------------------------------------------------------------------------

// readLogTail reads a log's answer and keeps its end: the last checkLogLines lines, at most
// checkLogBytes, without terminal colours, carriage-return redraws or the token.
func readLogTail(resp *http.Response, token string) (string, error) {
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("the forge answered %s for the log", resp.Status)
	}
	// Keep a window of the end while reading on.
	const window = 64 << 10
	var tail []byte
	buf := make([]byte, 32<<10)
	body := io.LimitReader(resp.Body, checkLogRead)
	for {
		n, err := body.Read(buf)
		tail = append(tail, buf[:n]...)
		if len(tail) > 2*window {
			tail = append(tail[:0], tail[len(tail)-window:]...)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	if len(tail) > window {
		tail = tail[len(tail)-window:]
	}
	return mask(logTail(string(tail)), token), nil
}

var (
	// ansiEscape is a terminal escape sequence: colours, erasing a line.
	ansiEscape = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	// gitlabSection is the marker GitLab writes around a collapsible section of a trace.
	gitlabSection = regexp.MustCompile(`section_(?:start|end):\d+:[^\s\r\n]*`)
	// githubStamp is the time GitHub writes before each line of a job's log.
	githubStamp = regexp.MustCompile(`(?m)^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d+)?Z `)
)

// logTail cleans a log for reading and keeps its last lines.
func logTail(s string) string {
	s = ansiEscape.ReplaceAllString(s, "")
	s = gitlabSection.ReplaceAllString(s, "")
	s = githubStamp.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	// Reading kept only the log's end, so its first line may be cut; the last lines are what
	// says why it failed.
	if len(lines) > checkLogLines {
		lines = lines[len(lines)-checkLogLines:]
	}
	for i, l := range lines {
		// A carriage return redraws the line: what follows the last one is what showed.
		if j := strings.LastIndexByte(l, '\r'); j >= 0 {
			lines[i] = l[j+1:]
		}
	}
	out := strings.Join(lines, "\n")
	if len(out) > checkLogBytes {
		out = out[len(out)-checkLogBytes:]
		if i := strings.IndexByte(out, '\n'); i >= 0 {
			out = out[i+1:]
		}
	}
	return strings.ToValidUTF8(out, "")
}

// --- the tool -------------------------------------------------------------------------------

func (s *Service) toolPullRequestChecks(ctx context.Context, sb store.Sandbox, params json.RawMessage) (any, error) {
	var in struct {
		pullRequestArgs
		Branch string `json:"branch"`
	}
	if err := decodeArgs(params, &in); err != nil {
		return nil, err
	}
	f, rv, err := s.reviewForge(ctx, sb.EnvironmentID, in.Forge)
	if err != nil {
		return nil, err
	}
	cr, ok := rv.(CheckReader)
	if !ok {
		return nil, integrations.ToolError("Studio can't read the checks of %s", f.Name)
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
	number := in.Number
	if number < 1 {
		if number, err = openPullRequestOf(ctx, rv, token, owner, repo, in.Branch); err != nil {
			return nil, err
		}
	}
	out, err := cr.PullRequestChecks(ctx, token, owner, repo, number)
	if err != nil {
		return nil, integrations.ToolError("%s", err)
	}
	out.Forge, out.Repository, out.Number = in.Forge, owner+"/"+repo, number
	if out.Forge == "" {
		out.Forge = f.Name
	}
	s.fillCheckLogs(ctx, cr, token, owner, repo, out.Checks, nil)
	return out, nil
}

// openPullRequestOf is the number of the open pull request from branch.
func openPullRequestOf(ctx context.Context, rv Reviewer, token, owner, repo, branch string) (int, error) {
	if branch == "" {
		return 0, integrations.ToolError("give the pull request's number, or its branch")
	}
	prs, err := rv.ListPullRequests(ctx, token, owner, repo, "open", maxPullRequests)
	if err != nil {
		return 0, integrations.ToolError("%s", err)
	}
	for _, p := range prs {
		if p.Head == branch {
			return p.Number, nil
		}
	}
	return 0, integrations.ToolError("no open pull request of %s/%s is from branch %q; give its number", owner, repo, branch)
}

// checkLogCache keeps the logs of finished checks, which don't change, by repository and log.
// A nil one keeps none.
type checkLogCache struct {
	mu sync.Mutex
	m  map[string]string
}

// checkLogsKept bounds the cache; past it, it starts again.
const checkLogsKept = 200

func (c *checkLogCache) get(key string) (string, bool) {
	if c == nil {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	log, ok := c.m[key]
	return log, ok
}

func (c *checkLogCache) put(key, log string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil || len(c.m) >= checkLogsKept {
		c.m = map[string]string{}
	}
	c.m[key] = log
}

// fillCheckLogs puts the end of its log into each of the first failed checks that has one.
// A log that can't be read is left out: the check's link still leads to it.
func (s *Service) fillCheckLogs(ctx context.Context, cr CheckReader, token, owner, repo string, checks []PRCheck, cache *checkLogCache) {
	n := 0
	for i := range checks {
		c := &checks[i]
		if !c.failed() || c.logID == 0 || n == checkLogsPerRead {
			continue
		}
		n++
		key := owner + "/" + repo + "#" + strconv.FormatInt(c.logID, 10)
		if log, ok := cache.get(key); ok {
			c.Log = log
			continue
		}
		log, err := cr.CheckLog(ctx, token, owner, repo, *c)
		if err != nil {
			slog.Debug("gitreview: a check's log", "repo", owner+"/"+repo, "check", c.Name, "err", err)
			continue
		}
		c.Log = log
		cache.put(key, log)
	}
}
