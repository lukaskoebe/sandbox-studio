package gitreview

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Reviewer is what a forge needs for teammates to review its pull requests
// (docs/git-review.md#pull-request-reviews): Forgejo, Gitea and GitHub, whose REST APIs share
// the shapes used here. GitLab's merge requests have no review object and come later.
type Reviewer interface {
	// ReadPullRequest returns a pull request with its commits, files, reviews and comments.
	ReadPullRequest(ctx context.Context, token, owner, repo string, number int) (PullRequestInfo, error)
	// PullRequestDiff returns the pull request's unified diff, cut at max bytes; truncated
	// says whether it was.
	PullRequestDiff(ctx context.Context, token, owner, repo string, number int, max int64) (diff string, truncated bool, err error)
	// ListPullRequests lists a repository's pull requests in state open, closed or all, most
	// recently updated first, at most limit of them.
	ListPullRequests(ctx context.Context, token, owner, repo, state string, limit int) ([]PRSummary, error)
	// PostReview posts a review and returns its web URL.
	PostReview(ctx context.Context, token, owner, repo string, number int, r ReviewPost) (string, error)
}

// PullRequestInfo is a pull request as git_pull_request_read answers it.
type PullRequestInfo struct {
	Number       int             `json:"number"`
	Title        string          `json:"title"`
	Body         string          `json:"body,omitempty"`
	State        string          `json:"state" doc:"open or closed"`
	Merged       bool            `json:"merged,omitempty"`
	URL          string          `json:"url,omitempty"`
	Author       string          `json:"author"`
	Base         PRBranch        `json:"base"`
	Head         PRBranch        `json:"head"`
	Commits      []PRCommit      `json:"commits"`
	MoreCommits  bool            `json:"moreCommits,omitempty" doc:"The pull request has more commits than listed"`
	Files        []PRFile        `json:"files"`
	MoreFiles    bool            `json:"moreFiles,omitempty" doc:"The pull request changes more files than listed"`
	Reviews      []PRReview      `json:"reviews"`
	LineComments []PRLineComment `json:"lineComments,omitempty" doc:"The reviews' comments on lines of the diff"`
	Comments     []PRComment     `json:"comments" doc:"The comments on the pull request's conversation"`
}

// PRBranch is a pull request's base or head.
type PRBranch struct {
	Ref string `json:"ref"`
	SHA string `json:"sha"`
}

// PRCommit is one commit of a pull request.
type PRCommit struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
	Author  string `json:"author"`
}

