package tui

import (
	"cmp"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/api"
	"github.com/lukaskoebe/sandbox-studio/internal/chat"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
)

// The Changes tab is the web's (web/src/components/computer/changes.tsx): a rail of what waits
// for your review, what went out in the last day, each repository's changes as git sees them and
// what the open thread changed, and beside it the row under the cursor: a push's review, a
// change's diff, a repository's facts or the files a request changed.

const (
	changesEvery     = 5 * time.Second  // how often the tab reads git again while it shows
	changesSlowEvery = 20 * time.Second // and while only its label does
	pushedFor        = 24 * time.Hour   // how long an allowed push stays in PUSHED
	pushedEvery      = 3                // the reads of git between two reads of PUSHED, 15 s
	railBeside       = 70               // the pane width from which the rail is beside main
)

// changeRowKind is what a row of the rail is.
type changeRowKind int

const (
	crSection changeRowKind = iota // WAITING FOR YOU, PUSHED, REPOSITORIES, THIS THREAD
	crNote                         // dim words: staged, not staged, untracked, clean, an error
	crWaiting                      // a push, pull request, review or comment that waits
	crPushed                       // one allowed in the last day
	crRepo                         // a repository's header: ▾ web · fix-login ↑2
	crChange                       // one change: M src/login.tsx +2 −2
	crTurn                         // a request of the thread that changed files
)

// changeRow is a row of the rail. The rows from crWaiting on take the cursor, by their key.
type changeRow struct {
	kind     changeRowKind
	key      string
	text     string
	count    int
	approval api.ApprovalView
	repo     int // the repository, by its index in the status
	change   int // the change, by its index in the repository's
	turn     chat.Turn
}

func (r changeRow) pickable() bool { return r.kind >= crWaiting }

// changeDiff is a change's diff as read, or the files a turn changed.
type changeDiff struct {
	sig       string // the change it was read for, so a change that moved on is read again
	diff      string
	truncated bool
	err       string
	files     []agentproto.FileChange
	more      int
}

// changesState is the Changes tab of the computer on show.
type changesState struct {
	sandbox   string
	gen       int // counts the computers shown, so the reads of one left behind are dropped
	status    []agentproto.GitRepo
	truncated bool   // more repositories than the status lists
	statusErr string // why git couldn't be read
	loaded    bool   // git's status arrived, or its error
	pushed    []api.ApprovalView
	pushedOK  bool
	busy      bool // a read is under way
	tick      int  // the tick that waits for the next read; 0 for none
	serial    int
	slow      bool // the tick that waits is the slow one
	reads     int
	ended     int // the ended requests of the open thread when git was read last

	cursor  string          // the row under the cursor, by its key
	at      int             // and its place among the rows that take it, for when it goes
	open    map[string]bool // the repositories enter opened (true) or folded, by path
	diffs   map[string]changeDiff
	asked   map[string]bool // the diffs being read, by row
	version int             // counts the diffs read, so main is laid out again

	scroll  int  // main's first line on show, for what isn't a review
	unified bool // v turned side by side off
	view    changeView

	// The line cursor c puts on main: the row it is for, and the line's file and place in its
	// diff, which stay as the layout changes; lineRaw 0 for none.
	lineKey  string
	lineFile string
	lineRaw  int
	comments map[string][]lineComment // a waiting push's comments on lines, by approval
}

// changeView is main laid out for a row and a width, which a long diff makes worth keeping.
type changeView struct {
	key     string
	version int
	width   int
	side    bool
	lines   []string
	files   []diffFile
	refs    []diffRef
}

type (
	changesLoaded struct {
		gen                    int
		sandbox                string
		readStatus, readPushed bool
		status                 api.GitStatus
		statusErr              error
		pushed                 []api.ApprovalView
		pushedErr              error
	}
	changesTick      struct{ gen, serial int }
	changeDiffLoaded struct {
		gen int
		key string
		d   changeDiff
	}
)

// The keys of the Changes tab. j and k move the rail's cursor, and the arrows scroll main.
var (
	keyRailDown   = key.NewBinding(key.WithKeys("j"))
	keyRailUp     = key.NewBinding(key.WithKeys("k"))
	keyRailBar    = key.NewBinding(key.WithKeys("j"), key.WithHelp("j/k", "change"))
	keyLineUp     = key.NewBinding(key.WithKeys("up"))
	keyLineDown   = key.NewBinding(key.WithKeys("down"), key.WithHelp("↑↓", "scroll"))
	keyFold       = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "fold"))
	keyChangesTab = key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "changes"))
)

// changesWanted is the computer whose changes the frame shows, in the Changes tab or its label,
// and whether the tab itself is on show.
func (m *app) changesWanted() (string, bool) {
	if m.screen != screenMain {
		return "", false
	}
	sb := m.computerOn()
	if sb == nil || m.mainPane() != paneComputer && m.sidePane() != paneComputer || !slices.Contains(m.computerTabs(), tabChanges) {
		return "", false
	}
	return sb.ID, m.comp.tab == tabChanges
}

