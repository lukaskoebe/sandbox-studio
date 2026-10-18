package tui

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/lukaskoebe/sandbox-studio/internal/api"
)

// pagerState is the review of one git approval: the push's facts, its commits and its diff, or
// the pull request it would open, in a pager the user scrolls before deciding.
type pagerState struct {
	approval api.ApprovalView
	back     screen // where esc and a decision return to: the approvals or the Git screen
	scroll   int    // the first line on show
	unified  bool   // v turned side by side off, on a terminal wide enough for it

	// lines caches the review laid out for width, which a long diff makes worth keeping, and
	// files where each file's diff starts in it.
	width int
	side  bool
	lines []string
	files []diffFile
}

// diffFile is one file of a push's diff: its path, its lines added and removed, and the line of
// the review its diff starts on.
type diffFile struct {
	path       string
	adds, dels int
	line       int
}

// The keys of the pager. y, n and i decide as the web's review does: approve and push (or open
// the pull request), reject, ignore.
var (
	keyPush      = key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "approve and push"))
	keyOpenPR    = key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "open pull request"))
	keyReject    = key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "reject"))
	keyReview    = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "review"))
	keyScrollBar = key.NewBinding(key.WithKeys("pgdown"), key.WithHelp("↑↓/pgdn", "scroll"))
	keyPrevFile  = key.NewBinding(key.WithKeys("["))
	keyNextFile  = key.NewBinding(key.WithKeys("]"))
	keyFilesBar  = key.NewBinding(key.WithKeys("]"), key.WithHelp("[/]", "file"))
	keySide      = key.NewBinding(key.WithKeys("v"), key.WithHelp("v", "side by side"))
)

// sideWidth is the width from which the visual look shows a diff's hunks side by side.
const sideWidth = 160

// openPager shows the review of a git approval.
func (m *app) openPager(a api.ApprovalView) {
	m.pager = pagerState{approval: a, back: screenApprovals}
	m.screen = screenPager
}

// pagerLines is the review laid out for width w.
func (m *app) pagerLines(w int) []string {
	p := &m.pager
	side := m.sideBySide()
	if p.lines == nil || p.width != w || p.side != side {
		p.lines, p.files = m.reviewLines(p.approval, w, side)
		p.width, p.side = w, side
	}
	return p.lines
}

// sideBySide reports whether the review shows its hunks side by side: in the visual look, on a
// terminal of sideWidth columns, unless v turned it off.
func (m *app) sideBySide() bool {
	return !m.look.plain && m.width >= sideWidth && !m.pager.unified
}

// splitWidth is the width from which the git review shows its file list beside the diff.
const splitWidth = 120

// pagerSplit reports whether the review shows its file list beside the diff.
func (m *app) pagerSplit(w int) bool {
	a := m.pager.approval
	return !m.look.plain && w >= splitWidth && a.Git != nil && a.Git.Review != nil &&
		strings.Contains(a.Git.Review.Diff, "diff --git")
}

// pagerListWidth is the width of the review's file list.
func pagerListWidth(w int) int {
	return min(max(w/5, 24), 36)
}

// reviewWidth is the width the review is laid out in: what the file list leaves.
func (m *app) reviewWidth(w int) int {
	if m.pagerSplit(w) {
		return w - pagerListWidth(w) - 2
	}
	return w
}

// pagerFiles are the files of the review on show, with where each one starts.
func (m *app) pagerFiles() []diffFile {
	m.pagerLines(m.reviewWidth(m.width))
	return m.pager.files
}

// currentFile is the file whose diff the top of the view is in, or -1 above the first.
func (m *app) currentFile() int {
	cur := -1
	for i, f := range m.pagerFiles() {
		if f.line <= m.pager.scroll {
			cur = i
		}
	}
	return cur
}

// reviewLines lays out a git approval as the web's review does: the facts, the pull request,
// the commits and the diff, its hunks side by side when side is set. It returns where each
// file's diff starts.
func (m *app) reviewLines(a api.ApprovalView, w int, side bool) ([]string, []diffFile) {
	git := a.Git
	title := styleAccent.Render(kindTitle(a.Kind))
	if !m.look.plain {
		title = m.pal.hue(areaGit).Bold(true).Render(kindChip(a))
	}
	lines := []string{title + "  " + styleBold.Render(gitSummary(a)), ""}
	rv, pr := git.Review, git.PullRequest
	if rv != nil && !m.look.plain {
		lines[1] = m.changeStrip(rv.Files, rv.Additions, rv.Deletions)
		lines = append(lines, "")
	}
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
		if rv.Diff != "" {
			diff, files := m.diffLines(rv.Diff, w, side)
			for i := range files {
				files[i].line += len(lines)
			}
			return append(lines, diff...), files
		}
	}
	if rv == nil && pr == nil {
		lines = append(lines, "", styleDim.Render("Studio has no review of this push."))
	}
	return lines, nil
}

