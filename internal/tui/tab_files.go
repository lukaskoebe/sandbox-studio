package tui

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif" // the files the Files tab draws as pictures
	_ "image/png"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/api"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
)

// The Files tab is the web's Files tab, to read: a folder of the computer and the file opened
// from it, as text with its line numbers or as a picture. From a pane of filesBeside cells the
// folder's list is beside the file; narrower, one shows at a time. r goes from /workspace to
// /tmp to the whole computer. Editing and uploads stay in the browser.

const (
	filesBeside = 80               // the pane width from which the list is beside the file
	filesListW  = 30               // the list's width beside the file
	filesEvery  = 10 * time.Second // how often the folder on show is read again
)

// filesRoots are the roots r goes through, as the web's root menu has them: /workspace, /tmp
// and the whole computer.
var filesRoots = []string{"", agentproto.FilesRootTmp, agentproto.FilesRootFS}

// filesState is the Files tab of the computer on show.
type filesState struct {
	sandbox string
	gen     int    // counts the computers shown, so the reads of one left behind are dropped
	root    string // "", "tmp" or "fs", as filesRoots has them
	dir     string // the folder on show, relative to the root; "" for the root itself

	listed    string // the folder the entries are of, as folderKey has it
	entries   []agentproto.FileEntry
	truncated bool   // more entries than the guest lists
	err       string // why the folder couldn't be read
	busy      bool   // a read of the folder is under way
	stale     bool   // the folder is read again at the next watch
	serial    int    // counts the waits for the next read, so an earlier one is dropped

	cursor int       // the entry under the cursor
	goal   string    // the entry the cursor goes to once the folder is read: the one left
	open   *fileView // the file opened, which stays beside the list as it moves
}

// fileView is a file opened in the Files tab.
type fileView struct {
	root, path string // the file, by its root and its path under it
	mod        int64  // its time of change when it was opened, so a newer one is read again
	loaded     bool
	stale      bool // it changed since it was read: read again, showing it meanwhile
	busy       bool
	size       int64
	lines      []string    // its text, cleaned, by line
	img        image.Image // its picture
	note       string      // why it shows neither: "Larger than 1 MiB…"
	err        string
	scroll     int
	drawn      []string // the picture as drawn, at drawnW cells
	drawnW     int
}

type (
	filesLoaded struct {
		gen  int
		key  string
		list api.FileList
		err  error
	}
	fileLoaded struct {
		gen        int
		root, path string
		content    api.FileContent
		err        error
	}
	filesTick struct{ gen, serial int }
)

// The keys of the Files tab.
var (
	keyFilesOpen  = key.NewBinding(key.WithKeys("enter", "right"), key.WithHelp("enter", "open"))
	keyFilesUp    = key.NewBinding(key.WithKeys("left", "backspace"), key.WithHelp("←", "up"))
	keyFilesClose = key.NewBinding(key.WithKeys("left", "backspace"), key.WithHelp("←", "the folder"))
	keyFilesWeb   = key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "in the browser"))
	keyFilesDown  = key.NewBinding(key.WithKeys("j", "down"), key.WithHelp("j/k", "move"))
	keyFilesUpRow = key.NewBinding(key.WithKeys("k", "up"))
)

// keyFilesRoot is r, its help naming the root it goes to.
func (m *app) keyFilesRoot() key.Binding {
	return key.NewBinding(key.WithKeys("r"), key.WithHelp("r", rootPath(nextRoot(m.files.root))))
}

// rootPath is a root's folder on the computer.
func rootPath(root string) string {
	switch root {
	case agentproto.FilesRootTmp:
		return "/tmp"
	case agentproto.FilesRootFS:
		return "/"
	}
	return "/workspace"
}

// nextRoot is the root after root.
func nextRoot(root string) string {
	i := slices.Index(filesRoots, root)
	return filesRoots[(i+1)%len(filesRoots)]
}

// absPath is a path under a root as the computer has it.
func absPath(root, rel string) string {
	if rel == "" {
		return rootPath(root)
	}
	return path.Join(rootPath(root), rel)
}

// folderKey names a folder: its root and its path.
func folderKey(root, dir string) string { return root + ":" + dir }

