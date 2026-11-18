package tui

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/api"
	"github.com/lukaskoebe/sandbox-studio/internal/chat"
	"github.com/lukaskoebe/sandbox-studio/internal/gitreview"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

const loginDiff = `diff --git a/src/login.tsx b/src/login.tsx
--- a/src/login.tsx
+++ b/src/login.tsx
@@ -10,7 +10,7 @@ export function Login() {
   return (
     <form onSubmit={submit}>
       <input name="user" />
-      <div onClick={submit}>Sign in</div>
+      <button type="submit">Sign in</button>
     </form>
   )
 }
`

// sampleStatus is git's status of api-refactor: web with a staged change, a rename, an
// untracked file and a conflict, and api, which starts folded.
func sampleStatus() []agentproto.GitRepo {
	return []agentproto.GitRepo{
		{Path: "web", Branch: "fix-login", Head: "a4c2f0d9e1", Upstream: "origin/fix-login", Ahead: 2, Changes: []agentproto.GitChange{
			{Path: "src/login.tsx", Area: "unstaged", Status: "modified", Added: 1, Removed: 1},
			{Path: "src/form.tsx", Area: "staged", Status: "renamed", From: "src/old-form.tsx", Added: 3},
			{Path: "notes.md", Area: "untracked", Status: "added"},
			{Path: "src/merge.go", Area: "unstaged", Status: "conflicted"},
		}},
		{Path: "api", Branch: "main", Upstream: "origin/main", Behind: 1, Changes: []agentproto.GitChange{
			{Path: "main.go", Area: "unstaged", Status: "modified", Added: 4, Removed: 2},
		}},
	}
}

// withGitStatus gives the Changes tab of api-refactor git's status, and the diff of its
// src/login.tsx read.
func withGitStatus(m *app) *app { return withGitStatusOf(m, "sb2") }

// withGitStatusOf gives the Changes tab of computer id git's status, and the diff of its
// src/login.tsx read.
func withGitStatusOf(m *app, id string) *app {
	m.changes = changesState{sandbox: id, gen: m.changes.gen + 1, status: sampleStatus(), loaded: true, pushedOK: true}
	ch := m.changes.status[0].Changes[0]
	m.changes.diffs = map[string]changeDiff{changeKey("web", ch): {sig: changeSig(ch), diff: loginDiff}}
	return m
}

// onLogin puts the rail's cursor on src/login.tsx, whose diff withGitStatus reads.
func onLogin(m *app) *app {
	m.changes.cursor = "change/web|unstaged|src/login.tsx"
	return m
}

// rowKeys are the keys of the rail's rows, and a word for the rows that take no cursor.
func rowKeys(m *app) []string {
	var out []string
	for _, r := range m.changeRows() {
		switch {
		case r.key != "":
			out = append(out, r.key)
		case r.kind == crSection:
			out = append(out, "§"+r.text)
		default:
			out = append(out, "·"+r.text)
		}
	}
	return out
}

func TestChangeRowsFollowGit(t *testing.T) {
	m := withGitStatus(openComputer(sampleApp(), "sb2", tabChanges))
	samplePush(m)
	want := []string{
		"§WAITING FOR YOU", "review/g1",
		"§REPOSITORIES", "repo/web",
		"·staged", "change/web|staged|src/form.tsx",
		"·not staged", "change/web|unstaged|src/login.tsx", "change/web|unstaged|src/merge.go",
		"·untracked", "change/web|untracked|notes.md",
		"repo/api",
	}
	if got := rowKeys(m); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("rows:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if m.changeCursor(m.changeRows()); m.changes.cursor != "review/g1" {
		t.Errorf("the first pick is the review that waits: %q", m.changes.cursor)
	}
	if got := m.changesLabel(); got != "Changes 5" {
		t.Errorf("the tab's label: %q", got)
	}
	m.changes.cursor = "repo/api"
	if m = press(m, "enter"); !strings.Contains(strings.Join(rowKeys(m), " "), "change/api|unstaged|main.go") {
		t.Errorf("enter on api's header opens it: %v", rowKeys(m))
	}
	m.changes.cursor = "repo/web"
	if m = press(m, "enter"); strings.Contains(strings.Join(rowKeys(m), " "), "change/web") {
		t.Errorf("enter on web's header folds it: %v", rowKeys(m))
	}
}

func TestChangesFirstPick(t *testing.T) {
	m := withGitStatus(openComputer(sampleApp(), "sb2", tabChanges))
	if m.changeCursor(m.changeRows()); m.changes.cursor != "change/web|staged|src/form.tsx" {
		t.Errorf("no review waits: the first change, %q", m.changes.cursor)
	}
	m = openComputer(sampleApp(), "sb2", tabChanges)
	samplePush(m)
	m.changes.cursor = ""
	if at := m.changeCursor(m.changeRows()); at < 0 || m.changes.cursor != "" {
		t.Errorf("before git's status the pick isn't kept: at %d, cursor %q", at, m.changes.cursor)
	}
}

