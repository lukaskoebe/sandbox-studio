package sandboxes

import (
	"context"
	"fmt"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// spanKinds are the spans the terminal watcher records: they end with the sandbox's guest.
var spanKinds = []string{store.SpanTerminal, store.SpanRun, store.SpanPreview}

// terminalKey names a terminal for its spans: its name and when it was created, so a new
// session under an old name is a new span.
func terminalKey(t Terminal) string { return fmt.Sprintf("%s@%d", t.Name, t.Created) }

// terminalSpans are the spans list shows open: each terminal, and the program in the
// foreground of those that run one other than a harness.
func terminalSpans(list []Terminal) (terms, runs []store.SessionSpan) {
	for _, t := range list {
		key := terminalKey(t)
		harness := t.Harness
		if harness == "" && harnessPrograms[t.Program] {
			harness = t.Program
		}
		var opened time.Time
		if t.Created > 0 {
			opened = time.Unix(t.Created, 0)
		}
		terms = append(terms, store.SessionSpan{Key: key, Label: t.Label, ChatID: t.Chat, Harness: harness, OpenedAt: opened})
		if t.State != TerminalBusy {
			continue
		}
		run := store.SessionSpan{Key: fmt.Sprintf("%s@%d", key, t.Started), Parent: key, Label: commandLine(t.Foreground)}
		if t.Started > 0 {
			run.OpenedAt = time.Unix(t.Started, 0)
		}
		runs = append(runs, run)
	}
	return terms, runs
}

// recordSpans makes the spans of kind open in sandbox id those of live, and announces a
// change.
func (m *Manager) recordSpans(ctx context.Context, envID, id, kind string, live []store.SessionSpan, at time.Time) {
	if m.Store == nil {
		return
	}
	changed, err := m.Store.SyncSessionSpans(ctx, store.SpanScope{EnvironmentID: envID, SandboxID: id, Kind: kind}, live, at)
	if err != nil {
		if m.Log != nil {
			m.Log.Warn("recording sessions", "sandbox", id, "kind", kind, "err", err)
		}
		return
	}
	if changed && m.Bus != nil {
		m.Bus.Publish(events.Event{Topic: events.TopicSessions, EnvironmentID: envID, SandboxID: id})
	}
}

// recordTerminals records the terminals and runs of sandbox id that list shows.
func (m *Manager) recordTerminals(ctx context.Context, envID, id string, list []Terminal, at time.Time) {
	terms, runs := terminalSpans(list)
	m.recordSpans(ctx, envID, id, store.SpanTerminal, terms, at)
	m.recordSpans(ctx, envID, id, store.SpanRun, runs, at)
}

// recordPorts records the ports listening in sandbox id, as previews.
func (m *Manager) recordPorts(ctx context.Context, envID, id string, at time.Time) {
	ports, err := m.Hub.Ports(ctx, id)
	if err != nil {
		return // a guest that doesn't answer keeps what it had open
	}
	live := make([]store.SessionSpan, 0, len(ports))
	for _, p := range ports {
		live = append(live, store.SessionSpan{Key: fmt.Sprint(p.Port), Label: fmt.Sprintf(":%d", p.Port)})
	}
	m.recordSpans(ctx, envID, id, store.SpanPreview, live, at)
}

// closeSpansExcept closes the terminal, run and preview spans of sandboxes the watcher no
// longer sees running: those not in running.
func (m *Manager) closeSpansExcept(ctx context.Context, running map[string]bool, at time.Time) {
	if m.Store == nil {
		return
	}
	keep := make([]string, 0, len(running))
	for id := range running {
		keep = append(keep, id)
	}
	n, err := m.Store.CloseSessionSpansExcept(ctx, spanKinds, keep, at)
	if err != nil && m.Log != nil {
		m.Log.Warn("closing the sessions of stopped sandboxes", "err", err)
	}
	if n > 0 && m.Bus != nil {
		m.Bus.Publish(events.Event{Topic: events.TopicSessions})
	}
}

// spanDays is how long closed spans are kept.
const spanDays = 14

// pruneSpans deletes the spans that closed more than spanDays ago.
func (m *Manager) pruneSpans(ctx context.Context, at time.Time) {
	if m.Store != nil {
		_ = m.Store.PruneSessionSpans(ctx, at.AddDate(0, 0, -spanDays))
	}
}
