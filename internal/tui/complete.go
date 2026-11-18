package tui

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/chat"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
)

// The composer completes as you type, in a box over it. A draft that starts with / lists the
// commands: they go where a key would, so the keys of a thread needn't be known. @ at the start
// of a word lists the files of the thread's computer, as the web's composer does
// (web/src/components/chat/composer.tsx), and a pick goes into the prompt as @path with the
// file mentioned.

type menuKind int

const (
	menuNone menuKind = iota
	menuSlash
	menuMention
)

// menuAnchor is where a menu opened: its kind and the byte offset of its / or @ in the draft.
type menuAnchor struct {
	kind menuKind
	at   int
}

// menuRows is how many items the composer's menu shows at most.
const (
	menuRows    = 9
	mentionRows = 8 // as the web's picker shows
)

// slashCommand is a command of the composer's / menu. sb is the thread's computer, nil when it
// went; shown leaves a command out where it can't go.
type slashCommand struct {
	name  string
	hint  func(m *app, sb *sandboxes.View) string
	shown func(m *app, sb *sandboxes.View) bool
	run   func(m *app, sb *sandboxes.View) tea.Cmd
}

// menuItem is a row of the composer's menu: a command or a file.
type menuItem struct {
	cmd  *slashCommand
	file *fileRanked
	hi   []int // the label's letters that match, as rune indexes
}

var (
	keyMenuMove  = key.NewBinding(key.WithKeys("down"), key.WithHelp("↑/↓", "move"))
	keyMenuTab   = key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "complete"))
	keyMenuRun   = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "run"))
	keyMenuAdd   = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "add"))
	keyMenuInto  = key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "into a folder"))
	keyMenuClose = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "close"))
)

// computerShows reports whether sb is there and has its tabs: not while it is made or failed.
func computerShows(sb *sandboxes.View) bool {
	if sb == nil {
		return false
	}
	ph := phaseOf(*sb)
	return ph != phaseCreating && ph != phaseFailed
}

func computerReady(sb *sandboxes.View) bool { return sb != nil && phaseOf(*sb) == phaseReady }

// slashCommands are the composer's commands, in the order the menu lists them.
var slashCommands = []slashCommand{
	{
		name: "term",
		hint: func(m *app, sb *sandboxes.View) string {
			p := &m.ports
			if p.sandbox != sb.ID || !p.loaded || len(p.terminals) == 0 {
				return sb.Name + "'s terminal"
			}
			names := make([]string, 0, len(p.terminals))
			for _, t := range p.terminals {
				names = append(names, t.Name)
			}
			return sb.Name + "'s terminal: " + strings.Join(names, ", ")
		},
		shown: func(_ *app, sb *sandboxes.View) bool { return computerShows(sb) },
		run:   func(m *app, _ *sandboxes.View) tea.Cmd { return m.shellTab() },
	},
	{
		name:  "files",
		hint:  func(_ *app, sb *sandboxes.View) string { return "browse " + sb.Name + "'s files" },
		shown: func(_ *app, sb *sandboxes.View) bool { return computerReady(sb) },
		run:   func(m *app, sb *sandboxes.View) tea.Cmd { return m.showTab(sb.ID, tabFiles) },
	},
	{
		name:  "changes",
		hint:  func(_ *app, sb *sandboxes.View) string { return "what changed on " + sb.Name + ", as a diff" },
		shown: func(_ *app, sb *sandboxes.View) bool { return computerReady(sb) },
		run:   func(m *app, _ *sandboxes.View) tea.Cmd { m.changesView(); return nil },
	},
	{
		name:  "browser",
		hint:  func(m *app, sb *sandboxes.View) string { return m.teammateName() + "'s browser on " + sb.Name },
		shown: func(_ *app, sb *sandboxes.View) bool { return computerShows(sb) && sb.TeammateID != "" },
		run:   func(m *app, sb *sandboxes.View) tea.Cmd { return m.showTab(sb.ID, tabBrowser) },
	},
	{
		name:  "details",
		hint:  func(_ *app, sb *sandboxes.View) string { return sb.Name + "'s status, ports and checkpoints" },
		shown: func(_ *app, sb *sandboxes.View) bool { return computerShows(sb) },
		run:   func(m *app, sb *sandboxes.View) tea.Cmd { return m.showTab(sb.ID, tabDetails) },
	},
	{
		name:  "threads",
		hint:  func(m *app, _ *sandboxes.View) string { return m.teammateName() + "'s threads, in the sidebar" },
		shown: func(m *app, _ *sandboxes.View) bool { return m.here.kind == placeThread || m.here.kind == placeMate },
		run: func(m *app, _ *sandboxes.View) tea.Cmd {
			if !m.sidebarKeys() {
				m.toggleSidebar()
			}
			m.cursorToHere()
			return nil
		},
	},
	{
		name: "new",
		hint: func(m *app, _ *sandboxes.View) string { return "a new thread with " + m.teammateName() },
		shown: func(m *app, sb *sandboxes.View) bool {
			return m.chats.id != "" && sb != nil && sb.TeammateID != ""
		},
		run: func(m *app, sb *sandboxes.View) tea.Cmd { return m.newThread(sb.TeammateID) },
	},
	{
		name:  "stop",
		hint:  func(m *app, _ *sandboxes.View) string { return "stop " + m.teammateName() + "'s turn" },
		shown: func(m *app, _ *sandboxes.View) bool { return m.chats.id != "" && m.chats.running() },
		run:   func(m *app, _ *sandboxes.View) tea.Cmd { return m.stopTurn() },
	},
	{
		name: "start",
		hint: func(_ *app, sb *sandboxes.View) string {
			if phaseOf(*sb) == phaseSuspended {
				return "resume " + sb.Name
			}
			return "start " + sb.Name
		},
		shown: func(_ *app, sb *sandboxes.View) bool {
			return sb != nil && (phaseOf(*sb) == phaseStopped || phaseOf(*sb) == phaseSuspended)
		},
		run: func(m *app, sb *sandboxes.View) tea.Cmd { return m.toggle(sb) },
	},
	{
		name:  "stop-computer",
		hint:  func(_ *app, sb *sandboxes.View) string { return "stop " + sb.Name },
		shown: func(_ *app, sb *sandboxes.View) bool { return computerReady(sb) },
		run:   func(m *app, sb *sandboxes.View) tea.Cmd { return m.toggle(sb) },
	},
	{
		name:  "home",
		hint:  func(_ *app, _ *sandboxes.View) string { return "what waits for you, what works and what is quiet" },
		shown: func(_ *app, _ *sandboxes.View) bool { return true },
		run:   func(m *app, _ *sandboxes.View) tea.Cmd { return m.open(place{}) },
	},
	{
		name:  "search",
		hint:  func(_ *app, _ *sandboxes.View) string { return "find anything by name, as ctrl+k" },
		shown: func(_ *app, _ *sandboxes.View) bool { return true },
		run:   func(m *app, _ *sandboxes.View) tea.Cmd { return m.openSearch("") },
	},
	{
		name:  "keys",
		hint:  func(_ *app, _ *sandboxes.View) string { return "every key of this place" },
		shown: func(_ *app, _ *sandboxes.View) bool { return true },
		run:   func(m *app, _ *sandboxes.View) tea.Cmd { m.openHelp(); return nil },
	},
}

