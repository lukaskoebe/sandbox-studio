package tui

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/lukaskoebe/sandbox-studio/internal/api"
	"github.com/lukaskoebe/sandbox-studio/internal/chat"
)

// typeText types s into what has the keys, a key at a time.
func typeText(m *app, s string) *app {
	for _, r := range s {
		m = press(m, string(r))
	}
	return m
}

// cursorRef is what the line under the Changes tab's line cursor is.
func cursorRef(t *testing.T, m *app) diffRef {
	t.Helper()
	rows := m.changeRows()
	at := m.changeCursor(rows)
	if at < 0 {
		t.Fatal("no row under the rail's cursor")
	}
	w, _, pw := m.changesMainRect()
	_, _, refs, _ := m.mainView(rows[at], w, pw)
	i, ok := m.lineCursor(rows[at], refs)
	if !ok {
		t.Fatal("no line cursor")
	}
	return refs[i]
}

func TestDiffLinesSayWhatEachLineIs(t *testing.T) {
	m := sampleApp()
	_, _, refs := m.diffLines(sampleDiff, 80, false)
	var got []string
	for _, r := range refs {
		if r.line > 0 {
			mark := "+"
			if r.removed {
				mark = "-"
			} else if r.old > 0 {
				mark = " "
			}
			got = append(got, mark+r.file+":"+strconv.Itoa(r.line))
		}
	}
	want := []string{
		" src/login.tsx:10", " src/login.tsx:11", " src/login.tsx:12", "-src/login.tsx:13", "+src/login.tsx:13",
		" src/login.tsx:14", " src/login.tsx:15", " src/login.tsx:16",
		"+src/login.test.tsx:1", "+src/login.test.tsx:2", "+src/login.test.tsx:3", "+src/login.test.tsx:4", "+src/login.test.tsx:5", "+src/login.test.tsx:6",
	}
	if !slices.Equal(got, want) {
		t.Errorf("refs:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// Side by side, the row of a removed line and the added one beside it is the added line,
	// and holds the removed one too.
	m = visualApp(180, 50)
	lines, _, refs := m.diffLines(sampleDiff, 160, true)
	i := slices.IndexFunc(refs, func(r diffRef) bool { return r.file == "src/login.tsx" && r.line == 13 })
	if i < 0 || refs[i].removed || refs[i].left == 0 || !strings.Contains(ansi.Strip(lines[i]), "<div onClick") {
		t.Fatalf("the paired row: %+v %q", refs[i], ansi.Strip(lines[i]))
	}
	removed := slices.IndexFunc(refs, func(r diffRef) bool { return r.removed })
	if removed >= 0 {
		t.Errorf("no row side by side is the removed line alone: %+v", refs[removed])
	}
}

func TestComposeNoteAsTheWebDoes(t *testing.T) {
	comments := []lineComment{
		{diffRef: diffRef{file: "src/login.tsx", line: 13, raw: 9, code: `<button type="submit">Sign in</button>`}, text: "Keep the class names."},
		{diffRef: diffRef{file: "src/login.tsx", line: 13, raw: 8, removed: true, code: "<div onClick={submit}>Sign in</div>"}, text: "Why did this go?\nIt had a test."},
		{diffRef: diffRef{file: "src/login.test.tsx", line: 2, raw: 21}, text: "An empty line?"},
	}
	want := "Fix the empty email case.\n\n" +
		"Comments on the diff:\n" +
		"- src/login.tsx:13 (removed line), on `<div onClick={submit}>Sign in</div>`:\n  Why did this go?\n  It had a test.\n" +
		"- src/login.tsx:13, on `<button type=\"submit\">Sign in</button>`:\n  Keep the class names.\n" +
		"- src/login.test.tsx:2:\n  An empty line?"
	if got := composeNote("  Fix the empty email case.\n", comments); got != want {
		t.Errorf("note:\n%s\nwant:\n%s", got, want)
	}
	if got := composeNote("", comments[:1]); !strings.HasPrefix(got, "Comments on the diff:\n- src/login.tsx:13, on") {
		t.Errorf("comments alone: %q", got)
	}
	if got := composeNote(" ", nil); got != "" {
		t.Errorf("nothing: %q", got)
	}
}

func TestLineCommentsGoWithTheRequestForChanges(t *testing.T) {
	m := sampleApp()
	client, rec := recordingClient(t)
	m.client = client
	samplePush(m)
	m = press(onCard(m, 0), "enter", "c")
	if r := cursorRef(t, m); r.file != "src/login.tsx" || r.line != 10 {
		t.Fatalf("c puts the cursor on the diff's first line: %+v", r)
	}
	m = press(m, "down", "down", "down")
	if r := cursorRef(t, m); !r.removed || r.line != 13 {
		t.Fatalf("three lines down is the removed line: %+v", r)
	}
	m = press(m, "enter")
	if m.typing == nil || m.typing.label != "Comment on src/login.tsx:13 (removed line): " {
		t.Fatalf("enter asks for the comment: %+v", m.typing)
	}
	m = press(typeText(m, "Why did this go?"), "enter", "down", "enter")
	m = press(typeText(m, "Keep the class names."), "enter")
	if n := len(m.changes.comments["g1"]); n != 2 {
		t.Fatalf("%d comments collected", n)
	}
	if !strings.Contains(m.notice.text, "2 comments on the push: r sends them") {
		t.Errorf("notice %q", m.notice.text)
	}
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "╰ Keep the class names.") || !strings.Contains(view, "╰ Why did this go?") {
		t.Errorf("the comments aren't under their lines:\n%s", view)
	}

	// enter on a line with a comment edits it; esc lets go of the cursor, and the review keeps
	// the keys.
	m = press(m, "enter")
	if m.typing == nil || m.typing.text.value() != "Keep the class names." {
		t.Fatalf("enter on a commented line: %+v", m.typing)
	}
	m = press(m, "esc", "esc")
	if m.lineCursorOn() || m.comp.tab != tabChanges {
		t.Fatalf("esc lets go of the cursor and stays: cursor %v, tab %v", m.lineCursorOn(), m.comp.tab)
	}

	m = press(m, "r")
	if m.typing == nil || !strings.Contains(m.typing.label, "The 2 comments on lines go with it.") {
		t.Fatalf("r says the comments go with it: %+v", m.typing)
	}
	m, cmd := pressCmd(typeText(m, "Fix it"), "enter")
	m = run(m, cmd)
	rec.mu.Lock()
	requests := slices.Clone(rec.requests)
	rec.mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("calls: %+v", requests)
	}
	var decision struct{ Note string }
	_ = json.Unmarshal([]byte(requests[0].body), &decision)
	want := "Fix it\n\nComments on the diff:\n" +
		"- src/login.tsx:13 (removed line), on `<div onClick={submit}>Sign in</div>`:\n  Why did this go?\n" +
		"- src/login.tsx:13, on `<button type=\"submit\">Sign in</button>`:\n  Keep the class names."
	if decision.Note != want {
		t.Errorf("the decision's note:\n%s\nwant:\n%s", decision.Note, want)
	}
	var feedback struct {
		Note    string
		NewChat bool
	}
	_ = json.Unmarshal([]byte(requests[1].body), &feedback)
	if feedback.Note != want || !feedback.NewChat {
		t.Errorf("the feedback: %+v", feedback)
	}
	if _, ok := m.changes.comments["g1"]; ok {
		t.Error("the comments that went out stay")
	}
}

