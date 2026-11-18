package tui

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/api"
)

// y on a push's row asks once more; a second y approves the push, as the pager's y does.
func TestPushRowAsksOnceMoreThenApproves(t *testing.T) {
	m := sampleApp()
	client, rec := recordingClient(t)
	m.client = client
	samplePush(m)
	m = press(onCard(m, 0), "y")
	if m.inbox.pushing != "g1" {
		t.Fatalf("y on the push's row did not ask: %q", m.inbox.pushing)
	}
	if line := stripANSI(m.noticeLine(m.width)); !strings.Contains(line, "Push 2 commits to fix-login without opening the diff?") || !strings.Contains(line, "y Push") {
		t.Fatalf("the question: %q", line)
	}
	m = pressRun(t, m, "y")
	got := rec.last(t)
	if got.method != http.MethodPost || got.path != "/api/environments/env1/approvals/g1/decide" || !strings.Contains(got.body, `"action":"allow"`) {
		t.Errorf("the second y sent %s %s %s", got.method, got.path, got.body)
	}
	if m.inbox.pushing != "" {
		t.Error("the question stays after the answer")
	}
}

// Any key but y keeps the push, and arrows move over the question's choices first.
func TestPushRowQuestionKeepsOnAnyOtherKey(t *testing.T) {
	for _, k := range []string{"n", "esc", "j", "enter"} {
		m := sampleApp()
		samplePush(m)
		m = press(onCard(m, 0), "y")
		m, cmd := pressCmd(m, k)
		if cmd != nil || m.inbox.pushing != "" || !strings.HasPrefix(m.notice.text, "Kept ") {
			t.Errorf("%s after y: cmd %v, asking %q, notice %q", k, cmd != nil, m.inbox.pushing, m.notice.text)
		}
	}
	// ← then enter presses Push, as a confirmation's arrows do.
	m := sampleApp()
	samplePush(m)
	m = press(onCard(m, 0), "y", "left")
	if _, cmd := pressCmd(m, "enter"); cmd == nil {
		t.Error("← then enter does not push")
	}
}

// When the request under the cursor is decided, the cursor goes to the request that takes its
// place.
func TestTheCursorGoesToTheNextRequestAfterADecision(t *testing.T) {
	m := sampleApp()
	sampleApprovals(m)
	m = onCard(m, 1) // the group of three hosts
	rest := slices.DeleteFunc(slices.Clone(m.approvals), func(a api.ApprovalView) bool { return a.ID == "n2" || a.ID == "n3" || a.ID == "n4" })
	m = run(m, func() tea.Msg { return approvalsLoaded{list: rest} })
	if c := cursorCard(t, m); c.first().ID != "n1" {
		t.Errorf("after the group was decided the cursor is on %s, want n1", c.first().ID)
	}
}

// A refresh with a new request above the cursor keeps the cursor on its request.
func TestARefreshKeepsTheCursorOnItsRequest(t *testing.T) {
	m := sampleApp()
	sampleApprovals(m)
	m = onCard(m, 2)
	arrived := netApproval("n9", "example.org", m.now, "example.org")
	m = run(m, func() tea.Msg { return approvalsLoaded{list: append([]api.ApprovalView{arrived}, m.approvals...)} })
	if c := cursorCard(t, m); c.first().ID != "n1" {
		t.Errorf("the cursor moved to %s with the refresh", c.first().ID)
	}
}

// tab goes to the next request and wraps around the end; with none, it says so.
func TestTabGoesAroundTheRequests(t *testing.T) {
	m := sampleApp()
	sampleApprovals(m)
	m = press(onCard(m, 2), "tab")
	if c := cursorCard(t, m); c.first().ID != "g1" {
		t.Errorf("tab from the last request: %s, want g1", c.first().ID)
	}
	m.approvals = nil
	m = press(m, "tab")
	if m.notice.text != "Nothing waits for you." {
		t.Errorf("tab with no requests: %q", m.notice.text)
	}
}

// Until the cursor moves, it is on the top row, whatever arrives above what it showed first.
func TestTheCursorStartsOnTheFirstRequest(t *testing.T) {
	m := sampleApp()
	m.approvals = nil
	m.goHome()
	_ = shown(m) // Home shows demo's row alone
	sampleApprovals(m)
	if c := cursorCard(t, m); c.first().ID != "g1" {
		t.Errorf("the first request to arrive is not under the cursor: %s", c.first().ID)
	}
}

// enter on a computer's row opens it, and esc there comes back to Home's list.
func TestEnterOnAComputerRowOpensIt(t *testing.T) {
	m := sampleApp()
	m.approvals = nil
	m.goHome()
	m = press(m, "enter")
	if m.screen != screenDetail || m.selectedID() != "sb4" {
		t.Fatalf("enter on demo's row: screen %v, %s", m.screen, m.selectedID())
	}
	if m = press(m, "esc"); !m.inboxKeys() {
		t.Errorf("esc from the computer: screen %v, the list has the keys %v", m.screen, m.inboxKeys())
	}
}