// mentionAt finds the @ the draft ends in, as the web's activeQuery does: at the start or after
// a space, and what follows it, which has no space, @ or quote.
var mentionAt = regexp.MustCompile(`(^|\s)@([^\s@"]*)$`)

// rawMenu is the menu the draft asks for, closed or not: its anchor and what follows it.
func (m *app) rawMenu() (menuAnchor, string) {
	text := m.chats.draft.value()
	if strings.HasPrefix(text, "/") && !strings.ContainsAny(text, " \t\n") {
		return menuAnchor{menuSlash, 0}, text[1:]
	}
	if loc := mentionAt.FindStringSubmatchIndex(text); loc != nil {
		return menuAnchor{menuMention, loc[3]}, text[loc[4]:loc[5]]
	}
	return menuAnchor{}, ""
}

// menu is the composer's open menu: closed with esc, it stays closed until the draft asks for
// another.
func (m *app) menu() (menuAnchor, string) {
	c := &m.chats
	if !c.composing {
		return menuAnchor{}, ""
	}
	a, q := m.rawMenu()
	if a.kind == menuNone || a == c.menuShut {
		return menuAnchor{}, ""
	}
	return a, q
}

// chatComputer is the thread's computer as the environment has it now, nil when it went.
func (m *app) chatComputer() *sandboxes.View {
	id := m.chats.sandbox.ID
	if id == "" {
		return nil
	}
	sb, ok := m.sandboxByID(id)
	if !ok {
		return nil
	}
	return &sb
}

