package tui

import (
	"fmt"
	"net/http"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/lukaskoebe/sandbox-studio/internal/api"
	"github.com/lukaskoebe/sandbox-studio/internal/chat"
	"github.com/lukaskoebe/sandbox-studio/internal/gitreview"
)

// Comments on the lines of a diff in the Changes tab: c puts a cursor on main's lines, ↑ and ↓
// move it, enter asks for the comment and esc lets go. On a push that waits, the comments
// collect under their lines and go with r's note as the web's review writes them
// (docs/git-review.md, "Line comments"), or L leaves them on the push for its agent to read. On
// a change or a request, the comment goes into the thread's composer as @path:line, the file
// mentioned, for the next prompt.

// diffRef is what a line of a diff laid out is, for a comment on it: the file, the line's number
// in the new file or, removed, in the old one, and its code. A line that is no line of a file,
// such as a hunk's head, has no number.
type diffRef struct {
	file    string // the diff's path of the file; a change's or a request's path in /workspace
	line    int
	old     int // an unchanged line's number in the old file
	removed bool
	code    string // the line without its mark, trimmed, as the web quotes it
	raw     int    // its place in the diff as it came, from 1, which orders the comments
	left    int    // side by side, the removed line drawn beside it, by its place; 0 for none
}

// holds reports whether the line laid out is the diff's line raw of file, or has it beside it.
func (r diffRef) holds(file string, raw int) bool {
	return r.line > 0 && r.file == file && (r.raw == raw || r.left == raw)
}

// inFile names the file of a diff's lines.
func inFile(refs []diffRef, file string) []diffRef {
	for i := range refs {
		if refs[i].line > 0 {
			refs[i].file = file
		}
	}
	return refs
}

// lineComment is a comment on a line of a push's diff.
type lineComment struct {
	diffRef
	text string
}

// maxNote is the longest note the feedback endpoint takes.
const maxNote = 8000

// takesComments reports whether a is a push that waits, whose lines take comments: a decided
// one's comments have nowhere to go, and a pull request's are written on the forge.
func takesComments(a api.ApprovalView) bool {
	return a.Kind == gitreview.KindPush && a.Git != nil && gitPending(a)
}

// The keys of the line cursor.
var (
	keyComment       = key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "comment"))
	keyLeaveComments = key.NewBinding(key.WithKeys("L"), key.WithHelp("L", "leave the comments"))
	keySaveComment   = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "comment"))
	keyCursorLine    = key.NewBinding(key.WithKeys("up", "down"), key.WithHelp("↑↓", "line"))
	keyCursorDone    = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "done"))
)

// commentWhere is where a comment is, as the web names it: "cron/billing.go:13", with
// "(removed line)" after a line the change takes out.
func commentWhere(r diffRef) string {
	where := fmt.Sprintf("%s:%d", r.file, r.line)
	if r.removed {
		where += " (removed line)"
	}
	return where
}

// composeNote is the note the agent gets, as the web's review writes it: the general note, then
// each line comment, in the diff's order, with where it is and the line's code.
func composeNote(note string, comments []lineComment) string {
	var parts []string
	if n := strings.TrimSpace(note); n != "" {
		parts = append(parts, n)
	}
	if len(comments) > 0 {
		sorted := slices.SortedStableFunc(slices.Values(comments), func(a, b lineComment) int { return a.raw - b.raw })
		var b strings.Builder
		b.WriteString("Comments on the diff:")
		for _, c := range sorted {
			b.WriteString("\n- " + commentWhere(c.diffRef))
			if c.code != "" {
				b.WriteString(", on `" + c.code + "`")
			}
			b.WriteString(":\n  " + strings.ReplaceAll(strings.TrimSpace(c.text), "\n", "\n  "))
		}
		parts = append(parts, b.String())
	}
	return strings.Join(parts, "\n\n")
}