// watchChanges reads git's status of the computer on show every changesEvery while its Changes
// tab shows and every changesSlowEvery while only the label does, at once when a request of the
// open thread ends, and PUSHED every pushedEvery reads while the tab shows. A stopped computer
// has no status to read. It also reads the diff of the row under the cursor.
func (m *app) watchChanges() tea.Cmd {
	id, active := m.changesWanted()
	if id == "" {
		return nil
	}
	c := &m.changes
	if c.sandbox != id {
		m.changes = changesState{sandbox: id, gen: c.gen + 1}
		c = &m.changes
	}
	var cmds []tea.Cmd
	ended := m.endedTurns(id)
	if !c.busy && (c.tick == 0 || active && c.slow || ended > c.ended) {
		ready := false
		if sb, ok := m.sandboxByID(id); ok {
			ready = phaseOf(sb) == phaseReady
		}
		pushed := active && (!c.pushedOK || c.reads%pushedEvery == 0)
		switch {
		case ready || pushed:
			c.busy, c.tick, c.ended = true, 0, ended
			c.reads++
			cmds = append(cmds, m.loadChanges(id, ready, pushed))
		case c.tick == 0:
			cmds = append(cmds, m.changesTickIn(active))
		}
	}
	return tea.Batch(append(cmds, m.watchChangeData())...)
}

// endedTurns counts the ended requests of the thread on show, if it is on the computer.
func (m *app) endedTurns(id string) int {
	c := &m.chats
	if !c.on || c.reading || c.sandbox.ID != id {
		return 0
	}
	n := 0
	for _, t := range c.turns {
		if t.EndedAt != nil {
			n++
		}
	}
	return n
}

// changesTickIn waits for the next read.
func (m *app) changesTickIn(active bool) tea.Cmd {
	c := &m.changes
	c.serial++
	c.tick, c.slow = c.serial, !active
	every := changesEvery
	if !active {
		every = changesSlowEvery
	}
	gen, serial := c.gen, c.serial
	return tea.Tick(every, func(time.Time) tea.Msg { return changesTick{gen: gen, serial: serial} })
}

// loadChanges reads git's status of a computer, and the environment's allowed approvals for
// PUSHED.
func (m *app) loadChanges(id string, status, pushed bool) tea.Cmd {
	ctx, client, env, gen := m.ctx, m.client, m.env.ID, m.changes.gen
	return func() tea.Msg {
		rctx, cancel := requestContext(ctx)
		defer cancel()
		out := changesLoaded{gen: gen, sandbox: id, readStatus: status, readPushed: pushed}
		if status {
			out.status, out.statusErr = client.GitStatus(rctx, env, id)
		}
		if pushed {
			out.pushed, out.pushedErr = client.ApprovalsWith(rctx, env, "approved")
		}
		return out
	}
}

// changesUpdate applies the messages of the Changes tab. ok is false for other messages.
func (m *app) changesUpdate(msg tea.Msg) (cmd tea.Cmd, ok bool) {
	c := &m.changes
	switch msg := msg.(type) {
	case changesLoaded:
		if msg.gen != c.gen {
			return nil, true
		}
		c.busy = false
		if msg.readStatus {
			c.loaded = true
			if msg.statusErr != nil {
				c.status, c.statusErr = nil, gitStatusError(msg.statusErr)
			} else {
				c.status, c.truncated, c.statusErr = msg.status.Repos, msg.status.Truncated, ""
			}
			c.view.lines = nil
		}
		if msg.readPushed && msg.pushedErr == nil {
			c.pushed, c.pushedOK = m.pushedOf(msg.sandbox, msg.pushed), true
		}
		_, active := m.changesWanted()
		return m.changesTickIn(active), true
	case changesTick:
		if msg.gen == c.gen && msg.serial == c.tick {
			c.tick = 0 // watchChanges reads again, while the tab or its label shows
		}
		return nil, true
	case changeDiffLoaded:
		if msg.gen != c.gen {
			return nil, true
		}
		delete(c.asked, msg.key)
		if c.diffs == nil {
			c.diffs = map[string]changeDiff{}
		}
		c.diffs[msg.key] = msg.d
		c.version++
		return nil, true
	}
	return nil, false
}

// gitStatusError says why git couldn't be read, in the TUI's words: an older guest needs a
// restart of the computer.
func gitStatusError(err error) string {
	text := err.Error()
	if strings.HasPrefix(text, "restart the sandbox") {
		return "Restart the computer to see its git changes: its agent is from an older Studio."
	}
	return "Git couldn't be read: " + text
}

