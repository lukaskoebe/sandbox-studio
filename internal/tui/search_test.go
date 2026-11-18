package tui

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// clickAt is a left click at x, y.
func clickAt(x, y int) tea.MouseClickMsg { return tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft} }

// The matching is the web palette's (web/src/components/command-palette.tsx): the same scores,
// so the TUI and the web rank alike.
func TestFuzzyMatchesAsTheWebDoes(t *testing.T) {
	for _, c := range []struct {
		q, s  string
		ok    bool
		score float64
	}{
		{"net", "Network", true, 100 - 7*0.05},                    // its start
		{"work", "Network", true, 60 - 3*0.2 - 7*0.05},            // inside a word
		{"log", "Fix the login form", true, 82 - 8*0.2 - 18*0.05}, // a word's start
		{"x", "Network", false, 0},
		{"nw", "Network", true, 18 + 6 - 3*0.3},                        // letters in order: n at a start, w three on
		{"netwrok", "Network", true, 10},                               // one typo: two letters swapped
		{"secerts", "Secrets", true, 10},                               // and again
		{"actvty", "Activity", true, 18 + 6 + 4 + 4 + 4 - 7*0.3},       // letters in order, close together
		{"netwerk", "the Network", true, 10},                           // a letter changed, at a word's start
		{"netwrk", "the Network", true, 18 + 6 + 4*4 - 6*0.3 - 4*0.05}, // letters in order come first
		{"zzzzzz", "Network", false, 0},
	} {
		got, ok := fuzzy(c.q, c.s)
		if ok != c.ok || ok && !near(got.score, c.score) {
			t.Errorf("fuzzy(%q, %q) = %v %v, want %v %v", c.q, c.s, got.score, ok, c.score, c.ok)
		}
	}
}

func near(a, b float64) bool { return a-b < 1e-9 && b-a < 1e-9 }

