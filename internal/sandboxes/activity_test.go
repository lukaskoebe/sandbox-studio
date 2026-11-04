package sandboxes

import (
	"context"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// A terminal counts the seconds between looks that found it had printed, at the minute of
// its latest output; a preview counts its requests and 5xx answers. The watcher writes both
// to the sandbox's environment.
func TestActivityIsCountedAndWritten(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	env, err := s.CreateEnvironment(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	sb, err := s.CreateSandbox(ctx, store.Sandbox{EnvironmentID: env.ID, Name: "box", CPUs: 1, MemoryMiB: 512, WorkspaceMiB: 1024, DockerMiB: 1024})
	if err != nil {
		t.Fatal(err)
	}
	m := &Manager{Store: s}
	at := time.Unix(time.Now().Unix()/60*60, 0) // a whole minute
	term := func(activity time.Time) []Terminal {
		return []Terminal{{Session: agentproto.Session{Name: "main", Created: 1, Activity: activity.Unix()}}}
	}
	quiet := []Terminal{{Session: agentproto.Session{Name: "old", Created: 1, Activity: at.Add(-time.Hour).Unix()}}}

	m.noteTerminals(env.ID, sb.ID, append(term(at.Add(-3*time.Second)), quiet...), at)                    // new and printing: 10 s
	m.noteTerminals(env.ID, sb.ID, append(term(at.Add(8*time.Second)), quiet...), at.Add(10*time.Second)) // printed again: 10 s
	m.noteTerminals(env.ID, sb.ID, append(term(at.Add(8*time.Second)), quiet...), at.Add(20*time.Second)) // silent
	m.noteTerminals(env.ID, sb.ID, append(term(at.Add(70*time.Second)), quiet...), at.Add(3*time.Minute)) // a late look: a minute
	m.CountPreview(sb.ID, 8080, 200)
	m.CountPreview(sb.ID, 8080, 502)
	m.CountPreview("gone", 8080, 200) // a sandbox Studio doesn't know waits
	m.flushActivity(ctx, at.Add(3*time.Minute))

	acts, err := s.SessionActivityIn(ctx, env.ID, at.Add(-time.Hour), at.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][2]int{}
	for _, a := range acts {
		k := a.Kind + " " + a.Key + " " + a.At.Sub(at).String()
		if a.Kind == store.SpanPreview {
			k = "preview 8080" // counted when they came, which is now
		}
		c := got[k]
		got[k] = [2]int{c[0] + a.Count, c[1] + a.Errors}
	}
	want := map[string][2]int{
		"terminal main@1 -1m0s": {10, 0},
		"terminal main@1 0s":    {10, 0},
		"terminal main@1 1m0s":  {60, 0},
		"preview 8080":          {2, 1},
	}
	if len(got) != len(want) {
		t.Errorf("activity %v, want %v", got, want)
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s = %v, want %v", k, got[k], w)
		}
	}
	if left := m.activity.take(); len(left) != 1 {
		t.Errorf("kept %v, want the unknown sandbox's count", left)
	}
}
