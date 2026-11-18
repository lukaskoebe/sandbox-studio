package tui

import (
	"context"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/lukaskoebe/sandbox-studio/internal/api"
)

// Search Studio (ctrl+k, or / where the screen has no filter of its own) finds anything by name,
// as the web's palette does (web/src/components/command-palette.tsx): teammates, computers and
// their tabs, threads, the environment's places, the Settings sections and actions, grouped by
// kind with where each lives on the right. With nothing typed it lists what was opened last and
// what waits for you. > keeps a search to actions, @ to teammates, and / searches the files of
// the computer in view, opening the pick in its Files tab.

// The groups of Search Studio, in the web's words.
const (
	groupRecent      = "Recent"
	groupWaiting     = "Waiting for you"
	groupTeammates   = "Teammates"
	groupComputers   = "Computers"
	groupThreads     = "Threads"
	groupEnvironment = "Environment"
	groupSettings    = "Settings"
	groupActions     = "Actions"
	groupFiles       = "Files"
)

// searchScopes are the prefixes that keep a search to one group; / searches files instead.
var searchScopes = map[byte]string{'>': groupActions, '@': groupTeammates}

// fileIndexEvery is how long a computer's file index is kept before a search reads it again.
const fileIndexEvery = 30 * time.Second

var keySearchAny = key.NewBinding(key.WithKeys("ctrl+k"), key.WithHelp("ctrl+k", "search"))

// searchItem is what Search Studio finds: where it lives says place, and keys are more words it
// answers to.
type searchItem struct {
	id    string // "tm:<id>", "pc:<id>", "tab:<sb>:<tab>", "th:<chat>", "env:<page>", "set:<section>", "act:<name>", "ap:<id>", "file:<path>"
	group string
	title string
	place string
	keys  string
	glyph string // drawn before the title, styled; "" for none
	// part is a part of the computer its place names, such as a tab: found by its own name or
	// keys, which the place only narrows, so a name alone doesn't list every part of it.
	part  bool
	boost float64 // added to the score, so the likelier one comes first
	run   func(m *app) tea.Cmd
}

// searchHit is an item found, with the letters of its title that matched.
type searchHit struct {
	item searchItem
	hi   []int // rune indexes into the title
}

// searchGroupHits is a group's hits, in the order they show.
type searchGroupHits struct {
	group string
	hits  []searchHit
}

// searchState is Search Studio while it is open, and what it keeps between openings.
type searchState struct {
	open   bool
	query  input
	sel    int
	recent []string              // the ids opened last, this run only
	files  map[string]*fileIndex // the computers' file indexes, by ID
	rows   []searchRowHit        // where the hits were drawn, for the mouse
	box    rect                  // where the box was drawn
}

// searchRowHit is a hit's row on screen.
type searchRowHit struct{ y, x0, x1, index int }

// fileIndex is a computer's files as the index read lists them.
type fileIndex struct {
	all       []fileCandidate
	at        time.Time // when it was read
	busy      bool
	loaded    bool
	truncated bool
	err       string
}

type fileIndexLoaded struct {
	sandbox string
	index   api.FileIndex
	err     error
}

// openSearch opens Search Studio with a query typed.
func (m *app) openSearch(query string) tea.Cmd {
	m.search.open, m.search.sel = true, 0
	m.search.query.set(query)
	m.drawer, m.pane.focus = false, false
	m.blurButtons()
	return m.searchFiles()
}

// closeSearch closes Search Studio.
func (m *app) closeSearch() {
	m.search.open, m.search.rows = false, nil
	m.search.query.set("")
}

// searchKey answers the keys while Search Studio is open: the arrows move, enter opens the
// hit, esc or ctrl+k closes it, and the rest edit the query.
func (m *app) searchKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := &m.search
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "ctrl+k":
		m.closeSearch()
		return m, nil
	case "enter":
		hits := searchRows(m.searchGroups())
		if len(hits) == 0 {
			return m, nil
		}
		return m, m.runSearch(hits[min(s.sel, len(hits)-1)].item)
	case "up", "ctrl+p":
		m.searchMove(-1)
		return m, nil
	case "down", "ctrl+n", "tab":
		m.searchMove(1)
		return m, nil
	}
	if s.query.key(msg) {
		s.sel = 0
		return m, m.searchFiles()
	}
	return m, nil
}

// searchMove moves the cursor by dir hits, around the ends.
func (m *app) searchMove(dir int) {
	n := len(searchRows(m.searchGroups()))
	if n == 0 {
		return
	}
	at := min(m.search.sel, n-1)
	m.search.sel = (at + dir + n) % n
}

// runSearch closes Search Studio and opens the hit, remembering it with what was opened last
// unless it is an action, a request or a file.
func (m *app) runSearch(it searchItem) tea.Cmd {
	m.closeSearch()
	if !strings.HasPrefix(it.id, "act:") && !strings.HasPrefix(it.id, "ap:") && !strings.HasPrefix(it.id, "file:") {
		recent := []string{it.id}
		for _, id := range m.search.recent {
			if id != it.id {
				recent = append(recent, id)
			}
		}
		m.search.recent = recent[:min(len(recent), 6)]
	}
	return it.run(m)
}