func TestOneTypo(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"abc", "abc", true},
		{"abc", "abd", true},  // changed
		{"abc", "acb", true},  // swapped
		{"abc", "ab", true},   // dropped
		{"abc", "abcd", true}, // added
		{"abc", "xbz", false},
		{"abcd", "ab", false},
		{"abcd", "badc", false},
	} {
		if got := oneTypo([]rune(c.a), []rune(c.b)); got != c.want {
			t.Errorf("oneTypo(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

// searchFor opens Search Studio on m and types the query.
func searchFor(m *app, query string) *app {
	m = press(m, "ctrl+k")
	return typeText(m, query)
}

// hitIDs are the ids of what the search lists, in order.
func hitIDs(m *app) []string {
	var out []string
	for _, h := range searchRows(m.searchGroups()) {
		out = append(out, h.item.id)
	}
	return out
}

func TestSearchFindsPlacesThroughTypos(t *testing.T) {
	for q, want := range map[string]string{
		"netwrok": "env:network",
		"secerts": "env:secrets",
		"actvty":  "env:activity",
		"iris":    "tm:t1",
		"blub":    "pc:sb1",
		"fix log": "th:c1",
		"forges":  "set:forges",
		"github":  "set:forges", // a word of its keys
		"quit":    "act:quit",
	} {
		m := searchFor(sampleMorning(sampleApp()), q)
		if got := hitIDs(m); len(got) == 0 || got[0] != want {
			t.Errorf("%q finds %v first, want %s", q, got, want)
		}
	}
}

func TestLogFindsTheThreadAndActivity(t *testing.T) {
	m := searchFor(threadWithCard(sampleApp()), "log")
	got := hitIDs(m)
	for _, want := range []string{"th:c1", "env:activity"} {
		if !slices.Contains(got, want) {
			t.Errorf("log finds %v, without %s", got, want)
		}
	}
}

// A tab is found by its own name; the computer's name only narrows it.
func TestPartsNeedAWordOfTheirOwn(t *testing.T) {
	m := searchFor(sampleMorning(sampleApp()), "blub")
	for _, id := range hitIDs(m) {
		if strings.HasPrefix(id, "tab:") {
			t.Fatalf("blub lists its tab %s", id)
		}
	}
	m = searchFor(sampleMorning(sampleApp()), "blub files")
	if got := hitIDs(m); len(got) == 0 || got[0] != "tab:sb1:files" {
		t.Fatalf("blub files finds %v", got)
	}
	// A computer that is off has no Files tab.
	m = searchFor(sampleMorning(sampleApp()), "docs-site files")
	if got := hitIDs(m); slices.Contains(got, "tab:sb3:files") {
		t.Fatalf("docs-site, stopped, lists its Files tab: %v", got)
	}
}

func TestPrefixesKeepASearchToAGroup(t *testing.T) {
	m := searchFor(sampleMorning(sampleApp()), ">")
	groups := m.searchGroups()
	if len(groups) != 1 || groups[0].group != groupActions || len(groups[0].hits) < 5 {
		t.Fatalf("> lists %+v", groups)
	}
	m = searchFor(sampleMorning(sampleApp()), "@")
	if got := hitIDs(m); !slices.Equal(got, []string{"tm:t1", "tm:t2"}) {
		t.Fatalf("@ lists %v", got)
	}
	m = searchFor(sampleMorning(sampleApp()), "@fi")
	if got := hitIDs(m); !slices.Equal(got, []string{"tm:t2"}) {
		t.Fatalf("@fi lists %v", got)
	}
	m = searchFor(sampleMorning(sampleApp()), ">new")
	for _, id := range hitIDs(m) {
		if !strings.HasPrefix(id, "act:") {
			t.Fatalf(">new lists %s", id)
		}
	}
}

func TestNothingTypedListsRecentThenWaiting(t *testing.T) {
	m := sampleMorning(sampleApp())
	m = press(searchFor(m, "secrets"), "enter")
	m = press(searchFor(m, "iris"), "enter")
	m = press(searchFor(m, ">quit"), "esc") // closed, not run
	m = press(m, "ctrl+k")
	groups := m.searchGroups()
	if len(groups) != 2 || groups[0].group != groupRecent || groups[1].group != groupWaiting {
		t.Fatalf("groups %+v", groups)
	}
	var recent []string
	for _, h := range groups[0].hits {
		recent = append(recent, h.item.id)
	}
	if !slices.Equal(recent, []string{"tm:t1", "env:secrets"}) {
		t.Fatalf("recent %v", recent)
	}
	if w := groups[1].hits; len(w) == 0 || w[0].item.title != "Finn wants to push 2 commits to sandbox-studio/main" &&
		!strings.HasPrefix(w[0].item.id, "ap:") {
		t.Fatalf("waiting %+v", w)
	}
}

// Each kind of hit lands on its place.
func TestSearchOpensWhatItFinds(t *testing.T) {
	for _, c := range []struct {
		query string
		check func(m *app) bool
	}{
		{"@iris", func(m *app) bool { return m.here.kind == placeMate && m.here.mate == "t1" }},
		{"api-refactor", func(m *app) bool { return m.here.kind == placeComputer && m.here.sandbox == "sb2" }},
		{"api-refactor changes", func(m *app) bool {
			return m.here.kind == placeComputer && m.here.sandbox == "sb2" && m.comp.tab == tabChanges
		}},
		{"blub browser", func(m *app) bool { return m.computerOn().ID == "sb1" && m.comp.tab == tabBrowser }},
		{"fix the login", func(m *app) bool { return m.here.kind == placeThread && m.here.chat == "c1" }},
		{"network", func(m *app) bool { return m.screen == screenActivity }},
		{"computers", func(m *app) bool { return m.screen == screenComputers }},
		{"providers", func(m *app) bool {
			return m.screen == screenSettings && settingsSections[m.settings.cursor].path == "providers" &&
				strings.Contains(m.notice.text, "/settings/providers")
		}},
		{"new thread with finn", func(m *app) bool { return m.here.kind == placeThread && m.here.mate == "t2" && m.here.chat == "" }},
		{"new teammate", func(m *app) bool { return strings.Contains(m.notice.text, "/teammates") }},
		{"keys", func(m *app) bool { return m.screen == screenHelp }},
	} {
		m := press(searchFor(sampleMorning(sampleApp()), c.query), "enter")
		if m.search.open || !c.check(m) {
			t.Errorf("%q: open %v, here %+v, screen %v, tab %v, notice %q", c.query, m.search.open, m.here, m.screen, m.comp.tab, m.notice.text)
		}
	}
}

// A request that waits goes Home with the cursor on its row.
func TestWaitingHitGoesHomeOnItsRow(t *testing.T) {
	m := threadWithCard(sampleApp())
	m = press(m, "ctrl+k")
	hits := searchRows(m.searchGroups())
	i := slices.IndexFunc(hits, func(h searchHit) bool { return h.item.id == "ap:p1" })
	if i < 0 {
		t.Fatalf("Iris's npm test isn't listed: %v", hitIDs(m))
	}
	if hits[i].item.title != "Iris asks to run npm test" || hits[i].item.place != "Fix the login form" {
		t.Fatalf("the request reads %q from %q", hits[i].item.title, hits[i].item.place)
	}
	m.search.sel = i
	m = press(m, "enter")
	if m.here.kind != placeHome || m.inbox.cursor != "card/p1" {
		t.Fatalf("here %+v, cursor %q", m.here, m.inbox.cursor)
	}
}

func TestArrowsMoveAroundAndEscCloses(t *testing.T) {
	m := searchFor(sampleMorning(sampleApp()), ">")
	n := len(searchRows(m.searchGroups()))
	if m = press(m, "up"); m.search.sel != n-1 {
		t.Fatalf("up from the first goes to the last: %d of %d", m.search.sel, n)
	}
	if m = press(m, "ctrl+n"); m.search.sel != 0 {
		t.Fatalf("ctrl+n from the last goes to the first: %d", m.search.sel)
	}
	if m = press(m, "ctrl+k"); m.search.open {
		t.Fatal("ctrl+k doesn't close it")
	}
	if m = press(m, "/"); !m.search.open || m.search.query.value() != "" {
		t.Fatalf("/ opens it empty: open %v, %q", m.search.open, m.search.query.value())
	}
	if m = press(m, "esc"); m.search.open {
		t.Fatal("esc doesn't close it")
	}
}

// / is Activity's filter there; ctrl+k is search everywhere.
func TestActivityKeepsItsSlash(t *testing.T) {
	m := press(sampleApp(), "l")
	if m = press(m, "/"); m.search.open || !m.filtering {
		t.Fatalf("/ on Activity: search %v, filtering %v", m.search.open, m.filtering)
	}
	m = press(m, "esc", "ctrl+k")
	if !m.search.open {
		t.Fatal("ctrl+k on Activity doesn't open search")
	}
}

// The composer takes / as text, and ctrl+k opens search over it.
func TestComposerKeepsSlashButNotCtrlK(t *testing.T) {
	m := press(sampleChat(sampleApp()), "p", "/")
	if m.search.open || m.chats.draft.value() != "/" {
		t.Fatalf("/ in the composer: search %v, draft %q", m.search.open, m.chats.draft.value())
	}
	if m = press(m, "ctrl+k"); !m.search.open {
		t.Fatal("ctrl+k in the composer doesn't open search")
	}
	if m = press(m, "esc"); m.search.open || !m.chats.composing || m.chats.draft.value() != "/" {
		t.Fatalf("esc: search %v, composing %v, draft %q", m.search.open, m.chats.composing, m.chats.draft.value())
	}
}

// withFileIndex gives computer id a file index as the guest lists it.
func withFileIndex(m *app, id string) *app {
	if m.search.files == nil {
		m.search.files = map[string]*fileIndex{}
	}
	m.search.files[id] = &fileIndex{loaded: true, at: m.now, all: fileCandidates(
		[]string{"web/src/login.tsx", "web/src/login.test.tsx", "web/src/app.tsx", "web/package.json", "docs/logging.md", "README.md"},
		[]string{"web/src/login.tsx"},
	)}
	return m
}

func TestRankFilesAsMentionsDo(t *testing.T) {
	all := fileCandidates(
		[]string{"web/src/login.tsx", "web/src/login.test.tsx", "web/src/app.tsx", "web/package.json", "docs/logging.md", "README.md"},
		[]string{"web/src/app.tsx"},
	)
	paths := func(rs []fileRanked) []string {
		var out []string
		for _, r := range rs {
			out = append(out, r.c.path)
		}
		return out
	}
	for _, c := range []struct {
		query string
		want  []string
	}{
		// Names that start with it, the shorter path first.
		{"log", []string{"docs/logging.md", "web/src/login.tsx", "web/src/login.test.tsx"}},
		// Nothing typed: the changed files, then the top of the workspace, folders first.
		{"", []string{"web/src/app.tsx", "web/", "docs/", "README.md"}},
		// A folder lists its own entries, folders first.
		{"web/", []string{"web/src/", "web/package.json"}},
		// A query with a slash looks in that folder.
		{"web/src/log", []string{"web/src/login.tsx", "web/src/login.test.tsx"}},
		// Letters in order in a name, when nothing has the run: the tighter first.
		{"lgn", []string{"web/src/login.tsx", "web/src/login.test.tsx", "docs/logging.md"}},
		// Letters along the path, for three letters or more.
		{"wsa", []string{"web/src/app.tsx"}},
	} {
		if got := paths(rankFiles(all, c.query, 12)); !slices.Equal(got, c.want) {
			t.Errorf("rank %q = %v, want %v", c.query, got, c.want)
		}
	}
}

func TestSlashSearchesTheFilesOfTheComputerInView(t *testing.T) {
	m := searchFor(withFileIndex(threadWithCard(sampleApp()), "sb1"), "/log")
	got := hitIDs(m)
	if len(got) == 0 || got[0] != "file:web/src/login.tsx" {
		t.Fatalf("/log finds %v", got)
	}
	m = press(m, "enter")
	if m.comp.tab != tabFiles || m.files.sandbox != "sb1" || m.files.dir != "web/src" || m.files.open == nil ||
		m.files.open.path != "web/src/login.tsx" {
		t.Fatalf("tab %v, files %+v", m.comp.tab, m.files)
	}
	if m.here.kind != placeThread || m.here.chat != "c1" {
		t.Fatalf("the file opens beside the thread: here %+v", m.here)
	}
	// A pick isn't one of the recent.
	if m = press(m, "ctrl+k"); len(m.search.recent) != 0 {
		t.Fatalf("recent %v", m.search.recent)
	}
}

func TestSlashOnHomeSaysToOpenAComputer(t *testing.T) {
	m := searchFor(sampleMorning(sampleApp()), "/")
	if len(hitIDs(m)) != 0 || m.searchEmpty() != "Open a computer to search its files." {
		t.Fatalf("hits %v, empty %q", hitIDs(m), m.searchEmpty())
	}
	m = searchFor(withFileIndex(threadWithCard(sampleApp()), "sb1"), "/zzz")
	if m.searchEmpty() != "No file of blub matches “zzz”." {
		t.Fatalf("empty %q", m.searchEmpty())
	}
}

// A click on a hit opens it; a click outside the box closes it.
func TestClickOpensAHitOrCloses(t *testing.T) {
	m := searchFor(visualApp(80, 24), "secrets")
	m.View()
	if len(m.search.rows) == 0 {
		t.Fatal("no hit was drawn")
	}
	r := m.search.rows[0]
	next, _ := m.Update(clickAt(r.x0+2, r.y))
	m = next.(*app)
	if m.search.open || !strings.Contains(m.notice.text, "/secrets") {
		t.Fatalf("click on Secrets: open %v, notice %q", m.search.open, m.notice.text)
	}
	m = press(m, "ctrl+k")
	m.View()
	next, _ = m.Update(clickAt(0, 10))
	if m = next.(*app); m.search.open {
		t.Fatal("a click outside doesn't close it")
	}
	// The sidebar's search row opens it.
	m = visualApp(120, 30)
	m.View()
	next, _ = m.Update(clickAt(3, 1))
	if m = next.(*app); !m.search.open {
		t.Fatal("a click on / Search doesn't open it")
	}
}

func TestGoldenSearch(t *testing.T) {
	golden(t, "search", searchFor(threadWithCard(sampleApp()), "log"))
}

func TestGoldenSearchEmpty(t *testing.T) {
	m := sampleMorning(sampleApp())
	m = press(searchFor(m, "secrets"), "enter")
	m = press(searchFor(m, "iris"), "enter")
	golden(t, "search_empty", press(m, "ctrl+k"))
}

func TestGoldenSearchFiles(t *testing.T) {
	golden(t, "search_files", searchFor(withFileIndex(threadWithCard(sampleApp()), "sb1"), "/log"))
}

func TestGoldenWideSearch(t *testing.T) {
	m := threadWithCard(sampleApp())
	m.width, m.height = 140, 40
	golden(t, "wide_search_140x40", searchFor(m, "log"))
}

func TestGoldenNarrowSearch(t *testing.T) {
	m := threadWithCard(sampleApp())
	m.width, m.height = 40, 20
	golden(t, "narrow_search_40x20", searchFor(m, "log"))
}

func TestVisualSearch(t *testing.T) {
	m := threadWithCard(visualApp(80, 24))
	goldenVisual(t, "visual_search", searchFor(m, "log"), true)
}
