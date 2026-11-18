package tui

import (
	"bytes"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/api"
	"github.com/lukaskoebe/sandbox-studio/internal/browser"
)

// sampleFolder is /workspace of api-refactor as its guest lists it: folders first.
func sampleFolder(m *app) []agentproto.FileEntry {
	at := func(d time.Duration) int64 { return m.now.Add(-d).Unix() }
	return []agentproto.FileEntry{
		{Name: "docs", Type: "dir", ModTime: at(2 * time.Hour)},
		{Name: "node_modules", Type: "dir", ModTime: at(26 * time.Hour)},
		{Name: "src", Type: "dir", ModTime: at(3 * time.Minute)},
		{Name: "current", Type: "symlink", ModTime: at(48 * time.Hour)},
		{Name: "README.md", Type: "file", Size: 2_315, ModTime: at(4 * time.Hour)},
		{Name: "package.json", Type: "file", Size: 1_208, ModTime: at(3 * time.Minute)},
		{Name: "tsconfig.json", Type: "file", Size: 412, ModTime: at(26 * time.Hour)},
	}
}

// sampleSource is src/login.tsx of api-refactor.
const sampleSource = "import { useState } from \"react\"\n\nexport function Login() {\n\tconst [name, setName] = useState(\"\")\n\treturn (\n\t\t<form onSubmit={submit}>\n\t\t\t<input value={name} onChange={(e) => setName(e.target.value)} />\n\t\t</form>\n\t)\n}\n"

// withFiles gives the Files tab of computer id its /workspace listed, the cursor on package.json.
func withFiles(m *app, id string) *app {
	m.files = filesState{sandbox: id, gen: m.files.gen + 1, listed: folderKey("", ""), entries: sampleFolder(m), cursor: 5}
	return m
}

// withFileOpen opens src/login.tsx in the tab, its folder listed.
func withFileOpen(m *app) *app {
	f := &m.files
	f.dir, f.listed, f.cursor = "src", folderKey("", "src"), 1
	f.entries = []agentproto.FileEntry{
		{Name: "components", Type: "dir", ModTime: m.now.Add(-time.Hour).Unix()},
		{Name: "login.tsx", Type: "file", Size: int64(len(sampleSource)), ModTime: m.now.Add(-3 * time.Minute).Unix()},
		{Name: "main.tsx", Type: "file", Size: 318, ModTime: m.now.Add(-26 * time.Hour).Unix()},
	}
	f.open = &fileView{path: "src/login.tsx", loaded: true}
	f.open.size, f.open.lines, f.open.img, f.open.note = fileShown(api.FileContent{Size: int64(len(sampleSource)), MimeType: "text/plain", Data: []byte(sampleSource)})
	return m
}

// keyAlone is m after a key, without the reads Update starts after it, so the test runs them.
func keyAlone(m *app, k string) *app {
	next, _ := m.update(keyMsg(k))
	return next.(*app)
}

func TestGoldenFiles(t *testing.T) {
	golden(t, "files", withFiles(openComputer(sampleApp(), "sb2", tabFiles), "sb2"))
}

// At 80 columns the file opened shows in place of its folder.
func TestGoldenFilesOpen(t *testing.T) {
	golden(t, "files_open", withFileOpen(withFiles(openComputer(sampleApp(), "sb2", tabFiles), "sb2")))
}

// At 140 columns the computer beside a thread is under 80 cells: the file opened shows in its
// folder's place.
func TestGoldenWideFiles(t *testing.T) {
	m := sampleChat(visualApp(140, 40))
	m.chats.composing = false
	m.comp.tab = tabFiles
	m = withFileOpen(withFiles(settle(m), "sb1"))
	if rc, _ := m.paneRectOf(paneComputer); rc.w >= filesBeside {
		t.Fatalf("the computer is %d cells", rc.w)
	}
	goldenVisual(t, "wide_files_140x40", m, false)
}

// The site shows this one: at 120 columns the computer is 89 cells, the list beside the file.
func TestGoldenVisualFiles(t *testing.T) {
	m := withFileOpen(withFiles(openComputer(visualApp(120, 30), "sb2", tabFiles), "sb2"))
	goldenVisual(t, "visual_files", m, true)
}