// searchQuery is the query as typed, its scope, and the words to match, lowercased.
func (m *app) searchQuery() (raw, scope, q string) {
	raw = m.search.query.value()
	if raw != "" {
		scope = searchScopes[raw[0]]
	}
	q = raw
	if scope != "" {
		q = q[1:]
	}
	return raw, scope, strings.ToLower(strings.TrimSpace(q))
}

// searchGroups are the hits of the query, by group: with nothing typed what was opened last and
// what waits; otherwise the best five of each group (twelve in a scope), the groups in the order
// of their best hit.
func (m *app) searchGroups() []searchGroupHits {
	raw, scope, q := m.searchQuery()
	if strings.HasPrefix(raw, "/") {
		return []searchGroupHits{{groupFiles, m.fileHits(strings.TrimSpace(raw[1:]))}}
	}
	items := m.searchItems()
	if q == "" && scope == "" {
		byID := map[string]searchItem{}
		for _, it := range items {
			byID[it.id] = it
		}
		var last []searchHit
		for _, id := range m.search.recent {
			if it, ok := byID[id]; ok && len(last) < 5 {
				it.group = groupRecent
				last = append(last, searchHit{item: it})
			}
		}
		var waiting []searchHit
		for _, it := range m.waitingItems() {
			waiting = append(waiting, searchHit{item: it})
		}
		return nonEmptyGroups([]searchGroupHits{{groupRecent, last}, {groupWaiting, waiting}})
	}
	type scored struct {
		hit   searchHit
		score float64
	}
	var hits []scored
	for _, it := range items {
		if scope != "" && it.group != scope {
			continue
		}
		if q == "" {
			hits = append(hits, scored{hit: searchHit{item: it}})
			continue
		}
		if score, hi, ok := matchItem(q, it); ok {
			hits = append(hits, scored{searchHit{it, hi}, score})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	limit := 5
	if scope != "" {
		limit = 12
	}
	var out []searchGroupHits
	for _, h := range hits {
		i := slices.IndexFunc(out, func(g searchGroupHits) bool { return g.group == h.hit.item.group })
		if i < 0 {
			out = append(out, searchGroupHits{group: h.hit.item.group})
			i = len(out) - 1
		}
		if len(out[i].hits) < limit {
			out[i].hits = append(out[i].hits, h.hit)
		}
	}
	return out
}

// nonEmptyGroups drops the groups without a hit.
func nonEmptyGroups(groups []searchGroupHits) []searchGroupHits {
	return slices.DeleteFunc(groups, func(g searchGroupHits) bool { return len(g.hits) == 0 })
}

// searchRows are the groups' hits in the order they show, which the cursor moves over.
func searchRows(groups []searchGroupHits) []searchHit {
	var out []searchHit
	for _, g := range groups {
		out = append(out, g.hits...)
	}
	return out
}

// searchItems are what Search Studio finds, built from what is loaded.
func (m *app) searchItems() []searchItem {
	var out []searchItem
	for _, t := range m.team {
		out = append(out, searchItem{
			id: "tm:" + t.ID, group: groupTeammates, title: t.Name, place: "teammate",
			keys: "teammate agent " + t.Role, glyph: styleDim.Render("@"),
			run: func(m *app) tea.Cmd { return m.open(place{kind: placeMate, mate: t.ID}) },
		})
	}
	for _, sb := range m.sandboxes {
		owner := "your computer"
		if name := m.teammates[sb.TeammateID]; name != "" {
			owner = name + "'s computer"
		}
		ph := phaseOf(sb)
		keys := "computer sandbox vm " + sb.TemplateName
		if sb.ForkDefaults != nil {
			keys += " base"
		}
		out = append(out, searchItem{
			id: "pc:" + sb.ID, group: groupComputers, title: sb.Name,
			place: owner + " · " + strings.ToLower(ph.label()), keys: keys, glyph: m.phaseDot(ph),
			run: func(m *app) tea.Cmd { return m.open(place{kind: placeComputer, sandbox: sb.ID}) },
		})
		// Its tabs, as its pane shows them: Terminal and Details while it is off too, Files and
		// Changes while it runs, and the browser on a teammate's.
		if ph == phaseCreating || ph == phaseFailed {
			continue
		}
		tabs := []computerTab{tabTerminal}
		if ph == phaseReady {
			tabs = append(tabs, tabFiles, tabChanges)
		}
		if sb.TeammateID != "" {
			tabs = append(tabs, tabBrowser)
		}
		for _, tab := range append(tabs, tabDetails) {
			name := tabNames[tab]
			extra := ""
			if tab == tabBrowser {
				extra = " web page devtools"
			}
			out = append(out, searchItem{
				id: "tab:" + sb.ID + ":" + strings.ToLower(name), group: groupComputers, title: name,
				place: sb.Name, keys: strings.ToLower(name) + " tab" + extra, part: true, glyph: styleDim.Render("›"),
				run: func(m *app) tea.Cmd { return m.showTab(sb.ID, tab) },
			})
		}
	}
	for _, x := range m.searchThreads() {
		who := m.teammates[x.mate]
		out = append(out, searchItem{
			id: "th:" + x.chat.ID, group: groupThreads, title: chatTitle(x.chat),
			place: strings.Join(nonEmpty(who, m.sandboxName(x.chat.SandboxID)), " · "),
			keys:  "thread chat conversation", glyph: m.sessionMark(x, m.stateOf(x)),
			run: func(m *app) tea.Cmd {
				if cmd := m.enterOf(x); cmd != nil {
					return cmd
				}
				m.setNotice("That thread's computer is gone.", false)
				return nil
			},
		})
	}
	out = append(out, searchItem{
		id: "env:home", group: groupEnvironment, title: "Home", place: "Studio",
		keys: "start overview dashboard approvals requests lines timeline inbox", glyph: styleDim.Render("◆"),
		run: func(m *app) tea.Cmd { return m.open(place{}) },
	})
	for _, p := range envPlaces {
		if p == envSettings {
			continue // Settings' sections are below
		}
		out = append(out, searchItem{
			id: "env:" + envPlaceIDs[p], group: groupEnvironment, title: placeName(p), place: "Environment",
			keys: envPlaceKeys[p], glyph: styleDim.Render("◆"),
			run: func(m *app) tea.Cmd { return m.openPlace(p) },
		})
	}
	for i, sec := range settingsSections {
		if envSectionPaths[sec.path] {
			continue // the environment's own pages, above
		}
		out = append(out, searchItem{
			id: "set:" + sec.path, group: groupSettings, title: sec.name,
			place: "Settings › " + sectionGroupName(sec.group), keys: settingsKeys[sec.path] + " settings",
			glyph: styleDim.Render(","),
			run: func(m *app) tea.Cmd {
				cmd := m.openSettings()
				m.settings.cursor = i
				m.setNotice("open in browser: "+m.browserURL("/e/"+m.env.ID+"/settings/"+sec.path), false)
				return cmd
			},
		})
	}
	return append(out, m.actionItems()...)
}

// searchThreads are every thread of the environment, the latest first, each as the sidebar
// would open it.
func (m *app) searchThreads() []session {
	seen := map[string]bool{}
	var out []session
	for _, sb := range m.sandboxes {
		for _, c := range m.sess.chats[sb.ID] {
			if seen[c.ID] {
				continue
			}
			seen[c.ID] = true
			out = append(out, session{kind: sessionChat, sandbox: c.SandboxID, mate: firstNonEmpty(c.TeammateID, sb.TeammateID), chat: c})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].chat.UpdatedAt.After(out[j].chat.UpdatedAt) })
	return out
}

// actionItems are what Search Studio does rather than opens.
func (m *app) actionItems() []searchItem {
	act := func(id, title, place, keys, glyph string, run func(m *app) tea.Cmd) searchItem {
		return searchItem{id: "act:" + id, group: groupActions, title: title, place: place, keys: keys, glyph: styleDim.Render(glyph), run: run}
	}
	var out []searchItem
	for _, t := range m.team {
		out = append(out, act("new-thread:"+t.ID, "New thread with "+t.Name, t.Name, "start work chat ask task begin", "+",
			func(m *app) tea.Cmd { return m.newThread(t.ID) }))
	}
	out = append(out,
		act("new-computer", "New computer…", "Computers", "add create sandbox vm", "+",
			func(m *app) tea.Cmd { return m.rowActionDo(actionNewComputer) }),
		act("new-teammate", "New teammate…", "Teammates", "add create agent", "+",
			func(m *app) tea.Cmd { return m.rowActionDo(actionNewTeammate) }),
		act("environment", "Switch environment…", "Environment", "env workspace change", "›",
			func(m *app) tea.Cmd { m.openEnvs(); return nil }))
	if sb := m.computerOn(); sb != nil {
		verb := map[phase]string{phaseReady: "Stop", phaseStopped: "Start", phaseSuspended: "Resume"}[phaseOf(*sb)]
		if verb != "" {
			id := sb.ID
			out = append(out, act("power", verb+" "+sb.Name, "the computer in view", "computer power on off turn boot shut down", "›",
				func(m *app) tea.Cmd {
					sb, ok := m.sandboxByID(id)
					if !ok {
						return nil
					}
					return m.toggle(&sb)
				}))
		}
	}
	return append(out,
		act("keys", "Keys", "?", "help shortcuts keyboard", "?", func(m *app) tea.Cmd { m.openHelp(); return nil }),
		act("quit", "Quit", "q", "exit close leave", "›", func(*app) tea.Cmd { return tea.Quit }))
}

// waitingItems are the first five requests Home lists, each going Home with the cursor on it.
func (m *app) waitingItems() []searchItem {
	var out []searchItem
	for _, c := range m.cards() {
		if len(out) == 5 {
			break
		}
		a := c.first()
		glyph, what, where, _ := m.rowCells(inboxRow{card: &c})
		title, from := what, strings.Join(nonEmpty(m.cardWho(a), where), " · ")
		if a.ChatPermission != nil {
			// "Iris asks to run npm test", from its thread.
			title, from = m.cardWho(a)+" "+lowerFirst(what), where
		}
		id := a.ID
		out = append(out, searchItem{
			id: "ap:" + id, group: groupWaiting, title: title, place: from, glyph: glyph,
			run: func(m *app) tea.Cmd {
				cmd := m.open(place{})
				m.inboxOn(id)
				return cmd
			},
		})
	}
	return out
}

// showTab shows a tab of a computer, which gets the keys: beside the place on show when it is
// the computer there, else on the computer's own place.
func (m *app) showTab(id string, tab computerTab) tea.Cmd {
	var cmd tea.Cmd
	if sb := m.computerOn(); m.screen != screenMain || sb == nil || sb.ID != id {
		cmd = m.open(place{kind: placeComputer, sandbox: id})
	}
	if slices.Contains(m.computerTabs(), tab) {
		m.comp.tab = tab
	}
	m.flipTo(true)
	return cmd
}

// The environment's places as the web names their pages, and the words they answer to.
var (
	envPlaceIDs = map[envPlace]string{
		envComputers: "computers", envNetwork: "network", envSecrets: "secrets", envMemory: "memory", envActivity: "activity",
	}
	envPlaceKeys = map[envPlace]string{
		envComputers: "sandboxes vms forks bases inventory rebase",
		envNetwork:   "rules level security hosts allow deny internet requests connections",
		envSecrets:   "keys tokens vault credentials passwords",
		envMemory:    "shared facts pages conflicts knowledge",
		envActivity:  "log history decisions pushes reviews requests",
	}
	envSectionPaths = map[string]bool{"network": true, "secrets": true, "memory": true, "activity": true}
	settingsKeys    = map[string]string{
		"general":   "start at login setup environment file export import",
		"providers": "anthropic openai api key models",
		"forges":    "github gitlab forgejo codeberg ssh token folder",
		"templates": "builds images dockerfile",
		"runtime":   "microsandbox vm cpu memory disk",
		"ports":     "host ports local network vpn private addresses",
		"updates":   "version release",
	}
)

// sectionGroupName is a Settings group's label as a place: "THIS COMPUTER" is "This computer".
func sectionGroupName(g string) string {
	return g[:1] + strings.ToLower(g[1:])
}

// lowerFirst lowercases the first letter of s.
func lowerFirst(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	if len(r) > 1 && unicode.IsUpper(r[1]) {
		return s // an initialism: "CPU", "VM"
	}
	r[0] = unicode.ToLower(r[0])
	return string(r)
}

// The matching, as the web's palette has it, so the TUI and the web rank alike.

// isWordStart reports whether a word starts after r.
func isWordStart(r rune) bool {
	return unicode.IsSpace(r) || strings.ContainsRune("/-_.'’›:", r)
}

// fuzzyMatch is how well a word matches a title, and the letters that matched.
type fuzzyMatch struct {
	score float64
	idx   []int
}

// fuzzy is how well one word of the query, lowercased, matches s: as a run of letters, best at
// the start of s or of a word in it, else as its letters in order and close together, else, for
// a word of six letters or more, as the start of a word in s one typo away ("netwrok",
// "secerts"), last. The indexes are the matched runes, for highlighting.
func fuzzy(q, s string) (fuzzyMatch, bool) {
	qr := []rune(q)
	t := []rune(strings.Map(unicode.ToLower, s))
	if len(t) != len([]rune(s)) {
		t = []rune(s) // a letter that lowers into more letters: match as it is
	}
	if at := runesIndex(t, qr); at >= 0 {
		base := 60.0
		switch {
		case at == 0:
			base = 100
		case isWordStart(t[at-1]):
			base = 82
		}
		idx := make([]int, len(qr))
		for i := range idx {
			idx[i] = at + i
		}
		return fuzzyMatch{base - float64(at)*0.2 - float64(len(t))*0.05, idx}, true
	}
	if len(qr) < 2 {
		return fuzzyMatch{}, false
	}
	var best fuzzyMatch
	found := false
	for st := runeIndexFrom(t, qr[0], 0); st >= 0; st = runeIndexFrom(t, qr[0], st+1) {
		var idx []int
		qi, score, prev := 0, 18.0, -2
		for i := st; i < len(t) && qi < len(qr); i++ {
			if t[i] != qr[qi] {
				continue
			}
			if i == prev+1 {
				score += 4
			}
			if i == 0 || isWordStart(t[i-1]) {
				score += 6
			}
			idx = append(idx, i)
			prev = i
			qi++
		}
		if qi < len(qr) {
			break
		}
		span := idx[len(idx)-1] - idx[0]
		if span > len(qr)*2+3 {
			continue
		}
		score -= float64(span)*0.3 + float64(idx[0])*0.05
		if !found || score > best.score {
			best, found = fuzzyMatch{score, idx}, true
		}
	}
	if found || len(qr) < 6 {
		return best, found
	}
	for w := range t {
		if w > 0 && !isWordStart(t[w-1]) || t[w] != qr[0] {
			continue
		}
		for _, n := range []int{len(qr), len(qr) - 1, len(qr) + 1} {
			if w+n > len(t) {
				continue
			}
			seg := t[w : w+n]
			if !slices.ContainsFunc(seg, isWordStart) && oneTypo(qr, seg) {
				idx := make([]int, n)
				for i := range idx {
					idx[i] = w + i
				}
				return fuzzyMatch{10, idx}, true
			}
		}
	}
	return fuzzyMatch{}, false
}

// oneTypo reports whether a and b are at most one typo apart: a letter changed, added or
// dropped, or two swapped.
func oneTypo(a, b []rune) bool {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	at := func(s []rune, k int) rune {
		if k < len(s) {
			return s[k]
		}
		return -1
	}
	from := func(s []rune, k int) []rune { return s[min(k, len(s)):] }
	if len(a) == len(b) {
		return slices.Equal(from(a, i+1), from(b, i+1)) ||
			at(a, i) == at(b, i+1) && at(a, i+1) == at(b, i) && slices.Equal(from(a, i+2), from(b, i+2))
	}
	if len(a)-len(b) > 1 || len(b)-len(a) > 1 {
		return false
	}
	if len(a) > len(b) {
		return slices.Equal(from(a, i+1), from(b, i))
	}
	return slices.Equal(from(a, i), from(b, i+1))
}

// matchItem scores an item for the query, lowercased: every word must match the title, or start
// a word of the keys or the place; a part needs a word of its own, in the title or the keys.
func matchItem(q string, it searchItem) (float64, []int, bool) {
	score, own := 0.0, !it.part
	var hi []int
	keys := " " + strings.ToLower(it.keys)
	where := " " + strings.ToLower(it.place)
	for _, word := range strings.Fields(q) {
		a, ok := fuzzy(word, it.title)
		inKeys := strings.Contains(keys, " "+word)
		kw := 0.0
		if inKeys || strings.Contains(where, " "+word) {
			kw = 34
		}
		switch {
		case ok && a.score >= kw:
			score += a.score
			own = true
			hi = append(hi, a.idx...)
		case kw > 0:
			score += kw
			own = own || inKeys
		default:
			return 0, nil, false
		}
	}
	if !own {
		return 0, nil, false
	}
	slices.Sort(hi)
	return score + it.boost, slices.Compact(hi), true
}

// runesIndex is where q starts in t, or -1.
func runesIndex(t, q []rune) int {
	for i := 0; i+len(q) <= len(t); i++ {
		if slices.Equal(t[i:i+len(q)], q) {
			return i
		}
	}
	return -1
}

// runeIndexFrom is where r is in t from index from on, or -1.
func runeIndexFrom(t []rune, r rune, from int) int {
	for i := from; i < len(t); i++ {
		if t[i] == r {
			return i
		}
	}
	return -1
}

// The files of a computer, ranked as the web's @ mentions are (web/src/lib/mentions.ts).

// fileCandidate is a file or folder of the index; a folder's path ends with a slash.
type fileCandidate struct {
	path, name, dir string
	folder          bool
	files           int // a folder's files, at any depth
	changed         bool
}

// fileRanked is a candidate ranked for a query: the span of its name that matches, or for a
// fuzzy match the letters of its path, as byte offsets into the path.
type fileRanked struct {
	c     fileCandidate
	tier  int
	cost  int
	match []int // [start, end) in the name
	hits  []int
}

// fileCandidates are the index as files and folders.
func fileCandidates(paths, changed []string) []fileCandidate {
	isChanged := map[string]bool{}
	for _, p := range changed {
		isChanged[p] = true
	}
	var out []fileCandidate
	folders := map[string]int{}
	var order []string
	for _, p := range paths {
		cut := strings.LastIndex(p, "/")
		out = append(out, fileCandidate{path: p, name: p[cut+1:], dir: p[:cut+1], changed: isChanged[p]})
		for i := strings.Index(p, "/"); i >= 0; {
			f := p[:i+1]
			if _, ok := folders[f]; !ok {
				order = append(order, f)
			}
			folders[f]++
			j := strings.Index(p[i+1:], "/")
			if j < 0 {
				break
			}
			i += j + 1
		}
	}
	for _, f := range order {
		cut := strings.LastIndex(f[:len(f)-1], "/")
		out = append(out, fileCandidate{path: f, name: f[cut+1 : len(f)-1], dir: f[:cut+1], folder: true, files: folders[f]})
	}
	return out
}

// lowerASCII lowercases the ASCII letters of s, which keeps its byte offsets.
func lowerASCII(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return r
	}, s)
}