// pushedOf are a computer's pushes and pull requests allowed in the last day, newest first.
func (m *app) pushedOf(id string, list []api.ApprovalView) []api.ApprovalView {
	var out []api.ApprovalView
	for _, a := range list {
		if a.SandboxID == id && (a.Kind == "git.push" || a.Kind == "git.pr") && a.Git != nil && !a.UpdatedAt.Before(m.now.Add(-pushedFor)) {
			out = append(out, a)
		}
	}
	slices.SortStableFunc(out, func(a, b api.ApprovalView) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	return out
}

// waitingOf are the reviews that wait on a computer: its pushes, pull requests, forge reviews
// and comments, oldest first, as Home lists them.
func (m *app) waitingOf(id string) []api.ApprovalView {
	var out []api.ApprovalView
	for _, a := range m.approvals {
		if a.SandboxID == id && strings.HasPrefix(a.Kind, "git.") && gitPending(a) && (a.Git != nil || a.ForgeReview != nil || a.ForgeComment != nil || a.ForgeResolve != nil) {
			out = append(out, a)
		}
	}
	slices.SortStableFunc(out, func(a, b api.ApprovalView) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return out
}

// changeCount is the changes of every repository, for the tab's label.
func (m *app) changeCount() int {
	n := 0
	for _, r := range m.changes.status {
		n += len(r.Changes)
	}
	return n
}

// areaOrder is the order of git's areas in the rail, and their names.
var areaOrder = map[string]int{"staged": 0, "unstaged": 1, "untracked": 2}

var areaNames = map[string]string{"staged": "staged", "unstaged": "not staged", "untracked": "untracked"}

// repoOpen reports whether a repository's changes show: the first one's at first, the others'
// once enter opens them.
func (m *app) repoOpen(i int, path string) bool {
	if v, ok := m.changes.open[path]; ok {
		return v
	}
	return i == 0
}

// changeRows are the rail's rows.
func (m *app) changeRows() []changeRow {
	c := &m.changes
	var rows []changeRow
	if waiting := m.waitingOf(c.sandbox); len(waiting) > 0 {
		rows = append(rows, changeRow{kind: crSection, text: "WAITING FOR YOU", count: len(waiting)})
		for _, a := range waiting {
			rows = append(rows, changeRow{kind: crWaiting, key: "review/" + a.ID, approval: a})
		}
	}
	var pushed []api.ApprovalView
	for _, a := range c.pushed {
		if !slices.ContainsFunc(rows, func(r changeRow) bool { return r.approval.ID == a.ID }) {
			pushed = append(pushed, a)
		}
	}
	if len(pushed) > 0 {
		rows = append(rows, changeRow{kind: crSection, text: "PUSHED", count: len(pushed)})
		for _, a := range pushed {
			rows = append(rows, changeRow{kind: crPushed, key: "review/" + a.ID, approval: a})
		}
	}
	if len(c.status) > 0 {
		rows = append(rows, changeRow{kind: crSection, text: "REPOSITORIES", count: len(c.status)})
	}
	for i, r := range c.status {
		rows = append(rows, changeRow{kind: crRepo, key: "repo/" + r.Path, repo: i})
		if !m.repoOpen(i, r.Path) {
			continue
		}
		switch {
		case r.Error != "":
			rows = append(rows, changeRow{kind: crNote, text: oneLine(r.Error)})
			continue
		case len(r.Changes) == 0:
			rows = append(rows, changeRow{kind: crNote, text: "clean"})
			continue
		}
		order := make([]int, len(r.Changes))
		for j := range order {
			order[j] = j
		}
		slices.SortStableFunc(order, func(a, b int) int {
			return cmp.Compare(areaOrder[r.Changes[a].Area], areaOrder[r.Changes[b].Area])
		})
		area := ""
		for _, j := range order {
			ch := r.Changes[j]
			if ch.Area != area {
				area = ch.Area
				rows = append(rows, changeRow{kind: crNote, text: areaNames[area]})
			}
			rows = append(rows, changeRow{kind: crChange, key: changeKey(r.Path, ch), repo: i, change: j})
		}
		if r.Truncated {
			rows = append(rows, changeRow{kind: crNote, text: "and more"})
		}
	}
	if turns := m.changedTurns(c.sandbox); len(turns) > 0 {
		rows = append(rows, changeRow{kind: crSection, text: "THIS THREAD", count: len(turns)})
		for _, t := range turns {
			rows = append(rows, changeRow{kind: crTurn, key: "turn/" + t.ID, turn: t})
		}
	}
	return rows
}

// changeKey names a change across reads: its repository, area and path.
func changeKey(repo string, ch agentproto.GitChange) string {
	return "change/" + repo + "|" + ch.Area + "|" + ch.Path
}

// changeSig is what a change is now, so a diff read for an earlier state of it is read again.
func changeSig(ch agentproto.GitChange) string {
	return fmt.Sprintf("%s|%s|%d|%d|%t", ch.Status, ch.From, ch.Added, ch.Removed, ch.Binary)
}

// changedTurns are the requests of the thread on show that changed files, newest first.
func (m *app) changedTurns(id string) []chat.Turn {
	c := &m.chats
	if !c.on || c.reading || c.sandbox.ID != id || m.here.kind != placeThread {
		return nil
	}
	var out []chat.Turn
	for _, t := range c.turns {
		if t.Changes != nil && len(t.Changes.Files) > 0 {
			out = append(out, t)
		}
	}
	slices.SortFunc(out, func(a, b chat.Turn) int { return cmp.Compare(b.Seq, a.Seq) })
	return out
}

// changeCursor is the index in rows of the row under the cursor, -1 for none: the row it was on,
// else the one at its place, else at first the first review that waits, the first change, the
// thread's newest request or the first row. The first pick waits for git's status.
func (m *app) changeCursor(rows []changeRow) int {
	c := &m.changes
	var picks []int
	for i, r := range rows {
		if r.pickable() {
			picks = append(picks, i)
		}
	}
	if len(picks) == 0 {
		return -1
	}
	for n, i := range picks {
		if rows[i].key == c.cursor {
			c.at = n
			return i
		}
	}
	at := -1
	if c.cursor != "" {
		at = picks[min(c.at, len(picks)-1)]
	} else {
		for _, kind := range []changeRowKind{crWaiting, crChange, crTurn} {
			if at = slices.IndexFunc(rows, func(r changeRow) bool { return r.kind == kind }); at >= 0 {
				break
			}
		}
		if at < 0 {
			at = picks[0]
		}
		sb, _ := m.sandboxByID(c.sandbox)
		if !c.loaded && phaseOf(sb) == phaseReady {
			return at // git's status may bring a better first pick
		}
	}
	c.cursor, c.at = rows[at].key, slices.Index(picks, at)
	return at
}

// moveChange moves the rail's cursor by d rows that take it, main showing the new row from its top.
func (m *app) moveChange(d int) {
	rows := m.changeRows()
	at := m.changeCursor(rows)
	if at < 0 {
		return
	}
	var picks []int
	for i, r := range rows {
		if r.pickable() {
			picks = append(picks, i)
		}
	}
	n := min(max(slices.Index(picks, at)+d, 0), len(picks)-1)
	c := &m.changes
	if rows[picks[n]].key != c.cursor {
		c.cursor, c.at, c.scroll = rows[picks[n]].key, n, 0
	}
}

// watchChangeData reads what the row under the cursor shows: a change's diff, or the files a
// request changed.
func (m *app) watchChangeData() tea.Cmd {
	c := &m.changes
	rows := m.changeRows()
	at := m.changeCursor(rows)
	if at < 0 {
		return nil
	}
	r := rows[at]
	if c.asked[r.key] {
		return nil
	}
	mark := func() {
		if c.asked == nil {
			c.asked = map[string]bool{}
		}
		c.asked[r.key] = true
	}
	switch r.kind {
	case crChange:
		repo := c.status[r.repo]
		ch := repo.Changes[r.change]
		sig := changeSig(ch)
		if d, ok := c.diffs[r.key]; ch.Binary || ok && d.sig == sig {
			return nil
		}
		mark()
		return m.loadChangeDiff(r.key, sig, repo.Path, ch)
	case crTurn:
		if _, ok := c.diffs[r.key]; ok {
			return nil
		}
		mark()
		return m.loadTurnChanges(r.key, r.turn)
	}
	return nil
}

// loadChangeDiff reads a change's diff.
func (m *app) loadChangeDiff(key, sig, repo string, ch agentproto.GitChange) tea.Cmd {
	ctx, client, env, id, gen := m.ctx, m.client, m.env.ID, m.changes.sandbox, m.changes.gen
	return func() tea.Msg {
		rctx, cancel := requestContext(ctx)
		defer cancel()
		d := changeDiff{sig: sig}
		out, err := client.GitDiff(rctx, env, id, repo, ch.Path, ch.From, ch.Area)
		if err != nil {
			d.err = gitStatusError(err)
		}
		d.diff, d.truncated = out.Diff, out.Truncated
		return changeDiffLoaded{gen: gen, key: key, d: d}
	}
}

// loadTurnChanges reads the files a request changed, with their diffs.
func (m *app) loadTurnChanges(key string, t chat.Turn) tea.Cmd {
	ctx, client, env, gen := m.ctx, m.client, m.env.ID, m.changes.gen
	return func() tea.Msg {
		rctx, cancel := requestContext(ctx)
		defer cancel()
		var d changeDiff
		out, err := client.TurnChanges(rctx, env, t.ChatID, t.ID)
		if err != nil {
			d.err = "The changes couldn't be read: " + err.Error()
		}
		d.files, d.more = out.Files, out.More
		return changeDiffLoaded{gen: gen, key: key, d: d}
	}
}

// changesTab is the Changes tab in w cells and h rows: the rail beside main from railBeside
// cells, else a line of the rail over it.
func (m *app) changesTab(sb sandboxes.View, w, h int) []string {
	rows := m.changeRows()
	at := m.changeCursor(rows)
	switch {
	case h <= 0:
		return nil
	case len(rows) == 0:
		return m.changesEmpty(sb, w)
	}
	var r *changeRow
	if at >= 0 {
		r = &rows[at]
	}
	if w < railBeside {
		head := []string{m.railLine(rows, at, w), styleDim.Render(strings.Repeat("─", w))}
		return append(head, m.changeMain(r, w, w, max(h-2, 1))...)
	}
	rw := railWidth(w)
	rail := m.railLines(rows, at, rw, h)
	return joinPanes(rail, m.changeMain(r, w-rw-2, w, h), rw, h)
}

// railWidth is the rail's width in a pane of w cells.
func railWidth(w int) int {
	return min(max(w/4, 24), 36)
}

// changesEmpty is the tab with nothing in its rail: why, and what would bring something.
func (m *app) changesEmpty(sb sandboxes.View, w int) []string {
	c := &m.changes
	say := func(text string) []string {
		var lines []string
		for _, l := range wrapText(text, min(w-2, 76)) {
			lines = append(lines, " "+styleDim.Render(l))
		}
		return lines
	}
	switch {
	case phaseOf(sb) != phaseReady:
		return append([]string{""}, say("Git's changes are read while "+sb.Name+" runs. Details, then s starts it.")...)
	case !c.loaded:
		return []string{"", " " + styleDim.Render("Reading git…")}
	case c.statusErr != "":
		return append([]string{""}, say(c.statusErr)...)
	}
	return append([]string{""}, say("No changes on "+sb.Name+": no git repository in /workspace, and nothing waits for your review.")...)
}

// railLines are the rail's rows in w cells and h rows, the cursor's row in view.
func (m *app) railLines(rows []changeRow, at, w, h int) []string {
	offset := 0
	if at >= h-1 {
		offset = at - h + 2
	}
	var lines []string
	for i := offset; i < len(rows) && len(lines) < h; i++ {
		if i > offset && rows[i].kind == crSection {
			if len(lines) == h-1 {
				break
			}
			lines = append(lines, "")
		}
		if rows[i].pickable() {
			m.hitRow(len(lines), 0, w, rowChange, i)
		}
		lines = append(lines, m.railRow(rows[i], i == at, w))
	}
	return lines
}

// railLine is the rail in one line of w cells, for a narrow pane: the row under the cursor and
// where it is among the rows.
func (m *app) railLine(rows []changeRow, at, w int) string {
	var picks []int
	for i, r := range rows {
		if r.pickable() {
			picks = append(picks, i)
		}
	}
	if at < 0 {
		return ""
	}
	n := slices.Index(picks, at) + 1
	where := styleDim.Render(fmt.Sprintf(" · %d of %d ›", n, len(picks)))
	row := m.railRow(rows[at], false, max(w-2-lipgloss.Width(where), 8))
	return styleDim.Render("‹") + strings.TrimRight(row, " ") + where
}

// railRow is one row of the rail in w cells.
func (m *app) railRow(r changeRow, cur bool, w int) string {
	c := &m.changes
	mark := "  "
	if cur {
		style := styleDim
		if m.computerKeys() {
			style = styleAccent
		}
		mark = style.Render("▸") + " "
		if !m.look.plain {
			mark = style.Render("▌") + " "
		}
	}
	switch r.kind {
	case crSection:
		head := styleDim.Render(r.text + fmt.Sprintf("  %d", r.count))
		if !m.look.plain {
			head = m.pal.hue(areaGit).Bold(true).Render(r.text) + styleDim.Render(fmt.Sprintf("  %d", r.count))
		}
		return " " + head
	case crNote:
		return "    " + styleDim.Render(clip(r.text, w-4))
	case crWaiting, crPushed:
		what, state := railReview(r.approval)
		if cur {
			what = styleBold.Render(what)
		}
		if r.kind == crWaiting {
			return mark + fitStyled(what, w-2)
		}
		if state == "failed" {
			state = styleFailed.Render(state)
		} else {
			state = styleDim.Render(state)
		}
		return mark + spread(clip(what, w-3-lipgloss.Width(state)), state, w-2)
	case crRepo:
		repo := c.status[r.repo]
		fold := "▾ "
		if !m.repoOpen(r.repo, repo.Path) {
			fold = "▸ "
		}
		name := firstNonEmpty(repo.Path, "workspace")
		if cur {
			name = styleBold.Render(name)
		}
		branch := firstNonEmpty(repo.Branch, "detached")
		if repo.Ahead > 0 {
			branch += fmt.Sprintf(" ↑%d", repo.Ahead)
		}
		if repo.Behind > 0 {
			branch += fmt.Sprintf(" ↓%d", repo.Behind)
		}
		n := ""
		if len(repo.Changes) > 0 {
			n = styleDim.Render(fmt.Sprint(len(repo.Changes)))
		}
		return mark + spread(fold+name+styleDim.Render(" · "+branch), n, w-2)
	case crChange:
		ch := c.status[r.repo].Changes[r.change]
		counts := m.lineCounts(ch.Added, ch.Removed, ch.Binary)
		room := max(w-2-2-lipgloss.Width(counts)-1, 4)
		path := ch.Path
		if lipgloss.Width(path) > room {
			path = ansi.TruncateLeft(path, lipgloss.Width(path)-room+1, "…")
		}
		if cur {
			path = styleBold.Render(path)
		}
		return mark + spread(m.changeMark(ch.Status)+" "+path, counts, w-2)
	case crTurn:
		files := r.turn.Changes.Files
		n := styleDim.Render(fmt.Sprintf("%d %s", len(files)+r.turn.Changes.More, plural(len(files)+r.turn.Changes.More, "file", "files")))
		what := firstNonEmpty(oneLine(r.turn.Prompt), "A request")
		if cur {
			what = styleBold.Render(what)
		}
		return mark + spread(clip(what, w-3-lipgloss.Width(n)), n, w-2)
	}
	return ""
}

// railReview is a review's words in the rail, as the web's: where it goes, and for a pushed one
// what became of it.
func railReview(a api.ApprovalView) (string, string) {
	switch {
	case a.ForgeComment != nil:
		c := a.ForgeComment
		return fmt.Sprintf("Comment on %s/%s#%d", c.Owner, c.Repo, c.Number), ""
	case a.ForgeResolve != nil:
		c := a.ForgeResolve
		return fmt.Sprintf("Threads of %s/%s#%d", c.Owner, c.Repo, c.Number), ""
	case a.ForgeReview != nil:
		d := a.ForgeReview.Draft
		return fmt.Sprintf("Review of %s/%s#%d", d.Owner, d.Repo, d.PullRequest.Number), ""
	case a.Git == nil:
		return a.Subject, ""
	}
	p := a.Git.Push
	pr := ""
	if i := strings.LastIndex(strings.TrimRight(p.PRURL, "/"), "/"); i >= 0 {
		pr = "#" + strings.TrimRight(p.PRURL, "/")[i+1:]
	}
	if a.Git.PullRequest != nil {
		return "Pull request from " + a.Git.PullRequest.Head, firstNonEmpty(pr, "opened")
	}
	state := firstNonEmpty(pr, "pushed")
	if p.State == "failed" {
		state = "failed"
	}
	return "Push to " + gitBranch(a), state
}

// changeMark is a change's letter, as git status's: A added, M modified, D deleted, R renamed,
// ! conflicted.
func (m *app) changeMark(status string) string {
	added, removed := styleAdded, styleFailed
	if !m.look.plain {
		added, removed = m.pal.added, m.pal.removed
	}
	switch status {
	case "added":
		return added.Render("A")
	case "deleted":
		return removed.Render("D")
	case "renamed":
		return styleAccent.Render("R")
	case "conflicted":
		return styleFailed.Bold(true).Render("!")
	}
	return styleAccent.Render("M")
}

// lineCounts are a change's lines added and removed, or that it is binary.
func (m *app) lineCounts(adds, dels int, binary bool) string {
	switch {
	case binary:
		return styleDim.Render("binary")
	case adds == 0 && dels == 0:
		return "" // untracked, or a conflict: git counts no lines
	}
	added, removed := styleAdded, styleFailed
	if !m.look.plain {
		added, removed = m.pal.added, m.pal.removed
	}
	var parts []string
	if adds > 0 {
		parts = append(parts, added.Render(fmt.Sprintf("+%d", adds)))
	}
	if dels > 0 {
		parts = append(parts, removed.Render(fmt.Sprintf("−%d", dels)))
	}
	return strings.Join(parts, " ")
}

// changeMain is what main shows for the row r in w cells and h rows, in a pane of pw cells,
// with the line cursor on its line.
func (m *app) changeMain(r *changeRow, w, pw, h int) []string {
	if r == nil {
		return nil
	}
	if (r.kind == crWaiting || r.kind == crPushed) && !isGit(r.approval) {
		return m.cardLines(card{approvals: []api.ApprovalView{r.approval}}, true, w)
	}
	lines, _, refs, scroll := m.mainView(*r, w, pw)
	*scroll = min(max(*scroll, 0), max(len(lines)-h, 0))
	view := lines[*scroll:min(*scroll+h, len(lines))]
	if at, ok := m.lineCursor(*r, refs); ok && at >= *scroll && at < *scroll+len(view) {
		view = slices.Clone(view)
		view[at-*scroll] = m.cursorLine(view[at-*scroll], w)
	}
	return view
}

// changesSide reports whether the tab shows diffs side by side in a pane of pw cells.
func (m *app) changesSide(pw int) bool {
	return !m.look.plain && pw >= sideWidth && !m.changes.unified
}

// reviewOn makes a the review on show, keeping how far it is scrolled while it is the same one.
func (m *app) reviewOn(a api.ApprovalView) {
	p := &m.pager
	switch {
	case p.approval.ID != a.ID:
		m.pager = pagerState{approval: a, openPR: defaultOpenPR(a), unified: p.unified}
	case p.approval.Status != a.Status || !p.approval.UpdatedAt.Equal(a.UpdatedAt):
		p.approval, p.lines = a, nil
	}
}

// changeLines lays out what main shows for a repository, a change or a request in w cells,
// where each file's diff starts, and what each line of a diff is.
func (m *app) changeLines(r changeRow, w int, side bool) ([]string, []diffFile, []diffRef) {
	c := &m.changes
	v := &c.view
	if v.lines != nil && v.key == r.key && v.version == c.version && v.width == w && v.side == side {
		return v.lines, v.files, v.refs
	}
	var lines []string
	var files []diffFile
	var refs []diffRef
	switch r.kind {
	case crRepo:
		lines = m.repoLines(c.status[r.repo], w)
	case crChange:
		lines, files, refs = m.changeDiffLines(r, w, side)
	case crTurn:
		lines, files, refs = m.turnLines(r, w, side)
	}
	*v = changeView{key: r.key, version: c.version, width: w, side: side, lines: lines, files: files, refs: refs}
	return lines, files, refs
}

// repoLines are a repository's facts: its branch, upstream and changes.
func (m *app) repoLines(r agentproto.GitRepo, w int) []string {
	lines := []string{styleBold.Render(clip("/workspace/"+r.Path, w)), ""}
	lines = append(lines, fact("Branch", firstNonEmpty(r.Branch, "detached")))
	if r.Head != "" {
		lines = append(lines, fact("Commit", shortSHA(r.Head)))
	}
	if r.Upstream != "" {
		lines = append(lines, fact("Upstream", fmt.Sprintf("%s · %d ahead, %d behind", r.Upstream, r.Ahead, r.Behind)))
	}
	for i, rm := range r.Remotes {
		label := ""
		if i == 0 {
			label = "Remotes"
		}
		lines = append(lines, fact(label, rm.Name+styleDim.Render(" → "+rm.Forge+"/"+rm.Repository)))
	}
	counts := map[string]int{}
	for _, ch := range r.Changes {
		counts[ch.Area]++
	}
	var parts []string
	for _, area := range []string{"staged", "unstaged", "untracked"} {
		if counts[area] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[area], areaNames[area]))
		}
	}
	lines = append(lines, fact("Changes", firstNonEmpty(strings.Join(parts, ", "), "none")))
	if r.Error != "" {
		lines = append(lines, "", styleFailed.Render(clip(oneLine(r.Error), w)))
	}
	return append(lines, "", styleDim.Render(clip("j and k go through its changes; enter folds them.", w)))
}