// PRFile is a file a pull request changes.
type PRFile struct {
	Path      string `json:"path"`
	Status    string `json:"status" doc:"added, modified, removed, renamed and so on, as the forge says"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

// PRReview is a review posted on a pull request.
type PRReview struct {
	Author string    `json:"author"`
	State  string    `json:"state" doc:"As the forge says, such as APPROVED, REQUEST_CHANGES or COMMENTED"`
	Body   string    `json:"body,omitempty"`
	When   time.Time `json:"when"`
}

// PRLineComment is a review's comment on a line of a pull request's diff.
type PRLineComment struct {
	Author string    `json:"author"`
	Path   string    `json:"path"`
	Line   int       `json:"line,omitempty" doc:"The line's number in the new file, or in the old one for side old"`
	Side   string    `json:"side" enum:"new,old"`
	Body   string    `json:"body"`
	When   time.Time `json:"when"`
}

// PRComment is a comment on a pull request's conversation.
type PRComment struct {
	Author string    `json:"author"`
	Body   string    `json:"body"`
	When   time.Time `json:"when"`
}

// Review verdicts.
const (
	VerdictComment        = "comment"
	VerdictApprove        = "approve"
	VerdictRequestChanges = "request_changes"
)

// ReviewComment is a review's comment on one line of the diff.
type ReviewComment struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Side string `json:"side" enum:"new,old" doc:"new: a line added or kept, numbered in the new file; old: a removed line, numbered in the old file"`
	Body string `json:"body"`
}

// ReviewPost is a review as Studio posts it.
type ReviewPost struct {
	Verdict  string
	Body     string
	CommitID string
	Comments []ReviewComment
}

// pageSize is how many commits, files, reviews and comments a read lists.
const pageSize = 50

// restAPI is one call to a forge's REST API, as Forgejo and GitHub make it.
type restAPI func(ctx context.Context, token, method, path string, in, out any) error

func readPullRequest(ctx context.Context, call restAPI, github bool, token, owner, repo string, number int) (PullRequestInfo, error) {
	base := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo)
	n := strconv.Itoa(number)
	type user struct {
		Login string `json:"login"`
	}
	var pr struct {
		Number  int    `json:"number"`
		Title   string `json:"title"`
		Body    string `json:"body"`
		State   string `json:"state"`
		Merged  bool   `json:"merged"`
		HTMLURL string `json:"html_url"`
		User    user   `json:"user"`
		Base    struct {
			Ref string `json:"ref"`
			SHA string `json:"sha"`
		} `json:"base"`
		Head struct {
			Ref string `json:"ref"`
			SHA string `json:"sha"`
		} `json:"head"`
	}
	if err := call(ctx, token, http.MethodGet, base+"/pulls/"+n, nil, &pr); err != nil {
		return PullRequestInfo{}, err
	}
	info := PullRequestInfo{Number: pr.Number, Title: pr.Title, Body: pr.Body, State: pr.State, Merged: pr.Merged, URL: pr.HTMLURL,
		Author: pr.User.Login, Base: PRBranch(pr.Base), Head: PRBranch(pr.Head),
		Commits: []PRCommit{}, Files: []PRFile{}, Reviews: []PRReview{}, LineComments: []PRLineComment{}, Comments: []PRComment{}}
	// Forgejo pages with limit, GitHub with per_page; each ignores the other's.
	page := fmt.Sprintf("?limit=%d&per_page=%d", pageSize+1, pageSize+1)

	var commits []struct {
		SHA    string `json:"sha"`
		Commit struct {
			Message string `json:"message"`
			Author  struct {
				Name string `json:"name"`
			} `json:"author"`
		} `json:"commit"`
	}
	if err := call(ctx, token, http.MethodGet, base+"/pulls/"+n+"/commits"+page, nil, &commits); err != nil {
		return info, err
	}
	for i, c := range commits {
		if i == pageSize {
			info.MoreCommits = true
			break
		}
		subject, _, _ := strings.Cut(c.Commit.Message, "\n")
		info.Commits = append(info.Commits, PRCommit{SHA: c.SHA, Subject: subject, Author: c.Commit.Author.Name})
	}

	var files []struct {
		Filename  string `json:"filename"`
		Status    string `json:"status"`
		Additions int    `json:"additions"`
		Deletions int    `json:"deletions"`
	}
	if err := call(ctx, token, http.MethodGet, base+"/pulls/"+n+"/files"+page, nil, &files); err != nil {
		return info, err
	}
	for i, f := range files {
		if i == pageSize {
			info.MoreFiles = true
			break
		}
		info.Files = append(info.Files, PRFile{Path: f.Filename, Status: f.Status, Additions: f.Additions, Deletions: f.Deletions})
	}

	var reviews []struct {
		ID          int64     `json:"id"`
		Comments    int       `json:"comments_count"`
		User        user      `json:"user"`
		State       string    `json:"state"`
		Body        string    `json:"body"`
		SubmittedAt time.Time `json:"submitted_at"`
	}
	if err := call(ctx, token, http.MethodGet, base+"/pulls/"+n+"/reviews"+page, nil, &reviews); err != nil {
		return info, err
	}
	for _, r := range reviews[:min(len(reviews), pageSize)] {
		info.Reviews = append(info.Reviews, PRReview{Author: r.User.Login, State: r.State, Body: r.Body, When: r.SubmittedAt})
	}

	// The reviews' line comments: GitHub lists them for the whole pull request, Forgejo per
	// review, so only reviews that have some are asked.
	type lineComment struct {
		User         user      `json:"user"`
		Path         string    `json:"path"`
		Body         string    `json:"body"`
		CreatedAt    time.Time `json:"created_at"`
		Line         *int      `json:"line"`              // GitHub; null once the line is outdated
		OriginalLine int       `json:"original_line"`     // GitHub
		Side         string    `json:"side"`              // GitHub: LEFT or RIGHT
		Position     int       `json:"position"`          // Forgejo: the line in the new file
		OldPosition  int       `json:"original_position"` // Forgejo: the line in the old file
	}
	add := func(list []lineComment) {
		for _, c := range list {
			if len(info.LineComments) == pageSize {
				return
			}
			lc := PRLineComment{Author: c.User.Login, Path: c.Path, Body: c.Body, When: c.CreatedAt, Side: "new"}
			switch {
			case github && c.Side == "LEFT":
				lc.Side = "old"
				fallthrough
			case github:
				lc.Line = c.OriginalLine
				if c.Line != nil {
					lc.Line = *c.Line
				}
			case c.Position > 0:
				lc.Line = c.Position
			default:
				lc.Line, lc.Side = c.OldPosition, "old"
			}
			info.LineComments = append(info.LineComments, lc)
		}
	}
	if github {
		var list []lineComment
		if err := call(ctx, token, http.MethodGet, base+"/pulls/"+n+"/comments"+page, nil, &list); err != nil {
			return info, err
		}
		add(list)
	} else {
		for _, r := range reviews[:min(len(reviews), pageSize)] {
			if r.Comments == 0 || len(info.LineComments) == pageSize {
				continue
			}
			var list []lineComment
			path := base + "/pulls/" + n + "/reviews/" + strconv.FormatInt(r.ID, 10) + "/comments"
			if err := call(ctx, token, http.MethodGet, path, nil, &list); err != nil {
				return info, err
			}
			add(list)
		}
	}

	var comments []struct {
		User      user      `json:"user"`
		Body      string    `json:"body"`
		CreatedAt time.Time `json:"created_at"`
	}
	if err := call(ctx, token, http.MethodGet, base+"/issues/"+n+"/comments"+page, nil, &comments); err != nil {
		return info, err
	}
	for _, c := range comments[:min(len(comments), pageSize)] {
		info.Comments = append(info.Comments, PRComment{Author: c.User.Login, Body: c.Body, When: c.CreatedAt})
	}
	return info, nil
}

// postReview posts a review. Forgejo and GitHub name the verdicts slightly differently and
// place a comment by side (GitHub) or by which position is set (Forgejo).
func postReview(ctx context.Context, call restAPI, github bool, token, owner, repo string, number int, r ReviewPost) (string, error) {
	event := map[string]string{VerdictComment: "COMMENT", VerdictApprove: "APPROVED", VerdictRequestChanges: "REQUEST_CHANGES"}[r.Verdict]
	if github && r.Verdict == VerdictApprove {
		event = "APPROVE"
	}
	if event == "" {
		return "", fmt.Errorf("unknown verdict %q", r.Verdict)
	}
	comments := []map[string]any{}
	for _, c := range r.Comments {
		m := map[string]any{"path": c.Path, "body": c.Body}
		switch {
		case github && c.Side == "old":
			m["line"], m["side"] = c.Line, "LEFT"
		case github:
			m["line"], m["side"] = c.Line, "RIGHT"
		case c.Side == "old":
			m["old_position"] = c.Line
		default:
			m["new_position"] = c.Line
		}
		comments = append(comments, m)
	}
	in := map[string]any{"event": event, "body": r.Body, "commit_id": r.CommitID, "comments": comments}
	var out struct {
		ID      int64  `json:"id"`
		HTMLURL string `json:"html_url"`
	}
	path := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/pulls/" + strconv.Itoa(number) + "/reviews"
	if err := call(ctx, token, http.MethodPost, path, in, &out); err != nil {
		return "", err
	}
	return out.HTMLURL, nil
}

// callText fetches a text resource, such as a diff, cut at max bytes.
func callText(ctx context.Context, client *http.Client, auth, token, rawURL string, headers map[string]string, max int64) (string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Authorization", auth)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", false, errors.New(mask(err.Error(), token))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return "", false, err
	}
	if resp.StatusCode/100 != 2 {
		msg := strings.TrimSpace(string(data))
		if len(msg) > 300 {
			msg = msg[:300] + "..."
		}
		return "", false, fmt.Errorf("the forge answered %s: %s", resp.Status, mask(msg, token))
	}
	if int64(len(data)) > max {
		// Cut at the last whole line.
		data = data[:max]
		if i := strings.LastIndexByte(string(data), '\n'); i >= 0 {
			data = data[:i+1]
		}
		return string(data), true, nil
	}
	return string(data), false, nil
}

// --- Forgejo and GitHub ---------------------------------------------------------------------

func (f *Forgejo) ReadPullRequest(ctx context.Context, token, owner, repo string, number int) (PullRequestInfo, error) {
	return readPullRequest(ctx, f.call, false, token, owner, repo, number)
}

func (f *Forgejo) PullRequestDiff(ctx context.Context, token, owner, repo string, number int, max int64) (string, bool, error) {
	u := f.BaseURL + "/api/v1/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/pulls/" + strconv.Itoa(number) + ".diff"
	return callText(ctx, f.Client, "token "+token, token, u, nil, max)
}

func (f *Forgejo) PostReview(ctx context.Context, token, owner, repo string, number int, r ReviewPost) (string, error) {
	return postReview(ctx, f.call, false, token, owner, repo, number, r)
}

func (g *GitHub) rest(ctx context.Context, token, method, path string, in, out any) error {
	_, err := g.call(ctx, token, method, path, in, out)
	return err
}

func (g *GitHub) ReadPullRequest(ctx context.Context, token, owner, repo string, number int) (PullRequestInfo, error) {
	return readPullRequest(ctx, g.rest, true, token, owner, repo, number)
}

func (g *GitHub) PullRequestDiff(ctx context.Context, token, owner, repo string, number int, max int64) (string, bool, error) {
	u := g.api() + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/pulls/" + strconv.Itoa(number)
	return callText(ctx, g.Client, "Bearer "+token, token, u,
		map[string]string{"Accept": "application/vnd.github.diff", "X-GitHub-Api-Version": GitHubAPIVersion}, max)
}

func (g *GitHub) PostReview(ctx context.Context, token, owner, repo string, number int, r ReviewPost) (string, error) {
	return postReview(ctx, g.rest, true, token, owner, repo, number, r)
}

// --- the diff's lines ---------------------------------------------------------------------

// diffLines are the lines of a unified diff a review comment can be on, per file: new holds
// the added and kept lines by their number in the new file, old the removed ones by their
// number in the old file.
type diffLines struct {
	new, old map[string]map[int]bool
}

func parseDiffLines(diff string) diffLines {
	d := diffLines{new: map[string]map[int]bool{}, old: map[string]map[int]bool{}}
	var oldPath, newPath string
	var oldLine, newLine int
	add := func(m map[string]map[int]bool, path string, n int) {
		if m[path] == nil {
			m[path] = map[int]bool{}
		}
		m[path][n] = true
	}
	inHunk := false
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			inHunk = false
			// diff --git a/x b/y: the paths, until --- and +++ say them exactly.
			if i := strings.Index(line, " b/"); i >= 0 {
				oldPath = strings.TrimPrefix(line[len("diff --git "):i], "a/")
				newPath = line[i+3:]
			}
			continue
		case !inHunk && strings.HasPrefix(line, "--- "):
			if p := strings.TrimPrefix(line, "--- "); p != "/dev/null" {
				oldPath = strings.TrimPrefix(p, "a/")
			}
			continue
		case !inHunk && strings.HasPrefix(line, "+++ "):
			if p := strings.TrimPrefix(line, "+++ "); p != "/dev/null" {
				newPath = strings.TrimPrefix(p, "b/")
			}
			continue
		case strings.HasPrefix(line, "@@ "):
			var o, n int
			// @@ -o[,c] +n[,c] @@
			fields := strings.Fields(line)
			if len(fields) >= 3 {
				o, _ = strconv.Atoi(strings.Split(strings.TrimPrefix(fields[1], "-"), ",")[0])
				n, _ = strconv.Atoi(strings.Split(strings.TrimPrefix(fields[2], "+"), ",")[0])
			}
			oldLine, newLine, inHunk = o, n, true
			continue
		}
		if !inHunk {
			continue
		}
		switch {
		case strings.HasPrefix(line, "+"):
			add(d.new, newPath, newLine)
			newLine++
		case strings.HasPrefix(line, "-"):
			add(d.old, oldPath, oldLine)
			oldLine++
		case strings.HasPrefix(line, " "):
			add(d.new, newPath, newLine)
			oldLine++
			newLine++
		}
	}
	return d
}

// has reports whether a comment's line is one the diff shows.
func (d diffLines) has(c ReviewComment) bool {
	if c.Side == "old" {
		return d.old[c.Path][c.Line]
	}
	return d.new[c.Path][c.Line]
}

// file reports whether the diff shows any line of path.
func (d diffLines) file(path string) bool {
	return len(d.new[path]) > 0 || len(d.old[path]) > 0
}