// mainView is what main shows for the row r in w cells, in a pane of pw cells: its lines, where
// each file starts, what each line of a diff is, and the scroll position that goes with them. A
// push that waits has its comments under their lines.
func (m *app) mainView(r changeRow, w, pw int) ([]string, []diffFile, []diffRef, *int) {
	if r.kind == crWaiting || r.kind == crPushed {
		m.reviewOn(r.approval)
		m.pagerLines(w, m.changesSide(pw))
		p := &m.pager
		if cs := m.changes.comments[r.approval.ID]; len(cs) > 0 && takesComments(r.approval) {
			lines, files, refs := withComments(p.lines, p.files, p.refs, cs, w)
			return lines, files, refs, &p.scroll
		}
		return p.lines, p.files, p.refs, &p.scroll
	}
	lines, files, refs := m.changeLines(r, w, m.changesSide(pw))
	return lines, files, refs, &m.changes.scroll
}

// withComments is a diff's lines with each comment dim under the line it is on, and where its
// files start then. The comments' lines take none.
func withComments(lines []string, files []diffFile, refs []diffRef, comments []lineComment, w int) ([]string, []diffFile, []diffRef) {
	out := make([]string, 0, len(lines)+2*len(comments))
	outRefs := make([]diffRef, 0, cap(out))
	moved := make([]int, len(lines)) // where each line went
	for i, l := range lines {
		moved[i] = len(out)
		out = append(out, l)
		var ref diffRef
		if i < len(refs) {
			ref = refs[i]
		}
		outRefs = append(outRefs, ref)
		for _, c := range comments {
			if !ref.holds(c.file, c.raw) {
				continue
			}
			for j, t := range wrapText(c.text, max(w-4, 8)) {
				mark := "    "
				if j == 0 {
					mark = "  ╰ "
				}
				out = append(out, styleDim.Render(mark+t))
				outRefs = append(outRefs, diffRef{})
			}
		}
	}
	shifted := slices.Clone(files)
	for i := range shifted {
		if f := shifted[i].line; f >= 0 && f < len(moved) {
			shifted[i].line = moved[f]
		}
	}
	return out, shifted, outRefs
}

// lineCursor is the line of main the cursor is on for the row r, among the lines refs describe.
// A cursor whose line is gone from the diff lets go.
func (m *app) lineCursor(r changeRow, refs []diffRef) (int, bool) {
	c := &m.changes
	if c.lineRaw == 0 || c.lineKey != r.key {
		return -1, false
	}
	for i, ref := range refs {
		if ref.holds(c.lineFile, c.lineRaw) {
			c.lineRaw = ref.raw // side by side, the row's own line
			return i, true
		}
	}
	c.lineRaw = 0
	return -1, false
}

// lineCursorOn reports whether the Changes tab has a line cursor, so esc lets go of it first.
func (m *app) lineCursorOn() bool {
	c := &m.changes
	if c.lineRaw == 0 || m.comp.tab != tabChanges {
		return false
	}
	rows := m.changeRows()
	at := m.changeCursor(rows)
	return at >= 0 && rows[at].key == c.lineKey
}

// scrollOff is how many lines main keeps in view under the line cursor, where its comment shows.
const scrollOff = 2

// putLineCursor puts the cursor on the line at of main, scrolled so it shows in h rows with
// scrollOff lines under it.
func (m *app) putLineCursor(r changeRow, ref diffRef, at int, scroll *int, h int) {
	c := &m.changes
	c.lineKey, c.lineFile, c.lineRaw = r.key, ref.file, ref.raw
	off := min(scrollOff, max(h-1, 0)/2)
	switch {
	case at < *scroll:
		*scroll = at
	case at+off >= *scroll+h:
		*scroll = at + off - h + 1
	}
}

// nextRef is the first line from from, going by dir, that a comment can be on; -1 for none.
func nextRef(refs []diffRef, from, dir int) int {
	for i := from; i >= 0 && i < len(refs); i += dir {
		if refs[i].line > 0 {
			return i
		}
	}
	return -1
}

