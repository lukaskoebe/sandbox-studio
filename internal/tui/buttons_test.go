package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/lukaskoebe/sandbox-studio/internal/api"
	"github.com/lukaskoebe/sandbox-studio/internal/policy"
)

// blockedAgent is Home on api-refactor's codex, blocked on a network request.
func blockedAgent(t *testing.T) *app {
	t.Helper()
	m := paneApp()
	m.approvals[0].Network = &policy.NetworkRequest{Host: "proxy.golang.org", Port: 443, SandboxName: "api-refactor"}
	m = press(m, "j", "j", "j", "j", "j")
	if x, _ := m.paneTarget(); x.term.Name != "codex" {
		t.Fatalf("on %+v", x)
	}
	return m
}

func TestArrowsMoveOverACardsTogglesAndChoices(t *testing.T) {
	m := sampleApp()
	sampleApprovals(m)
	m = press(m, "a", "j", "j") // the network request
	c := m.cards()[m.approvalCursor]
	owner := cardOwner(c)
	if !isNetwork(c.first()) || m.buttonAt(owner) >= 0 {
		t.Fatalf("on %+v, focus %+v", c.first().Kind, m.buttons)
	}

	// The first arrow is on the first choice, past the toggles; ← steps back over them.
	m = press(m, "right")
	if at := m.buttonAt(owner); at != len(cardToggles(c)) {
		t.Fatalf("the first arrow focuses %d", at)
	}
	m = press(m, "left", "left", "left")
	if at := m.buttonAt(owner); at != 0 {
		t.Fatalf("← stops on the pattern: %d", at)
	}
	before := m.choiceOf(c).pattern
	m = press(m, "space")
	if m.choiceOf(c).pattern == before {
		t.Error("space on the pattern does not step it")
	}
	if m.screen != screenApprovals {
		t.Fatalf("pressing a toggle left the card: %v", m.screen)
	}
	if out := ansi.Strip(press(m, "?").View().Content); !strings.Contains(out, "←/→ space") {
		t.Errorf("the help does not list the arrows:\n%s", out)
	}
	m = press(m, "esc")

	// Enter on a choice decides, as its letter does.
	m = press(m, "right", "right", "right")
	if _, cmd := pressCmd(m, "enter"); cmd == nil {
		t.Error("enter on a choice decides nothing")
	}
}

func TestBannerAnswersFromThePaneAndGivesTheKeysBack(t *testing.T) {
	m := blockedAgent(t)
	m.pane.focus = true
	if !m.paneFocused() {
		t.Fatal("the pane does not have the keys")
	}
	if out := shown(m); !strings.Contains(out, "alt+a answers it") {
		t.Fatalf("the banner does not say how to answer while the agent has the keys:\n%s", out)
	}
	m = press(m, "alt+a")
	c, _ := m.paneCard(must(m.paneTarget()))
	if m.paneFocused() || !m.pane.answer || m.buttonAt(bannerOwner(c)) != 0 {
		t.Fatalf("alt+a does not hand the banner the keys: focus %v answer %v %+v", m.pane.focus, m.pane.answer, m.buttons)
	}
	if out := shown(m); !strings.Contains(out, "Allow once") || !strings.Contains(out, "More…") {
		t.Fatalf("the banner's choices:\n%s", out)
	}
	m = press(m, "right") // Allow
	m, cmd := pressCmd(m, "space")
	if cmd == nil {
		t.Fatal("space on Allow decides nothing")
	}
	if !m.paneFocused() || m.pane.answer || m.buttons.owner != "" {
		t.Errorf("after the decision the keys are not back with the agent: focus %v answer %v", m.pane.focus, m.pane.answer)
	}

	// esc lets go of the banner too.
	m = press(m, "alt+a", "esc")
	if !m.paneFocused() || m.pane.answer {
		t.Errorf("esc does not give the keys back: focus %v answer %v", m.pane.focus, m.pane.answer)
	}
}

