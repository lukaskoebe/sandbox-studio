package tui

import (
	"fmt"
	"net/http"
	"slices"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/api"
)

// Home's own row reads what was decided today, for EARLIER TODAY under Home's list (inbox.go),
// and keeps the last decision that made a rule, which u undoes for a while.

// undoFor is how long u undoes the last decision.
const undoFor = time.Minute

// homeEvery is how often Home reads the decisions again while it shows them.
const homeEvery = 5 * time.Second

// homeState is what Home's row reads beyond the approvals that wait.
type homeState struct {
	env     string
	decided []api.ApprovalView // the environment's approvals, every status
	loaded  bool
	busy    bool // a read, or the wait for the next one, is under way
	gen     int
	undo    *undoState // the last decision, while u undoes it
}

// undoState is a decision that made a rule: undoing it deletes the rule, so the next request
// asks again.
type undoState struct {
	env, rule, what string
	until           time.Time
}

type (
	homeLoaded struct {
		gen  int
		list []api.ApprovalView
		err  error
	}
	homeTick struct{ gen int }
)

var (
	keyUndo   = key.NewBinding(key.WithKeys("u"), key.WithHelp("u", "undo"))
	keyAlways = key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "always"))
	keyOnceY  = key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "allow once"))
)

// homeShows reports whether Home is on show.
func (m *app) homeShows() bool {
	return m.inboxShows()
}

// watchHome reads the environment's decisions while Home's row shows, again every homeEvery.
func (m *app) watchHome() tea.Cmd {
	h := &m.home
	if m.client == nil || !m.homeShows() || h.busy {
		return nil
	}
	if h.env != m.env.ID {
		*h = homeState{env: m.env.ID, gen: h.gen + 1, undo: h.undo}
	}
	h.busy = true
	ctx, client, env, gen := m.ctx, m.client, m.env.ID, h.gen
	return func() tea.Msg {
		rctx, cancel := requestContext(ctx)
		defer cancel()
		list, err := client.Approvals(rctx, env)
		return homeLoaded{gen: gen, list: list, err: err}
	}
}

// homeUpdate applies the messages of Home's row. ok is false for other messages.
func (m *app) homeUpdate(msg tea.Msg) (cmd tea.Cmd, ok bool) {
	h := &m.home
	switch msg := msg.(type) {
	case homeLoaded:
		if msg.gen != h.gen {
			return nil, true
		}
		if msg.err == nil {
			h.decided, h.loaded = msg.list, true
		}
		gen := h.gen
		return tea.Tick(homeEvery, func(time.Time) tea.Msg { return homeTick{gen} }), true
	case homeTick:
		if msg.gen == h.gen {
			h.busy = false
		}
		return nil, true
	}
	return nil, false
}

// undoable is the last decision while u still undoes it.
func (m *app) undoable() *undoState {
	if u := m.home.undo; u != nil && m.now.Before(u.until) {
		return u
	}
	return nil
}

// undo deletes the rule the last decision made.
func (m *app) undo() tea.Cmd {
	u := m.undoable()
	m.home.undo = nil
	if u == nil {
		m.setNotice("Nothing to undo.", false)
		return nil
	}
	ctx, client := m.ctx, m.client
	return func() tea.Msg {
		rctx, cancel := requestContext(ctx)
		defer cancel()
		err := client.Do(rctx, http.MethodDelete, "/api/environments/"+u.env+"/rules/"+u.rule, nil, nil)
		if err != nil && apiStatus(err) != http.StatusNotFound {
			return actionDone{err: fmt.Errorf("could not undo: %w", err)}
		}
		return actionDone{done: "Undone: " + u.what + ". The rule is gone; the next request asks again."}
	}
}

// permissionAlias is the pick y, a or n stands for on an agent's permission request: allow once,
// always allow and deny once, as the web's buttons. It is 0 for another key or a missing option.
func permissionAlias(msg tea.KeyPressMsg, picks []pick) int {
	want := map[string]pick{"y": {action: "allow"}, "a": {action: "allow", remember: true}, "n": {action: "deny"}}
	w, ok := want[msg.String()]
	if !ok {
		return 0
	}
	for i, p := range picks {
		if p.action == w.action && p.remember == w.remember && i < 9 {
			return i + 1
		}
	}
	return 0
}

// earlier are today's decisions in the environment, newest first, at most h of them.
func (m *app) earlier(w, h int) []string {
	hs := &m.home
	if !hs.loaded {
		return []string{styleDim.Render("Loading…")}
	}
	y, mo, d := m.now.Date()
	var list []api.ApprovalView
	for _, a := range hs.decided {
		ay, amo, ad := a.UpdatedAt.Local().Date()
		if a.Status != "pending" && ay == y && amo == mo && ad == d {
			list = append(list, a)
		}
	}
	slices.SortStableFunc(list, func(x, y api.ApprovalView) int { return y.UpdatedAt.Compare(x.UpdatedAt) })
	if len(list) == 0 {
		return []string{styleDim.Render("Nothing decided yet today.")}
	}
	var out []string
	for _, a := range list {
		if len(out) >= h {
			break
		}
		out = append(out, m.decisionMark(a)+" "+styleDim.Render(a.UpdatedAt.Local().Format("15:04"))+"  "+
			styleBold.Render(fit(firstNonEmpty(approvalSource(a), "—"), 12))+" "+clip(decisionText(a), max(w-22, 8)))
	}
	return out
}

// decisionMark is a decision's mark: ✓ allowed, ✕ denied, · ignored.
func (m *app) decisionMark(a api.ApprovalView) string {
	switch a.Status {
	case "approved":
		if m.look.plain {
			return styleAccent.Render("✓")
		}
		return m.pal.running.Render("✓")
	case "denied":
		return styleFailed.Render("✕")
	}
	return styleDim.Render("·")
}

// decisionText says what was decided: "Allowed registry.npmjs.org", "Pushed fix/x".
func decisionText(a api.ApprovalView) string {
	switch {
	case isNetwork(a):
		return decisionWords(a) + " " + hostOf(a)
	case isGit(a) && a.Status == "approved":
		if a.Git.PullRequest != nil {
			return "Opened a pull request " + reviewTitle(a)
		}
		return "Pushed " + reviewTitle(a)
	case isGit(a):
		return map[string]string{"denied": "Rejected the push to ", "dismissed": "Ignored the push to "}[a.Status] + reviewTitle(a)
	}
	word := map[string]string{"approved": "Allowed", "denied": "Denied", "dismissed": "Ignored"}[a.Status]
	return firstNonEmpty(word, a.Status) + ": " + oneLine(summary(a))
}