func TestFilesReadThroughStudio(t *testing.T) {
	m := openComputer(sampleApp(), "sb2", tabFiles)
	list := mustJSON(t, api.FileList{Entries: sampleFolder(m)})
	client, g := fakeGuest(t, map[string]string{
		"/api/environments/env1/sandboxes/sb2/files":         list,
		"/api/environments/env1/sandboxes/sb2/files/content": mustJSON(t, api.FileContent{Size: 9, MimeType: "text/plain", Data: []byte("a\x1b[2Jb\tc\n")}),
	})
	m.client = client
	m.files = filesState{}
	cmd := m.watchFiles()
	if m.files.sandbox != "sb2" || !m.files.busy || cmd == nil {
		t.Fatalf("the tab reads no folder: %+v", m.files)
	}
	m = run(m, cmd)
	if len(m.files.entries) != 7 || m.files.listed != folderKey("", "") || m.files.busy {
		t.Fatalf("the folder: %+v", m.files)
	}
	if m.watchFiles() != nil {
		t.Error("the folder is read again before its tick")
	}
	m.files.cursor = 4 // README.md
	m = run(keyAlone(m, "enter"), m.watchFiles())
	if v := m.files.open; v == nil || !v.loaded || len(v.lines) != 1 || v.lines[0] != "ab    c" {
		t.Fatalf("the file opened: %+v", v)
	}
	// The folder says README.md changed: it is read again, and shows meanwhile.
	changed := sampleFolder(m)
	changed[4].ModTime++
	m.filesUpdate(filesLoaded{gen: m.files.gen, key: folderKey("", ""), list: api.FileList{Entries: changed}})
	if v := m.files.open; !v.stale || !strings.Contains(ansi.Strip(m.View().Content), "ab    c") {
		t.Fatalf("a changed file: stale %v", v.stale)
	}
	if cmd := m.watchFiles(); cmd == nil || !m.files.open.busy {
		t.Fatal("a changed file isn't read again")
	}
	asked := strings.Join(g.list(), "\n")
	for _, want := range []string{"/api/environments/env1/sandboxes/sb2/files?path=", "/api/environments/env1/sandboxes/sb2/files/content?path=README.md"} {
		if !strings.Contains(asked, want) {
			t.Errorf("Studio wasn't asked %s:\n%s", want, asked)
		}
	}
	m = keyAlone(keyAlone(m, "r"), "r")
	if m.files.root != agentproto.FilesRootFS || m.files.dir != "" {
		t.Fatalf("r twice: root %q, dir %q", m.files.root, m.files.dir)
	}
	run(m, m.watchFiles())
	if asked := strings.Join(g.list(), "\n"); !strings.Contains(asked, "files?path=&root=fs") {
		t.Errorf("the whole computer wasn't asked for:\n%s", asked)
	}
}

// ← goes up a folder, the cursor on the one it came from; at the root it stays and says so.
func TestFilesGoUp(t *testing.T) {
	m := withFiles(openComputer(sampleApp(), "sb2", tabFiles), "sb2")
	m.files.cursor = 2 // src
	m = press(m, "enter")
	if m.files.dir != "src" || m.files.cursor != 0 {
		t.Fatalf("enter on src: dir %q, cursor %d", m.files.dir, m.files.cursor)
	}
	m = press(m, "left")
	if m.files.dir != "" || m.files.goal != "src" {
		t.Fatalf("← from src: dir %q, goal %q", m.files.dir, m.files.goal)
	}
	m.filesUpdate(filesLoaded{gen: m.files.gen, key: folderKey("", ""), list: api.FileList{Entries: sampleFolder(m)}})
	if m.files.cursor != 2 {
		t.Errorf("the cursor isn't on src: %d", m.files.cursor)
	}
	m = press(m, "left")
	if m.files.dir != "" || !strings.Contains(m.notice.text, "/workspace is the top") {
		t.Errorf("← at the root: dir %q, notice %q", m.files.dir, m.notice.text)
	}
}

