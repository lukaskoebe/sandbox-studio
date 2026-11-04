package sandboxes

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// activityLog counts how busy terminals and previews are, by minute, until the terminal
// watcher writes the counts to session_activity (store.AddSessionActivity) for Home's lines.
// Chats and browser sessions need no counting: their tool calls and actions are logged.
type activityLog struct {
	mu     sync.Mutex
	counts map[activityKey]activityCount
}

type activityKey struct {
	sandbox, kind, key string
	minute             int64
}

type activityCount struct{ count, errors int }

// outputSlice is the longest stretch one look at a terminal counts as printing: the
// watcher looks every terminalsEvery while a UI tab is open, and less often otherwise.
const outputSlice = time.Minute

// activityKept is how long counts of a sandbox Studio can't place wait to be written.
const activityKept = time.Hour

func (a *activityLog) add(k activityKey, count, errors int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.counts == nil {
		a.counts = map[activityKey]activityCount{}
	}
	c := a.counts[k]
	c.count += count
	c.errors += errors
	a.counts[k] = c
}

// take returns the counts so far and forgets them.
func (a *activityLog) take() map[activityKey]activityCount {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := a.counts
	a.counts = nil
	return out
}

// noteOutput counts the terminals of list that printed since prev, the previous look at
// them: the seconds between the looks, up to outputSlice, at the minute of the latest
// output. A terminal the previous look didn't see counts if it printed in the last
// terminalsEvery.
func (m *Manager) noteOutput(id string, prev, list []Terminal, prevAt, at time.Time) {
	before := make(map[string]int64, len(prev))
	for _, t := range prev {
		before[terminalKey(t)] = t.Activity
	}
	since := at.Sub(prevAt)
	for _, t := range list {
		if t.Activity <= 0 {
			continue
		}
		last, seen := before[terminalKey(t)]
		var d time.Duration
		switch {
		case seen && t.Activity > last:
			d = min(since, outputSlice)
		case !seen && at.Sub(time.Unix(t.Activity, 0)) <= terminalsEvery:
			d = terminalsEvery
		default:
			continue
		}
		if s := int(d.Round(time.Second) / time.Second); s > 0 {
			m.activity.add(activityKey{id, store.SpanTerminal, terminalKey(t), t.Activity / 60}, s, 0)
		}
	}
}

// CountPreview counts a request the preview of port in sandbox id answered with status; 0
// is a connection taken over, such as a WebSocket. Answers of 500 and up count as errors.
func (m *Manager) CountPreview(id string, port, status int) {
	errors := 0
	if status >= 500 {
		errors = 1
	}
	m.activity.add(activityKey{id, store.SpanPreview, fmt.Sprint(port), time.Now().Unix() / 60}, 1, errors)
}

// flushActivity writes the counts so far and announces the environments they changed.
// Counts of sandboxes it can't place are kept for activityKept, in case the store was busy.
func (m *Manager) flushActivity(ctx context.Context, at time.Time) {
	counts := m.activity.take()
	if len(counts) == 0 || m.Store == nil {
		return
	}
	envs := map[string]string{} // sandbox ID → environment ID, "" when unknown
	var rows []store.ActivityCount
	for k, c := range counts {
		env, ok := envs[k.sandbox]
		if !ok {
			if sb, err := m.Store.LookupSandbox(ctx, k.sandbox); err == nil {
				env = sb.EnvironmentID
			}
			envs[k.sandbox] = env
		}
		if env == "" {
			if at.Unix()/60-k.minute < int64(activityKept/time.Minute) {
				m.activity.add(k, c.count, c.errors)
			}
			continue
		}
		rows = append(rows, store.ActivityCount{EnvironmentID: env, SandboxID: k.sandbox, Kind: k.kind, Key: k.key,
			Minute: k.minute, Count: c.count, Errors: c.errors})
	}
	if err := m.Store.AddSessionActivity(ctx, rows); err != nil {
		if m.Log != nil {
			m.Log.Warn("recording session activity", "err", err)
		}
		for _, r := range rows {
			m.activity.add(activityKey{r.SandboxID, r.Kind, r.Key, r.Minute}, r.Count, r.Errors)
		}
		return
	}
	if m.Bus == nil {
		return
	}
	published := map[string]bool{}
	for _, r := range rows {
		if !published[r.EnvironmentID] {
			published[r.EnvironmentID] = true
			m.Bus.Publish(events.Event{Topic: events.TopicSessions, EnvironmentID: r.EnvironmentID})
		}
	}
}