// filesWanted is the computer whose Files tab is on show.
func (m *app) filesWanted() string {
	if sb := m.tabOnShow(tabFiles); sb != nil {
		return sb.ID
	}
	return ""
}

// watchFiles reads the folder on show while the tab shows, again every filesEvery, and the file
// opened. A stopped computer has none to read.
func (m *app) watchFiles() tea.Cmd {
	id := m.filesWanted()
	if id == "" {
		return nil
	}
	f := &m.files
	if f.sandbox != id {
		m.files = filesState{sandbox: id, gen: f.gen + 1}
		f = &m.files
	}
	if sb, ok := m.sandboxByID(id); !ok || phaseOf(sb) != phaseReady {
		return nil
	}
	var cmds []tea.Cmd
	if k := folderKey(f.root, f.dir); !f.busy && (f.listed != k || f.stale) {
		f.busy, f.stale = true, false
		cmds = append(cmds, m.loadFiles(f.root, f.dir))
	}
	if v := f.open; v != nil && (!v.loaded || v.stale) && !v.busy {
		v.busy, v.stale = true, false
		cmds = append(cmds, m.loadFile(v.root, v.path))
	}
	return tea.Batch(cmds...)
}

// loadFiles reads a folder of the computer of the tab.
func (m *app) loadFiles(root, dir string) tea.Cmd {
	ctx, client, env, id, gen := m.ctx, m.client, m.env.ID, m.files.sandbox, m.files.gen
	return func() tea.Msg {
		rctx, cancel := requestContext(ctx)
		defer cancel()
		list, err := client.Files(rctx, env, id, root, dir)
		return filesLoaded{gen: gen, key: folderKey(root, dir), list: list, err: err}
	}
}

// loadFile reads a file of the computer of the tab.
func (m *app) loadFile(root, p string) tea.Cmd {
	ctx, client, env, id, gen := m.ctx, m.client, m.env.ID, m.files.sandbox, m.files.gen
	return func() tea.Msg {
		rctx, cancel := requestContext(ctx)
		defer cancel()
		content, err := client.FileContent(rctx, env, id, root, p)
		return fileLoaded{gen: gen, root: root, path: p, content: content, err: err}
	}
}

// filesUpdate applies the messages of the Files tab. ok is false for other messages.
func (m *app) filesUpdate(msg tea.Msg) (cmd tea.Cmd, ok bool) {
	f := &m.files
	switch msg := msg.(type) {
	case filesLoaded:
		if msg.gen != f.gen {
			return nil, true
		}
		f.busy = false
		if msg.key != folderKey(f.root, f.dir) {
			return nil, true // the folder on show changed meanwhile: watchFiles reads it
		}
		if msg.err != nil {
			f.entries, f.truncated, f.err = nil, false, filesError(msg.err, "browse")
		} else {
			f.entries, f.truncated, f.err = msg.list.Entries, msg.list.Truncated, ""
		}
		if f.listed != msg.key {
			f.cursor = 0
		}
		f.listed = msg.key
		if f.goal != "" {
			if i := slices.IndexFunc(f.entries, func(e agentproto.FileEntry) bool { return e.Name == f.goal }); i >= 0 {
				f.cursor = i
			}
			f.goal = ""
		}
		f.cursor = min(max(f.cursor, 0), max(len(f.entries)-1, 0))
		if v := f.open; v != nil && v.root == f.root && parentOf(v.path) == f.dir {
			for _, e := range f.entries {
				if e.Name == path.Base(v.path) && e.ModTime != v.mod && v.loaded {
					v.mod, v.stale = e.ModTime, true // it changed: read it again
				}
			}
		}
		f.serial++
		gen, serial := f.gen, f.serial
		return tea.Tick(filesEvery, func(time.Time) tea.Msg { return filesTick{gen: gen, serial: serial} }), true
	case filesTick:
		if msg.gen == f.gen && msg.serial == f.serial {
			f.stale = true // watchFiles reads it again, while the tab shows
		}
		return nil, true
	case fileLoaded:
		v := f.open
		if msg.gen != f.gen || v == nil || v.root != msg.root || v.path != msg.path {
			return nil, true
		}
		v.busy, v.loaded, v.drawn = false, true, nil
		if msg.err != nil {
			v.err = filesError(msg.err, "read")
			return nil, true
		}
		v.err = ""
		v.size, v.lines, v.img, v.note = fileShown(msg.content)
		return nil, true
	}
	return nil, false
}

