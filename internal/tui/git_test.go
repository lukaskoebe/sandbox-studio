package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/lukaskoebe/sandbox-studio/internal/api"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// gitSample is the Git screen with the waiting push of samplePush and a decided one, as its
// read brings them, with a network approval the screen leaves out.
func gitSample(t *testing.T) *app {
	t.Helper()
	m := sampleApp()
	waiting := samplePush(m)
	pushed := waiting
	pushed.ID, pushed.Status = "g0", "approved"
	git := *waiting.Git
	git.Push.State, git.Push.Result = "pushed", "fix-login → a4c2f0d"
	pushed.Git = &git
	other := api.ApprovalView{Approval: store.Approval{ID: "n1", Kind: "network.request", Status: "pending"}}
	m = press(m, "g")
	if m.screen != screenGit {
		t.Fatalf("g on Home opens %v", m.screen)
	}
	m = run(m, func() tea.Msg { return gitLoaded{gen: m.git.gen, list: []api.ApprovalView{pushed, other, waiting}} })
	return m
}

func TestGitScreenListsThePushesWaitingFirst(t *testing.T) {
	m := gitSample(t)
	if len(m.git.list) != 2 || m.git.list[0].ID != "g1" || m.git.wait != 1 {
		t.Fatalf("the list: %+v", m.git.list)
	}
	out := ansi.Strip(strings.Join(m.gitBody(120, 20), "\n"))
	for _, want := range []string{"WAITING FOR YOU  1", "DECIDED  1", "Push 2 commits to fix-login on GitHub/acme/web", "api-refactor", "waiting", "pushed · fix-login → a4c2f0d"} {
		if !strings.Contains(out, want) {
			t.Errorf("the Git screen lacks %q:\n%s", want, out)
		}
	}
	if m.activeTab() != tabGit {
		t.Errorf("the header marks %v", m.activeTab())
	}
}

func TestGitScreenReviewsAndReturns(t *testing.T) {
	m := press(gitSample(t), "enter")
	if m.screen != screenPager || m.pager.approval.ID != "g1" {
		t.Fatalf("enter reviews the waiting push: %v", m.screen)
	}
	if m = press(m, "esc"); m.screen != screenGit {
		t.Fatalf("esc returns to %v, not the Git screen", m.screen)
	}
	m = press(m, "j", "enter") // the decided push: its review has no decision
	if _, cmd := m.key(keyMsg("y")); cmd != nil || !strings.Contains(m.notice.text, "Decided: pushed") {
		t.Errorf("a decided push was decided again: %q", m.notice.text)
	}
	if got := ansi.Strip(m.keyBar(80, 0)); strings.Contains(got, "reject") {
		t.Errorf("the key bar offers a decision on a decided push: %q", got)
	}
	if m = press(m, "esc", "esc"); m.screen != screenHome {
		t.Errorf("esc from the Git screen returns to %v", m.screen)
	}
}

func TestGitTabOpensTheGitScreen(t *testing.T) {
	m := sampleApp()
	m.openTab(tabSpan{tab: tabGit})
	if m.screen != screenGit {
		t.Fatalf("the Git tab opens %v", m.screen)
	}
	m.git.loaded = true
	if out := strings.Join(m.gitBody(80, 20), "\n"); !strings.Contains(out, "No pushes yet") {
		t.Errorf("the empty Git screen:\n%s", out)
	}
}