// hasInOrder reports whether q's letters are all in s, in order.
func hasInOrder(q, s string) bool {
	i := 0
	for k := 0; k < len(q); k++ {
		j := strings.IndexByte(s[i:], q[k])
		if j < 0 {
			return false
		}
		i += j + 1
	}
	return true
}

// fuzzyCost is what a fuzzy run costs: the letters it skips, less two for each it has at a
// word's start.
func fuzzyCost(hits []int, s string, n int) int {
	c := hits[len(hits)-1] - hits[0] + 1 - n
	for _, i := range hits {
		if i == 0 || strings.IndexByte("/-_. ", s[i-1]) >= 0 {
			c -= 2
		}
	}
	return c
}

// inName is where the letters of q are in a name, in order: the tightest run from any start,
// which may skip at most a few more letters than q has.
func inName(q, s string) (int, []int, bool) {
	bestCost, found := 0, false
	var best []int
	for st := strings.IndexByte(s, q[0]); st >= 0; {
		hits := []int{st}
		for i := st; len(hits) < len(q); {
			j := strings.IndexByte(s[i+1:], q[len(hits)])
			if j < 0 {
				break
			}
			i += j + 1
			hits = append(hits, i)
		}
		if len(hits) < len(q) {
			break
		}
		if hits[len(hits)-1]-hits[0]+1-len(q) <= len(q)+3 {
			if c := fuzzyCost(hits, s, len(q)); !found || c < bestCost {
				bestCost, best, found = c, hits, true
			}
		}
		j := strings.IndexByte(s[st+1:], q[0])
		if j < 0 {
			break
		}
		st += j + 1
	}
	return bestCost, best, found
}