// changeStrip is the review's summary: the files, the lines added and removed, and their share
// as a bar.
func (m *app) changeStrip(files, adds, dels int) string {
	const w = 15
	n := 0
	if adds+dels > 0 {
		n = int(float64(adds) / float64(adds+dels) * w)
		if adds > 0 && n == 0 {
			n = 1
		}
	}
	return fmt.Sprintf("%d %s  ", files, plural(files, "file", "files")) + m.pal.added.Render(fmt.Sprintf("+%d", adds)) + " " +
		m.pal.removed.Render(fmt.Sprintf("−%d", dels)) + "  " + m.pal.added.Render(strings.Repeat("█", n)) +
		m.pal.removed.Render(strings.Repeat("▒", w-n))
}

// diffLines lays out a unified diff in w cells, as it comes or with its hunks side by side, and
// says where each file starts in it and how many lines it adds and removes.
func (m *app) diffLines(diff string, w int, side bool) ([]string, []diffFile) {
	var out []string
	var files []diffFile
	half := max((w-1)/2, 12)
	num := func(n int) string { return styleDim.Render(fmt.Sprintf("%4d ", n)) }
	added, removed := styleAdded, styleFailed
	if !m.look.plain {
		added, removed = m.pal.added, m.pal.removed
	}
	var dels, adds []string
	var delAt, addAt []int
	oldN, newN := 0, 0
	// flush pairs the removed lines of a run with the added ones, row by row, and marks the words
	// each pair changes: side by side, or the removed lines above the added ones.
	flush := func() {
		left, right := pairedLines(dels, adds, removed, added)
		if !side {
			for _, l := range left {
				out = append(out, removed.Render("-")+l)
			}
			for _, l := range right {
				out = append(out, added.Render("+")+l)
			}
			dels, adds, delAt, addAt = nil, nil, nil, nil
			return
		}
		for i := range max(len(left), len(right)) {
			l, r := strings.Repeat(" ", half), ""
			if i < len(left) {
				l = num(delAt[i]) + fitStyled(left[i], half-5)
			}
			if i < len(right) {
				r = num(addAt[i]) + fitStyled(right[i], half-5)
			}
			out = append(out, l+styleDim.Render("│")+r)
		}
		dels, adds, delAt, addAt = nil, nil, nil, nil
	}
	hunk := false
	for _, l := range strings.Split(strings.TrimRight(diff, "\n"), "\n") {
		if strings.HasPrefix(l, "diff --git") {
			flush()
			hunk = false
			path := strings.TrimPrefix(l, "diff --git ")
			if i := strings.LastIndex(l, " b/"); i >= 0 {
				path = l[i+3:]
			}
			files = append(files, diffFile{path: path, line: len(out)})
		}
		if strings.HasPrefix(l, "@@") {
			hunk = true
			_, _ = fmt.Sscanf(l, "@@ -%d", &oldN)
			if i := strings.Index(l, " +"); i >= 0 {
				_, _ = fmt.Sscanf(l[i+2:], "%d", &newN)
			}
		}
		if f := len(files) - 1; f >= 0 && hunk {
			switch {
			case strings.HasPrefix(l, "+"):
				files[f].adds++
			case strings.HasPrefix(l, "-"):
				files[f].dels++
			}
		}
		// The plain look shows the diff as it comes; the visual one marks changed words.
		if !hunk || (!side && m.look.plain) {
			flush()
			out = append(out, diffLine(l, added, removed))
			continue
		}
		text := strings.ReplaceAll(l, "\t", "    ")
		switch {
		case !side && !strings.HasPrefix(l, "+") && !strings.HasPrefix(l, "-"):
			flush()
			out = append(out, diffLine(l, added, removed))
		case strings.HasPrefix(l, "@@"):
			flush()
			out = append(out, styleAccent.Render(clip(text, w)))
		case strings.HasPrefix(l, "+"):
			adds, addAt = append(adds, text[1:]), append(addAt, newN)
			newN++
		case strings.HasPrefix(l, "-"):
			dels, delAt = append(dels, text[1:]), append(delAt, oldN)
			oldN++
		case strings.HasPrefix(l, "\\"):
			flush()
			out = append(out, styleDim.Render(text))
		default:
			flush()
			text = strings.TrimPrefix(text, " ")
			out = append(out, num(oldN)+fit(text, half-5)+styleDim.Render("│")+num(newN)+fit(text, half-5))
			oldN++
			newN++
		}
	}
	flush()
	return out, files
}

