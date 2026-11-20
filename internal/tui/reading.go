package tui

import (
	"fmt"
	"strings"

	"github.com/lukaskoebe/sandbox-studio/internal/api"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// The reading pane shows the row under the cursor of Home's list: a push's review, which pgup and
// pgdn scroll, a request's card with its choices, a thread or a terminal as it goes on, a
// computer's dashboard, or a teammate's next scheduled run.

// reading is the reading pane for the row r, in the part rc of the screen.
func (m *app) reading(r inboxRow, rc rect) []string {
	switch {
	case r.card != nil && isGit(r.card.first()):
		return m.readingReview(r.card.first(), rc)
	case r.card != nil:
		return m.readingCard(*r.card, rc)
	case r.thread != nil:
		sb, _ := m.sandboxByID(r.thread.sandbox)
		return m.sessionPane(*r.thread, sb, rc)
	case r.sb != "":
		if sb, ok := m.sandboxByID(r.sb); ok {
			return m.dashboard(sb, rc.w, rc.h)
		}
	case r.task != nil:
		return m.readingTask(*r.task, rc.w, rc.h)
	case r.lead:
		return m.readingLead(rc)
	}
	return nil
}

// readingReview is a push's or pull request's review, from the scroll on: its commits, files and
// diff, as the git review shows them.
func (m *app) readingReview(a api.ApprovalView, rc rect) []string {
	in := &m.inbox
	if in.review.id != a.ID || in.review.w != rc.w || in.review.lines == nil {
		in.review.id, in.review.w = a.ID, rc.w
		in.review.lines, _, _ = m.reviewLines(a, rc.w, false)
	}
	lines := in.review.lines
	in.scroll = min(in.scroll, max(len(lines)-rc.h, 0))
	return lines[in.scroll:]
}

// readingCard is a request's card with its choices, sliding in when it is new. Its keycaps are
// pressed with a click.
func (m *app) readingCard(c card, rc rect) []string {
	if m.look.plain {
		lines := m.cardLines(c, true, rc.w)
		lines[0] = strings.Replace(lines[0], styleAccent.Render("▸")+" ", "  ", 1) // the list's ▸ says which
		return lines
	}
	block, caps := m.visualCard(c, true, rc.w)
	hidden := len(block) - m.sliding(c, len(block))
	block = block[hidden:]
	for _, k := range caps {
		if y := k.y - hidden; y >= 0 && y < rc.h {
			m.hitKeys(rc.y+y, rc.x+k.x0, []keySpan{{x1: k.x1 - k.x0, key: k.key}})
		}
	}
	return block
}

// readingTask is a teammate's scheduled task: what it is, and when it runs next and ran last.
func (m *app) readingTask(t store.Task, w, h int) []string {
	label := func(name string) string { return styleDim.Render(fit(name, 11)) }
	who := m.teammates[t.TeammateID]
	lines := []string{
		spread(styleBold.Render(clip(oneLine(t.Title), w*2/3)), styleDim.Render(who), w),
		styleDim.Render(strings.Repeat("─", w)),
	}
	if t.Details != "" {
		details := wrapText(t.Details, w)
		if len(details) > 4 {
			details = append(details[:3], styleDim.Render("…"))
		}
		lines = append(append(lines, details...), "")
	}
	if t.NextRunAt != nil {
		when := runWhen(*t.NextRunAt, m.now)
		if t.Schedule.Repeats() {
			when += styleDim.Render(" · repeats")
		}
		lines = append(lines, label("NEXT RUN")+when)
	}
	if r := t.LastRun; r != nil {
		lines = append(lines, label("LAST RUN")+fmt.Sprintf("%s ago", shortAge(m.now.Sub(r.At)))+styleDim.Render(" · "+r.Outcome))
	}
	lines = append(lines, "", styleDim.Render(clip("enter shows "+firstNonEmpty(who, "the teammate")+"; the web's teammate page edits the schedule.", w)))
	return lines[:min(len(lines), h)]
}
