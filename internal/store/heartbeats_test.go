package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestHeartbeatSchedule(t *testing.T) {
	ctx := context.Background()
	s, work, other := openTeammateStore(t)
	p, err := s.CreateTeammate(ctx, Teammate{EnvironmentID: work.ID, Name: "Iris", Harness: "claude", GitName: "Iris", GitEmail: "iris@agents.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Heartbeat != nil {
		t.Fatalf("a new teammate has a schedule: %+v", p.Heartbeat)
	}

	on := &Heartbeat{EveryHours: 4, FromHour: 9, ToHour: 18}
	got, err := s.SetHeartbeat(ctx, work.ID, p.ID, on)
	if err != nil || got.Heartbeat == nil || *got.Heartbeat != *on {
		t.Fatalf("set: %+v (%v)", got.Heartbeat, err)
	}
	list, err := s.HeartbeatTeammates(ctx)
	if err != nil || len(list) != 1 || list[0].ID != p.ID || *list[0].Heartbeat != *on {
		t.Fatalf("scheduled teammates: %+v (%v)", list, err)
	}

	if got, err := s.SetHeartbeat(ctx, work.ID, p.ID, nil); err != nil || got.Heartbeat != nil {
		t.Fatalf("off: %+v (%v)", got.Heartbeat, err)
	}
	if list, _ := s.HeartbeatTeammates(ctx); len(list) != 0 {
		t.Fatalf("a teammate with its schedule off is listed: %+v", list)
	}
	if _, err := s.SetHeartbeat(ctx, other.ID, p.ID, on); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a teammate of another environment: %v", err)
	}
}