// changeDiffLines are a change's head and its diff, whose lines name the file by its path in
// /workspace.
func (m *app) changeDiffLines(r changeRow, w int, side bool) ([]string, []diffFile, []diffRef) {
	c := &m.changes
	repo := c.status[r.repo]
	ch := repo.Changes[r.change]
	name := ch.Path
	if ch.From != "" {
		name = ch.From + " → " + ch.Path
	}
	head := spread(styleBold.Render(clip(name, w-12)), m.lineCounts(ch.Added, ch.Removed, ch.Binary), w)
	where := areaNames[ch.Area] + " · " + firstNonEmpty(repo.Path, "workspace")
	lines := []string{head, styleDim.Render(clip(where, w)), ""}
	d, ok := c.diffs[r.key]
	switch {
	case ch.Binary:
		return append(lines, styleDim.Render("A binary file: git shows no lines of it.")), nil, nil
	case !ok:
		return append(lines, styleDim.Render("Reading the diff…")), nil, nil
	case d.err != "":
		return append(lines, styleFailed.Render(clip(d.err, w))), nil, nil
	case strings.TrimSpace(d.diff) == "":
		return append(lines, styleDim.Render("No lines changed.")), nil, nil
	}
	diff, files, refs := m.diffLines(hunksOnly(d.diff), w, side)
	for i := range files {
		files[i].line += len(lines)
	}
	refs = append(make([]diffRef, len(lines)), inFile(refs, path.Join(repo.Path, ch.Path))...)
	lines = append(lines, diff...)
	if d.truncated {
		lines = append(lines, "", styleDim.Render("The diff is cut at 512 KiB."))
	}
	return lines, files, refs
}

