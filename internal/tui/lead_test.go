package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/lead"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// withLead gives m's Home a Lead that wrote today's day.
func withLead(m *app) *app {
	at := m.now.Add(-time.Hour)
	m.inbox.lead = &lead.LeadView{
		Settings: store.LeadSettings{Name: "Guide"},
		Today:    lead.LeadToday{Day: "2026-10-13", Summary: "Iris finished the coupon importer.", WrittenAt: &at, Turns: 4, Threads: 2},
	}
	m.inbox.day = &lead.LeadDayView{Day: "2026-10-13", Written: true,
		Markdown: "# Tuesday 13 October 2026 · a coupon day\n\nIris finished the coupon importer.\n\n## Iris\n\n- Finished the coupon importer, and its tests pass on the second try."}
	m.inbox.leadEnv = m.env.ID
	return m
}

// Lead's row heads QUIET with the day's summary; its reading pane is the day, and w writes it.
func TestLeadRowShowsTheDayAndWritesIt(t *testing.T) {
	m := withLead(sampleApp())
	m.approvals = nil
	m.goHome()
	rows := m.inboxRows()
	at := leadRow(rows)
	if at < 0 {
		t.Fatalf("Home has no Lead row: %+v", rows)
	}
	for _, r := range rows[:at] {
		if r.section == inboxQuiet {
			t.Fatalf("a quiet row %q comes before Lead's", r.id)
		}
	}
	m.inbox.cursor = "lead"
	screen := shown(m)
	for _, want := range []string{"Guide", "Iris finished the coupon importer.", "a coupon day", "• Finished the coupon importer"} {
		if !strings.Contains(screen, want) {
			t.Errorf("Home doesn't show %q:\n%s", want, screen)
		}
	}
	m, cmd := pressCmd(m, "w")
	if cmd == nil || !m.inbox.lead.Writing {
		t.Fatalf("w didn't ask Lead to write the day: cmd %v, writing %v", cmd != nil, m.inbox.lead.Writing)
	}
	if !strings.Contains(shown(m), "writing the day") {
		t.Errorf("Lead's row doesn't say it writes")
	}
}

// Without a turn today and nothing written, Home has no Lead row; nor for another environment.
func TestLeadRowWaitsForWork(t *testing.T) {
	m := withLead(sampleApp())
	m.inbox.lead.Today = lead.LeadToday{Day: "2026-10-13"}
	if leadRow(m.inboxRows()) >= 0 {
		t.Errorf("Lead is listed on a day without work")
	}
	m = withLead(sampleApp())
	m.inbox.leadEnv = "env2"
	if leadRow(m.inboxRows()) >= 0 {
		t.Errorf("another environment's Lead is listed")
	}
}

func leadRow(rows []inboxRow) int {
	for i, r := range rows {
		if r.lead {
			return i
		}
	}
	return -1
}