// parentOf is the folder of a path under a root, "" for the root itself.
func parentOf(p string) string {
	if d := path.Dir(p); d != "." {
		return d
	}
	return ""
}

// filesError says why a folder or file couldn't be read, in the TUI's words: an older guest
// needs a restart of the computer to look outside /workspace.
func filesError(err error, what string) string {
	text := err.Error()
	if strings.HasPrefix(text, "restart the sandbox") {
		return "Restart the computer to " + what + " outside /workspace: its agent is from an older Studio."
	}
	return "Couldn't " + what + " it: " + text
}

// fileShown is what the tab shows of a file: its text by line, its picture, or a note that
// says why neither.
func fileShown(c api.FileContent) (size int64, lines []string, img image.Image, note string) {
	size = c.Size
	switch {
	case c.TooLarge:
		return size, nil, nil, "Larger than 1 MiB, at " + bytesText(size) + ". The browser downloads it."
	case c.Unreadable:
		return size, nil, nil, "The computer's user can't read it."
	case c.Binary:
		return size, nil, nil, "Not text: " + firstNonEmpty(c.MimeType, "binary") + ", " + bytesText(size) + ". The browser downloads it."
	case strings.HasPrefix(c.MimeType, "image/"):
		img, _, _ = image.Decode(bytes.NewReader(c.Data)) // a WebP shows its note alone
		return size, nil, img, "A picture, " + bytesText(size) + "."
	}
	text := strings.ReplaceAll(cleanText(string(c.Data)), "\t", "    ")
	if text = strings.TrimSuffix(text, "\n"); text != "" {
		lines = strings.Split(text, "\n")
	}
	return size, lines, nil, ""
}

// filesTab is the Files tab in w cells and h rows: the list beside the file from filesBeside
// cells, else the file opened or the list.
func (m *app) filesTab(sb sandboxes.View, w, h int) []string {
	f := &m.files
	switch {
	case h <= 0:
		return nil
	case phaseOf(sb) != phaseReady:
		return append([]string{""}, sayDim("Files are read while "+sb.Name+" runs. Details, then s starts it.", w)...)
	case m.filesSplit(w):
		right := []string{"", " " + styleDim.Render("enter opens the file under the cursor.")}
		if f.open != nil {
			right = m.fileLines(f.open, w-filesListW-2, h)
		}
		return joinPanes(m.filesList(filesListW, h), right, filesListW, h)
	case f.open != nil:
		return m.fileLines(f.open, w, h)
	}
	return m.filesList(w, h)
}

// filesSplit reports whether the list is beside the file in a pane of w cells: from filesBeside,
// but for a folder that couldn't be read while no file is open, whose reason takes the width.
func (m *app) filesSplit(w int) bool {
	return w >= filesBeside && (m.files.err == "" || m.files.open != nil)
}

// sayDim is text in dim lines of at most w cells, one cell in.
func sayDim(text string, w int) []string {
	var lines []string
	for _, l := range wrapText(text, min(w-2, 76)) {
		lines = append(lines, " "+styleDim.Render(l))
	}
	return lines
}

// filesList is the folder on show in w cells and h rows: its path, then its entries with the
// cursor's in view.
func (m *app) filesList(w, h int) []string {
	f := &m.files
	where := absPath(f.root, f.dir)
	if f.dir != "" {
		where += "/"
	}
	if lipgloss.Width(where) > w-1 {
		where = ansi.TruncateLeft(where, lipgloss.Width(where)-(w-2), "…")
	}
	lines := []string{" " + styleBold.Render(where)}
	switch {
	case f.err != "":
		return append(lines, sayDim(f.err, w)...)
	case f.listed != folderKey(f.root, f.dir):
		return append(lines, " "+styleDim.Render("Reading…"))
	case len(f.entries) == 0:
		return append(lines, " "+styleDim.Render("Empty."))
	}
	rows := h - 1
	if f.truncated {
		rows--
	}
	offset := 0
	if f.cursor >= rows {
		offset = f.cursor - rows + 1
	}
	for i := offset; i < len(f.entries) && len(lines) < rows+1; i++ {
		m.hitRow(len(lines), 0, w, rowFilesEntry, i)
		lines = append(lines, m.entryRow(f.entries[i], i == f.cursor, w))
	}
	if f.truncated && len(lines) < h {
		lines = append(lines, " "+styleDim.Render(clip("More than 1,000: the browser lists them.", w-1)))
	}
	return lines
}