// cursorIntoView moves the line cursor onto a line in view after main scrolled under it.
func (m *app) cursorIntoView(r changeRow, w, h, pw int) {
	_, _, refs, scroll := m.mainView(r, w, pw)
	at, ok := m.lineCursor(r, refs)
	if !ok || at >= *scroll && at < *scroll+h {
		return
	}
	i := nextRef(refs, *scroll, 1)
	if at >= *scroll+h || i < 0 || i >= *scroll+h {
		i = nextRef(refs, min(*scroll+h, len(refs))-1, -1)
	}
	if i >= 0 {
		m.putLineCursor(r, refs[i], i, scroll, h)
	}
}

// commentKey answers the keys of the line cursor on the row r, with main w cells wide and h rows
// high in a pane of pw cells, and reports whether the key was one of them.
func (m *app) commentKey(msg tea.KeyPressMsg, r changeRow, w, h, pw int) (tea.Cmd, bool) {
	review := r.kind == crWaiting || r.kind == crPushed
	if review && key.Matches(msg, keyLeaveComments) {
		return m.leaveComments(r.approval), true
	}
	_, _, refs, scroll := m.mainView(r, w, pw)
	at, on := m.lineCursor(r, refs)
	switch {
	case !on && key.Matches(msg, keyComment):
		m.startLineCursor(r, refs, scroll, h)
		return nil, true
	case !on:
		return nil, false
	case key.Matches(msg, keyComment):
		m.changes.lineRaw = 0
		return nil, true
	case key.Matches(msg, keyLineUp, keyLineDown):
		dir := 1
		if key.Matches(msg, keyLineUp) {
			dir = -1
		}
		if i := nextRef(refs, at+dir, dir); i >= 0 {
			m.putLineCursor(r, refs[i], i, scroll, h)
		}
		return nil, true
	case key.Matches(msg, keySaveComment):
		m.askComment(r, refs[at])
		return nil, true
	}
	return nil, false
}

// startLineCursor puts the cursor on the first line in view that a comment can be on, where a
// comment on it has somewhere to go.
func (m *app) startLineCursor(r changeRow, refs []diffRef, scroll *int, h int) {
	switch r.kind {
	case crRepo:
		m.setNotice("A repository has no lines to comment on: j goes to its changes.", false)
		return
	case crWaiting, crPushed:
		switch a := r.approval; {
		case !gitPending(a):
			m.setNotice(gitDecidedText(a)+" Its comments have nowhere to go.", false)
			return
		case !takesComments(a):
			m.setNotice("Only a push takes comments on its lines; a pull request's are written on the forge.", false)
			return
		}
	case crChange, crTurn:
		if sb := m.computerOn(); sb == nil || !m.composesOn(sb.ID) {
			m.setNotice("A comment on a change goes to a thread on this computer: open one, then c.", false)
			return
		}
	}
	i := nextRef(refs, *scroll, 1)
	if i < 0 {
		i = nextRef(refs, min(*scroll, len(refs)-1), -1)
	}
	if i < 0 {
		m.setNotice("No lines here to comment on.", false)
		return
	}
	m.putLineCursor(r, refs[i], i, scroll, h)
}

