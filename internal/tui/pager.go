package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/api"
)

// pagerState is the review of one git approval: the push's facts, its commits and its diff, or
// the pull request it would open, in a pager the user scrolls before deciding.
type pagerState struct {
	approval api.ApprovalView
	scroll   int // the first line on show

	// lines caches the review laid out for width, which a long diff makes worth keeping.
	width int
	lines []string
}

// The keys of the pager. y, n and i decide as the web's review does: approve and push (or open
// the pull request), reject, ignore.
var (
	keyPush      = key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "approve and push"))
	keyOpenPR    = key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "open pull request"))
	keyReject    = key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "reject"))
	keyReview    = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "review"))
	keyScrollBar = key.NewBinding(key.WithKeys("pgdown"), key.WithHelp("↑↓/pgdn", "scroll"))
)

// openPager shows the review of a git approval.
func (m *app) openPager(a api.ApprovalView) {
	m.pager = pagerState{approval: a}
	m.screen = screenPager
}

// pagerLines is the review laid out for width w.
func (m *app) pagerLines(w int) []string {
	p := &m.pager
	if p.lines == nil || p.width != w {
		p.lines, p.width = m.reviewLines(p.approval, w), w
	}
	return p.lines
}

// reviewLines lays out a git approval as the web's review does: the facts, the pull request,
// the commits and the diff.
func (m *app) reviewLines(a api.ApprovalView, w int) []string {
	git := a.Git
	lines := []string{styleAccent.Render(kindTitle(a.Kind)) + "  " + styleBold.Render(gitSummary(a)), ""}
	rv, pr := git.Review, git.PullRequest
	lines = append(lines, fact("Repository", strings.Join(nonEmpty(git.Push.ForgeName, git.Push.Owner, git.Push.Repo), "/")))
	sandbox := firstNonEmpty(approvalSource(a), git.Push.SandboxID)
	if git.Push.TeammateName != "" {
		sandbox += styleDim.Render(" · teammate ") + git.Push.TeammateName
	}
	lines = append(lines, fact("Sandbox", sandbox))
	branch := ""
	switch {
	case pr != nil:
		branch = pr.Head + " → " + pr.Base
	case rv != nil:
		branch = rv.Branch
		if rv.Old == "" {
			branch += styleDim.Render(" · new branch")
		}
	}
	if branch != "" {
		lines = append(lines, fact("Branch", branch))
	}
	if rv != nil {
		old := "∅"
		if rv.Old != "" {
			old = shortSHA(rv.Old)
		}
		lines = append(lines, fact("Change", fmt.Sprintf("%s → %s · %d %s ", old, shortSHA(rv.New), rv.Files, plural(rv.Files, "file", "files"))+
			styleAdded.Render(fmt.Sprintf("+%d", rv.Additions))+" "+styleFailed.Render(fmt.Sprintf("−%d", rv.Deletions))))
	}
	switch {
	case pr != nil:
		lines = append(lines, "", styleDim.Render("The push was approved and is on the forge. Approving opens this pull request."))
	default:
		lines = append(lines, "", styleDim.Render("Nothing reaches the forge until you approve; the agent sees a rejection on"),
			styleDim.Render("its next fetch."))
		if rv != nil && rv.Upstream != "" {
			lines = append(lines, styleDim.Render("Approving pushes it "+rv.Upstream+"."))
		}
	}

	if pr != nil {
		lines = append(lines, "", styleBold.Render("Pull request"), "  "+pr.Title)
		for _, l := range wrapText(pr.Body, w-4) {
			lines = append(lines, "    "+l)
		}
	}
	if rv != nil {
		n := len(rv.Commits) + rv.MoreCommits
		lines = append(lines, "", styleBold.Render(fmt.Sprintf("Commits (%d)", n)))
		for _, c := range rv.Commits {
			right := styleDim.Render(c.Author + " · " + age(m.now.Sub(c.When)))
			lines = append(lines, spread("  "+styleDim.Render(shortSHA(c.SHA))+" "+c.Subject, right, w))
			if body := strings.TrimSpace(c.Body); body != "" {
				for _, l := range wrapText(body, w-10) {
					lines = append(lines, "          "+styleDim.Render(l))
				}
			}
		}
		if rv.MoreCommits > 0 {
			lines = append(lines, "  "+styleDim.Render(fmt.Sprintf("and %d more", rv.MoreCommits)))
		}
		head := styleBold.Render("Diff")
		if rv.DiffTruncated {
			head += styleDim.Render(" · truncated")
		}
		lines = append(lines, "", head)
		if rv.Diff == "" {
			lines = append(lines, "  "+styleDim.Render("No changes."))
		}
		for _, l := range strings.Split(strings.TrimRight(rv.Diff, "\n"), "\n") {
			if rv.Diff != "" {
				lines = append(lines, diffLine(l))
			}
		}
	}
	if rv == nil && pr == nil {
		lines = append(lines, "", styleDim.Render("Studio has no review of this push."))
	}
	return lines
}