func TestLeaveTheCommentsOnThePush(t *testing.T) {
	m := sampleApp()
	client, rec := recordingClient(t)
	m.client = client
	samplePush(m)
	m = press(onCard(m, 0), "enter", "L")
	if !strings.Contains(m.notice.text, "No comments to leave") {
		t.Errorf("L without comments: %q", m.notice.text)
	}
	m = press(m, "c", "enter")
	m = press(typeText(m, "Return early here."), "enter")
	m, cmd := pressCmd(m, "L")
	m = run(m, cmd)
	f := rec.last(t)
	var body map[string]any
	_ = json.Unmarshal([]byte(f.body), &body)
	if f.path != "/api/environments/env1/approvals/g1/feedback" || len(body) != 1 ||
		body["note"] != "Comments on the diff:\n- src/login.tsx:10, on `return (`:\n  Return early here." {
		t.Errorf("L leaves the note alone: %s %s", f.path, f.body)
	}
	if !strings.Contains(m.notice.text, "Left 1 comment on fix-login for Finn to read.") {
		t.Errorf("notice %q", m.notice.text)
	}
	if len(m.changes.comments["g1"]) != 1 {
		t.Error("the comments left stay on show")
	}
}

func TestADecidedPushTakesNoComments(t *testing.T) {
	m := sampleApp()
	a := samplePush(m)
	m = press(onCard(m, 0), "enter")
	a.Status, a.Git.Push.State = "approved", "pushed"
	m.approvals = []api.ApprovalView{a}
	m.changes.pushed, m.changes.pushedOK = []api.ApprovalView{a}, true
	m = press(m, "c")
	if m.changes.lineRaw != 0 || !strings.Contains(m.notice.text, "nowhere to go") {
		t.Errorf("c on a decided push: cursor %d, notice %q", m.changes.lineRaw, m.notice.text)
	}
}

