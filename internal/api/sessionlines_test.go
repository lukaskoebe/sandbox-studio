package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func TestSessionLinesHaveTurnsSpansAndEvents(t *testing.T) {
	ctx := context.Background()
	h, s := newTestServer(t)
	env, err := s.Store.CreateEnvironment(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	sb, err := s.Store.CreateSandbox(ctx, store.Sandbox{EnvironmentID: env.ID, Name: "web"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Store.CreateChat(ctx, store.Chat{EnvironmentID: env.ID, SandboxID: sb.ID, TeammateID: "ada", Harness: "claude", Title: "Fix checkout"})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := s.Store.StartChatTurn(ctx, c.ID, "chat", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.EndChatTurn(ctx, c.ID, turn.ID, "end_turn", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.StartChatTurn(ctx, c.ID, "chat", ""); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err := s.Store.SyncSessionSpans(ctx, store.SpanScope{EnvironmentID: env.ID, SandboxID: sb.ID, Kind: store.SpanPreview},
		[]store.SessionSpan{{Key: "5173", Label: ":5173"}}, now); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.RecordSandboxEvent(ctx, store.SandboxEvent{EnvironmentID: env.ID, SandboxID: sb.ID, Kind: store.EventCheckpoint, Name: "web", CheckpointName: "before"}); err != nil {
		t.Fatal(err)
	}
	// A turn and a span from before the window are left out.
	if _, err := s.Store.SyncSessionSpans(ctx, store.SpanScope{EnvironmentID: env.ID, SandboxID: sb.ID, Kind: store.SpanTerminal},
		[]store.SessionSpan{{Key: "old@1", OpenedAt: now.Add(-5 * time.Hour)}}, now.Add(-5*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.SyncSessionSpans(ctx, store.SpanScope{EnvironmentID: env.ID, SandboxID: sb.ID, Kind: store.SpanTerminal}, nil, now.Add(-4*time.Hour)); err != nil {
		t.Fatal(err)
	}

	rec := do(h, http.MethodGet, testOrigin+"/api/environments/"+env.ID+"/sessions?hours=2", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("sessions: %d %s", rec.Code, rec.Body)
	}
	lines := decode[SessionLines](t, rec)
	if len(lines.Turns) != 2 || lines.Turns[0].Title != "Fix checkout" || lines.Turns[0].EndedAt == nil || lines.Turns[1].EndedAt != nil ||
		lines.Turns[0].SandboxID != sb.ID || lines.Turns[0].TeammateID != "ada" {
		t.Errorf("turns = %+v, want the ended turn and the running one", lines.Turns)
	}
	if len(lines.Spans) != 1 || lines.Spans[0].Kind != store.SpanPreview || lines.Spans[0].ClosedAt != nil {
		t.Errorf("spans = %+v, want the open preview alone", lines.Spans)
	}
	if len(lines.Events) != 1 || lines.Events[0].Kind != store.EventCheckpoint {
		t.Errorf("events = %+v", lines.Events)
	}
	if rec := do(h, http.MethodGet, testOrigin+"/api/environments/nope/sessions", ""); rec.Code != http.StatusNotFound {
		t.Errorf("an unknown environment: %d", rec.Code)
	}
}