// alongPath is where the letters of q are along a path that has them all: the run nearest its
// end, found backward in one pass, so the name's letters count first.
func alongPath(q, s string) (int, []int) {
	hits := make([]int, len(q))
	i := len(s)
	for b := len(q) - 1; b >= 0; b-- {
		i = strings.LastIndexByte(s[:max(i, 0)], q[b])
		hits[b] = max(i, 0)
	}
	return fuzzyCost(hits, s, len(q)), hits
}

// fileAhead orders two ranked files: the tier, the cost, changed first, files before folders,
// the shorter path, the path.
func fileAhead(a, b fileRanked) int {
	bool01 := func(v bool) int {
		if v {
			return 1
		}
		return 0
	}
	switch {
	case a.tier != b.tier:
		return a.tier - b.tier
	case a.cost != b.cost:
		return a.cost - b.cost
	case a.c.changed != b.c.changed:
		return bool01(b.c.changed) - bool01(a.c.changed)
	case a.c.folder != b.c.folder:
		return bool01(a.c.folder) - bool01(b.c.folder)
	case len(a.c.path) != len(b.c.path):
		return len(a.c.path) - len(b.c.path)
	}
	return strings.Compare(a.c.path, b.c.path)
}

// rankFiles are the candidates for a query, best first: names that start with it, then names
// that contain it, then paths that do, then names and then paths that have its letters in order
// (fuzzy, the tighter first, and only while the others leave room). A query with a slash looks
// in that folder, and an empty one after it lists the folder. Without a query, the changed files
// and the top of the workspace.
func rankFiles(all []fileCandidate, query string, limit int) []fileRanked {
	q := lowerASCII(query)
	slash := strings.LastIndex(q, "/")
	within, term := q[:slash+1], q[slash+1:]
	var scored []fileRanked
	type rest struct {
		c         fileCandidate
		name, rel string
	}
	var others []rest
	for _, c := range all {
		p := lowerASCII(c.path)
		if within != "" && !strings.HasPrefix(p, within) || p == within {
			continue
		}
		if term == "" {
			switch {
			case lowerASCII(c.dir) == within:
				tier := 1
				if c.folder {
					tier = 0
				}
				scored = append(scored, fileRanked{c: c, tier: tier})
			case within == "" && c.changed:
				scored = append(scored, fileRanked{c: c, tier: -1})
			}
			continue
		}
		name := lowerASCII(c.name)
		rel := p[len(within):]
		switch at := strings.Index(name, term); {
		case at == 0:
			scored = append(scored, fileRanked{c: c, tier: 0, match: []int{0, len(term)}})
		case at > 0:
			scored = append(scored, fileRanked{c: c, tier: 1, match: []int{at, at + len(term)}})
		case strings.Contains(rel, term):
			scored = append(scored, fileRanked{c: c, tier: 2})
		default:
			others = append(others, rest{c, name, rel})
		}
	}
	sort.SliceStable(scored, func(i, j int) bool { return fileAhead(scored[i], scored[j]) < 0 })
	room := limit - len(scored)
	var fuzzyHits []fileRanked
	if len(term) >= 2 && room > 0 {
		for _, o := range others {
			if !hasInOrder(term, o.rel) {
				continue
			}
			var f fileRanked
			if cost, hits, ok := inName(term, o.name); ok {
				f = fileRanked{c: o.c, tier: 3, cost: cost, hits: shift(hits, len(o.c.dir))}
			} else if len(term) >= 3 {
				cost, hits := alongPath(term, o.rel)
				f = fileRanked{c: o.c, tier: 4, cost: cost, hits: shift(hits, len(within))}
			} else {
				continue
			}
			if len(fuzzyHits) == room && fileAhead(f, fuzzyHits[room-1]) >= 0 {
				continue
			}
			k := len(fuzzyHits)
			for k > 0 && fileAhead(f, fuzzyHits[k-1]) < 0 {
				k--
			}
			fuzzyHits = slices.Insert(fuzzyHits, k, f)
			if len(fuzzyHits) > room {
				fuzzyHits = fuzzyHits[:room]
			}
		}
	}
	return append(scored[:min(len(scored), limit)], fuzzyHits...)
}