// diffLine colors one line of a unified diff, as the web's diff view does: added lines in
// added's style, removed ones in removed's.
func diffLine(l string, added, removed lipgloss.Style) string {
	l = strings.ReplaceAll(l, "\t", "    ")
	switch {
	case strings.HasPrefix(l, "+++"), strings.HasPrefix(l, "---"):
		return styleDim.Render(l)
	case strings.HasPrefix(l, "+"):
		return added.Render(l)
	case strings.HasPrefix(l, "-"):
		return removed.Render(l)
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

// pagerBody is the part of the review in view, h lines from the scroll position, with the file
// list beside it on a wide terminal.
func (m *app) pagerBody(w, h int) []string {
	lines := m.pagerLines(m.reviewWidth(w))
	p := &m.pager
	p.scroll = min(max(p.scroll, 0), max(len(lines)-h, 0))
	view := lines[p.scroll:min(p.scroll+h, len(lines))]
	if !m.pagerSplit(w) {
		return view
	}
	listW := pagerListWidth(w)
	return joinPanes(m.fileList(listW, h), view, listW, h)
}

// fileList is the review's files in w cells: the one in view under the accent's rule, each with
// its lines added and removed as a small bar.
func (m *app) fileList(w, h int) []string {
	files := m.pagerFiles()
	lines := []string{styleDim.Render(fmt.Sprintf("FILES  %d", len(files)))}
	cur := m.currentFile()
	most := 1
	for _, f := range files {
		most = max(most, f.adds+f.dels)
	}
	const barW = 6
	rows := max(h-1, 1)
	offset := 0
	if cur >= rows {
		offset = cur - rows + 1
	}
	for i := offset; i < len(files) && len(lines) < h; i++ {
		f := files[i]
		m.hitRow(len(lines), 0, w, rowFile, i)
		name := ansi.TruncateLeft(f.path, max(lipgloss.Width(f.path)-(w-3-barW-1), 0), "…")
		if i == cur {
			name = styleBold.Render(name)
		}
		lines = append(lines, m.cursorMark(i == cur)+fitStyled(name, w-3-barW-1)+" "+m.fileBar(f, most, barW))
	}
	return lines
}

// fileBar is a file's lines added and removed as a bar of at most w cells, scaled to the file
// that changes most.
func (m *app) fileBar(f diffFile, most, w int) string {
	total := f.adds + f.dels
	if total == 0 {
		return strings.Repeat(" ", w)
	}
	cells := max(int(float64(total)/float64(most)*float64(w)+0.5), 1)
	plus := int(float64(cells)*float64(f.adds)/float64(total) + 0.5)
	if f.adds > 0 && plus == 0 {
		plus = 1
	}
	if f.dels > 0 && plus == cells && cells > 1 {
		plus--
	}
	minus := cells - plus
	return m.pal.added.Render(strings.Repeat("+", plus)) + m.pal.removed.Render(strings.Repeat("−", minus)) +
		strings.Repeat(" ", w-cells)
}

// pagerKey scrolls the review and decides it, while it waits. A decision returns to the screen
// the review came from.
func (m *app) pagerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := &m.pager
	_, h := m.size()
	page := max(h-6, 1)
	a := p.approval
	switch {
	case key.Matches(msg, keyBack):
		m.screen = p.back
	case key.Matches(msg, keyPush, keyReject, keyIgnore) && !gitPending(a):
		m.setNotice(gitDecidedText(a), false)
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
		p.scroll = len(m.pagerLines(m.reviewWidth(w)))
	case key.Matches(msg, keyNextFile):
		for _, f := range m.pagerFiles() {
			if f.line > p.scroll {
				p.scroll = f.line
				break
			}
		}
	case key.Matches(msg, keyPrevFile):
		for _, f := range slices.Backward(m.pagerFiles()) {
			if f.line < p.scroll {
				p.scroll = f.line
				break
			}
		}
	case key.Matches(msg, keySide):
		p.unified = !p.unified
	case key.Matches(msg, keyPush):
		m.screen = p.back
		if a.Git.PullRequest != nil {
			return m, m.decide(card{approvals: []api.ApprovalView{a}}, Decision{Action: "allow", Scope: "sandbox"}, "Opening", "the pull request")
		}
		return m, m.decide(card{approvals: []api.ApprovalView{a}}, Decision{Action: "allow", Scope: "sandbox"}, "Approved", "the push of "+gitBranch(a))
	case key.Matches(msg, keyReject):
		m.screen = p.back
		return m, m.decide(card{approvals: []api.ApprovalView{a}}, Decision{Action: "deny", Scope: "sandbox"}, "Rejected", gitWhat(a))
	case key.Matches(msg, keyIgnore):
		m.screen = p.back
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
	bindings := []key.Binding{approve, keyReject, keyIgnore, keyScrollBar}
	if !gitPending(m.pager.approval) {
		bindings = []key.Binding{keyScrollBar}
	}
	if !m.look.plain && len(m.pagerFiles()) > 1 {
		bindings = append(bindings, keyFilesBar)
	}
	if !m.look.plain && m.width >= sideWidth {
		bindings = append(bindings, keySide)
	}
	return append(bindings, keyBack, keyHelp)
}

// isGit reports whether an approval is a push or pull request the pager can review.
func isGit(a api.ApprovalView) bool {
	return strings.HasPrefix(a.Kind, "git.") && a.Git != nil
}
