package sandboxes

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func TestCheckpointScheduleTakesDueCheckpoints(t *testing.T) {
	f := newCheckpointFixture(t)
	if _, err := f.manager.SetCheckpointSchedule(f.ctx, f.env.ID, f.sandbox.ID, 2); !errors.Is(err, ErrCheckpointSchedule) {
		t.Fatalf("SetCheckpointSchedule(2) = %v; want ErrCheckpointSchedule", err)
	}
	manual := f.createReadyCheckpoint(t, "mine")
	if _, err := f.manager.SetCheckpointSchedule(f.ctx, f.env.ID, f.sandbox.ID, 1); err != nil {
		t.Fatal(err)
	}
	autos := func() []store.Checkpoint {
		t.Helper()
		all, err := f.store.Checkpoints(f.ctx, f.env.ID, f.sandbox.ID)
		if err != nil {
			t.Fatal(err)
		}
		return slices.DeleteFunc(all, func(cp store.Checkpoint) bool { return !cp.Auto })
	}

	now := time.Now()
	f.manager.runCheckpointSchedules(f.ctx, now) // The first one is taken at once, even of a stopped sandbox.
	if got := autos(); len(got) != 1 || got[0].State != store.CheckpointStateReady || got[0].Name != "Auto "+now.Format("2006-01-02 15:04") {
		t.Fatalf("scheduled checkpoints after the first run = %+v", got)
	}
	f.manager.runCheckpointSchedules(f.ctx, now.Add(30*time.Minute))
	if got := autos(); len(got) != 1 {
		t.Fatalf("a checkpoint was taken before it was due: %+v", got)
	}
	f.manager.runCheckpointSchedules(f.ctx, now.Add(61*time.Minute))
	if got := autos(); len(got) != 1 {
		t.Fatalf("a stopped sandbox that didn't run got another checkpoint: %+v", got)
	}

	f.runtime.setStatus(VMName(f.sandbox), runtime.StatusRunning)
	f.runtime.fail("create-checkpoint", errors.New("disk full"))
	f.manager.runCheckpointSchedules(f.ctx, now.Add(62*time.Minute))
	cs, err := f.manager.CheckpointSchedule(f.ctx, f.env.ID, f.sandbox.ID)
	if err != nil || cs.EveryHours != 1 || cs.Note == "" {
		t.Fatalf("schedule after a failed checkpoint = %+v, %v; want a note", cs, err)
	}
	f.runtime.clearFailure("create-checkpoint")
	f.manager.runCheckpointSchedules(f.ctx, now.Add(70*time.Minute))
	if got := autos(); len(got) != 1 {
		t.Fatalf("the schedule retried at once after a failure: %+v", got)
	}
	f.manager.runCheckpointSchedules(f.ctx, now.Add(80*time.Minute))
	if got := autos(); len(got) != 2 {
		t.Fatalf("scheduled checkpoints of a running sandbox = %+v; want 2", got)
	}
	if cs, _ := f.manager.CheckpointSchedule(f.ctx, f.env.ID, f.sandbox.ID); cs.Note != "" {
		t.Fatalf("the note stayed after a checkpoint was taken: %q", cs.Note)
	}

	if _, err := f.manager.SetCheckpointSchedule(f.ctx, f.env.ID, f.sandbox.ID, 0); err != nil {
		t.Fatal(err)
	}
	f.manager.runCheckpointSchedules(f.ctx, now.Add(5*time.Hour))
	if got := autos(); len(got) != 2 {
		t.Fatalf("turning the schedule off changed its checkpoints: %+v", got)
	}
	if _, err := f.store.Checkpoint(f.ctx, f.env.ID, f.sandbox.ID, manual.ID); err != nil {
		t.Fatalf("the user's checkpoint: %v", err)
	}
}

func TestPrunedAutoCheckpoints(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip(err)
	}
	now := time.Date(2026, 10, 30, 10, 30, 0, 0, berlin) // the week holds the end of summer time
	var cps []store.Checkpoint
	// Every 4 hours for 10 days, and one of the user's among them.
	for at := now.Add(-10 * 24 * time.Hour); !at.After(now); at = at.Add(4 * time.Hour) {
		cps = append(cps, store.Checkpoint{ID: at.Format(time.DateTime), Auto: true, State: store.CheckpointStateReady, CreatedAt: at})
	}
	cps = append(cps, store.Checkpoint{ID: "mine", State: store.CheckpointStateReady, CreatedAt: now.Add(-9 * 24 * time.Hour)})

	pruned := prunedAutoCheckpoints(cps, now)
	gone := map[string]bool{}
	for _, cp := range pruned {
		gone[cp.ID] = true
	}
	var kept []string
	for _, cp := range cps {
		if !gone[cp.ID] {
			kept = append(kept, cp.ID)
		}
	}
	// Today's three, then the last of each of the seven days before; summer time ends on the 25th.
	want := []string{
		"2026-10-23 23:30:00", "2026-10-24 23:30:00", "2026-10-25 22:30:00", "2026-10-26 22:30:00",
		"2026-10-27 22:30:00", "2026-10-28 22:30:00", "2026-10-29 22:30:00",
		"2026-10-30 02:30:00", "2026-10-30 06:30:00", "2026-10-30 10:30:00", "mine",
	}
	if !slices.Equal(kept, want) {
		t.Fatalf("kept %v; want %v", kept, want)
	}
}