// The cursor stays on its change across reads, and on the change at its place once it goes.
func TestChangesCursorKeptAcrossReads(t *testing.T) {
	m := withGitStatus(openComputer(sampleApp(), "sb2", tabChanges))
	m.changes.cursor = "change/web|unstaged|src/login.tsx"
	m.changeCursor(m.changeRows())
	status := sampleStatus()
	status[0].Changes = append([]agentproto.GitChange{{Path: "a.txt", Area: "unstaged", Status: "added"}}, status[0].Changes...)
	m = run(m, func() tea.Msg {
		return changesLoaded{gen: m.changes.gen, sandbox: "sb2", readStatus: true, status: api.GitStatus{Repos: status}}
	})
	if m.changes.cursor != "change/web|unstaged|src/login.tsx" {
		t.Errorf("a read moved the cursor: %q", m.changes.cursor)
	}
	status = sampleStatus()
	status[0].Changes = status[0].Changes[1:]
	m = run(m, func() tea.Msg {
		return changesLoaded{gen: m.changes.gen, sandbox: "sb2", readStatus: true, status: api.GitStatus{Repos: status}}
	})
	if m.changeCursor(m.changeRows()); m.changes.cursor == "change/web|unstaged|src/login.tsx" || m.changes.cursor == "" {
		t.Errorf("a change that went keeps the cursor: %q", m.changes.cursor)
	}
}

func TestChangesSayWhatAnOlderGuestNeeds(t *testing.T) {
	m := openComputer(sampleApp(), "sb2", tabChanges)
	err := &APIError{Status: http.StatusConflict, Detail: "restart the sandbox to see its git changes: its agent is from an older Studio"}
	m = run(m, func() tea.Msg {
		return changesLoaded{gen: m.changes.gen, sandbox: "sb2", readStatus: true, statusErr: err}
	})
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "Restart the computer to see its git changes") {
		t.Errorf("the tab lacks the older guest's words:\n%s", view)
	}
}

func TestChangesOfAStoppedComputer(t *testing.T) {
	m := openComputer(sampleApp(), "sb3", tabChanges)
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "Git's changes are read while docs-site runs.") {
		t.Errorf("a stopped computer's tab:\n%s", view)
	}
	if cmd := m.watchChanges(); cmd != nil {
		if _, ok := cmd().(changesLoaded); ok {
			t.Error("a stopped computer's git is read")
		}
	}
}

// openReview opens the computer's Changes tab beside the thread that shows it, and a review place
// for a push of another environment.
func TestOpenReviewWhere(t *testing.T) {
	m := visualApp(140, 40)
	a := samplePush(m)
	m.open(place{kind: placeThread, mate: "t2", chat: "c9", sandbox: "sb2"})
	if cmd := m.openReview(a); m.here.kind != placeThread || m.comp.tab != tabChanges || m.focus != focusSide || cmd != nil {
		t.Errorf("beside the thread: here %+v, tab %v, focus %v", m.here, m.comp.tab, m.focus)
	}

	m = sampleApp()
	a = samplePush(m)
	a.EnvironmentID = "env2"
	m.approvals = nil
	m.openReview(a)
	if m.here.kind != placeReview || m.mainPane() != paneReview || m.crumb() == "" {
		t.Fatalf("another environment's push: here %+v, pane %v", m.here, m.mainPane())
	}
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "fix-login") || !strings.Contains(view, "Review") {
		t.Errorf("the review place:\n%s", view)
	}
	m = press(m, "j")
	if m.pager.scroll != 1 {
		t.Errorf("j scrolls the review place: %d", m.pager.scroll)
	}
	if m = press(m, "esc"); m.here.kind != placeHome {
		t.Errorf("esc leaves the review place: %+v", m.here)
	}
}

func TestCShowsTheChanges(t *testing.T) {
	m := sampleChat(visualApp(140, 40))
	m.chats.composing = false
	if m = press(m, "c"); m.comp.tab != tabChanges || m.focus != focusSide {
		t.Errorf("c beside the thread: tab %v, focus %v", m.comp.tab, m.focus)
	}
	m = sampleChat(sampleApp())
	m.chats.composing = false
	if m = press(m, "c"); m.comp.tab != tabChanges || !m.flip {
		t.Errorf("c at 80 columns: tab %v, flip %v", m.comp.tab, m.flip)
	}
}

