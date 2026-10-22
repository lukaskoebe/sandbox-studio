package store

import (
	"context"
	"errors"
	"testing"
)

func TestMergeLifecycle(t *testing.T) {
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
	m, err := s.CreateMerge(ctx, Merge{EnvironmentID: env.ID, SourceID: "src", ForkID: "fork"})
	if err != nil {
		t.Fatal(err)
	}
	if m.State != MergePreparing || !m.Open() {
		t.Fatalf("new merge = %+v", m)
	}
	if _, err := s.CreateMerge(ctx, Merge{EnvironmentID: env.ID, SourceID: "src", ForkID: "other"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("second open merge: %v, want ErrConflict", err)
	}
	m.ScratchID, m.State, m.Report = "scratch", MergeConflicts, `{"conflicts":1}`
	if err := s.UpdateMerge(ctx, m, nil); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"src", "fork", "scratch"} {
		list, err := s.MergesOf(ctx, env.ID, id)
		if err != nil || len(list) != 1 || list[0].Report != `{"conflicts":1}` {
			t.Fatalf("MergesOf(%s) = %+v, %v", id, list, err)
		}
	}
	m.State = MergeAccepted
	ev := SandboxEvent{EnvironmentID: env.ID, SandboxID: "src", Kind: EventMerge, Generation: 1, Name: "src", OtherSandboxID: "fork"}
	if err := s.UpdateMerge(ctx, m, &ev); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateMerge(ctx, Merge{EnvironmentID: env.ID, ID: m.ID, State: MergeAborted}, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("update of a closed merge: %v, want ErrConflict", err)
	}
	evs, err := s.SandboxEvents(ctx, env.ID, "src")
	if err != nil || len(evs) != 1 || evs[0].Kind != EventMerge || evs[0].OtherSandboxID != "fork" {
		t.Fatalf("events = %+v, %v", evs, err)
	}
	if open, err := s.OpenMerges(ctx); err != nil || len(open) != 0 {
		t.Fatalf("open = %+v, %v", open, err)
	}
	if _, err := s.CreateMerge(ctx, Merge{EnvironmentID: env.ID, SourceID: "src", ForkID: "fork"}); err != nil {
		t.Fatalf("a merge after the last closed: %v", err)
	}
}
