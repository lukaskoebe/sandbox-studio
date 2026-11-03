package store

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestSessionSpansFollowWhatIsLive(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	env, err := s.CreateEnvironment(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Unix(1_800_000_000, 0)
	scope := SpanScope{EnvironmentID: env.ID, SandboxID: "sb", Kind: SpanTerminal}
	sync := func(at time.Time, live ...SessionSpan) bool {
		t.Helper()
		changed, err := s.SyncSessionSpans(ctx, scope, live, at)
		if err != nil {
			t.Fatal(err)
		}
		return changed
	}
	main := SessionSpan{Key: "main@1", Label: "main", OpenedAt: t0.Add(-time.Hour)}
	if !sync(t0, main, SessionSpan{Key: "claude@2", Harness: "claude"}) {
		t.Fatal("two new terminals changed nothing")
	}
	if sync(t0.Add(time.Minute), main, SessionSpan{Key: "claude@2", Harness: "claude"}) {
		t.Fatal("the same terminals changed something")
	}
	// Another sandbox's terminals are out of scope.
	if _, err := s.SyncSessionSpans(ctx, SpanScope{EnvironmentID: env.ID, SandboxID: "other", Kind: SpanTerminal}, []SessionSpan{{Key: "main@1"}}, t0); err != nil {
		t.Fatal(err)
	}
	main.Label = "psql"
	if !sync(t0.Add(2*time.Minute), main) {
		t.Fatal("a closed terminal changed nothing")
	}
	spans, err := s.SessionSpans(ctx, env.ID, t0.Add(-2*time.Hour), t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 3 {
		t.Fatalf("spans = %+v, want 3", spans)
	}
	byKey := map[string]SessionSpan{}
	for _, sp := range spans {
		if sp.SandboxID == "sb" {
			byKey[sp.Key] = sp
		}
	}
	if m := byKey["main@1"]; !m.OpenedAt.Equal(main.OpenedAt) || m.ClosedAt != nil || m.Label != "psql" {
		t.Errorf("main = %+v, want open since an hour before with the new label", m)
	}
	if c := byKey["claude@2"]; !c.OpenedAt.Equal(t0) || c.ClosedAt == nil || !c.ClosedAt.Equal(t0.Add(2*time.Minute)) || c.Harness != "claude" {
		t.Errorf("claude = %+v, want open from t0 to t0+2m", c)
	}
	// A window after the close leaves the closed one out.
	later, err := s.SessionSpans(ctx, env.ID, t0.Add(10*time.Minute), t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(later) != 2 {
		t.Errorf("later spans = %+v, want the two open ones", later)
	}
	// Sandboxes that stopped running close, but those kept.
	if n, err := s.CloseSessionSpansExcept(ctx, []string{SpanTerminal, SpanPreview}, []string{"other"}, t0.Add(3*time.Minute)); err != nil || n != 1 {
		t.Fatalf("closing all but other: %d, %v; want 1", n, err)
	}
	if err := s.PruneSessionSpans(ctx, t0.Add(150*time.Second)); err != nil {
		t.Fatal(err)
	}
	left, err := s.SessionSpans(ctx, env.ID, t0.Add(-2*time.Hour), t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 2 {
		t.Errorf("after pruning = %+v, want main and other's", left)
	}
}

// Watchers that sync the same sandbox at once open each session once.
func TestSessionSpansSyncedAtOnceOpenOnce(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "studio.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	env, err := s.CreateEnvironment(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(1_800_000_000, 0)
	scope := SpanScope{EnvironmentID: env.ID, SandboxID: "sb", Kind: SpanTerminal}
	live := []SessionSpan{{Key: "main@1", Label: "main"}, {Key: "dev@2", Label: "dev"}}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.SyncSessionSpans(ctx, scope, live, at); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	spans, err := s.SessionSpans(ctx, env.ID, at.Add(-time.Hour), at.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 2 {
		t.Fatalf("%d spans: %+v", len(spans), spans)
	}
}
