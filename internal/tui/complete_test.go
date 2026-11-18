package tui

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/api"
	"github.com/lukaskoebe/sandbox-studio/internal/chat"
)

// sentPrompt is the last prompt rec got, as Studio reads it.
func sentPrompt(t *testing.T, rec *recorder) api.PromptRequest {
	t.Helper()
	var p api.PromptRequest
	if err := json.Unmarshal([]byte(rec.last(t).body), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

// composing opens the composer of the thread Fix the login form, on blub.
func composing(m *app) *app {
	return press(sampleChat(m), "p")
}

// menuLabels are the labels of the composer's menu, in order.
func menuLabels(m *app) []string {
	var out []string
	for _, it := range m.menuItems() {
		out = append(out, menuLabel(it))
	}
	return out
}

func TestSlashOpensTheCommandsOnlyAtTheStart(t *testing.T) {
	m := typeText(composing(sampleApp()), "/")
	got := menuLabels(m)
	for _, want := range []string{"/term", "/files", "/changes", "/browser", "/details", "/home", "/search", "/keys"} {
		if !slices.Contains(got, want) {
			t.Errorf("/ lists %v, without %s", got, want)
		}
	}
	// blub runs and the thread is idle: no /start, no /stop of a turn.
	for _, not := range []string{"/start", "/stop"} {
		if slices.Contains(got, not) {
			t.Errorf("/ lists %s: %v", not, got)
		}
	}
	m = typeText(composing(sampleApp()), "see /tmp")
	if a, _ := m.menu(); a.kind != menuNone {
		t.Errorf("a / inside the text opens the menu: %+v", a)
	}
	m = typeText(composing(sampleApp()), "/tmp is full")
	if a, _ := m.menu(); a.kind != menuNone {
		t.Errorf("a / followed by words keeps the menu: %+v", a)
	}
}

func TestSlashFiltersAndTabCompletes(t *testing.T) {
	m := typeText(composing(sampleApp()), "/ch")
	if got := menuLabels(m); len(got) == 0 || got[0] != "/changes" {
		t.Fatalf("/ch lists %v", got)
	}
	m = typeText(composing(sampleApp()), "/stop")
	if got := menuLabels(m); !slices.Equal(got, []string{"/stop-computer"}) {
		t.Fatalf("/stop lists %v", got)
	}
	m = press(typeText(composing(sampleApp()), "/br"), "tab")
	if got := m.chats.draft.value(); got != "/browser" {
		t.Fatalf("tab completes to %q", got)
	}
	// A typo still finds it.
	m = typeText(composing(sampleApp()), "/chnges")
	if got := menuLabels(m); len(got) == 0 || got[0] != "/changes" {
		t.Fatalf("/chnges lists %v", got)
	}
}

func TestEnterRunsTheCommand(t *testing.T) {
	for _, c := range []struct {
		draft string
		check func(m *app) bool
	}{
		{"/files", func(m *app) bool { return m.comp.tab == tabFiles && m.here.chat == "c1" }},
		{"/changes", func(m *app) bool { return m.comp.tab == tabChanges }},
		{"/details", func(m *app) bool { return m.comp.tab == tabDetails }},
		{"/home", func(m *app) bool { return m.here.kind == placeHome }},
		{"/search", func(m *app) bool { return m.search.open }},
		{"/keys", func(m *app) bool { return m.screen == screenHelp }},
		{"/new", func(m *app) bool { return m.here.kind == placeThread && m.here.chat == "" && m.here.mate == "t1" }},
		{"/threads", func(m *app) bool { return m.sidebarKeys() }},
	} {
		m := press(typeText(composing(sampleApp()), c.draft), "enter")
		if m.chats.draft.value() != "" || !c.check(m) {
			t.Errorf("%s: draft %q, here %+v, tab %v, screen %v", c.draft, m.chats.draft.value(), m.here, m.comp.tab, m.screen)
		}
	}
}

// A draft that starts with / and names no command goes as text.
func TestAnUnknownCommandIsSent(t *testing.T) {
	client, rec := recordingClient(t)
	m := composing(sampleApp())
	m.client = client
	m = typeText(m, "/usr")
	if got := m.menuEmpty(); got != "No command is called /usr: enter sends it as text." {
		t.Fatalf("empty %q", got)
	}
	pressRun(t, m, "enter")
	if p := sentPrompt(t, rec); p.Text != "/usr" {
		t.Fatalf("sent %q", p.Text)
	}
}

func TestEscClosesTheMenuAndKeepsTheText(t *testing.T) {
	m := press(typeText(composing(sampleApp()), "/te"), "esc")
	if a, _ := m.menu(); a.kind != menuNone || m.chats.draft.value() != "/te" || !m.chats.composing {
		t.Fatalf("esc: menu %+v, draft %q, composing %v", a, m.chats.draft.value(), m.chats.composing)
	}
	// It stays closed while the / does, and enter sends the text.
	if m = typeText(m, "x"); func() bool { a, _ := m.menu(); return a.kind != menuNone }() {
		t.Fatal("the menu opened again on the next letter")
	}
	// A second esc closes the composer, as always.
	if m = press(m, "esc"); m.chats.composing {
		t.Fatal("the second esc keeps the composer")
	}
	// Once the / is gone, a new one opens it again.
	m = press(m, "p", "backspace", "backspace", "backspace", "backspace", "/")
	if a, _ := m.menu(); a.kind != menuSlash {
		t.Fatalf("a new / doesn't open it: %+v, draft %q", a, m.chats.draft.value())
	}
}

func TestArrowsMoveTheMenu(t *testing.T) {
	m := typeText(composing(sampleApp()), "/")
	n := len(m.menuItems())
	if m = press(m, "up"); m.chats.menuSel != n-1 {
		t.Fatalf("up from the first: %d of %d", m.chats.menuSel, n)
	}
	if m = press(m, "down"); m.chats.menuSel != 0 {
		t.Fatalf("down from the last: %d", m.chats.menuSel)
	}
	if m = press(m, "down", "down", "enter"); m.comp.tab != tabChanges {
		t.Fatalf("the third command runs: tab %v", m.comp.tab)
	}
}

func TestAtListsTheThreadsComputerFiles(t *testing.T) {
	m := withFileIndex(composing(sampleApp()), "sb1")
	m = typeText(m, "fix @")
	if got := menuLabels(m); len(got) == 0 || got[0] != "web/src/login.tsx" {
		t.Fatalf("@ lists %v: the changed file first", got)
	}
	m = typeText(m, "log")
	if got := menuLabels(m); !slices.Equal(got, []string{"web/src/login.tsx", "docs/logging.md", "web/src/login.test.tsx"}) {
		t.Fatalf("@log lists %v: the changed one first", got)
	}
	// Not inside a word: an address isn't a mention.
	m = typeText(withFileIndex(composing(sampleApp()), "sb1"), "me@home")
	if a, _ := m.menu(); a.kind != menuNone {
		t.Fatalf("me@home opens the menu: %+v", a)
	}
}

func TestPickingAFileMentionsIt(t *testing.T) {
	client, rec := recordingClient(t)
	m := withFileIndex(composing(sampleApp()), "sb1")
	m.client = client
	m = press(typeText(m, "check @web/src/log"), "enter")
	c := &m.chats
	if got := c.draft.value(); got != "check @web/src/login.tsx " {
		t.Fatalf("the draft %q", got)
	}
	if !slices.Equal(c.mentions, []chat.Mention{{Path: "web/src/login.tsx"}}) {
		t.Fatalf("mentions %+v", c.mentions)
	}
	if a, _ := m.menu(); a.kind != menuNone || !c.composing {
		t.Fatalf("after the pick: menu %+v, composing %v", a, c.composing)
	}
	// Lines go with it, as coupon.ts:20-28 does on the web.
	m = press(typeText(m, "and @login.test:3-9"), "enter")
	if got := c.draft.value(); got != "check @web/src/login.tsx and @web/src/login.test.tsx:3-9 " {
		t.Fatalf("the draft %q", got)
	}
	if c.mentions[1] != (chat.Mention{Path: "web/src/login.test.tsx", Start: 3, End: 9}) {
		t.Fatalf("mentions %+v", c.mentions)
	}
	// Both go with the prompt; Studio stores them (TestChatPromptMentionsFiles).
	pressRun(t, m, "enter")
	p := sentPrompt(t, rec)
	want := []chat.Mention{{Path: "web/src/login.tsx"}, {Path: "web/src/login.test.tsx", Start: 3, End: 9}}
	if strings.TrimSpace(p.Text) != "check @web/src/login.tsx and @web/src/login.test.tsx:3-9" || !slices.Equal(p.Mentions, want) {
		t.Fatalf("sent %+v", p)
	}
}

func TestAFolderNarrowsTheMenu(t *testing.T) {
	m := withFileIndex(composing(sampleApp()), "sb1")
	m = press(typeText(m, "@we"), "tab")
	if got := m.chats.draft.value(); got != "@web/" || len(m.chats.mentions) != 0 {
		t.Fatalf("tab on web/: draft %q, mentions %+v", got, m.chats.mentions)
	}
	if got := menuLabels(m); !slices.Equal(got, []string{"web/src/", "web/package.json"}) {
		t.Fatalf("web/ lists %v", got)
	}
}

// A mention taken out of the text doesn't go with it.
func TestARemovedMentionIsNotSent(t *testing.T) {
	client, rec := recordingClient(t)
	m := withFileIndex(composing(sampleApp()), "sb1")
	m.client = client
	m = press(typeText(m, "@login.tsx"), "enter")
	m = press(m, "ctrl+u")
	m = typeText(m, "never mind")
	pressRun(t, m, "enter")
	if p := sentPrompt(t, rec); p.Text != "never mind" || len(p.Mentions) != 0 {
		t.Fatalf("sent %+v", p)
	}
}

func TestAtSaysWhyItListsNothing(t *testing.T) {
	m := typeText(composing(sampleApp()), "@")
	if got := m.menuEmpty(); got != "Listing the files…" {
		t.Fatalf("before the index: %q", got)
	}
	m = typeText(withFileIndex(composing(sampleApp()), "sb1"), "@zzzz")
	if got := m.menuEmpty(); got != "No file matches." {
		t.Fatalf("no match: %q", got)
	}
	m = sampleApp()
	m.open(place{kind: placeThread, mate: "t1", sandbox: "sb3"})
	m = typeText(press(m, "p"), "@")
	if got := m.menuEmpty(); got != "Start docs-site to mention its files." {
		t.Fatalf("stopped: %q", got)
	}
}

func TestSplitLines(t *testing.T) {
	for _, c := range []struct {
		in         string
		query      string
		start, end int
	}{
		{"coupon.ts", "coupon.ts", 0, 0},
		{"coupon.ts:20", "coupon.ts", 20, 0},
		{"coupon.ts:20-28", "coupon.ts", 20, 28},
		{"coupon.ts:28-20", "coupon.ts", 28, 0},
		{"coupon.ts:", "coupon.ts", 0, 0},
		{"coupon.ts:-9", "coupon.ts", 0, 0},
	} {
		q, s, e := splitLines(c.in)
		if q != c.query || s != c.start || e != c.end {
			t.Errorf("splitLines(%q) = %q %d %d", c.in, q, s, e)
		}
	}
}

func TestTheIdleComposerSaysWhatItCompletes(t *testing.T) {
	m := sampleChat(sampleApp())
	if line := strings.Join(m.composer(80), ""); !strings.Contains(line, "@ file  / command") {
		t.Fatalf("idle composer %q", line)
	}
}

func TestClickPicksAMenuRow(t *testing.T) {
	m := typeText(composing(visualApp(80, 24)), "/")
	m.View()
	var row *rowHit
	for i, r := range m.mouse.rows {
		if r.target == rowMenu && r.index == 1 {
			row = &m.mouse.rows[i]
		}
	}
	if row == nil {
		t.Fatal("no menu row was drawn")
	}
	next, _ := m.Update(clickAt(row.x0+3, row.y))
	if m = next.(*app); m.comp.tab != tabFiles {
		t.Fatalf("a click on /files: tab %v, draft %q", m.comp.tab, m.chats.draft.value())
	}
}

func TestGoldenComposerSlash(t *testing.T) {
	golden(t, "composer_slash", typeText(composing(sampleApp()), "/"))
}

func TestGoldenComposerMention(t *testing.T) {
	golden(t, "composer_mention", typeText(withFileIndex(composing(sampleApp()), "sb1"), "look at @log"))
}

func TestVisualComposerSlash(t *testing.T) {
	goldenVisual(t, "visual_composer_slash", typeText(composing(visualApp(80, 24)), "/"), true)
}
