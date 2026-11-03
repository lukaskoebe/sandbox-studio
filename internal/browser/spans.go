package browser

import (
	"context"
	"fmt"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// spansEvery is how often RecordSessions looks at the browser sessions.
const spansEvery = 10 * time.Second

// pageKey names a browser session for its spans: an ephemeral session's ID, or the
// persistent profile's run.
func (st *state) pageKey(pg *page) string {
	if pg.slot > 0 {
		return pg.id
	}
	return "profile:" + st.session
}

// openSpans are the browser sessions open now and the user's turns in them, by
// environment: each on the line of the sandbox that drives it. The profile belongs to the
// sandbox that last drove the VM in the run; a run no sandbox drove has no line.
func (s *Service) openSpans() map[string]map[string][]store.SessionSpan {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]map[string][]store.SessionSpan{}
	add := func(env, kind string, sp store.SessionSpan) {
		if out[env] == nil {
			out[env] = map[string][]store.SessionSpan{}
		}
		out[env][kind] = append(out[env][kind], sp)
	}
	for _, st := range s.states {
		if st.vm == "" || st.session == "" {
			continue
		}
		pages := []*page{}
		if st.driver.ID != "" {
			pages = append(pages, &st.page)
		}
		for _, pg := range st.eph {
			pages = append(pages, pg)
		}
		for _, pg := range pages {
			owner, opened := pg.owner, pg.opened
			if pg.slot == 0 {
				owner, opened = st.driver, time.Time{}
			}
			key := st.pageKey(pg)
			add(st.env, store.SpanBrowser, store.SessionSpan{SandboxID: owner.ID, Key: key, Label: pg.url,
				ChatID: pg.chat, TeammateID: st.teammate, OpenedAt: opened})
			if pg.takeover && pg.turn != nil {
				add(st.env, store.SpanDrive, store.SessionSpan{SandboxID: owner.ID, Parent: key, TeammateID: st.teammate,
					Key: fmt.Sprintf("%s@%d", key, pg.turn.since.Unix()), OpenedAt: pg.turn.since})
			}
		}
	}
	return out
}

// RecordSessions records the browser sessions as spans of their sandboxes every spansEvery
// until ctx ends, and publishes events.TopicSessions when one opened or closed.
func (s *Service) RecordSessions(ctx context.Context, bus *events.Bus) {
	t := time.NewTicker(spansEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.recordSpans(ctx, bus)
		}
	}
}

func (s *Service) recordSpans(ctx context.Context, bus *events.Bus) {
	envs, err := s.Store.Environments(ctx)
	if err != nil {
		return
	}
	open, at := s.openSpans(), s.now()
	for _, env := range envs {
		for _, kind := range []string{store.SpanBrowser, store.SpanDrive} {
			changed, err := s.Store.SyncSessionSpans(ctx, store.SpanScope{EnvironmentID: env.ID, Kind: kind}, open[env.ID][kind], at)
			if err == nil && changed && bus != nil {
				bus.Publish(events.Event{Topic: events.TopicSessions, EnvironmentID: env.ID})
			}
		}
	}
}
