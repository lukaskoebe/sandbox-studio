package tui

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/api"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
)

// Home's own row shows what needs you: the approvals that wait, as cards that are decided on the
// spot, the sessions working now and what was decided earlier today, as the web's Home does. tab
// moves to the next card; a decision that made a rule can be undone with u for a while.

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
	keyUndo     = key.NewBinding(key.WithKeys("u"), key.WithHelp("u", "undo"))
	keyNextCard = key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next card"))
	keyAlways   = key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "always"))
	keyOnceY    = key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "allow once"))
)

// homeShows reports whether Home's row is on show, beside the list or instead of it.
func (m *app) homeShows() bool {
	return m.screen == screenHome && m.at == atHome && m.env.ID != "" && m.loaded
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

// digitCard reports whether a card answers the digits, which on Home otherwise go into the
// sessions: an agent's permission with more choices than y, a and n, and a memory conflict.
func digitCard(a api.ApprovalView) bool {
	return a.MemoryConflict != nil || a.ChatPermission != nil
}

// needsKey answers the card in focus on Home's row: tab moves to the next card, and the keys of
// the card decide it as on the approvals screen. The keys that move about Studio keep doing so.
// ok is false when the key isn't the card's.
func (m *app) needsKey(msg tea.KeyPressMsg) (cmd tea.Cmd, ok bool) {
	cards := m.cards()
	if m.at != atHome || len(cards) == 0 {
		return nil, false
	}
	m.approvalCursor = min(m.approvalCursor, len(cards)-1)
	c := cards[m.approvalCursor]
	a := c.first()
	switch {
	case key.Matches(msg, keyNextCard):
		m.approvalCursor = (m.approvalCursor + 1) % len(cards)
		m.blurButtons()
		return nil, true
	case key.Matches(msg, keyUp, keyDown, keyBack, keyHelp, keyActivity, keySettings, keyEnv, keyFilter, keyGit, keyUndo):
		return nil, false
	}
	if a.ChatPermission != nil && !c.group {
		if n := permissionAlias(msg, permissionPicks(a.ChatPermission.Options)); n > 0 {
			msg = pressOf(fmt.Sprint(n))
		}
	} else if key.Matches(msg, keyApprovals) {
		return nil, false
	}
	if digit(msg) > 0 && (!digitCard(a) || c.group) {
		return nil, false // a session's number
	}
	_, cmd = m.approvalsKey(msg)
	if m.screen == screenPager {
		m.pager.back = screenHome
	}
	return cmd, true
}

// needsPane is Home's row in w cells and h rows: the cards that wait, the one in focus with its
// keys, and then the sessions working now and today's decisions. The narrow layout shows the
// cards not in focus on a line each, and the teammates under them instead.
func (m *app) needsPane(w, h int, narrow bool) []string {
	cards := m.cards()
	m.approvalCursor = min(m.approvalCursor, max(len(cards)-1, 0))
	x := m.paneX()
	if narrow {
		x = 0
	}
	head := styleBold.Render("Needs you")
	if n := len(cards); n > 0 {
		head += m.waitingMark(fmt.Sprintf(" · %d", n))
	}
	hint := ""
	if len(cards) > 1 {
		hint = styleDim.Render("tab next card")
	}
	lines := []string{spread(head, hint, w), styleDim.Render(strings.Repeat("─", w))}
	if len(cards) == 0 {
		mark := styleDim.Render("✓")
		if !m.look.plain {
			mark = m.pal.running.Render("✓")
		}
		lines = append(lines, mark+" "+styleDim.Render(clip("Nothing needs you. New asks show up here, and as a count in the list.", w-2)))
	}
	// The cards, the one in focus kept in view.
	var block []string
	var keys []keyHit
	focusAt, focusLen := 0, 0
	for i, c := range cards {
		focused := i == m.approvalCursor
		var lines []string
		switch {
		case narrow && !focused:
			lines = []string{m.waitingMark("▲") + " " + clip(cardLine(c), w-2)}
		case m.look.plain:
			lines = m.cardLines(c, focused, w)
		default:
			var caps []keyHit
			lines, caps = m.visualCard(c, focused, w)
			for _, k := range caps {
				k.y += len(block)
				keys = append(keys, k)
			}
		}
		if focused {
			focusAt, focusLen = len(block), len(lines)
		}
		block = append(block, lines...)
	}
	room := h - len(lines)
	offset := 0
	if focusAt+focusLen > room {
		offset = min(focusAt+focusLen-room, focusAt)
	}
	top := len(lines)
	for i := offset; i < len(block) && len(lines) < h; i++ {
		lines = append(lines, block[i])
	}
	for _, k := range keys {
		if y := top + k.y - offset; y >= top && y < len(lines) {
			m.hitKeys(bodyTop+y, x+k.x0, []keySpan{{x1: k.x1 - k.x0, key: k.key}})
		}
	}
	if len(lines) < h {
		lines = append(lines, "")
	}
	if narrow {
		if len(lines)+1 < h {
			lines = append(lines, styleDim.Render("TEAMMATES"))
			rows := m.homeRows()
			if len(rows) > 0 && rows[0].kind == atHome {
				rows = rows[1:]
			}
			lines = append(lines, m.treeLines(rows, w, h-len(lines))...)
		}
		return lines
	}
	if working := m.workingNow(w); len(working) > 0 && len(lines)+2 < h {
		lines = append(lines, styleDim.Render("WORKING NOW"))
		lines = append(lines, working...)
		lines = append(lines, "")
	}
	if len(lines)+2 < h {
		lines = append(lines, styleDim.Render("EARLIER TODAY"))
		lines = append(lines, m.earlier(w, h-len(lines))...)
	}
	return lines
}

// narrowHome is Home's row on a narrow terminal, which has no room for the list beside it.
func (m *app) narrowHome(w, h int) []string {
	return m.needsPane(w, h, true)
}

// cardLine is a card on one line: its kind's glyph and what it asks.
func cardLine(c card) string {
	a := c.first()
	glyph := strings.SplitN(kindChip(a), " ", 2)[0]
	who := firstNonEmpty(approvalSource(a), "A sandbox")
	switch {
	case c.group:
		return fmt.Sprintf("%s %s wants %d hosts", glyph, who, len(c.approvals))
	case isNetwork(a):
		return glyph + " " + who + " wants " + hostOf(a)
	case isGit(a):
		return glyph + " " + gitSummary(a)
	}
	return glyph + " " + oneLine(summary(a))
}

// workingNow are the rows of the sessions that work or wait on you, in w cells.
func (m *app) workingNow(w int) []string {
	var out []string
	for _, sb := range m.sandboxes {
		for _, x := range m.sessionsOf(sb) {
			st := m.stateOf(x)
			if x.kind == sessionReview || (st != stateWorking && st != stateBlocked) {
				continue
			}
			who := firstNonEmpty(m.teammates[sb.TeammateID], sb.Name)
			right := styleDim.Render(sb.Name + " · " + m.sessionWords(x, st))
			out = append(out, spread(m.stateMark(st)+" "+styleBold.Render(fit(who, 8))+clip(x.title(), w/2), right, w))
		}
	}
	return out
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

// reviewPane is a review's row in the pane: what was pushed, and how to review it.
func (m *app) reviewPane(a api.ApprovalView, sb sandboxes.View, w int) []string {
	who := firstNonEmpty(m.teammates[sb.TeammateID], approvalSource(a))
	lines := []string{
		spread(styleBold.Render(clip(reviewTitle(a), w/2))+styleDim.Render(" · "+strings.Join(nonEmpty(who, sb.Name), " · ")),
			m.waitingMark("▲ review"), w),
		styleDim.Render(strings.Repeat("─", w)),
		clip(gitSummary(a), w),
	}
	if r := a.Git.Review; r != nil {
		lines = append(lines, "")
		for i, c := range r.Commits {
			if i == 6 {
				break
			}
			lines = append(lines, styleAccent.Render(clip(c.SHA, 7))+" "+clip(oneLine(c.Subject), w-8))
		}
		if more := len(r.Commits) - 6 + r.MoreCommits; more > 0 {
			lines = append(lines, styleDim.Render(fmt.Sprintf("+ %d more", more)))
		}
	}
	return append(lines, "", styleDim.Render(clip("enter reviews the diff: y approves and pushes, r asks for changes, n rejects.", w)))
}

// needsBindings are the keys the key bar shows on Home's row: the card in focus's, then the ways
// about Studio.
func (m *app) needsBindings() []key.Binding {
	cards := m.cards()
	var bindings []key.Binding
	if len(cards) > 1 {
		bindings = append(bindings, keyNextCard)
	}
	if len(cards) > 0 {
		c := cards[min(m.approvalCursor, len(cards)-1)]
		a := c.first()
		switch {
		case c.group:
			bindings = append(bindings, keyAllowAll, keyDenyAll, keySplit, keyScope)
		case isNetwork(a):
			bindings = append(bindings, keyAllow, keyDeny, keyOnce, keyPattern, keyScope)
		case isGit(a):
			bindings = append(bindings, keyReview)
		case a.ChatPermission != nil:
			bindings = append(bindings, keyOnceY, keyAlways, keyDeny)
		default:
			for _, k := range kindBindings(a) {
				if k.Help().Desc != "back" {
					bindings = append(bindings, k)
				}
			}
		}
	}
	if m.undoable() != nil {
		bindings = append(bindings, keyUndo)
	}
	return append(bindings, keyDown, keyNumber, keyActivity, keySettings, keyHelp, keyQuit, keyNew)
}