// menuItems are the rows of the open menu: the commands that match, those whose name starts
// with what is typed first; or the files that match, ranked as the web ranks them.
func (m *app) menuItems() []menuItem {
	a, q := m.menu()
	sb := m.chatComputer()
	switch a.kind {
	case menuSlash:
		type scored struct {
			item  menuItem
			score float64
		}
		var out []scored
		for i := range slashCommands {
			cmd := &slashCommands[i]
			if !cmd.shown(m, sb) {
				continue
			}
			if q == "" {
				out = append(out, scored{item: menuItem{cmd: cmd}})
				continue
			}
			if strings.HasPrefix(cmd.name, strings.ToLower(q)) {
				hi := make([]int, len([]rune(q)))
				for j := range hi {
					hi[j] = j + 1 // after the /
				}
				out = append(out, scored{item: menuItem{cmd: cmd, hi: hi}, score: 1000})
				continue
			}
			if f, ok := fuzzy(strings.ToLower(q), cmd.name); ok {
				hi := make([]int, len(f.idx))
				for j, x := range f.idx {
					hi[j] = x + 1
				}
				out = append(out, scored{item: menuItem{cmd: cmd, hi: hi}, score: f.score})
			}
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i].score > out[j].score })
		items := make([]menuItem, len(out))
		for i, s := range out {
			items[i] = s.item
		}
		return items
	case menuMention:
		if sb == nil {
			return nil
		}
		ix := m.search.files[sb.ID]
		if ix == nil || !ix.loaded {
			return nil
		}
		query, _, _ := splitLines(q)
		var items []menuItem
		for _, r := range rankFiles(ix.all, query, mentionRows) {
			hi := slices.Clone(r.hits)
			if r.match != nil {
				for i := r.match[0]; i < r.match[1]; i++ {
					hi = append(hi, len(r.c.dir)+i)
				}
			}
			items = append(items, menuItem{file: &r, hi: runeOffsets(r.c.path, hi)})
		}
		return items
	}
	return nil
}

// linesQuery splits the lines off a mention's query, as the web's splitLines does:
// coupon.ts:20-28 is coupon.ts, from line 20 to 28.
var linesQuery = regexp.MustCompile(`^(.*):(\d*)(?:-(\d*))?$`)

func splitLines(q string) (query string, start, end int) {
	sub := linesQuery.FindStringSubmatch(q)
	if sub == nil {
		return q, 0, 0
	}
	start, _ = strconv.Atoi(sub[2])
	end, _ = strconv.Atoi(sub[3])
	if start == 0 || end <= start {
		end = 0 // Studio takes an end only after a start
	}
	return sub[1], start, end
}

// menuKey answers a key while the composer's menu is open. ok is false for the keys the
// composer takes as it always does.
func (m *app) menuKey(msg tea.KeyPressMsg) (cmd tea.Cmd, ok bool) {
	c := &m.chats
	a, _ := m.menu()
	if a.kind == menuNone {
		return nil, false
	}
	items := m.menuItems()
	switch msg.String() {
	case "up", "ctrl+p":
		if len(items) > 0 {
			c.menuSel = (c.menuSel + len(items) - 1) % len(items)
		}
		return nil, true
	case "down", "ctrl+n":
		if len(items) > 0 {
			c.menuSel = (c.menuSel + 1) % len(items)
		}
		return nil, true
	case "esc":
		c.menuShut = a
		return nil, true
	case "tab":
		if len(items) == 0 {
			return nil, true
		}
		it := items[min(c.menuSel, len(items)-1)]
		if it.cmd != nil {
			c.draft.set("/" + it.cmd.name)
			return nil, true
		}
		return m.menuPick(it), true
	case "enter":
		if len(items) == 0 {
			return nil, false // no command by that name: the draft goes as text
		}
		return m.menuPick(items[min(c.menuSel, len(items)-1)]), true
	}
	return nil, false
}

// menuPick runs a command, or puts a file into the draft: a folder narrows the menu to what is
// in it, and a file goes in as @path with a space after it, the file mentioned.
func (m *app) menuPick(it menuItem) tea.Cmd {
	c := &m.chats
	c.menuSel = 0
	if it.cmd != nil {
		sb := m.chatComputer()
		c.draft.set("")
		c.mentions, c.menuShut = nil, menuAnchor{}
		c.composing = false
		return it.cmd.run(m, sb)
	}
	a, q := m.menu()
	text := c.draft.value()
	if it.file.c.folder {
		c.draft.set(text[:a.at] + "@" + it.file.c.path)
		return m.mentionIndex()
	}
	_, start, end := splitLines(q)
	mention := chat.Mention{Path: it.file.c.path, Start: start, End: end}
	c.mentions = append(c.mentions, mention)
	c.draft.set(text[:a.at] + mentionText(mention) + " ")
	return nil
}

// mentionIndex reads the thread's computer's file index while @ is being typed.
func (m *app) mentionIndex() tea.Cmd {
	a, _ := m.menu()
	sb := m.chatComputer()
	if a.kind != menuMention || sb == nil {
		return nil
	}
	return m.readFileIndex(*sb)
}

// menuEdited follows an edit of the draft: the cursor goes back to the first row, a menu closed
// with esc may open again once its / or @ is gone, and @ reads the files.
func (m *app) menuEdited() tea.Cmd {
	c := &m.chats
	c.menuSel = 0
	if a, _ := m.rawMenu(); a != c.menuShut {
		c.menuShut = menuAnchor{}
	}
	return m.mentionIndex()
}