// hunksOnly is one file's diff from its first hunk: the head over it names the file already.
func hunksOnly(diff string) string {
	if strings.HasPrefix(diff, "@@") {
		return diff
	}
	if i := strings.Index(diff, "\n@@"); i >= 0 {
		return diff[i+1:]
	}
	return diff
}

// turnLines are the files a request changed, each with its diff.
func (m *app) turnLines(r changeRow, w int, side bool) ([]string, []diffFile, []diffRef) {
	c := &m.changes
	t := r.turn
	lines := []string{styleBold.Render(clip(firstNonEmpty(oneLine(t.Prompt), "A request"), w))}
	when := ""
	if t.EndedAt != nil {
		when = age(m.now.Sub(*t.EndedAt)) + " · "
	}
	n := len(t.Changes.Files) + t.Changes.More
	lines = append(lines, styleDim.Render(fmt.Sprintf("%s%d %s changed", when, n, plural(n, "file", "files"))), "")
	d, ok := c.diffs[r.key]
	switch {
	case !ok:
		return append(lines, styleDim.Render("Reading the changes…")), nil, nil
	case d.err != "":
		return append(lines, styleFailed.Render(clip(d.err, w))), nil, nil
	}
	var files []diffFile
	var refs []diffRef
	for _, f := range d.files {
		files = append(files, diffFile{path: f.Path, adds: f.Added, dels: f.Removed, line: len(lines)})
		lines = append(lines, spread(m.changeMark(f.Status)+" "+styleBold.Render(clip(f.Path, w-14)), m.lineCounts(f.Added, f.Removed, f.Binary), w))
		switch {
		case f.Binary:
			lines = append(lines, styleDim.Render("  A binary file."))
		case f.Diff == "":
			lines = append(lines, styleDim.Render("  Its diff is gone with the request's snapshot."))
		default:
			diff, _, dr := m.diffLines(hunksOnly(f.Diff), w, side)
			refs = append(append(refs, make([]diffRef, len(lines)-len(refs))...), inFile(dr, f.Path)...)
			lines = append(lines, diff...)
		}
		if f.Truncated {
			lines = append(lines, styleDim.Render("  The diff is cut."))
		}
		lines = append(lines, "")
	}
	if d.more > 0 {
		lines = append(lines, styleDim.Render(fmt.Sprintf("and %d more %s", d.more, plural(d.more, "file", "files"))))
	}
	return lines, files, refs
}

