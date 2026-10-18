package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/api"
)

// The Git screen is the web's Git page: the pushes and pull requests of the environment's
// sandboxes, the ones waiting for a review first, then the decided ones. Enter reviews one.

var keyGit = key.NewBinding(key.WithKeys("g"), key.WithHelp("g", "git"))

// gitDecided is how many decided pushes the screen lists, as the web does.
const gitDecided = 100

// gitState is the Git screen's list and its cursor.
type gitState struct {
	list   []api.ApprovalView // the waiting pushes, then the decided ones
	wait   int                // how many of list wait
	loaded bool
	cursor int
	back   screen // where esc returns to
	gen    int
}

// gitLoaded is the environment's git approvals, of every status.
type gitLoaded struct {
	gen  int
	list []api.ApprovalView
	err  error
}

// openGit shows the Git screen and reads its list.
func (m *app) openGit() tea.Cmd {
	g := &m.git
	if m.screen != screenGit && m.screen != screenPager {
		g.back = m.screen
		if g.back == screenChat || g.back == screenHelp {
			g.back = screenHome
		}
	}
	g.gen++
	m.screen = screenGit
	return m.loadGit()
}

// loadGit reads the environment's approvals and keeps its pushes and pull requests.
func (m *app) loadGit() tea.Cmd {
	ctx, client, env, gen := m.ctx, m.client, m.env.ID, m.git.gen
	return func() tea.Msg {
		rctx, cancel := requestContext(ctx)
		defer cancel()
		list, err := client.Approvals(rctx, env)
		return gitLoaded{gen: gen, list: list, err: err}
	}
}

// gitUpdate applies the Git screen's list.
func (m *app) gitUpdate(msg tea.Msg) (tea.Cmd, bool) {
	l, ok := msg.(gitLoaded)
	if !ok {
		return nil, false
	}
	if l.gen != m.git.gen {
		return nil, true
	}
	if l.err != nil {
		m.fail(l.err)
		return nil, true
	}
	var waiting, decided []api.ApprovalView
	for _, a := range l.list {
		switch {
		case !isGit(a):
		case a.Status == "pending":
			waiting = append(waiting, a)
		case len(decided) < gitDecided:
			decided = append(decided, a)
		}
	}
	g := &m.git
	g.list, g.wait, g.loaded = append(waiting, decided...), len(waiting), true
	g.cursor = min(g.cursor, max(len(g.list)-1, 0))
	return nil, true
}

// gitKey moves over the pushes and opens the one under the cursor.
func (m *app) gitKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	g := &m.git
	switch {
	case key.Matches(msg, keyBack):
		m.screen = g.back
	case key.Matches(msg, keyHelp):
		m.openHelp()
	case key.Matches(msg, keyUp):
		g.cursor = max(g.cursor-1, 0)
	case key.Matches(msg, keyDown):
		g.cursor = min(g.cursor+1, max(len(g.list)-1, 0))
	case key.Matches(msg, keyOpen) && g.cursor < len(g.list):
		m.openPager(g.list[g.cursor])
		m.pager.back = screenGit
	case key.Matches(msg, keyApprovals):
		m.openApprovals()
	case key.Matches(msg, keyActivity):
		return m, m.openActivity("")
	}
	return m, nil
}

// gitBindings are the keys the key bar shows on the Git screen.
func (m *app) gitBindings() []key.Binding {
	if len(m.git.list) == 0 {
		return []key.Binding{keyBack, keyHelp}
	}
	return []key.Binding{keyReview, keyUp, keyBack, keyHelp}
}

// gitBody lists the pushes: what each asks, its sandbox, where it stands and when it came.
func (m *app) gitBody(w, h int) []string {
	g := &m.git
	lines := []string{styleBold.Render("Git") + styleDim.Render(" · sandboxes push through Studio, which holds each push until you review it"), ""}
	switch {
	case !g.loaded:
		return append(lines, styleDim.Render("Loading…"))
	case len(g.list) == 0:
		return append(lines,
			"No pushes yet. When a sandbox pushes to one of your forges or opens a pull",
			"request, it waits here for your review.",
			"",
			styleDim.Render(clip("Forges are set up in the browser: "+m.browserURL("/e/"+m.env.ID+"/settings/git"), w)))
	}
	const colSandbox, colAge = 16, 8
	stateW := min(max(w/4, 16), 40) // the outcome, which says what the forge answered
	whatW := max(w-2-colSandbox-1-stateW-1-colAge, 12)
	// The rows a section's head takes above its first push, to keep the cursor in view.
	heads := func(i int) int {
		if i >= g.wait && g.wait > 0 {
			return 4
		}
		return 1
	}
	room := max(h-len(lines), 3)
	offset := 0
	if g.cursor+heads(g.cursor) >= room {
		offset = g.cursor + heads(g.cursor) - room + 1
	}
	section := func(title string, n int) {
		if len(lines) > 2 {
			lines = append(lines, "")
		}
		head := styleDim.Render(title + fmt.Sprintf("  %d", n))
		if !m.look.plain {
			head = m.pal.hue(areaGit).Bold(true).Render(title) + styleDim.Render(fmt.Sprintf("  %d", n))
		}
		lines = append(lines, head)
	}
	for i := offset; i < len(g.list) && len(lines) < h; i++ {
		switch {
		case i == offset && i < g.wait:
			section("WAITING FOR YOU", g.wait)
		case i == offset || i == g.wait:
			section("DECIDED", len(g.list)-g.wait)
		}
		a := g.list[i]
		m.hitRow(len(lines), 0, w, rowPush, i)
		what := fit(gitSummary(a), whatW)
		cursor := "  "
		if i == g.cursor {
			cursor, what = m.cursorMark(true), styleBold.Render(what)
		}
		sandbox := ""
		if a.Git.Review != nil {
			sandbox = a.Git.Review.Sandbox
		}
		lines = append(lines, cursor+what+" "+styleDim.Render(fit(firstNonEmpty(sandbox, approvalSource(a)), colSandbox))+" "+
			fitStyled(m.gitOutcome(a), stateW)+" "+styleDim.Render(age(m.now.Sub(a.CreatedAt))))
	}
	return lines
}

// gitOutcome is where a push stands: waiting, or what came of it, as the web's outcome says.
func (m *app) gitOutcome(a api.ApprovalView) string {
	p := a.Git.Push
	if a.Status == "pending" {
		return m.dotted("waiting", "waiting")
	}
	if a.Git.PullRequest != nil {
		switch {
		case p.PRURL != "":
			return "pull request opened"
		case p.PRResult != "":
			return styleDim.Render(oneLine(p.PRResult))
		}
		return styleDim.Render(a.Status)
	}
	text := p.State
	if p.Result != "" {
		text += styleDim.Render(" · " + oneLine(p.Result))
	}
	if p.State == "failed" {
		return m.dotted("failed", text)
	}
	return text
}

// dotted is text after a status dot of the visual look, or the text alone in the plain one.
func (m *app) dotted(status, text string) string {
	if m.look.plain {
		return text
	}
	return m.pal.dot(status) + " " + text
}

// gitPending reports whether a review in the pager can still be decided.
func gitPending(a api.ApprovalView) bool {
	return a.Status == "" || a.Status == "pending"
}

// gitDecidedText says how a push the pager shows was decided, for the key bar's place.
func gitDecidedText(a api.ApprovalView) string {
	return "Decided: " + strings.ToLower(firstNonEmpty(a.Git.Push.State, a.Status)) + "."
}