// shift adds d to each index.
func shift(idx []int, d int) []int {
	out := make([]int, len(idx))
	for i, v := range idx {
		out[i] = v + d
	}
	return out
}

// fileHits are the files of the computer in view that match the query, each opening in its
// Files tab.
func (m *app) fileHits(query string) []searchHit {
	sb := m.computerOn()
	if sb == nil {
		return nil
	}
	ix := m.search.files[sb.ID]
	if ix == nil || !ix.loaded {
		return nil
	}
	var out []searchHit
	for _, r := range rankFiles(ix.all, query, 12) {
		hi := slices.Clone(r.hits)
		if r.match != nil {
			for i := r.match[0]; i < r.match[1]; i++ {
				hi = append(hi, len(r.c.dir)+i)
			}
		}
		c, id := r.c, sb.ID
		out = append(out, searchHit{
			item: searchItem{
				id: "file:" + c.path, group: groupFiles, title: c.path, place: sb.Name,
				run: func(m *app) tea.Cmd { return m.openFileOf(id, c) },
			},
			hi: runeOffsets(c.path, hi),
		})
	}
	return out
}

// runeOffsets turns byte offsets into s into rune indexes.
func runeOffsets(s string, offsets []int) []int {
	want := map[int]bool{}
	for _, o := range offsets {
		want[o] = true
	}
	var out []int
	k := 0
	for i := range s {
		if want[i] {
			out = append(out, k)
		}
		k++
	}
	return out
}

