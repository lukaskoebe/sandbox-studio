package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/lukaskoebe/sandbox-studio/internal/api"
	"github.com/lukaskoebe/sandbox-studio/internal/gitreview"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

const sampleDiff = `diff --git a/src/login.tsx b/src/login.tsx
index 3b18e51..a4c2f0d 100644
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
diff --git a/src/login.test.tsx b/src/login.test.tsx
new file mode 100644
--- /dev/null
+++ b/src/login.test.tsx
@@ -0,0 +1,6 @@
+import { render } from "@testing-library/react"
+
+test("enter submits", () => {
+	const form = render(<Login />)
+	expect(form).toBeTruthy()
+})
`

// samplePush puts a pending push of api-refactor with two commits and a diff on the
// approvals screen.
func samplePush(m *app) api.ApprovalView {
	a := api.ApprovalView{
		Approval: store.Approval{ID: "g1", EnvironmentID: "env1", SandboxID: "sb2", Kind: "git.push", Status: "pending", CreatedAt: m.now.Add(-30 * time.Second)},
		Git: &gitreview.GitDetail{
			Push: store.GitPush{ForgeName: "GitHub", Owner: "acme", Repo: "web", Ref: "refs/heads/fix-login", TeammateName: "Finn"},
			Review: &gitreview.PushReview{
				Sandbox: "api-refactor", Branch: "fix-login", Old: "", New: "a4c2f0d9e1",
				Upstream: "over SSH to ssh://git@github.com/acme/web.git",
				Commits: []gitreview.Commit{
					{SHA: "a4c2f0d9e1", Author: "Finn", When: m.now.Add(-2 * time.Minute), Subject: "Test that enter submits the login form"},
					{SHA: "77d0b2c4aa", Author: "Finn", When: m.now.Add(-5 * time.Minute), Subject: "Submit the login form on enter", Body: "The sign-in control was a div."},
				},
				Files: 2, Additions: 7, Deletions: 1, Diff: sampleDiff,
			},
		},
	}
	m.approvals = []api.ApprovalView{a}
	return a
}

func TestGoldenGitPager(t *testing.T) {
	m := sampleApp()
	samplePush(m)
	golden(t, "git_pager", press(m, "a", "enter"))
}

func TestGoldenGitPagerScrolled(t *testing.T) {
	m := sampleApp()
	samplePush(m)
	golden(t, "git_pager_end", press(m, "a", "enter", "G"))
}

func TestGitCardOpensThePager(t *testing.T) {
	m := sampleApp()
	samplePush(m)
	m = press(m, "a", "y")
	if m.screen != screenApprovals || !strings.Contains(m.notice.text, "enter opens the diff") {
		t.Fatalf("y on the card: screen %v, notice %q", m.screen, m.notice.text)
	}
	m = press(m, "enter")
	if m.screen != screenPager || m.pager.approval.ID != "g1" {
		t.Fatalf("enter on the card: screen %v", m.screen)
	}
	m = press(m, "j", "j", "j")
	if m.pager.scroll != 3 {
		t.Fatalf("scroll %d", m.pager.scroll)
	}
	m = press(m, "G")
	m.frame()
	end := m.pager.scroll
	m = press(m, "j")
	m.frame()
	if m.pager.scroll != end || end == 0 {
		t.Fatalf("the pager scrolls past the end: %d, then %d", end, m.pager.scroll)
	}
	m = press(m, "g")
	if m.pager.scroll != 0 {
		t.Fatalf("g: %d", m.pager.scroll)
	}
	if m = press(m, "esc"); m.screen != screenApprovals {
		t.Fatalf("esc returns to %v", m.screen)
	}
}

func TestPagerDecides(t *testing.T) {
	for _, k := range []string{"y", "n", "i"} {
		m := sampleApp()
		samplePush(m)
		m = press(m, "a", "enter")
		next, cmd := m.Update(tea.KeyPressMsg{Code: rune(k[0]), Text: k})
		m = next.(*app)
		if cmd == nil || m.screen != screenApprovals {
			t.Fatalf("%s: cmd %v, screen %v", k, cmd != nil, m.screen)
		}
	}
}

func TestPagerForcePushesARewrite(t *testing.T) {
	m := sampleApp()
	a := samplePush(m)
	rv := a.Git.Review
	rv.Old, rv.Forced, rv.Base, rv.BaseBranch = "e4f5a6b7c8", true, "7f3e2101aa", "main"
	rv.Dropped = []gitreview.Commit{{SHA: "e4f5a6b7c8", Author: "Finn", When: m.now.Add(-time.Hour), Subject: "Submit the login form on enter", ReplacedBy: "77d0b2c4aa"}}
	rv.TipFiles = []string{"src/login.tsx"}
	m = press(m, "a", "enter")
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"Rewrites history · drops 1 commit", "→ 77d0b2c", "The tips differ in src/login.tsx.", "on main's tip 7f3e210", "F force-push"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the pager lacks %q:\n%s", want, view)
		}
	}
	m = press(m, "y")
	if m.screen != screenPager || !strings.Contains(m.notice.text, "F force-pushes it") {
		t.Fatalf("y on a rewrite: screen %v, notice %q", m.screen, m.notice.text)
	}
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'F', Text: "F"})
	if m = next.(*app); cmd == nil || m.screen != screenApprovals {
		t.Fatalf("F: cmd %v, screen %v", cmd != nil, m.screen)
	}
}

