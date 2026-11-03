package gitreview

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// GitLab's merge requests as pull requests (docs/git-review.md#pull-request-reviews). GitLab
// has no review object: a review is posted as an approval, a discussion per line comment and
// a note with the summary, and "request changes" is a note that says so.

// gitlabMaxAnswer bounds one answer of GitLab's that carries diffs.
const gitlabMaxAnswer = 8 << 20

func (g *GitLab) mr(owner, repo string, number int) string {
	return "/projects/" + gitlabProject(owner, repo) + "/merge_requests/" + strconv.Itoa(number)
}

func (g *GitLab) get(ctx context.Context, token, path string, out any, max int64) (http.Header, error) {
	return callJSONMax(ctx, g.Client, "Bearer "+token, token, http.MethodGet, g.BaseURL+"/api/v4"+path, nil, nil, out, max)
}

type gitlabUser struct {
	Username string `json:"username"`
}

// gitlabState is a merge request's state as a pull request's: GitLab's opened (and locked,
// while it merges) is open, closed and merged are closed.
func gitlabState(state string) (string, bool) {
	switch state {
	case "opened", "locked":
		return "open", false
	case "merged":
		return "closed", true
	}
	return "closed", false
}

// gitlabDiffEntry is one file of a merge request's diff, without the headers git writes.
type gitlabDiffEntry struct {
	OldPath     string `json:"old_path"`
	NewPath     string `json:"new_path"`
	NewFile     bool   `json:"new_file"`
	RenamedFile bool   `json:"renamed_file"`
	DeletedFile bool   `json:"deleted_file"`
	Diff        string `json:"diff"`
}

func (e gitlabDiffEntry) status() string {
	switch {
	case e.NewFile:
		return "added"
	case e.DeletedFile:
		return "removed"
	case e.RenamedFile:
		return "renamed"
	}
	return "modified"
}

// counts are the lines the entry adds and removes.
func (e gitlabDiffEntry) counts() (int, int) {
	var add, del int
	for _, l := range strings.Split(e.Diff, "\n") {
		switch {
		case strings.HasPrefix(l, "+"):
			add++
		case strings.HasPrefix(l, "-"):
			del++
		}
	}
	return add, del
}

// unified is the entry as git diff writes it.
func (e gitlabDiffEntry) unified() string {
	var b strings.Builder
	fmt.Fprintf(&b, "diff --git a/%s b/%s\n", e.OldPath, e.NewPath)
	if e.Diff == "" {
		return b.String()
	}
	from, to := "a/"+e.OldPath, "b/"+e.NewPath
	if e.NewFile {
		from = "/dev/null"
	}
	if e.DeletedFile {
		to = "/dev/null"
	}
	fmt.Fprintf(&b, "--- %s\n+++ %s\n", from, to)
	b.WriteString(e.Diff)
	if !strings.HasSuffix(e.Diff, "\n") {
		b.WriteByte('\n')
	}
	return b.String()
}

// diffs pages through a merge request's diffs until stop says enough, and says whether more
// were left. GitLab before 15.7 has no …/diffs; its …/changes answers them all at once.
func (g *GitLab) diffs(ctx context.Context, token, mr string, perPage int, stop func([]gitlabDiffEntry) bool) (bool, error) {
	var all []gitlabDiffEntry
	for page := 1; ; page++ {
		var list []gitlabDiffEntry
		h, err := g.get(ctx, token, fmt.Sprintf("%s/diffs?per_page=%d&page=%d", mr, perPage, page), &list, gitlabMaxAnswer)
		if err != nil && page == 1 && strings.Contains(err.Error(), "404") {
			var changes struct {
				Changes []gitlabDiffEntry `json:"changes"`
			}
			if _, cerr := g.get(ctx, token, mr+"/changes", &changes, gitlabMaxAnswer); cerr != nil {
				return false, err
			}
			stop(changes.Changes)
			return false, nil
		}
		if err != nil {
			return false, err
		}
		all = append(all, list...)
		next := h.Get("X-Next-Page")
		if stop(all) {
			return next != "", nil
		}
		if next == "" || len(list) == 0 {
			return false, nil
		}
	}
}