// computerKeys reports whether Studio's keys go to the computer pane.
func (m *app) computerKeys() bool {
	if m.screen != screenMain || m.sidebarKeys() || m.paneFocused() {
		return false
	}
	return m.focus == focusSide && m.sidePane() == paneComputer || m.focus == focusMain && m.mainPane() == paneComputer
}

// changesMainRect is the width of main in the tab and its rows, and the pane's width.
func (m *app) changesMainRect() (w, h, pw int) {
	rc, ok := m.paneRectOf(paneComputer)
	if !ok {
		return 0, 0, 0
	}
	pw, h = rc.w, rc.h-2 // the tab row and its rule
	if pw < railBeside {
		return pw, h - 2, pw
	}
	return pw - railWidth(pw) - 2, h, pw
}

// changesKey answers the keys of the Changes tab: j and k move the rail's cursor, the arrows,
// the pages and g and G scroll main, [ and ] go from file to file, v turns side by side off and
// on, enter folds a repository, and a review's keys decide it.
func (m *app) changesKey(msg tea.KeyPressMsg) tea.Cmd {
	c := &m.changes
	rows := m.changeRows()
	at := m.changeCursor(rows)
	switch {
	case key.Matches(msg, keyRailDown):
		m.moveChange(1)
		c.lineRaw = 0
		return nil
	case key.Matches(msg, keyRailUp):
		m.moveChange(-1)
		c.lineRaw = 0
		return nil
	case key.Matches(msg, keySide):
		c.unified = !c.unified
		m.pager.unified = c.unified
		return nil
	}
	if at < 0 {
		return nil
	}
	r := rows[at]
	if r.kind == crRepo && key.Matches(msg, keyFold) {
		if c.open == nil {
			c.open = map[string]bool{}
		}
		path := c.status[r.repo].Path
		c.open[path] = !m.repoOpen(r.repo, path)
		return nil
	}
	w, h, pw := m.changesMainRect()
	review := r.kind == crWaiting || r.kind == crPushed
	if review && !isGit(r.approval) {
		cd := card{approvals: []api.ApprovalView{r.approval}}
		if cardTakes(msg, cd) {
			return m.cardKey(msg, cd)
		}
		return nil
	}
	if cmd, ok := m.commentKey(msg, r, w, h, pw); ok {
		return cmd
	}
	lines, files, _, scroll := m.mainView(r, w, pw)
	if scrollKey(msg, scroll, len(lines), files, h) {
		m.cursorIntoView(r, w, h, pw)
		return nil
	}
	if review {
		return m.reviewKey(msg)
	}
	return nil
}