// askComment asks for the comment on the line ref: on a push that waits, one to collect with
// the others, which an empty one takes back; on a change or a request, one for the composer.
func (m *app) askComment(r changeRow, ref diffRef) {
	if r.kind == crWaiting || r.kind == crPushed {
		a := r.approval
		cs := m.changes.comments[a.ID]
		had := slices.IndexFunc(cs, func(c lineComment) bool { return c.file == ref.file && c.raw == ref.raw })
		p := &linePrompt{label: "Comment on " + commentWhere(ref) + ": ", enter: keySaveComment}
		if had >= 0 {
			p.text.set(cs[had].text)
		}
		p.submit = func(text string) (string, tea.Cmd) {
			text = strings.TrimSpace(text)
			cs := m.changes.comments[a.ID]
			switch {
			case text == "" && had < 0:
				return "write the comment, or esc", nil
			case text == "":
				cs = slices.Delete(cs, had, had+1)
			case had >= 0:
				cs[had].text = text
			default:
				cs = append(cs, lineComment{diffRef: ref, text: text})
			}
			if m.changes.comments == nil {
				m.changes.comments = map[string][]lineComment{}
			}
			m.changes.comments[a.ID] = cs
			m.setNotice(commentsSay(len(cs), firstNonEmpty(a.Git.Push.TeammateName, "its agent")), false)
			return "", nil
		}
		m.ask(p)
		return
	}
	mate := m.teammateName()
	m.ask(&linePrompt{
		label: "Comment on " + commentWhere(ref) + " for " + mate + ": ",
		enter: keySaveComment,
		submit: func(text string) (string, tea.Cmd) {
			text = strings.TrimSpace(text)
			if text == "" {
				return "write the comment, or esc", nil
			}
			sb := m.computerOn()
			if sb == nil || !m.composesOn(sb.ID) {
				return "the thread is gone: open one on this computer", nil
			}
			m.commentToComposer(ref, text)
			return "", nil
		},
	})
}

// commentsSay is what the notice says of the comments on a push.
func commentsSay(n int, who string) string {
	if n == 0 {
		return "No comments on the push."
	}
	return fmt.Sprintf("%d %s on the push: r sends %s with what to change, L leaves %s for %s.",
		n, plural(n, "comment", "comments"), plural(n, "it", "them"), plural(n, "it", "them"), who)
}

// composesOn reports whether the composer on show writes to a thread on the computer id.
func (m *app) composesOn(id string) bool {
	c := &m.chats
	place := m.here.kind == placeThread && m.here.term == "" || m.here.kind == placeMate
	return place && c.on && !c.reading && c.sandbox.ID == id && m.composerOpen()
}

// commentToComposer puts a comment on a line of a change into the thread's composer, on a line
// of its own: @path:line and the text, the file mentioned. A removed line is no line of the file
// now, so the comment mentions the file and says which line it was.
func (m *app) commentToComposer(ref diffRef, text string) {
	c := &m.chats
	mention := chat.Mention{Path: ref.file}
	say := mentionText(mention)
	if ref.removed {
		say += fmt.Sprintf(" (removed line %d", ref.line)
		if ref.code != "" {
			say += ", `" + ref.code + "`"
		}
		say += ")"
	} else {
		mention.Start = ref.line
		say = mentionText(mention)
	}
	draft := c.draft.value()
	if strings.TrimSpace(draft) != "" && !strings.HasSuffix(draft, "\n") {
		draft += "\n"
	}
	c.draft.set(draft + say + " " + text)
	c.mentions = append(c.mentions, mention)
	m.flipTo(false)
	c.composing = true
	m.setNotice("The comment is in the composer: enter sends it to "+m.teammateName()+".", false)
}

// mentionText is how a mention reads in a prompt, as the web writes it: @ and the path, quoted
// when it has spaces, and its lines.
func mentionText(m chat.Mention) string {
	p := m.Path
	if strings.ContainsAny(p, " \t\n\"") {
		p = `"` + strings.ReplaceAll(p, `"`, "") + `"`
	}
	switch {
	case m.Start == 0:
		return "@" + p
	case m.End > m.Start:
		return fmt.Sprintf("@%s:%d-%d", p, m.Start, m.End)
	}
	return fmt.Sprintf("@%s:%d", p, m.Start)
}

// keptMentions are the mentions the text still has, each once, as the web keeps them: one
// deleted from the text isn't sent.
func keptMentions(text string, mentions []chat.Mention) []chat.Mention {
	var out []chat.Mention
	seen := map[string]bool{}
	for _, m := range mentions {
		t := mentionText(m)
		if seen[t] || !hasMention(text, t) {
			continue
		}
		seen[t] = true
		out = append(out, m)
	}
	return out
}