// openFileOf shows a file or folder of a computer in its Files tab: a folder listed, a file
// opened beside its folder.
func (m *app) openFileOf(id string, c fileCandidate) tea.Cmd {
	cmd := m.showTab(id, tabFiles)
	dir := strings.TrimSuffix(c.dir, "/")
	f := filesState{sandbox: id, gen: m.files.gen + 1}
	if c.folder {
		f.dir = strings.TrimSuffix(c.path, "/")
	} else {
		f.dir, f.goal = dir, c.name
		f.open = &fileView{path: c.path}
	}
	m.files = f
	return cmd
}

// searchFiles reads the file index of the computer in view when the query asks for files and
// the one kept is older than fileIndexEvery.
func (m *app) searchFiles() tea.Cmd {
	raw := m.search.query.value()
	sb := m.computerOn()
	if !strings.HasPrefix(raw, "/") || sb == nil || phaseOf(*sb) != phaseReady || m.client == nil {
		return nil
	}
	if m.search.files == nil {
		m.search.files = map[string]*fileIndex{}
	}
	ix := m.search.files[sb.ID]
	if ix == nil {
		ix = &fileIndex{}
		m.search.files[sb.ID] = ix
	}
	if ix.busy || ix.loaded && m.now.Sub(ix.at) < fileIndexEvery && ix.err == "" {
		return nil
	}
	ix.busy = true
	ctx, client, env, id := m.ctx, m.client, m.env.ID, sb.ID
	return func() tea.Msg {
		rctx, cancel := context.WithTimeout(ctx, requestTimeout)
		defer cancel()
		index, err := client.FileIndex(rctx, env, id)
		return fileIndexLoaded{sandbox: id, index: index, err: err}
	}
}