// The requests of the thread that changed files are listed under THIS THREAD.
func TestChangesListTheThreadsRequests(t *testing.T) {
	m := sampleChat(visualApp(140, 40))
	m.chats.composing = false
	ended := m.now.Add(-time.Minute)
	m.chats.turns["t2"] = chat.Turn{ChatTurn: store.ChatTurn{ID: "t2", ChatID: "c1", Seq: 2, EndedAt: &ended}, Prompt: "Make enter submit the form",
		Changes: &chat.TurnChanges{Files: []agentproto.FileChange{{Path: "src/login.tsx", Status: "modified", Added: 1, Removed: 1}}}}
	m.changes = changesState{sandbox: "sb1", loaded: true}
	if got := rowKeys(m); strings.Join(got, " ") != "§THIS THREAD turn/t2" {
		t.Errorf("rows: %v", got)
	}
}

// guestRecorder is a Studio whose sandboxes answer git's reads from canned JSON, and records
// the paths and queries it was asked.
type guestRecorder struct {
	mu    sync.Mutex
	asked []string
}

func (g *guestRecorder) list() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.asked...)
}

// fakeGuest is a client of a Studio that answers each path from answers, {} for the others.
func fakeGuest(t *testing.T, answers map[string]string) (*Client, *guestRecorder) {
	t.Helper()
	g := &guestRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		g.mu.Lock()
		g.asked = append(g.asked, r.URL.RequestURI())
		g.mu.Unlock()
		answer, ok := answers[r.URL.Path]
		if !ok {
			answer = "{}"
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, answer)
	}))
	t.Cleanup(srv.Close)
	return &Client{addr: strings.TrimPrefix(srv.URL, "http://"), http: srv.Client()}, g
}

func TestChangesReadThroughStudio(t *testing.T) {
	m := openComputer(sampleApp(), "sb2", tabChanges)
	pushed := samplePush(m)
	m.approvals = nil
	pushed.Status, pushed.UpdatedAt = "approved", m.now.Add(-time.Hour)
	pushed.Git.Push.State, pushed.Git.Push.PRURL = "pushed", "https://github.com/acme/web/pull/12"
	old := pushed
	old.ID, old.UpdatedAt = "g0", m.now.Add(-30*time.Hour)
	list := fmt.Sprintf("[%s,%s]", mustJSON(t, pushed), mustJSON(t, old))
	status := mustJSON(t, api.GitStatus{Repos: sampleStatus()})
	client, g := fakeGuest(t, map[string]string{
		"/api/environments/env1/sandboxes/sb2/git":      status,
		"/api/environments/env1/sandboxes/sb2/git/diff": `{"diff":` + mustJSON(t, loginDiff) + `}`,
		"/api/environments/env1/approvals":              list,
	})
	m.client = client
	m = run(m, m.loadChanges("sb2", true, true))
	if len(m.changes.status) != 2 || len(m.changes.pushed) != 1 || m.changes.pushed[0].ID != "g1" {
		t.Fatalf("status %d repos, pushed %+v", len(m.changes.status), m.changes.pushed)
	}
	m.changes.cursor, m.changes.asked = "change/web|staged|src/form.tsx", nil
	m = run(m, m.watchChangeData())
	m.changes.cursor = "change/web|untracked|notes.md"
	m = run(m, m.watchChangeData())
	asked := g.list()
	for _, want := range []string{
		"/api/environments/env1/sandboxes/sb2/git",
		"/api/environments/env1/approvals?status=approved",
		"/api/environments/env1/sandboxes/sb2/git/diff?area=staged&from=src%2Fold-form.tsx&path=src%2Fform.tsx&repo=web",
		"/api/environments/env1/sandboxes/sb2/git/diff?area=untracked&path=notes.md&repo=web",
	} {
		if !strings.Contains(strings.Join(asked, "\n"), want) {
			t.Errorf("Studio wasn't asked %s:\n%s", want, strings.Join(asked, "\n"))
		}
	}
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"PUSHED", "#12"} {
		if !strings.Contains(view, want) {
			t.Errorf("the tab lacks %q:\n%s", want, view)
		}
	}
}