func TestBannerMoreReviewsTheCardAndReturnsToTheAgent(t *testing.T) {
	m := blockedAgent(t)
	m.pane.focus = true
	m = press(m, "alt+a", "a")
	if m.screen != screenApprovals || m.resuming == nil || !m.resuming.pane {
		t.Fatalf("a from the banner opens %v, resuming %+v", m.screen, m.resuming)
	}
	if got := m.cards()[m.approvalCursor].first().ID; got != m.approvals[0].ID {
		t.Errorf("the cursor is on %s", got)
	}
	// While it waits the screen stays; once decided, Home's pane has the keys again.
	m = run(m, func() tea.Msg { return approvalsLoaded{list: m.approvals} })
	if m.screen != screenApprovals {
		t.Fatalf("the approvals left before the decision: %v", m.screen)
	}
	m = run(m, func() tea.Msg { return approvalsLoaded{list: m.approvals[1:]} })
	if m.screen != screenHome || !m.paneFocused() || m.resuming != nil {
		t.Errorf("after the decision: screen %v focus %v", m.screen, m.pane.focus)
	}
}

func TestBannerShowsOnEverySessionOfTheSandbox(t *testing.T) {
	m := blockedAgent(t)
	m = press(m, "j") // api-refactor's next session, which isn't blocked
	if x := must(m.paneTarget()); x.sandbox != m.approvals[0].SandboxID || x.term.Name == "codex" || m.paneBanner(x, 90) == nil {
		t.Errorf("no banner over %+v", x)
	}
	m = press(m, "k", "k", "k") // blub's sessions
	if x, ok := m.paneTarget(); ok && x.sandbox != m.approvals[0].SandboxID && m.paneBanner(x, 90) != nil {
		t.Error("another sandbox shows the banner")
	}
}

func TestAttachedTerminalAnswersAndAttachesAgain(t *testing.T) {
	m := blockedAgent(t)
	sb := m.selected()
	id := m.approvals[0].ID
	m = run(m, func() tea.Msg { return attachDone{id: sb.ID, sandbox: sb.Name, name: "codex", waiting: id} })
	if m.screen != screenApprovals || m.resuming == nil || m.resuming.attach == nil {
		t.Fatalf("Ctrl-] on a waiting approval opens %v, resuming %+v", m.screen, m.resuming)
	}
	if !strings.Contains(m.notice.text, "you're back in codex") {
		t.Errorf("the notice: %q", m.notice.text)
	}
	next, cmd := m.Update(approvalsLoaded{list: []api.ApprovalView{}})
	if m = next.(*app); m.resuming != nil || cmd == nil {
		t.Errorf("after the decision the terminal is not attached again: resuming %+v", m.resuming)
	}
}

func TestConfirmationKeepsOnEnterAndArrowsChoose(t *testing.T) {
	m := press(sampleApp(), "d")
	sb := m.selected()
	if !strings.Contains(ansi.Strip(m.View().Content), "[n Keep]") {
		t.Fatalf("keep is not focused:\n%s", ansi.Strip(m.View().Content))
	}
	m = press(m, "enter")
	if m.confirm != nil {
		t.Fatal("enter does not keep")
	}
	m = press(m, "d", "left")
	if _, cmd := pressCmd(m, "enter"); cmd == nil {
		t.Errorf("← then enter does not delete %s", sb.Name)
	}
}

func TestChatAsksTakeTheArrows(t *testing.T) {
	m := pendingAsks(sampleChat(sampleApp()), `{"type":"object","properties":{"port":{"type":"integer","enum":[3000,8080]}}}`)
	m = press(m, "right", "right")
	if !strings.Contains(m.buttons.owner, "ask:") || m.buttons.at != 1 {
		t.Fatalf("the arrows are not on the permission's picks: %+v", m.buttons)
	}
	if out := ansi.Strip(m.View().Content); !strings.Contains(out, "←/→ choose") {
		t.Errorf("the key bar does not offer the arrows:\n%s", out)
	}
	if _, cmd := pressCmd(m, "space"); cmd == nil {
		t.Error("space on a pick answers nothing")
	}
}