// searchUpdate applies the file index read. ok is false for other messages.
func (m *app) searchUpdate(msg tea.Msg) (cmd tea.Cmd, ok bool) {
	loaded, ok := msg.(fileIndexLoaded)
	if !ok {
		return nil, false
	}
	ix := m.search.files[loaded.sandbox]
	if ix == nil {
		return nil, true
	}
	ix.busy, ix.at = false, m.now
	if loaded.err != nil {
		ix.err = loaded.err.Error()
		return nil, true
	}
	ix.err, ix.loaded, ix.truncated = "", true, loaded.index.Truncated
	ix.all = fileCandidates(loaded.index.Paths, loaded.index.Changed)
	return nil, true
}

// searchEmpty says why the search lists nothing.
func (m *app) searchEmpty() string {
	raw, scope, q := m.searchQuery()
	if strings.HasPrefix(raw, "/") {
		sb := m.computerOn()
		if sb == nil {
			return "Open a computer to search its files."
		}
		if phaseOf(*sb) != phaseReady {
			return "Start " + sb.Name + " to search its files."
		}
		ix := m.search.files[sb.ID]
		switch {
		case ix != nil && ix.err != "":
			return "Can't list " + sb.Name + "'s files: " + ix.err
		case ix == nil || !ix.loaded:
			return "Listing " + sb.Name + "'s files…"
		}
		return "No file of " + sb.Name + " matches “" + strings.TrimSpace(raw[1:]) + "”."
	}
	if q == "" && scope == "" {
		return "Nothing opened yet, and nothing waits for you."
	}
	return "Nothing matches “" + strings.TrimSpace(raw) + "”. Try a teammate, a computer, a thread, a setting or an action."
}

// searchMinW is the width below which Search Studio takes the whole body.
const searchMinW = 44