func (g *GitLab) ReadPullRequest(ctx context.Context, token, owner, repo string, number int) (PullRequestInfo, error) {
	mr := g.mr(owner, repo, number)
	var m struct {
		IID          int        `json:"iid"`
		Title        string     `json:"title"`
		Description  string     `json:"description"`
		State        string     `json:"state"`
		WebURL       string     `json:"web_url"`
		Author       gitlabUser `json:"author"`
		SourceBranch string     `json:"source_branch"`
		TargetBranch string     `json:"target_branch"`
		SHA          string     `json:"sha"`
		UpdatedAt    time.Time  `json:"updated_at"`
		DiffRefs     struct {
			StartSHA string `json:"start_sha"`
		} `json:"diff_refs"`
	}
	if err := g.call(ctx, token, http.MethodGet, mr, nil, &m); err != nil {
		return PullRequestInfo{}, err
	}
	state, merged := gitlabState(m.State)
	info := PullRequestInfo{Number: m.IID, Title: m.Title, Body: m.Description, State: state, Merged: merged, URL: m.WebURL,
		Author: m.Author.Username, Base: PRBranch{Ref: m.TargetBranch, SHA: m.DiffRefs.StartSHA}, Head: PRBranch{Ref: m.SourceBranch, SHA: m.SHA},
		Commits: []PRCommit{}, Files: []PRFile{}, Reviews: []PRReview{}, LineComments: []PRLineComment{}, Comments: []PRComment{}}

	// GitLab lists a merge request's commits newest first; the read keeps the newest and
	// lists them oldest first, as the other forges do.
	var commits []struct {
		ID         string `json:"id"`
		Title      string `json:"title"`
		AuthorName string `json:"author_name"`
	}
	if err := g.call(ctx, token, http.MethodGet, fmt.Sprintf("%s/commits?per_page=%d", mr, pageSize+1), nil, &commits); err != nil {
		return info, err
	}
	if len(commits) > pageSize {
		commits, info.MoreCommits = commits[:pageSize], true
	}
	for _, c := range slices.Backward(commits) {
		info.Commits = append(info.Commits, PRCommit{SHA: c.ID, Subject: c.Title, Author: c.AuthorName})
	}

	more, err := g.diffs(ctx, token, mr, pageSize, func(list []gitlabDiffEntry) bool {
		for _, e := range list[len(info.Files):] {
			if len(info.Files) == pageSize {
				break
			}
			add, del := e.counts()
			info.Files = append(info.Files, PRFile{Path: e.NewPath, Status: e.status(), Additions: add, Deletions: del})
		}
		info.MoreFiles = len(list) > pageSize
		return len(list) >= pageSize
	})
	if err != nil {
		return info, err
	}
	info.MoreFiles = info.MoreFiles || more

	// Notes: approvals and requested changes are system notes, line comments diff notes,
	// the rest the conversation.
	var notes []struct {
		Body      string     `json:"body"`
		Author    gitlabUser `json:"author"`
		CreatedAt time.Time  `json:"created_at"`
		System    bool       `json:"system"`
		Type      string     `json:"type"`
		Position  *struct {
			NewPath string `json:"new_path"`
			OldPath string `json:"old_path"`
			NewLine int    `json:"new_line"`
			OldLine int    `json:"old_line"`
		} `json:"position"`
	}
	if _, err := g.get(ctx, token, mr+"/notes?sort=asc&order_by=created_at&per_page=100", &notes, gitlabMaxAnswer); err != nil {
		return info, err
	}
	approved := map[string]bool{}
	for _, n := range notes {
		switch {
		case n.System:
			state := map[string]string{"approved this merge request": "APPROVED", "unapproved this merge request": "UNAPPROVED",
				"requested changes": "REQUEST_CHANGES"}[strings.TrimSpace(n.Body)]
			if state != "" && len(info.Reviews) < pageSize {
				info.Reviews = append(info.Reviews, PRReview{Author: n.Author.Username, State: state, When: n.CreatedAt})
				approved[n.Author.Username] = state == "APPROVED"
			}
		case n.Type == "DiffNote" && n.Position != nil:
			if len(info.LineComments) == pageSize {
				continue
			}
			lc := PRLineComment{Author: n.Author.Username, Path: n.Position.NewPath, Line: n.Position.NewLine, Side: "new", Body: n.Body, When: n.CreatedAt}
			if n.Position.NewLine == 0 {
				lc.Path, lc.Line, lc.Side = n.Position.OldPath, n.Position.OldLine, "old"
			}
			info.LineComments = append(info.LineComments, lc)
		default:
			if len(info.Comments) < pageSize {
				info.Comments = append(info.Comments, PRComment{Author: n.Author.Username, Body: n.Body, When: n.CreatedAt})
			}
		}
	}
	// Approvals whose note is past the first page, or that GitLab didn't note.
	var approvals struct {
		ApprovedBy []struct {
			User gitlabUser `json:"user"`
		} `json:"approved_by"`
	}
	if err := g.call(ctx, token, http.MethodGet, mr+"/approvals", nil, &approvals); err == nil {
		for _, a := range approvals.ApprovedBy {
			if !approved[a.User.Username] && len(info.Reviews) < pageSize {
				info.Reviews = append(info.Reviews, PRReview{Author: a.User.Username, State: "APPROVED", When: m.UpdatedAt})
			}
		}
	}
	return info, nil
}