// menuEmpty says why the open menu lists nothing.
func (m *app) menuEmpty() string {
	a, q := m.menu()
	if a.kind == menuSlash {
		return "No command is called /" + q + ": enter sends it as text."
	}
	sb := m.chatComputer()
	if sb == nil {
		return "This thread's computer is gone."
	}
	if phaseOf(*sb) != phaseReady {
		return "Start " + sb.Name + " to mention its files."
	}
	ix := m.search.files[sb.ID]
	switch {
	case ix != nil && ix.err != "":
		return "Can't list the files: " + ix.err
	case ix == nil || !ix.loaded:
		return "Listing the files…"
	}
	return "No file matches."
}

// menuBox is the open menu as a box of w cells and at most room rows, for over the composer,
// and the item each of its lines shows (-1 for none), for the mouse.
func (m *app) menuBox(w, room int) (out []string, shows []int) {
	a, q := m.menu()
	if a.kind == menuNone || w < 20 || room < 3 {
		return nil, nil
	}
	c := &m.chats
	items := m.menuItems()
	frame := styleDim
	if !m.look.plain {
		frame = m.pal.hue(areaChat)
	}
	inner := w - 2
	title := ""
	keys := " ↑↓ · tab completes · enter runs "
	if a.kind == menuMention {
		title = " Files in /workspace "
		if query, _, _ := splitLines(q); query != "" {
			title += "─ “" + query + "” "
		}
		keys = " ↑↓ · enter adds · tab into a folder · :12 lines "
	}
	out = []string{edge(frame, "╭", title, "╮", w)}
	rows := min(menuRows, room-2)
	if len(items) == 0 {
		out = append(out, frame.Render("│")+" "+fitStyled(styleDim.Render(m.menuEmpty()), inner-1)+frame.Render("│"))
		return append(out, edge(frame, "╰", keys, "╯", w)), nil
	}
	sel := min(c.menuSel, len(items)-1)
	first := 0
	if sel >= rows {
		first = sel - rows + 1
	}
	labelW := 0
	for _, it := range items {
		labelW = max(labelW, lipgloss.Width(menuLabel(it)))
	}
	for i := first; i < min(first+rows, len(items)); i++ {
		it := items[i]
		mark := "  "
		if i == sel {
			mark = styleAccent.Render("▌") + " "
			if m.look.plain {
				mark = "> "
			}
		}
		label := menuLabel(it)
		line := mark + highlight(label, it.hi, i == sel) + strings.Repeat(" ", labelW-lipgloss.Width(label)+2) + styleDim.Render(m.menuHint(it))
		out = append(out, frame.Render("│")+fitStyled(line, inner)+frame.Render("│"))
		for len(shows) < len(out)-1 {
			shows = append(shows, -1)
		}
		shows = append(shows, i)
	}
	return append(out, edge(frame, "╰", keys, "╯", w)), shows
}

// menuLabel is how an item reads in the menu: /name, or the file's path.
func menuLabel(it menuItem) string {
	if it.cmd != nil {
		return "/" + it.cmd.name
	}
	return it.file.c.path
}

// menuHint is what an item says beside its label.
func (m *app) menuHint(it menuItem) string {
	if it.cmd != nil {
		return it.cmd.hint(m, m.chatComputer())
	}
	c := it.file.c
	switch {
	case c.folder && c.files == 1:
		return "1 file"
	case c.folder:
		return fmt.Sprintf("%d files", c.files)
	case c.changed:
		return "changed"
	}
	return ""
}

// edge is a box's top or bottom border of w cells with a label after its corner.
func edge(frame lipgloss.Style, left, label, right string, w int) string {
	if label != "" && lipgloss.Width(label)+4 > w {
		label = ""
	}
	fill := max(w-3-lipgloss.Width(label), 0)
	if label == "" {
		return frame.Render(left + strings.Repeat("─", w-2) + right)
	}
	return frame.Render(left+"─") + styleDim.Render(label) + frame.Render(strings.Repeat("─", fill)+right)
}

// menuBindings are the keys the key bar shows while the composer's menu is open.
func (m *app) menuBindings() []key.Binding {
	a, _ := m.menu()
	if a.kind == menuMention {
		return []key.Binding{keyMenuMove, keyMenuAdd, keyMenuInto, keyMenuClose}
	}
	return []key.Binding{keyMenuMove, keyMenuTab, keyMenuRun, keyMenuClose}
}

// menuClick picks the menu's row i.
func (m *app) menuClick(i int) tea.Cmd {
	items := m.menuItems()
	if i < 0 || i >= len(items) {
		return nil
	}
	m.chats.menuSel = i
	return m.menuPick(items[i])
}