// hasMention reports whether text has the mention t as a whole word: @a.ts isn't in @a.tsx or
// @a.ts:3, but is at the end of a sentence.
func hasMention(text, t string) bool {
	space := func(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' }
	for i := 0; i < len(text); {
		j := strings.Index(text[i:], t)
		if j < 0 {
			return false
		}
		j += i
		end := j + len(t)
		before, after := byte(' '), byte(' ')
		if j > 0 {
			before = text[j-1]
		}
		if end < len(text) {
			after = text[end]
		}
		// A dot that a word or a path goes on after is part of a longer name.
		if space(before) && (space(after) || strings.IndexByte(",.;!?)", after) >= 0) &&
			(after != '.' || end+1 >= len(text) || !wordByte(text[end+1])) {
			return true
		}
		i = j + 1
	}
	return false
}

// wordByte reports whether b goes on a word or a path: a letter, a digit, _ or /.
func wordByte(b byte) bool {
	return b == '_' || b == '/' || 'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z' || '0' <= b && b <= '9'
}

// leaveComments leaves the comments on a push that waits, for any agent on its computer to
// read, without deciding it.
func (m *app) leaveComments(a api.ApprovalView) tea.Cmd {
	if !isGit(a) {
		return nil
	}
	cs := m.changes.comments[a.ID]
	switch {
	case !gitPending(a):
		m.setNotice(gitDecidedText(a), false)
		return nil
	case !takesComments(a):
		m.setNotice("Only a push takes comments on its lines.", false)
		return nil
	case len(cs) == 0:
		m.setNotice("No comments to leave: c puts a cursor on the diff's lines.", false)
		return nil
	}
	note := composeNote("", cs)
	if len(note) > maxNote {
		m.setNotice("The comments are longer than a note takes: shorten some.", true)
		return nil
	}
	ctx, client := m.ctx, m.client
	who := firstNonEmpty(a.Git.Push.TeammateName, "its agent")
	done := fmt.Sprintf("Left %d %s on %s for %s to read.", len(cs), plural(len(cs), "comment", "comments"), gitBranch(a), who)
	return func() tea.Msg {
		rctx, cancel := requestContext(ctx)
		defer cancel()
		path := "/api/environments/" + a.EnvironmentID + "/approvals/" + a.ID + "/feedback"
		if err := client.Do(rctx, http.MethodPost, path, map[string]any{"note": note}, nil); err != nil {
			return actionDone{err: fmt.Errorf("could not leave the comments: %w", err)}
		}
		return actionDone{done: done}
	}
}

// cursorLine is the line under the line cursor, in w cells: its text reversed, in the accent's
// color while the computer has the keys.
func (m *app) cursorLine(l string, w int) string {
	style := lipgloss.NewStyle().Reverse(true)
	if m.computerKeys() {
		style = styleAccent.Reverse(true)
	}
	return style.Render(fit(ansi.Strip(l), w))
}

// commentBindings are the keys of the line cursor on the row r, while it has one, and the
// comments' keys otherwise.
func (m *app) commentBindings(r changeRow, on bool) []key.Binding {
	if on {
		return []key.Binding{keySaveComment, keyCursorLine, keyCursorDone}
	}
	var bindings []key.Binding
	switch r.kind {
	case crWaiting, crPushed:
		if !takesComments(r.approval) {
			return nil
		}
		bindings = append(bindings, keyComment)
		if n := len(m.changes.comments[r.approval.ID]); n > 0 {
			bindings = append(bindings, key.NewBinding(key.WithKeys("L"), key.WithHelp("L", fmt.Sprintf("leave %d %s", n, plural(n, "comment", "comments")))))
		}
	case crChange, crTurn:
		if sb := m.computerOn(); sb != nil && m.composesOn(sb.ID) {
			bindings = append(bindings, keyComment)
		}
	}
	return bindings
}