func TestRecordBeatMovesScheduleAndLogsOutcomes(t *testing.T) {
	ctx := context.Background()
	s, work, _ := openTeammateStore(t)
	p, _ := s.CreateTeammate(ctx, Teammate{EnvironmentID: work.ID, Name: "Iris", Harness: "claude", GitName: "Iris", GitEmail: "iris@agents.invalid"})
	sb, err := s.CreateSandbox(ctx, Sandbox{EnvironmentID: work.ID, Name: "dev", TeammateID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	chat, err := s.CreateChat(ctx, Chat{EnvironmentID: work.ID, SandboxID: sb.ID, TeammateID: p.ID, Harness: "claude", Heartbeat: true})
	if err != nil {
		t.Fatal(err)
	}

	base := time.Unix(1_800_000_000, 0)
	// A check-in with nothing to do moves the schedule and is not logged.
	if err := s.RecordBeat(ctx, Beat{EnvironmentID: work.ID, TeammateID: p.ID, At: base}); err != nil {
		t.Fatal(err)
	}
	// A started one and a skipped one are logged, the newest first.
	if err := s.RecordBeat(ctx, Beat{EnvironmentID: work.ID, TeammateID: p.ID, At: base.Add(time.Hour), Outcome: BeatStarted, ChatID: chat.ID}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordBeat(ctx, Beat{EnvironmentID: work.ID, TeammateID: p.ID, At: base.Add(2 * time.Hour), Outcome: BeatSkipped, Reason: "no provider"}); err != nil {
		t.Fatal(err)
	}

	got, err := s.Teammate(ctx, work.ID, p.ID)
	if err != nil || !got.HeartbeatAt.Equal(base.Add(2*time.Hour)) {
		t.Fatalf("schedule moved to %v (%v)", got.HeartbeatAt, err)
	}
	beats, err := s.Beats(ctx, work.ID, p.ID, 0)
	if err != nil || len(beats) != 2 {
		t.Fatalf("logged beats: %+v (%v)", beats, err)
	}
	if beats[0].Outcome != BeatSkipped || beats[0].Reason != "no provider" || beats[0].ChatID != "" {
		t.Fatalf("newest beat: %+v", beats[0])
	}
	if beats[1].Outcome != BeatStarted || beats[1].ChatID != chat.ID {
		t.Fatalf("started beat: %+v", beats[1])
	}

	if err := s.RecordBeat(ctx, Beat{EnvironmentID: work.ID, TeammateID: "missing", At: base}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown teammate: %v", err)
	}
}

func TestRunningHeartbeatsCountsOnlyHeartbeatTurns(t *testing.T) {
	ctx := context.Background()
	s, work, _ := openTeammateStore(t)
	p, _ := s.CreateTeammate(ctx, Teammate{EnvironmentID: work.ID, Name: "Iris", Harness: "claude", GitName: "Iris", GitEmail: "iris@agents.invalid"})
	sb, err := s.CreateSandbox(ctx, Sandbox{EnvironmentID: work.ID, Name: "dev", TeammateID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	beat, err := s.CreateChat(ctx, Chat{EnvironmentID: work.ID, SandboxID: sb.ID, TeammateID: p.ID, Harness: "claude", Heartbeat: true})
	if err != nil {
		t.Fatal(err)
	}
	user, err := s.CreateChat(ctx, Chat{EnvironmentID: work.ID, SandboxID: sb.ID, TeammateID: p.ID, Harness: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []Chat{beat, user} {
		if _, err := s.SetChatState(ctx, work.ID, c.ID, ChatRunning, ChatIdle); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := s.RunningHeartbeats(ctx, work.ID, p.ID); err != nil || n != 1 {
		t.Fatalf("running heartbeats: %d (%v)", n, err)
	}
	if c, err := s.Chat(ctx, work.ID, beat.ID); err != nil || !c.Heartbeat {
		t.Fatalf("the heartbeat flag is not stored: %+v (%v)", c, err)
	}
	if c, err := s.Chat(ctx, work.ID, user.ID); err != nil || c.Heartbeat {
		t.Fatalf("a user chat is marked: %+v (%v)", c, err)
	}
}

func TestTeammateSandboxAndPendingApprovals(t *testing.T) {
	ctx := context.Background()
	s, work, _ := openTeammateStore(t)
	p, _ := s.CreateTeammate(ctx, Teammate{EnvironmentID: work.ID, Name: "Iris", Harness: "claude", GitName: "Iris", GitEmail: "iris@agents.invalid"})
	if _, err := s.TeammateSandbox(ctx, work.ID, p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a teammate without sandboxes: %v", err)
	}
	older, err := s.CreateSandbox(ctx, Sandbox{EnvironmentID: work.ID, Name: "older", TeammateID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	newer, err := s.CreateSandbox(ctx, Sandbox{EnvironmentID: work.ID, Name: "newer", TeammateID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	// Sandboxes created in the same second are told apart by their creation time here.
	if _, err := s.db.ExecContext(ctx, "UPDATE sandboxes SET created_at = created_at - 60 WHERE id = ?", older.ID); err != nil {
		t.Fatal(err)
	}
	// A browser VM is never a teammate's sandbox for check-ins, however recent.
	if _, err := s.CreateSandbox(ctx, Sandbox{EnvironmentID: work.ID, Name: "browser", TeammateID: p.ID, Kind: SandboxKindBrowser}); err != nil {
		t.Fatal(err)
	}
	sb, err := s.TeammateSandbox(ctx, work.ID, p.ID)
	if err != nil || sb.ID != newer.ID {
		t.Fatalf("latest sandbox: %+v (%v)", sb, err)
	}

	if n, err := s.PendingApprovals(ctx, work.ID, newer.ID); err != nil || n != 0 {
		t.Fatalf("no approvals yet: %d (%v)", n, err)
	}
	for _, subject := range []string{"github.com:443", "example.com:443"} {
		if _, _, err := s.RequestApproval(ctx, Approval{EnvironmentID: work.ID, SandboxID: newer.ID, Kind: "network.request",
			Subject: subject, Payload: json.RawMessage("{}")}); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := s.PendingApprovals(ctx, work.ID, newer.ID); err != nil || n != 2 {
		t.Fatalf("pending approvals: %d (%v)", n, err)
	}
	if n, err := s.PendingApprovals(ctx, work.ID, older.ID); err != nil || n != 0 {
		t.Fatalf("another sandbox's approvals: %d (%v)", n, err)
	}
}