// wheelChanges answers the wheel over the Changes tab: over the rail it moves the rail's row,
// over main it scrolls main, which drawing keeps within what main shows.
func (m *app) wheelChanges(x, dir int) {
	if rc, ok := m.paneRectOf(paneComputer); ok && rc.w >= railBeside && x < rc.x+railWidth(rc.w) {
		m.moveChange(dir)
		return
	}
	rows := m.changeRows()
	at := m.changeCursor(rows)
	if at < 0 {
		return
	}
	scroll := &m.changes.scroll
	if r := rows[at]; r.kind == crWaiting || r.kind == crPushed {
		scroll = &m.pager.scroll
	}
	*scroll = max(*scroll+dir*wheelLines, 0)
	if w, h, pw := m.changesMainRect(); m.changes.lineRaw != 0 {
		lines, _, _, s := m.mainView(rows[at], w, pw)
		*s = min(*s, max(len(lines)-h, 0))
		m.cursorIntoView(rows[at], w, h, pw)
	}
}

// scrollKey scrolls a view of n lines, h of them on show, by its key: a line, a page, an end or
// a file. It reports whether the key was one of them.
func scrollKey(msg tea.KeyPressMsg, scroll *int, n int, files []diffFile, h int) bool {
	page := max(h-2, 1)
	switch {
	case key.Matches(msg, keyLineUp):
		*scroll--
	case key.Matches(msg, keyLineDown):
		*scroll++
	case key.Matches(msg, keyPageUp):
		*scroll -= page
	case key.Matches(msg, keyPageDn):
		*scroll += page
	case key.Matches(msg, keyTop):
		*scroll = 0
	case key.Matches(msg, keyBottom):
		*scroll = n
	case key.Matches(msg, keyNextFile):
		for _, f := range files {
			if f.line > *scroll {
				*scroll = f.line
				break
			}
		}
	case key.Matches(msg, keyPrevFile):
		for _, f := range slices.Backward(files) {
			if f.line < *scroll {
				*scroll = f.line
				break
			}
		}
	default:
		return false
	}
	*scroll = min(max(*scroll, 0), max(n-h, 0))
	return true
}

