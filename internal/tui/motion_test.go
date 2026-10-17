package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/lukaskoebe/sandbox-studio/internal/api"
)

func TestGoldenVisualToast(t *testing.T) {
	m := visualApp(80, 24)
	next, _ := m.Update(actionDone{err: errors.New("stop api-refactor: the runtime did not answer in time, so the sandbox may still be running")})
	goldenVisual(t, "visual_toast", next.(*app), true)
}

func TestToastShowsTheResultForAWhile(t *testing.T) {
	m := visualApp(100, 30)
	m = press(m, "s") // a progress notice, which the result replaces
	next, cmd := m.Update(actionDone{done: "Stopped blub"})
	m = next.(*app)
	if cmd == nil || m.notice.text != "" {
		t.Fatalf("the result keeps the notice %q, or starts no expiry", m.notice.text)
	}
	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	if !strings.Contains(lines[3], "✓ Stopped blub") || !strings.HasSuffix(strings.TrimRight(lines[3], " "), "│") {
		t.Fatalf("no toast at the top right:\n%s", strings.Join(lines[:6], "\n"))
	}
	next, _ = m.Update(toastDone{id: m.toast.id - 1})
	if m = next.(*app); m.toast.text == "" {
		t.Fatal("an older toast's expiry took the new one away")
	}
	next, _ = m.Update(toastDone{id: m.toast.id})
	if m = next.(*app); m.toast.text != "" || strings.Contains(m.View().Content, "Stopped blub") {
		t.Error("the toast stays after its time")
	}

	plain := sampleApp()
	next, _ = plain.Update(actionDone{done: "Stopped blub"})
	if p := next.(*app); p.notice.text != "Stopped blub" || p.toast.text != "" {
		t.Errorf("the plain look reports on the notice line: notice %q, toast %q", p.notice.text, p.toast.text)
	}
}

func TestANewCardSlidesIn(t *testing.T) {
	m := visualApp(100, 30)
	sampleApprovals(m)
	m.primed = true
	for _, a := range m.approvals {
		m.known[a.ID] = true
	}
	m = press(m, "a")
	full := len(strings.Split(m.View().Content, "\n"))
	if full != 30 {
		t.Fatalf("frame of %d lines", full)
	}
	card := func() int {
		n := 0
		for _, l := range strings.Split(ansi.Strip(m.View().Content), "\n") {
			if strings.Contains(l, "example.org") || (n > 0 && !strings.Contains(l, "╰")) {
				n++
			} else if n > 0 {
				return n + 1
			}
		}
		return n
	}
	arrived := netApproval("n9", "example.org", m.now, "example.org")
	m.Update(approvalsLoaded{list: append([]api.ApprovalView{arrived}, m.approvals...)})
	if len(m.slide.ids) != 1 {
		t.Fatalf("no slide for the new card: %+v", m.slide)
	}
	first := card()
	for range slideSteps {
		m.Update(slideTick{})
	}
	if m.slide.ids != nil {
		t.Fatal("the slide never ends")
	}
	if last := card(); first >= last {
		t.Errorf("the card showed %d lines at first and %d at the end", first, last)
	}
}