func (g *GitLab) PullRequestDiff(ctx context.Context, token, owner, repo string, number int, max int64) (string, bool, error) {
	var b strings.Builder
	truncated := false
	more, err := g.diffs(ctx, token, g.mr(owner, repo, number), 20, func(list []gitlabDiffEntry) bool {
		b.Reset()
		for _, e := range list {
			u := e.unified()
			if int64(b.Len()+len(u)) > max {
				// Cut at the last whole line that fits.
				u = u[:max-int64(b.Len())]
				if i := strings.LastIndexByte(u, '\n'); i >= 0 {
					b.WriteString(u[:i+1])
				}
				truncated = true
				return true
			}
			b.WriteString(u)
		}
		return false
	})
	if err != nil {
		return "", false, err
	}
	return b.String(), truncated || more, nil
}

func (g *GitLab) ListPullRequests(ctx context.Context, token, owner, repo, state string, limit int) ([]PRSummary, error) {
	// GitLab's closed leaves out the merged ones, which a pull request's closed takes in.
	q := url.Values{"order_by": {"updated_at"}, "sort": {"desc"}, "per_page": {strconv.Itoa(limit)}}
	switch state {
	case "open":
		q.Set("state", "opened")
	case "closed":
		q.Set("state", "all")
		q.Set("per_page", strconv.Itoa(maxPullRequests))
	default:
		q.Set("state", "all")
	}
	var list []struct {
		IID          int          `json:"iid"`
		Title        string       `json:"title"`
		State        string       `json:"state"`
		Draft        bool         `json:"draft"`
		WIP          bool         `json:"work_in_progress"`
		WebURL       string       `json:"web_url"`
		Author       gitlabUser   `json:"author"`
		UpdatedAt    time.Time    `json:"updated_at"`
		SourceBranch string       `json:"source_branch"`
		TargetBranch string       `json:"target_branch"`
		Reviewers    []gitlabUser `json:"reviewers"`
		Labels       []string     `json:"labels"`
	}
	if err := g.call(ctx, token, http.MethodGet, "/projects/"+gitlabProject(owner, repo)+"/merge_requests?"+q.Encode(), nil, &list); err != nil {
		return nil, err
	}
	out := []PRSummary{}
	for _, m := range list {
		st, merged := gitlabState(m.State)
		if state == "closed" && st != "closed" {
			continue
		}
		if len(out) == limit {
			break
		}
		s := PRSummary{Number: m.IID, Title: m.Title, Author: m.Author.Username, State: st, Draft: m.Draft || m.WIP, Merged: merged,
			Base: m.TargetBranch, Head: m.SourceBranch, Labels: m.Labels, Updated: m.UpdatedAt, URL: m.WebURL}
		for _, r := range m.Reviewers {
			s.Reviewers = append(s.Reviewers, r.Username)
		}
		out = append(out, s)
	}
	return out, nil
}

// gitlabVersion is one version of a merge request's diff: GitLab places a comment on a
// version by its three commits.
type gitlabVersion struct {
	ID       int64             `json:"id"`
	HeadSHA  string            `json:"head_commit_sha"`
	BaseSHA  string            `json:"base_commit_sha"`
	StartSHA string            `json:"start_commit_sha"`
	Diffs    []gitlabDiffEntry `json:"diffs"`
}