// A guest from an older Studio lists only /workspace: the tab says to restart the computer.
func TestFilesOlderGuest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"detail":"restart the sandbox to browse outside /workspace: its agent is from an older Studio"}`))
	}))
	defer srv.Close()
	m := openComputer(sampleApp(), "sb2", tabFiles)
	m.client = &Client{addr: strings.TrimPrefix(srv.URL, "http://"), http: srv.Client()}
	m.files = filesState{sandbox: "sb2", gen: 1, root: agentproto.FilesRootTmp, busy: true}
	m = run(m, m.loadFiles(agentproto.FilesRootTmp, ""))
	if want := "Restart the computer to browse outside /workspace"; !strings.HasPrefix(m.files.err, want) {
		t.Fatalf("the error: %q", m.files.err)
	}
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "Restart the computer to browse outside") || !strings.Contains(view, "/tmp") {
		t.Errorf("the tab doesn't say it:\n%s", view)
	}
}

// A file that isn't text, too large or unreadable shows why; a picture draws on a terminal of
// 256 colors.
func TestFileNotes(t *testing.T) {
	for _, c := range []struct {
		content api.FileContent
		want    string
	}{
		{api.FileContent{Size: 20 << 20, TooLarge: true}, "Larger than 1 MiB, at 20.0 MiB"},
		{api.FileContent{Size: 4096, MimeType: "application/zip", Binary: true}, "Not text: application/zip, 4.0 KiB"},
		{api.FileContent{Unreadable: true}, "The computer's user can't read it."},
	} {
		if _, lines, img, note := fileShown(c.content); lines != nil || img != nil || !strings.HasPrefix(note, c.want) {
			t.Errorf("%+v: %q", c.content, note)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, samplePage()); err != nil {
		t.Fatal(err)
	}
	_, _, img, note := fileShown(api.FileContent{Size: int64(buf.Len()), MimeType: "image/png", Data: buf.Bytes()})
	if img == nil || !strings.HasPrefix(note, "A picture") {
		t.Fatalf("a PNG: %v, %q", img != nil, note)
	}
	m := withFiles(openComputer(visualApp(140, 40), "sb2", tabFiles), "sb2")
	m.files.open = &fileView{path: "shot.png", loaded: true, img: img, note: note}
	if lines := m.fileLines(m.files.open, 80, 20); len(lines) < 10 || !strings.Contains(lines[2], "▀") {
		t.Errorf("the picture isn't drawn: %d lines", len(lines))
	}
	if lines := m.fileLines(m.files.open, 80, 20); len(lines) > 20 {
		t.Errorf("the picture is taller than the pane: %d lines", len(lines))
	}
}

func TestFilesStoppedComputer(t *testing.T) {
	m := openComputer(sampleApp(), "sb3", tabFiles)
	if m.watchFiles() != nil {
		t.Error("a stopped computer's files are read")
	}
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "Files are read while docs-site runs") {
		t.Errorf("the tab doesn't say why:\n%s", view)
	}
}

// A click on an entry moves the cursor there; the wheel moves it.
func TestClickingAFilesEntry(t *testing.T) {
	m := withFiles(openComputer(visualApp(120, 30), "sb2", tabFiles), "sb2")
	m.View()
	rc, _ := m.paneRectOf(paneComputer)
	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	y := -1
	for i, l := range lines {
		if strings.Contains(l, "README.md") {
			y = i
		}
	}
	if y < 0 {
		t.Fatal("README.md isn't on show")
	}
	next, _ := m.Update(tea.MouseClickMsg{X: rc.x + 4, Y: y, Button: tea.MouseLeft})
	m = next.(*app)
	if m.files.cursor != 4 {
		t.Errorf("the click picked entry %d", m.files.cursor)
	}
	m.wheelFiles(rc.x+4, 1)
	if m.files.cursor != 5 {
		t.Errorf("the wheel moved to %d", m.files.cursor)
	}
}

func TestGoldenBrowser(t *testing.T) {
	m := openComputer(sampleApp(), "sb2", tabBrowser)
	golden(t, "browser", withBrowser(m))
}

func TestGoldenVisualBrowser(t *testing.T) {
	m := withBrowser(openComputer(visualApp(120, 36), "sb2", tabBrowser))
	m.picture.status.Driver, m.picture.status.Sessions = "api-refactor", []browser.BrowserSession{{ID: "s1"}}
	goldenVisual(t, "visual_browser", m, true)
}

// The Browser tab reads the teammate's browser; a computer of yours has no Browser tab.
func TestBrowserTab(t *testing.T) {
	m := openComputer(sampleApp(), "sb2", tabBrowser)
	if got := m.pictureWanted(); got != "t2" {
		t.Errorf("the tab reads the browser of %q", got)
	}
	m = withBrowser(m)
	m.picture.status.VM = "suspended"
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "Asleep") || !strings.Contains(view, "wakes when an agent opens a page") {
		t.Errorf("an asleep browser:\n%s", view)
	}
	m.picture.status = browser.BrowserStatus{VM: "running", URL: "https://intranet.example/", Blocked: &browser.Blocked{Host: "intranet.example", RuleHost: "*.example"}}
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "a rule for *.example blocks intranet.example") {
		t.Errorf("a blocked page:\n%s", view)
	}
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "/e/env1/sandboxes/sb2?tab=browser") {
		t.Errorf("the tab doesn't say where the browser drives it:\n%s", view)
	}
	if m = press(m, "s"); m.confirm != nil || m.notice.text != "" {
		t.Errorf("s on the Browser tab acts on the computer: %+v, %q", m.confirm, m.notice.text)
	}
}