// entryRow is an entry of the list in w cells: a folder with its slash, a file with its size,
// and on a wide list how long ago it changed.
func (m *app) entryRow(e agentproto.FileEntry, cur bool, w int) string {
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
	name := e.Name
	var right []string
	switch e.Type {
	case "dir":
		name += "/"
	case "symlink":
		name += styleDim.Render(" →")
	case "file":
		right = append(right, bytesText(e.Size))
	}
	if w >= 44 && e.ModTime > 0 {
		right = append(right, fmt.Sprintf("%4s", shortAge(m.now.Sub(time.Unix(e.ModTime, 0)))))
	}
	open := m.files.open
	switch {
	case cur || open != nil && open.root == m.files.root && open.path == path.Join(m.files.dir, e.Name):
		name = styleBold.Render(name)
	case e.Type == "other":
		name = styleDim.Render(name)
	case e.Type == "dir" && !m.look.plain:
		name = styleAccent.Render(name)
	}
	r := styleDim.Render(strings.Join(right, "  "))
	room := max(w-3-lipgloss.Width(r)-1, 4)
	return mark + spread(clip(name, room), r, w-3) + " "
}

// fileLines is a file opened in w cells and h rows: its name, folder and size, then its lines
// with their numbers, its picture or why neither shows.
func (m *app) fileLines(v *fileView, w, h int) []string {
	about := absPath(v.root, parentOf(v.path))
	if v.loaded && v.err == "" {
		about += " · " + bytesText(v.size)
	}
	head := styleBold.Render(path.Base(v.path)) + "  " + styleDim.Render(about)
	lines := []string{" " + clip(head, w-1), ""}
	body := max(h-2, 1)
	switch {
	case !v.loaded:
		return append(lines, " "+styleDim.Render("Reading…"))
	case v.err != "":
		return append(lines, sayDim(v.err, w)...)
	case v.img != nil && m.pictures():
		cols := min(w-2, max(body*2*v.img.Bounds().Dx()/max(v.img.Bounds().Dy(), 1), 1))
		if v.drawn == nil || v.drawnW != cols {
			v.drawn, v.drawnW = halfBlocks(v.img, cols), cols
		}
		for _, l := range v.drawn[:min(len(v.drawn), body)] {
			lines = append(lines, " "+l)
		}
		return lines
	case v.note != "":
		return append(lines, sayDim(v.note, w)...)
	case len(v.lines) == 0:
		return append(lines, " "+styleDim.Render("Empty."))
	}
	v.scroll = min(max(v.scroll, 0), max(len(v.lines)-body, 0))
	digits := max(len(strconv.Itoa(len(v.lines))), 3)
	for i := v.scroll; i < len(v.lines) && len(lines) < h; i++ {
		n := styleDim.Render(fmt.Sprintf("%*d", digits, i+1))
		lines = append(lines, n+"  "+clip(v.lines[i], w-digits-2))
	}
	return lines
}

// filesRect is the rows the file has and whether the list is beside it.
func (m *app) filesRect() (h int, beside bool) {
	rc, ok := m.paneRectOf(paneComputer)
	if !ok {
		return 0, false
	}
	return max(rc.h-2-2, 1), m.filesSplit(rc.w) // the tab row and its rule; the file's head
}