// drawSearch draws Search Studio over the frame's lines: a box centered over the body, at most
// 64 cells wide and 16 rows high, or the whole body on a narrow screen. It records its rows for
// the mouse.
func (m *app) drawSearch(lines []string, w, rows int) {
	m.search.rows = m.search.rows[:0]
	if rows < 5 || w < 12 {
		return
	}
	bw, maxH, top, x := min(w-8, 64), min(rows-2, 16), bodyTop+1, 0
	full := w < searchMinW
	if full {
		bw, maxH, top = w, rows, bodyTop
	}
	x = (w - bw) / 2
	inner := bw - 2
	border := styleAccent
	if m.look.plain {
		border = styleBold
	}
	edge := border.Render("│")

	raw := m.search.query.value()
	var field string
	if raw == "" {
		field = styleBold.Render(" › ") + styleAccent.Render("▏") + styleDim.Render(clip("Teammates, computers, threads, settings, actions", max(inner-4, 0)))
	} else {
		field = m.search.query.line(" › ", inner)
	}
	groups := m.searchGroups()
	hits := searchRows(groups)
	sel := min(m.search.sel, max(len(hits)-1, 0))

	// The list: each group's label, then its hits.
	type line struct {
		text string
		hit  int // the hit it draws, or -1
	}
	var list []line
	at := 0
	selLine := 0
	for _, g := range groups {
		list = append(list, line{" " + styleDim.Render(strings.ToUpper(g.group)), -1})
		for _, h := range g.hits {
			if at == sel {
				selLine = len(list)
			}
			list = append(list, line{m.searchLine(h, at == sel, inner), at})
			at++
		}
	}
	if len(hits) == 0 {
		text := lipgloss.NewStyle().Width(max(inner-2, 1)).Render(m.searchEmpty())
		for _, l := range strings.Split(text, "\n") {
			list = append(list, line{" " + styleDim.Render(l), -1})
		}
	}
	room := maxH - 4
	if !full {
		room = min(room, len(list))
	}
	offset := 0
	if selLine >= room {
		offset = selLine - room + 1
	}
	if selLine-1 == offset && offset > 0 {
		offset-- // keep the group's label in view above its first hit
	}
	head := "─ Search Studio "
	box := []string{border.Render("╭─") + " " + styleBold.Render("Search Studio") + " " +
		border.Render(strings.Repeat("─", max(inner-lipgloss.Width(head), 0))+"╮")}
	box = append(box, edge+fitStyled(field, inner)+edge, edge+border.Render(strings.Repeat("─", inner))+edge)
	for i := 0; i < room; i++ {
		text, hit := "", -1
		if offset+i < len(list) {
			text, hit = list[offset+i].text, list[offset+i].hit
		}
		if hit >= 0 {
			m.search.rows = append(m.search.rows, searchRowHit{y: top + len(box), x0: x + 1, x1: x + 1 + inner, index: hit})
		}
		box = append(box, edge+fitStyled(text, inner)+edge)
	}
	foot := "─ ↑↓ move · enter open · esc close "
	if inner-lipgloss.Width(foot) < 1 {
		foot = "─ esc close "
	}
	foot = clip(foot, inner)
	box = append(box, border.Render("╰─")+styleDim.Render(strings.TrimPrefix(foot, "─"))+
		border.Render(strings.Repeat("─", max(inner-lipgloss.Width(foot), 0))+"╯"))
	m.search.box = rect{x, top, bw, len(box)}
	for i, b := range box {
		y := top + i
		if y >= len(lines) {
			return
		}
		left := fitStyled(ansi.Cut(lines[y], 0, x), x)
		lines[y] = left + b + ansi.Cut(lines[y], x+bw, w)
	}
}

// searchLine is a hit's line in w cells: the cursor, its glyph, its title with the letters that
// matched in bold, and where it lives on the right.
func (m *app) searchLine(h searchHit, selected bool, w int) string {
	cursor := " "
	if selected {
		mark := "▌"
		if m.look.plain {
			mark = "▸"
		}
		cursor = styleAccent.Render(mark)
	}
	glyph := h.item.glyph
	if glyph == "" {
		glyph = " "
	}
	place := h.item.place
	if strings.Contains(strings.ToLower(h.item.title), strings.ToLower(place)) {
		place = "" // the title says it
	}
	placeW := min(lipgloss.Width(place), max(w/2-2, 0))
	room := w - 3 - placeW - 2
	title := highlight(h.item.title, h.hi, selected)
	left := cursor + glyph + " " + clip(title, max(room, 1))
	if place == "" {
		return left
	}
	return spread(left, styleDim.Render(clip(place, placeW))+" ", w)
}

// highlight draws text with the runes at hi in the accent, bold; the selected line's title is
// bold throughout.
func highlight(text string, hi []int, selected bool) string {
	base := lipgloss.NewStyle()
	if selected {
		base = styleBold
	}
	if len(hi) == 0 {
		return base.Render(text)
	}
	mark := map[int]bool{}
	for _, i := range hi {
		mark[i] = true
	}
	var b strings.Builder
	var run []rune
	on := false
	flush := func() {
		if len(run) == 0 {
			return
		}
		if on {
			b.WriteString(styleAccent.Bold(true).Render(string(run)))
		} else {
			b.WriteString(base.Render(string(run)))
		}
		run = run[:0]
	}
	k := 0
	for _, r := range text {
		if mark[k] != on {
			flush()
			on = mark[k]
		}
		run = append(run, r)
		k++
	}
	flush()
	return b.String()
}

// searchClick answers a click while Search Studio is open: on a hit it opens it, outside the box
// it closes it.
func (m *app) searchClick(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	for _, r := range m.search.rows {
		if r.y == msg.Y && msg.X >= r.x0 && msg.X < r.x1 {
			hits := searchRows(m.searchGroups())
			if r.index < len(hits) {
				return m, m.runSearch(hits[r.index].item)
			}
			return m, nil
		}
	}
	if !m.search.box.has(msg.X, msg.Y) {
		m.closeSearch()
	}
	return m, nil
}

// searchBindings are the keys the key bar shows while Search Studio is open.
func (m *app) searchBindings() []key.Binding {
	return []key.Binding{keySearchMove, keyOpen, keySearchClose}
}

var (
	keySearchMove  = key.NewBinding(key.WithKeys("down"), key.WithHelp("↑/↓", "move"))
	keySearchClose = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "close"))
)