func TestACommentOnAChangeGoesToTheComposer(t *testing.T) {
	m := sampleChat(visualApp(140, 40))
	m.chats.composing = false
	m.comp.tab = tabChanges
	m = onLogin(withGitStatusOf(m, "sb1"))
	m.flipTo(true)
	m = press(m, "c", "down", "down", "down", "down")
	if r := cursorRef(t, m); r.file != "web/src/login.tsx" || r.line != 13 || r.removed {
		t.Fatalf("the cursor names the file in /workspace, the repository's path first: %+v", r)
	}
	m = press(m, "enter")
	if m.typing == nil || m.typing.label != "Comment on web/src/login.tsx:13 for Iris: " {
		t.Fatalf("enter asks for the comment: %+v", m.typing)
	}
	m = press(typeText(m, "Is this the only button?"), "enter")
	c := &m.chats
	if got := c.draft.value(); got != "@web/src/login.tsx:13 Is this the only button?" {
		t.Errorf("the draft: %q", got)
	}
	if !c.composing || m.focus != focusMain {
		t.Errorf("the composer has the keys: composing %v, focus %v", c.composing, m.focus)
	}
	if !slices.Equal(c.mentions, []chat.Mention{{Path: "web/src/login.tsx", Start: 13}}) {
		t.Errorf("mentions %+v", c.mentions)
	}

	// A second comment goes on a line of its own; a removed line mentions the file alone.
	m = press(m, "esc", "tab", "up", "enter")
	m = press(typeText(m, "Keep it"), "enter")
	want := "@web/src/login.tsx:13 Is this the only button?\n@web/src/login.tsx (removed line 13, `<div onClick={submit}>Sign in</div>`) Keep it"
	if got := c.draft.value(); got != want {
		t.Errorf("the draft:\n%s\nwant\n%s", got, want)
	}

	// The send carries the mentions the text still has.
	client, rec := recordingClient(t)
	m.client = client
	c.draft.set("@web/src/login.tsx:13 Is this the only button?")
	m = run(m, m.send())
	var body struct {
		Text     string
		Mentions []chat.Mention
	}
	_ = json.Unmarshal([]byte(rec.last(t).body), &body)
	if len(body.Mentions) != 1 || body.Mentions[0] != (chat.Mention{Path: "web/src/login.tsx", Start: 13}) {
		t.Errorf("sent %+v", body)
	}
	if len(m.chats.mentions) != 0 {
		t.Errorf("the mentions sent stay: %+v", m.chats.mentions)
	}
}

func TestACommentOnAChangeNeedsAThread(t *testing.T) {
	m := onLogin(withGitStatus(openComputer(sampleApp(), "sb2", tabChanges)))
	m = press(m, "c")
	if m.changes.lineRaw != 0 || !strings.Contains(m.notice.text, "goes to a thread on this computer") {
		t.Errorf("c without a thread: cursor %d, notice %q", m.changes.lineRaw, m.notice.text)
	}
}

func TestKeptMentions(t *testing.T) {
	a := chat.Mention{Path: "src/a.ts"}
	a3 := chat.Mention{Path: "src/a.ts", Start: 3}
	spaced := chat.Mention{Path: "my notes.md", Start: 2, End: 4}
	if got := mentionText(spaced); got != `@"my notes.md":2-4` {
		t.Errorf("mentionText %q", got)
	}
	for _, tc := range []struct {
		text string
		want []chat.Mention
	}{
		{"look at @src/a.ts:3 and @src/a.ts.", []chat.Mention{a, a3}},
		{"@src/a.tsx only", nil},
		{"@src/a.ts:30 only", nil},
		{"see @src/a.ts.bak", nil},
		{"x@src/a.ts", nil},
		{`(@"my notes.md":2-4)`, nil},
		{`about @"my notes.md":2-4, then`, []chat.Mention{spaced}},
	} {
		if got := keptMentions(tc.text, []chat.Mention{a, a3, a, spaced}); !slices.Equal(got, tc.want) {
			t.Errorf("%q keeps %+v, want %+v", tc.text, got, tc.want)
		}
	}
}

// A push that waits with a comment under its line, and the cursor on the next line.
func TestGoldenVisualLineComments(t *testing.T) {
	m := visualApp(80, 24)
	samplePush(m)
	m = press(onCard(m, 0), "enter", "c", "down", "down", "down", "enter")
	m = press(typeText(m, "Why did this go? The test clicks the div."), "enter", "down")
	goldenVisual(t, "visual_line_comments", m, true)
}

// A comment on a change, in the thread's composer.
func TestGoldenLineCommentInTheComposer(t *testing.T) {
	m := sampleChat(visualApp(140, 40))
	m.chats.composing = false
	m.comp.tab = tabChanges
	m = onLogin(withGitStatusOf(m, "sb1"))
	m.flipTo(true)
	m = press(m, "c", "down", "down", "down", "down", "enter")
	m = press(typeText(m, "Is this the only button?"), "enter")
	goldenVisual(t, "wide_line_comment_composer_140x40", m, false)
}