func TestPagerNamesThePullRequest(t *testing.T) {
	m := sampleApp()
	a := samplePush(m)
	a.Kind = "git.pr"
	a.Git.Review = nil
	a.Git.PullRequest = &gitreview.PullRequest{Head: "fix-login", Base: "main", Title: "Submit the login form on enter", Body: "Fixes the sign-in button."}
	m.approvals = []api.ApprovalView{a}
	m = press(m, "a", "enter")
	body := strings.Join(m.pagerLines(80), "\n")
	for _, want := range []string{"fix-login → main", "Approving opens this pull request", "Fixes the sign-in button."} {
		if !strings.Contains(body, want) {
			t.Errorf("the review lacks %q:\n%s", want, body)
		}
	}
	if bar := m.keyBar(m.width, 0); !strings.Contains(bar, "open pull request") {
		t.Errorf("key bar: %s", bar)
	}
}

func TestPagerTogglesThePullRequest(t *testing.T) {
	m := sampleApp()
	a := samplePush(m)
	a.Git.Review.PullRequest = &gitreview.PullRequest{Head: "fix-login", Base: "main", Title: "Submit the login form on enter"}
	m.approvals = []api.ApprovalView{a}
	m = press(m, "a", "enter")
	if body := strings.Join(m.pagerLines(80), "\n"); !strings.Contains(body, "[x] into main: Submit the login form on enter") {
		t.Fatalf("a new branch's review doesn't open its pull request:\n%s", body)
	}
	if bar := m.keyBar(m.width, 0); !strings.Contains(bar, "pull request") {
		t.Errorf("key bar: %s", bar)
	}
	m = press(m, "p")
	if body := strings.Join(m.pagerLines(80), "\n"); !strings.Contains(body, "[ ] into main") || m.pager.openPR {
		t.Fatalf("p left the pull request on:\n%s", body)
	}
}

func TestPagerOpensAPullRequestLater(t *testing.T) {
	m := sampleApp()
	a := samplePush(m)
	a.Status = "approved"
	a.Git.Push.State = "pushed"
	a.Git.Review.PullRequest = &gitreview.PullRequest{Head: "fix-login", Base: "main", Title: "Submit the login form on enter"}
	m.approvals = nil
	m.openPager(a)
	if body := strings.Join(m.pagerLines(80), "\n"); !strings.Contains(body, "pushed without it · o opens it") {
		t.Fatalf("review:\n%s", body)
	}
	if bar := m.keyBar(m.width, 0); !strings.Contains(bar, "open pull request") {
		t.Errorf("key bar: %s", bar)
	}
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'o', Text: "o"})
	if m = next.(*app); cmd == nil || m.screen != screenApprovals {
		t.Fatalf("o: cmd %v, screen %v", cmd != nil, m.screen)
	}
}

// r on a teammate's push asks for a note, rejects the push with it and sends it to a new chat.
func TestRequestChangesRejectsAndSendsTheNote(t *testing.T) {
	m := sampleApp()
	client, rec := recordingClient(t)
	m.client = client
	samplePush(m)
	m = press(m, "a", "enter", "r")
	if m.typing == nil || !strings.Contains(m.typing.label, "What should Finn change?") {
		t.Fatalf("r asks for the note: %+v", m.typing)
	}
	m = press(m, "t", "e", "s", "t", "s")
	m, cmd := pressCmd(m, "enter")
	m = run(m, cmd)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.requests) != 2 {
		t.Fatalf("calls: %+v", rec.requests)
	}
	if d := rec.requests[0]; d.path != "/api/environments/env1/approvals/g1/decide" || !strings.Contains(d.body, `"deny"`) || !strings.Contains(d.body, `"note":"tests"`) {
		t.Errorf("the decision: %+v", d)
	}
	if f := rec.requests[1]; f.path != "/api/environments/env1/approvals/g1/feedback" || !strings.Contains(f.body, `"newChat":true`) {
		t.Errorf("the feedback: %+v", f)
	}
	if !strings.Contains(m.notice.text, "Asked Finn for changes on fix-login in a new chat.") {
		t.Errorf("notice %q", m.notice.text)
	}
}