func TestChangesReadATurnsChanges(t *testing.T) {
	m := sampleChat(visualApp(140, 40))
	m.chats.composing = false
	ended := m.now.Add(-time.Minute)
	m.chats.turns["t2"] = chat.Turn{ChatTurn: store.ChatTurn{ID: "t2", ChatID: "c1", Seq: 2, EndedAt: &ended}, Prompt: "Make enter submit the form",
		Changes: &chat.TurnChanges{Files: []agentproto.FileChange{{Path: "src/login.tsx", Status: "modified", Added: 1, Removed: 1}}}}
	changes := mustJSON(t, chat.TurnChanges{Files: []agentproto.FileChange{{Path: "src/login.tsx", Status: "modified", Added: 1, Removed: 1, Diff: loginDiff}}})
	client, g := fakeGuest(t, map[string]string{"/api/environments/env1/chats/c1/turns/t2/changes": changes})
	m.client = client
	m.comp.tab = tabChanges
	m.changes = changesState{sandbox: "sb1", loaded: true, gen: 3}
	m = run(m, m.watchChangeData())
	if asked := g.list(); len(asked) != 1 || asked[0] != "/api/environments/env1/chats/c1/turns/t2/changes" {
		t.Fatalf("asked %v", asked)
	}
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, `<button type="submit">Sign in</button>`) {
		t.Errorf("the request's diff isn't on show:\n%s", view)
	}
}

// Decisions are made in the tab with the review's keys, and the tab stays.
func TestChangesDecideAForgeComment(t *testing.T) {
	m := openComputer(sampleApp(), "sb2", tabChanges)
	m.approvals = []api.ApprovalView{{
		Approval:     store.Approval{ID: "fc1", EnvironmentID: "env1", SandboxID: "sb2", Kind: "git.comment", Status: "pending", CreatedAt: m.now.Add(-time.Minute)},
		ForgeComment: &gitreview.CommentDraft{Sandbox: "api-refactor", Forge: "GitHub", Owner: "acme", Repo: "web", Number: 12, Title: "Submit the login form on enter", Body: "Looks good, merging after CI."},
	}}
	m.changes.loaded = true
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"Comment on acme/web#12", "Comment on acme/web#12: Looks good", "enter open"} {
		if !strings.Contains(view, want) {
			t.Errorf("the tab lacks %q:\n%s", want, view)
		}
	}
}

func TestGoldenChanges(t *testing.T) {
	golden(t, "changes", onLogin(withGitStatus(openComputer(sampleApp(), "sb2", tabChanges))))
}

func TestGoldenChangesClean(t *testing.T) {
	m := openComputer(sampleApp(), "sb2", tabChanges)
	m.changes.status, m.changes.loaded = []agentproto.GitRepo{{Path: "web", Branch: "main", Upstream: "origin/main"}}, true
	golden(t, "changes_clean", m)
}

func TestGoldenChangesStopped(t *testing.T) {
	golden(t, "changes_stopped", openComputer(sampleApp(), "sb3", tabChanges))
}

func TestGoldenVisualChanges(t *testing.T) {
	goldenVisual(t, "visual_changes", onLogin(withGitStatus(openComputer(visualApp(80, 24), "sb2", tabChanges))), true)
}

// At 140 columns the tab beside a thread is under 70 cells: its rail is a line over main. The
// thread's request that changed files is under THIS THREAD.
func TestGoldenWideChanges(t *testing.T) {
	m := sampleChat(visualApp(140, 40))
	m.chats.composing = false
	ended := m.now.Add(-time.Minute)
	m.chats.turns["t1"] = chat.Turn{ChatTurn: store.ChatTurn{ID: "t1", ChatID: "c1", Seq: 1, EndedAt: &ended}, Prompt: "The login form doesn't submit on enter. Can you fix it?",
		Changes: &chat.TurnChanges{Files: []agentproto.FileChange{{Path: "src/login.tsx", Status: "modified", Added: 1, Removed: 1}}}}
	m = press(m, "c")
	goldenVisual(t, "wide_changes_140x40", onLogin(withGitStatusOf(m, "sb1")), false)
}

// At 180 columns alt+z gives the tab the frame: the rail beside a diff side by side.
func TestGoldenWideChangesZoomed(t *testing.T) {
	m := onLogin(withGitStatus(openComputer(visualApp(180, 50), "sb2", tabChanges)))
	goldenVisual(t, "wide_changes_180x50", press(m, "alt+z"), false)
}

// mustJSON is v as JSON.
func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A click on a row of the rail picks the row it is drawn on, under the tab row and its rule.
func TestClickingTheRailPicksItsRow(t *testing.T) {
	m := onLogin(withGitStatus(openComputer(visualApp(120, 30), "sb2", tabChanges)))
	view := ansi.Strip(m.View().Content)
	y := -1
	for i, l := range strings.Split(view, "\n") {
		if strings.Contains(l, "src/merge.go") {
			y = i
			break
		}
	}
	if y < 0 {
		t.Fatalf("no row of src/merge.go:\n%s", view)
	}
	rc, _ := m.paneRectOf(paneComputer)
	next, _ := m.Update(tea.MouseClickMsg{X: rc.x + 4, Y: y, Button: tea.MouseLeft})
	if got := next.(*app).changes.cursor; got != "change/web|unstaged|src/merge.go" {
		t.Errorf("the click picked %q", got)
	}
}
