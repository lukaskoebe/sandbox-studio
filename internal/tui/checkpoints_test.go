package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// sampleCheckpoints opens the checkpoints of docs-site, the stopped sandbox of the sample app,
// with one checkpoint the schedule took and one saved by hand.
func sampleCheckpoints() *app {
	m := sampleApp()
	m.cursor = 2
	m = press(m, "h")
	m.checkpoints.loaded = true
	m.checkpoints.list = []store.Checkpoint{
		{ID: "cp2", Name: "before the upgrade", State: "ready", CreatedAt: m.now.Add(-20 * time.Minute)},
		{ID: "cp1", Name: "Checkpoint 2026-10-16 03:00:00 UTC", State: "ready", Auto: true, CreatedAt: m.now.Add(-26 * time.Hour)},
	}
	return m
}

func TestGoldenCheckpoints(t *testing.T) {
	golden(t, "checkpoints", sampleCheckpoints())
}

func TestCheckpointsOpenFromHomeAndDetail(t *testing.T) {
	m := press(sampleApp(), "h")
	if m.screen != screenCheckpoints || m.checkpoints.sandbox != "sb1" || m.checkpoints.back != screenHome {
		t.Fatalf("h on home: screen %v, %+v", m.screen, m.checkpoints)
	}
	m = press(m, "esc", "enter", "h")
	if m.screen != screenCheckpoints || m.checkpoints.back != screenDetail {
		t.Fatalf("h on detail: screen %v, back %v", m.screen, m.checkpoints.back)
	}
	if m = press(m, "esc"); m.screen != screenDetail {
		t.Fatalf("esc went to %v", m.screen)
	}
}

// n saves a checkpoint under the name typed, or under the time when none is; the list then has
// it.
func TestSaveCheckpointThroughTheServer(t *testing.T) {
	ctx := context.Background()
	s := startStudio(t)
	s.addSandbox(t, "blub", runtime.StatusRunning)
	m := newApp(ctx, connect(t, s), s.Addr)
	m.at = atSandbox // on the sandbox's row, a row below Home's
	m.env = s.Env
	m = run(m, m.loadSandboxes())
	m, cmd := pressCmd(m, "h")
	m = run(m, cmd)
	if m.screen != screenCheckpoints || !m.checkpoints.loaded || len(m.checkpoints.list) != 0 {
		t.Fatalf("checkpoints: screen %v, %+v", m.screen, m.checkpoints)
	}
	if !strings.Contains(stripANSI(m.View().Content), "No checkpoints yet. Press n to save one.") {
		t.Fatalf("empty list:\n%s", stripANSI(m.View().Content))
	}

	m = press(m, "n")
	if m.typing == nil || !strings.Contains(stripANSI(m.noticeLine(80)), "Checkpoint name (enter for the time): ") {
		t.Fatalf("n asks nothing: %q", stripANSI(m.noticeLine(80)))
	}
	m = pressRun(t, m, "enter")
	name := checkpointName(m.now, nil)
	if m.notice.text != "Saved "+name+"." || m.notice.failed {
		t.Fatalf("save notice: %+v", m.notice)
	}
	m = press(m, "n", "w", "i", "p")
	m = pressRun(t, m, "enter")
	if m.notice.text != "Saved wip." {
		t.Fatalf("named save: %+v", m.notice)
	}
	m = run(m, m.loadCheckpoints())
	names := map[string]bool{}
	for _, cp := range m.checkpoints.list {
		names[cp.Name] = cp.State == "ready" && !cp.Auto
	}
	if len(names) != 2 || !names["wip"] || !names[name] {
		t.Fatalf("list: %+v", m.checkpoints.list)
	}

	// The runtime of the tests can't restore.
	m = press(m, "r")
	if m.notice.text != "Restoring needs a newer runtime." || m.checkpoints.restoring != nil {
		t.Fatalf("restore: %+v", m.notice)
	}
}

func TestCheckpointNameTakesTheNextFreeOne(t *testing.T) {
	now := time.Date(2026, 10, 17, 9, 30, 0, 0, time.UTC)
	base := "Checkpoint 2026-10-17 09:30:00 UTC"
	list := []store.Checkpoint{{Name: base}, {Name: base + " (2)"}}
	if got := checkpointName(now, list); got != base+" (3)" {
		t.Fatalf("name: %q", got)
	}
}

func TestSaveRefusesALongNameAndEscDropsIt(t *testing.T) {
	m := press(sampleCheckpoints(), "n")
	m.typing.text.set(strings.Repeat("x", 81))
	m = press(m, "enter")
	if m.typing == nil || !strings.Contains(stripANSI(m.noticeLine(200)), "at most 80 characters") {
		t.Fatalf("long name: %q", stripANSI(m.noticeLine(200)))
	}
	if m = press(m, "esc"); m.typing != nil || m.screen != screenCheckpoints {
		t.Fatalf("esc: typing %v, screen %v", m.typing != nil, m.screen)
	}
	m.sandboxes[2].Status = "starting"
	if m = press(m, "n"); m.typing != nil || !m.notice.failed {
		t.Fatalf("saved while starting: %+v", m.notice)
	}
}

// r asks before a restore of a stopped sandbox: y restores, any other key keeps it.
func TestRestoreAsksFirst(t *testing.T) {
	client, rec := recordingClient(t)
	m := sampleCheckpoints()
	m.client = client
	m.sandboxes[2].CheckpointRestoreSupported = true
	m = press(m, "down", "r")
	if m.checkpoints.restoring == nil || m.checkpoints.restoring.ID != "cp1" {
		t.Fatalf("r asks nothing: %+v", m.checkpoints.restoring)
	}
	if line := stripANSI(m.noticeLine(80)); !strings.Contains(line, "Restore docs-site to Checkpoint 2026-10-16 03:00:00 UTC?") {
		t.Fatalf("question: %q", line)
	}
	if bar := stripANSI(m.keyBar(m.width, 0)); !strings.HasPrefix(bar, "y restore") {
		t.Fatalf("bar: %q", bar)
	}
	m = press(m, "x")
	if m.checkpoints.restoring != nil || m.notice.text != "Kept docs-site as it is." || rec.count() != 0 {
		t.Fatalf("x: %+v, %d calls", m.notice, rec.count())
	}
	m = press(m, "r")
	m = pressRun(t, m, "y")
	if got := rec.last(t); got.method != "POST" || got.path != "/api/environments/env1/sandboxes/sb3/checkpoints/cp1/restore" {
		t.Fatalf("restore: %+v", got)
	}
	if m.notice.text != "docs-site is back at Checkpoint 2026-10-16 03:00:00 UTC." {
		t.Fatalf("restore notice: %+v", m.notice)
	}

	m.sandboxes[2].Status = "running"
	if m = press(m, "r"); m.checkpoints.restoring != nil || m.notice.text != "Stop docs-site to restore a checkpoint." {
		t.Fatalf("restore while running: %+v", m.notice)
	}
}