// diffLine colors one line of a unified diff, as the web's diff view does.
func diffLine(l string) string {
	l = strings.ReplaceAll(l, "\t", "    ")
	switch {
	case strings.HasPrefix(l, "+++"), strings.HasPrefix(l, "---"):
		return styleDim.Render(l)
	case strings.HasPrefix(l, "+"):
		return styleAdded.Render(l)
	case strings.HasPrefix(l, "-"):
		return styleFailed.Render(l)
	case strings.HasPrefix(l, "@@"):
		return styleAccent.Render(l)
	case strings.HasPrefix(l, "diff --git"):
		return styleBold.Render(l)
	}
	return l
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// pagerBody is the part of the review in view, h lines from the scroll position.
func (m *app) pagerBody(w, h int) []string {
	lines := m.pagerLines(w)
	p := &m.pager
	p.scroll = min(max(p.scroll, 0), max(len(lines)-h, 0))
	return lines[p.scroll:min(p.scroll+h, len(lines))]
}

// pagerKey scrolls the review and decides it. A decision returns to the approvals.
func (m *app) pagerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := &m.pager
	_, h := m.size()
	page := max(h-6, 1)
	a := p.approval
	switch {
	case key.Matches(msg, keyBack):
		m.screen = screenApprovals
	case key.Matches(msg, keyHelp):
		m.openHelp()
	case key.Matches(msg, keyUp):
		p.scroll--
	case key.Matches(msg, keyDown):
		p.scroll++
	case key.Matches(msg, keyPageUp):
		p.scroll -= page
	case key.Matches(msg, keyPageDn):
		p.scroll += page
	case key.Matches(msg, keyTop):
		p.scroll = 0
	case key.Matches(msg, keyBottom):
		w, _ := m.size()
		p.scroll = len(m.pagerLines(w))
	case key.Matches(msg, keyPush):
		m.screen = screenApprovals
		if a.Git.PullRequest != nil {
			return m, m.decide(card{approvals: []api.ApprovalView{a}}, Decision{Action: "allow", Scope: "sandbox"}, "Opening", "the pull request")
		}
		return m, m.decide(card{approvals: []api.ApprovalView{a}}, Decision{Action: "allow", Scope: "sandbox"}, "Approved", "the push of "+gitBranch(a))
	case key.Matches(msg, keyReject):
		m.screen = screenApprovals
		return m, m.decide(card{approvals: []api.ApprovalView{a}}, Decision{Action: "deny", Scope: "sandbox"}, "Rejected", gitWhat(a))
	case key.Matches(msg, keyIgnore):
		m.screen = screenApprovals
		return m, m.decide(card{approvals: []api.ApprovalView{a}}, Decision{Action: "dismiss", Scope: "sandbox"}, "Ignored", gitWhat(a))
	}
	p.scroll = max(p.scroll, 0) // pagerBody keeps it off the end
	return m, nil
}

// gitBranch is the branch a git approval pushes.
func gitBranch(a api.ApprovalView) string {
	if a.Git.Review != nil && a.Git.Review.Branch != "" {
		return a.Git.Review.Branch
	}
	return strings.TrimPrefix(a.Git.Push.Ref, "refs/heads/")
}

// gitWhat names a git approval in a notice.
func gitWhat(a api.ApprovalView) string {
	if a.Git.PullRequest != nil {
		return "the pull request"
	}
	return "the push of " + gitBranch(a)
}

// pagerBindings are the keys the key bar shows in the pager.
func (m *app) pagerBindings() []key.Binding {
	approve := keyPush
	if m.pager.approval.Git.PullRequest != nil {
		approve = keyOpenPR
	}
	return []key.Binding{approve, keyReject, keyIgnore, keyScrollBar, keyBack, keyHelp}
}

// isGit reports whether an approval is a push or pull request the pager can review.
func isGit(a api.ApprovalView) bool {
	return strings.HasPrefix(a.Kind, "git.") && a.Git != nil
}