// filesKey answers the keys of the Files tab. The list's cursor moves with j and k, and with the
// arrows while no file is beside it; enter opens the entry, ← goes up a folder or, where the file
// shows alone, back to its folder; r goes to the next root and o prints where the browser edits.
func (m *app) filesKey(msg tea.KeyPressMsg) tea.Cmd {
	f := &m.files
	h, beside := m.filesRect()
	fileShows := f.open != nil && f.open.loaded && len(f.open.lines) > 0
	alone := f.open != nil && !beside // the file shows in place of the list
	switch {
	case key.Matches(msg, m.keyFilesRoot()):
		f.root, f.dir, f.cursor, f.goal = nextRoot(f.root), "", 0, ""
		if alone {
			f.open = nil
		}
		m.setNotice("Showing "+rootPath(f.root)+".", false)
		return nil
	case key.Matches(msg, keyFilesWeb):
		m.setNotice("Edit and upload in the browser: "+m.browserURL("/e/"+m.env.ID+"/sandboxes/"+f.sandbox+"?tab=files"), false)
		return nil
	case alone && key.Matches(msg, keyFilesClose):
		f.open = nil
		return nil
	case alone:
		if fileShows {
			scrollKey(fixArrows(msg), &f.open.scroll, len(f.open.lines), nil, h)
		}
		return nil
	case beside && fileShows && (key.Matches(msg, keyLineUp, keyLineDown, keyPageUp, keyPageDn, keyTop, keyBottom)):
		scrollKey(msg, &f.open.scroll, len(f.open.lines), nil, h)
		return nil
	case key.Matches(msg, keyFilesDown):
		f.cursor = min(f.cursor+1, max(len(f.entries)-1, 0))
	case key.Matches(msg, keyFilesUpRow):
		f.cursor = max(f.cursor-1, 0)
	case key.Matches(msg, keyPageDn):
		f.cursor = min(f.cursor+max(h-2, 1), max(len(f.entries)-1, 0))
	case key.Matches(msg, keyPageUp):
		f.cursor = max(f.cursor-max(h-2, 1), 0)
	case key.Matches(msg, keyTop):
		f.cursor = 0
	case key.Matches(msg, keyBottom):
		f.cursor = max(len(f.entries)-1, 0)
	case key.Matches(msg, keyFilesOpen):
		m.openEntry()
	case key.Matches(msg, keyFilesUp):
		if f.dir == "" {
			m.setNotice(rootPath(f.root)+" is the top. r shows "+rootPath(nextRoot(f.root))+".", false)
			return nil
		}
		f.goal, f.dir, f.cursor = path.Base(f.dir), parentOf(f.dir), 0
	}
	return nil
}

// fixArrows makes j and k scroll a file shown alone, as the arrows do.
func fixArrows(msg tea.KeyPressMsg) tea.KeyPressMsg {
	switch msg.String() {
	case "j":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "k":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	}
	return msg
}

// openEntry opens the entry under the cursor: a folder shows its list, a file shows beside it,
// or in its place in a narrow pane.
func (m *app) openEntry() {
	f := &m.files
	if f.listed != folderKey(f.root, f.dir) || f.cursor >= len(f.entries) {
		return
	}
	e := f.entries[f.cursor]
	p := path.Join(f.dir, e.Name)
	switch e.Type {
	case "dir":
		f.dir, f.cursor = p, 0
	case "file":
		if v := f.open; v == nil || v.root != f.root || v.path != p {
			f.open = &fileView{root: f.root, path: p, mod: e.ModTime}
		}
	case "symlink":
		m.setNotice(e.Name+" is a link, which Studio doesn't follow. The terminal does.", false)
	default:
		m.setNotice(e.Name+" is neither a file nor a folder.", false)
	}
}

// wheelFiles answers the wheel over the Files tab: over the list it moves the cursor, over a
// file it scrolls it, which drawing keeps within the file.
func (m *app) wheelFiles(x, dir int) {
	f := &m.files
	rc, ok := m.paneRectOf(paneComputer)
	if !ok {
		return
	}
	overList := f.open == nil || m.filesSplit(rc.w) && x < rc.x+filesListW
	if overList {
		f.cursor = min(max(f.cursor+dir, 0), max(len(f.entries)-1, 0))
		return
	}
	f.open.scroll = max(f.open.scroll+dir*wheelLines, 0)
}

// filesBindings are the keys of the Files tab.
func (m *app) filesBindings() []key.Binding {
	f := &m.files
	_, beside := m.filesRect()
	if f.open != nil && !beside {
		return []key.Binding{keyFilesClose, keyLineDown, m.keyFilesRoot(), keyFilesWeb}
	}
	bindings := []key.Binding{keyFilesOpen, keyFilesDown}
	if f.dir != "" {
		bindings = append(bindings, keyFilesUp)
	}
	if beside && f.open != nil {
		bindings = append(bindings, keyLineDown)
	}
	return append(bindings, m.keyFilesRoot(), keyFilesWeb)
}
