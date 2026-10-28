package chat

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/acp/acptest"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func TestUndo(t *testing.T) {
	f := newFixture(t, &acptest.Agent{})
	ctx := context.Background()
	env := f.sb.EnvironmentID
	f.g.ws = map[string]string{"a": "1"}
	f.g.turnEdits = []map[string]string{{"a": "2", "b": "new"}, {"a": "3"}, {"b": ""}}
	var ids []string
	for _, p := range []string{"one", "two\nmore", "three"} {
		turn := f.send(t, p)
		f.wait(t, turn.ID)
		ids = append(ids, turn.ID)
	}
	log, _ := f.s.Get(ctx, env, f.chat.ID)
	first := log.Turns[0]
	if !first.Undoable || first.Changes == nil || len(first.Changes.Files) != 2 || first.Changes.Files[0].Path != "a" || first.Changes.Files[1].Status != "added" {
		t.Fatalf("turn 1 %+v %+v", first, first.Changes)
	}
	if c := log.Turns[2].Changes; c == nil || len(c.Files) != 1 || c.Files[0].Status != "deleted" {
		t.Fatalf("turn 3 changes %+v", c)
	}
	ch, err := f.s.Changes(ctx, env, f.chat.ID, ids[0])
	if err != nil || len(ch.Files) != 2 || ch.Files[0].Diff != "-1\n+2\n" {
		t.Fatalf("changes %+v %v", ch, err)
	}

	// Undo turn 2: the workspace is back as it was before it, turns 2 and 3 are undone.
	if err := f.s.Undo(ctx, env, f.chat.ID, ids[1]); err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(f.g.ws, map[string]string{"a": "2", "b": "new"}) {
		t.Fatalf("workspace %v", f.g.ws)
	}
	log, _ = f.s.Get(ctx, env, f.chat.ID)
	if log.Chat.State != store.ChatIdle || !log.Turns[0].Undoable || log.Turns[1].Undoable || log.Turns[1].UndoneAt == nil || log.Turns[2].UndoneAt == nil {
		t.Fatalf("after undo %+v", log.Turns)
	}
	last := log.Items[len(log.Items)-1]
	if last.Marker == nil || last.Marker.Kind != MarkerUndo || last.Marker.Text != "Undid this and 1 later change" || last.TurnID != ids[1] {
		t.Fatalf("marker %+v", last)
	}
	if err := f.s.Undo(ctx, env, f.chat.ID, ids[1]); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("undo twice: %v", err)
	}

	// The next prompt tells the agent, once.
	for range 2 {
		f.wait(t, f.send(t, "four").ID)
	}
	prompts, _, _ := f.agent.Snapshot()
	note := "[Studio] The user undid your changes from the last 2 requests; the files are back as they were before 'two'."
	if !strings.Contains(string(prompts[3]), note) || strings.Contains(string(prompts[4]), "[Studio]") {
		t.Fatalf("prompts %s / %s", prompts[3], prompts[4])
	}
	// Undo isn't offered while a turn runs or the chat is in the terminal.
	if _, err := f.s.Store.SetChatState(ctx, env, f.chat.ID, store.ChatTerminal); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Undo(ctx, env, f.chat.ID, ids[0]); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("undo in the terminal: %v", err)
	}
}

func TestSnapshotsCollected(t *testing.T) {
	f := newFixture(t, &acptest.Agent{})
	ctx := context.Background()
	turns := make([]store.ChatTurn, 0, keepTurns+2)
	for range keepTurns + 2 {
		turn := f.send(t, "x")
		f.wait(t, turn.ID)
		turns = append(turns, turn)
	}
	stored, _ := f.s.Store.ChatTurns(ctx, f.chat.ID)
	for i, tr := range stored {
		if want := i >= 2; (tr.Snapshot != "") != want {
			t.Fatalf("turn %d snapshot %q", i, tr.Snapshot)
		}
	}
	f.g.mu.Lock()
	n := len(f.g.snaps)
	_, old := f.g.snaps[turns[0].ID]
	f.g.mu.Unlock()
	if n != 2*keepTurns || old {
		t.Fatalf("guest kept %d snapshots, the oldest: %v", n, old)
	}
	if err := f.s.Undo(ctx, f.sb.EnvironmentID, f.chat.ID, turns[0].ID); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("undo a collected turn: %v", err)
	}
	// Deleting the chat drops its snapshots.
	if err := f.s.Delete(ctx, f.sb.EnvironmentID, f.chat.ID); err != nil {
		t.Fatal(err)
	}
	if len(f.g.snaps) != 0 {
		t.Fatalf("left %d snapshots", len(f.g.snaps))
	}
}

func TestSnapshotFailureKeepsTheTurn(t *testing.T) {
	f := newFixture(t, &acptest.Agent{})
	f.g.snapErr = errors.New("guest too old")
	turn := f.send(t, "one")
	f.wait(t, turn.ID)
	log, _ := f.s.Get(context.Background(), f.sb.EnvironmentID, f.chat.ID)
	if log.Turns[0].Undoable || log.Turns[0].Changes != nil || log.Turns[0].StopReason != "end_turn" {
		t.Fatalf("turn %+v", log.Turns[0])
	}
}

func TestSnapshotWithoutRoomKeepsTheTurn(t *testing.T) {
	f := newFixture(t, &acptest.Agent{})
	f.g.noRoom = true
	turn := f.send(t, "one")
	f.wait(t, turn.ID)
	log, _ := f.s.Get(context.Background(), f.sb.EnvironmentID, f.chat.ID)
	if log.Turns[0].Undoable || log.Turns[0].StopReason != "end_turn" {
		t.Fatalf("turn %+v", log.Turns[0])
	}
	stored, _ := f.s.Store.ChatTurns(context.Background(), f.chat.ID)
	if stored[0].Snapshot != "" {
		t.Fatalf("recorded snapshot %q, which the guest had no room for", stored[0].Snapshot)
	}
}

func TestTaskDoneOnlyAfterChanges(t *testing.T) {
	f := newFixture(t, &acptest.Agent{})
	var mu sync.Mutex
	var done []string
	f.s.OnTaskDone = func(sandboxID string) {
		mu.Lock()
		defer mu.Unlock()
		done = append(done, sandboxID)
	}
	f.g.ws = map[string]string{"a": "1"}
	f.g.turnEdits = []map[string]string{{"a": "2"}, {}}
	f.wait(t, f.send(t, "change a").ID)
	f.wait(t, f.send(t, "just answer").ID)
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(done, []string{f.sb.ID}) {
		t.Fatalf("tasks done %v; want one, for the turn that changed a file", done)
	}
}