// changesBindings are the keys of the Changes tab, a review's first.
func (m *app) changesBindings() []key.Binding {
	rows := m.changeRows()
	if len(rows) == 0 {
		return nil
	}
	at := m.changeCursor(rows)
	var bindings []key.Binding
	review := false
	if at >= 0 {
		r := rows[at]
		if r.key == m.changes.lineKey && m.lineCursorOn() {
			return m.commentBindings(r, true)
		}
		switch {
		case (r.kind == crWaiting || r.kind == crPushed) && isGit(r.approval):
			m.reviewOn(r.approval)
			bindings, review = append(m.reviewBindings(), m.commentBindings(r, false)...), true
		case r.kind == crWaiting:
			for _, k := range kindBindings(r.approval) {
				if k.Help().Desc != "back" {
					bindings = append(bindings, k)
				}
			}
		case r.kind == crRepo:
			bindings = []key.Binding{keyFold}
		default:
			bindings = m.commentBindings(r, false)
		}
	}
	bindings = append(bindings, keyRailBar)
	if !review { // a review's decisions take the room: ? lists the rest
		bindings = append(bindings, keyLineDown)
		if !m.look.plain {
			bindings = append(bindings, keyFilesBar)
		}
	}
	if _, _, pw := m.changesMainRect(); !m.look.plain && pw >= sideWidth {
		bindings = append(bindings, keySide)
	}
	return bindings
}

// changesView shows the computer's Changes tab with Studio's keys: beside the thread or the
// teammate's home, or in its place below splitWidth.
func (m *app) changesView() {
	if m.computerOn() == nil {
		m.setNotice("No computer to show the changes of.", false)
		return
	}
	m.comp.tab = tabChanges
	if m.hasSide() {
		if m.layoutFor(m.width).side > 0 {
			m.focus = focusSide
		} else {
			m.flip = true
		}
	}
}

// changesLabel is the tab's name in its row: Changes and the count of changes, once read.
func (m *app) changesLabel() string {
	if n := m.changeCount(); n > 0 && m.changes.sandbox == m.comp.sandbox {
		return fmt.Sprintf("Changes %d", n)
	}
	return "Changes"
}