// PostReview posts a review on a merge request: the approval first, so a refused one posts
// nothing, then a discussion per line comment on the diff version of r.CommitID (the newest
// without one), then the summary as a note. Request changes is the summary with
// **Changes requested.** in front, since GitLab's REST API has no such verdict.
func (g *GitLab) PostReview(ctx context.Context, token, owner, repo string, number int, r ReviewPost) (string, error) {
	mr := g.mr(owner, repo, number)
	body := r.Body
	switch r.Verdict {
	case VerdictComment:
	case VerdictApprove:
		in := map[string]any{}
		if r.CommitID != "" {
			in["sha"] = r.CommitID
		}
		if err := g.call(ctx, token, http.MethodPost, mr+"/approve", in, nil); err != nil {
			if strings.Contains(err.Error(), "409") {
				return "", errors.New("the merge request has commits newer than the reviewed one, and GitLab approves only its newest; draft the review again")
			}
			// GitLab refuses a second approval by the same user; that one stands.
			if !g.approvedByMe(ctx, token, mr) {
				return "", err
			}
		}
	case VerdictRequestChanges:
		body = strings.TrimSpace("**Changes requested.**\n\n" + body)
	default:
		return "", fmt.Errorf("unknown verdict %q", r.Verdict)
	}

	var webURL string
	if len(r.Comments) > 0 {
		var versions []gitlabVersion
		if err := g.call(ctx, token, http.MethodGet, mr+"/versions", nil, &versions); err != nil {
			return "", err
		}
		if len(versions) == 0 {
			return "", errors.New("GitLab lists no diff for the merge request")
		}
		// Versions come newest first.
		v := versions[0]
		for _, c := range versions {
			if c.HeadSHA == r.CommitID {
				v = c
				break
			}
		}
		if _, err := g.get(ctx, token, mr+"/versions/"+strconv.FormatInt(v.ID, 10), &v, gitlabMaxAnswer); err != nil {
			return "", err
		}
		var b strings.Builder
		for _, e := range v.Diffs {
			b.WriteString(e.unified())
		}
		lines := parseDiffLines(b.String())
		paths := map[string]gitlabDiffEntry{}
		for _, e := range v.Diffs {
			paths["new\x00"+e.NewPath] = e
			paths["old\x00"+e.OldPath] = e
		}
		for i, c := range r.Comments {
			side := c.Side
			if side == "" {
				side = "new"
			}
			e, ok := paths[side+"\x00"+c.Path]
			if !ok {
				return "", fmt.Errorf("comment %d: %s is not in the merge request's diff", i+1, c.Path)
			}
			pos := map[string]any{"position_type": "text", "base_sha": v.BaseSHA, "start_sha": v.StartSHA, "head_sha": v.HeadSHA,
				"old_path": e.OldPath, "new_path": e.NewPath}
			if side == "old" {
				pos["old_line"] = c.Line
			} else {
				pos["new_line"] = c.Line
				if old, ok := lines.kept[e.NewPath][c.Line]; ok {
					pos["old_line"] = old
				}
			}
			if err := g.call(ctx, token, http.MethodPost, mr+"/discussions", map[string]any{"body": c.Body, "position": pos}, nil); err != nil {
				return "", fmt.Errorf("comment %d: %w", i+1, err)
			}
		}
	}
	if body != "" {
		var note struct {
			ID int64 `json:"id"`
		}
		if err := g.call(ctx, token, http.MethodPost, mr+"/notes", map[string]any{"body": body}, &note); err != nil {
			return "", err
		}
		var m struct {
			WebURL string `json:"web_url"`
		}
		if err := g.call(ctx, token, http.MethodGet, mr, nil, &m); err == nil && m.WebURL != "" {
			webURL = fmt.Sprintf("%s#note_%d", m.WebURL, note.ID)
		}
	}
	return webURL, nil
}

// approvedByMe says whether the token's user already approved the merge request.
func (g *GitLab) approvedByMe(ctx context.Context, token, mr string) bool {
	var me gitlabUser
	var approvals struct {
		ApprovedBy []struct {
			User gitlabUser `json:"user"`
		} `json:"approved_by"`
	}
	if g.call(ctx, token, http.MethodGet, "/user", nil, &me) != nil || me.Username == "" ||
		g.call(ctx, token, http.MethodGet, mr+"/approvals", nil, &approvals) != nil {
		return false
	}
	for _, a := range approvals.ApprovedBy {
		if a.User.Username == me.Username {
			return true
		}
	}
	return false
}
