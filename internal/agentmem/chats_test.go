package agentmem

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agenthook"
	"github.com/lukaskoebe/sandbox-studio/internal/chat"
	"github.com/lukaskoebe/sandbox-studio/internal/memory"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

const chatUser = "Please always run the linter with golangci-lint before committing. The release checklist lives in docs/releasing.md and we tag from main only, never from a branch."

// newChat is a chat of ada's sandbox with its harness session and one finished turn.
func (f *fixture) newChat(session string) store.Chat {
	f.t.Helper()
	c, err := f.st.CreateChat(f.ctx, store.Chat{EnvironmentID: f.env, SandboxID: f.sbAda.ID, TeammateID: f.ada.ID, Harness: "claude"})
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.st.SetChatSession(f.ctx, c.ID, session); err != nil {
		f.t.Fatal(err)
	}
	f.turn(c, chatUser, "Understood. I ran the linter and it passed; tagging stays on main.")
	return c
}

func (f *fixture) turn(c store.Chat, user, agent string) {
	f.t.Helper()
	t, err := f.st.StartChatTurn(f.ctx, c.ID, "chat", "")
	if err != nil {
		f.t.Fatal(err)
	}
	add := func(kind string, body any) {
		b, _ := json.Marshal(body)
		if _, err := f.st.AddChatItem(f.ctx, store.ChatItem{ChatID: c.ID, TurnID: t.ID, Kind: kind, Body: b}); err != nil {
			f.t.Fatal(err)
		}
	}
	add(chat.KindUser, chat.UserBody{Text: user})
	add(chat.KindThought, chat.TextBody{Text: "secret thinking"})
	add(chat.KindTool, chat.ToolBody{Title: "golangci-lint run ./...", Status: "completed"})
	add(chat.KindAgent, chat.TextBody{Text: agent})
	if _, err := f.st.EndChatTurn(f.ctx, c.ID, t.ID, "end_turn", nil); err != nil {
		f.t.Fatal(err)
	}
}

func TestChatExtraction(t *testing.T) {
	f := newFixture(t)
	c := f.newChat("acp-1")
	f.llm.reply = `{"facts": [{"text": "Run golangci-lint before committing", "kind": "preference", "tier": "user", "entities": [], "attribute": "", "evidence": "always run the linter with golangci-lint"}],
		"summary": "Set up linting for the api repo; the linter passes and releases are tagged from main."}`
	if err := f.svc.extractChat(f.ctx, chatDue{env: f.env, chat: c.ID}); err != nil {
		t.Fatal(err)
	}
	if f.llm.n() != 1 {
		t.Fatalf("llm calls: %d", f.llm.n())
	}
	user := f.llm.calls[0].User
	for _, want := range []string{"user: Please always run", "assistant: [tool] golangci-lint run ./...", "assistant: Understood."} {
		if !strings.Contains(user, want) {
			t.Errorf("transcript lacks %q:\n%s", want, user)
		}
	}
	if strings.Contains(user, "secret thinking") || strings.Contains(user, "Summary of the session") {
		t.Errorf("transcript:\n%s", user)
	}
	facts, _ := f.mem.Facts(f.ctx, f.env, memory.FactFilter{Scope: memory.TeammateScope(f.ada.ID)})
	if len(facts) != 1 || facts[0].Tier != memory.TierUser {
		t.Fatalf("facts %+v", facts)
	}
	// The session is the chat's harness session, with the summary.
	sessions, _ := f.svc.Sessions(f.ctx, f.env, f.ada.ID, 10)
	if len(sessions) != 1 || sessions[0].SessionRef != "acp-1" || !strings.Contains(sessions[0].Summary, "linting") || sessions[0].SummaryAt == nil {
		t.Fatalf("sessions %+v", sessions)
	}

	// Nothing new: no call.
	if err := f.svc.extractChat(f.ctx, chatDue{env: f.env, chat: c.ID}); err != nil || f.llm.n() != 1 {
		t.Fatalf("re-extracted: %v, %d calls", err, f.llm.n())
	}
	// A new turn is read with the previous summary, and only the new turn.
	f.turn(c, "Also, the staging database is at db.staging.acme.internal and needs the VPN for every connection we make to it, from any sandbox.", "Noted: the staging database needs the VPN.")
	if err := f.svc.extractChat(f.ctx, chatDue{env: f.env, chat: c.ID}); err != nil || f.llm.n() != 2 {
		t.Fatalf("second turn: %v, %d calls", err, f.llm.n())
	}
	second := f.llm.calls[1].User
	if !strings.Contains(second, "Summary of the session before this excerpt:\n\nSet up linting") || strings.Contains(second, "Please always run") {
		t.Fatalf("second excerpt:\n%s", second)
	}
	// A running chat waits for its turn to end.
	f.turn(c, strings.Repeat("More context about the deploy pipeline. ", 20), "Ok.")
	if _, err := f.st.SetChatState(f.ctx, f.env, c.ID, store.ChatRunning); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.extractChat(f.ctx, chatDue{env: f.env, chat: c.ID}); err != nil || f.llm.n() != 2 {
		t.Fatalf("running chat extracted: %v, %d calls", err, f.llm.n())
	}
}

func TestChatExtractionWaitsForMore(t *testing.T) {
	f := newFixture(t)
	c, _ := f.st.CreateChat(f.ctx, store.Chat{EnvironmentID: f.env, SandboxID: f.sbAda.ID, TeammateID: f.ada.ID, Harness: "claude"})
	f.turn(c, "hi", "Hello!")
	if err := f.svc.extractChat(f.ctx, chatDue{env: f.env, chat: c.ID}); err != nil || f.llm.n() != 0 {
		t.Fatalf("short chat: %v, %d calls", err, f.llm.n())
	}
	if seq, _ := f.st.ChatMemorySeq(f.ctx, c.ID); seq != 0 {
		t.Fatalf("cursor moved past a short user turn: %d", seq)
	}
	// The startup scan finds it; with more conversation it is extracted, under chat-<id>.
	f.turn(c, chatUser, "Sure.")
	list, err := f.st.ChatsToRemember(f.ctx, f.now.Add(-time.Hour))
	if err != nil || len(list) != 1 || list[0].ID != c.ID {
		t.Fatalf("to remember: %v %+v", err, list)
	}
	if err := f.svc.extractChat(f.ctx, chatDue{env: f.env, chat: c.ID}); err != nil || f.llm.n() != 1 {
		t.Fatalf("longer chat: %v, %d calls", err, f.llm.n())
	}
	if !strings.Contains(f.llm.calls[0].User, "user: hi") {
		t.Fatalf("the short turn was not kept:\n%s", f.llm.calls[0].User)
	}
	sessions, _ := f.svc.Sessions(f.ctx, f.env, f.ada.ID, 10)
	if len(sessions) != 1 || sessions[0].SessionRef != "chat-"+c.ID {
		t.Fatalf("sessions %+v", sessions)
	}
	if list, _ := f.st.ChatsToRemember(f.ctx, f.now.Add(-time.Hour)); len(list) != 0 {
		t.Fatalf("still to remember: %+v", list)
	}
}

func TestChatSessionHooksDontExtract(t *testing.T) {
	f := newFixture(t)
	f.newChat("acp-1")
	f.hook(f.sbAda.ID, map[string]any{"event": agenthook.Stop, "sessionId": "acp-1", "transcript": transcript})
	select {
	case <-f.svc.jobs:
		t.Fatal("a chat's hook queued an extraction")
	default:
	}
	// A terminal session in the same sandbox still does.
	f.hook(f.sbAda.ID, map[string]any{"event": agenthook.Stop, "sessionId": "tui-1", "transcript": transcript})
	select {
	case <-f.svc.jobs:
	default:
		t.Fatal("a terminal session's hook queued nothing")
	}
}

func TestChatIdledDebounces(t *testing.T) {
	f := newFixture(t)
	f.svc.ChatIdle = 30 * time.Millisecond
	f.svc.ChatIdled(f.env, "c1")
	time.Sleep(10 * time.Millisecond)
	f.svc.ChatIdled(f.env, "c1") // moves the time out; one extraction
	select {
	case d := <-f.svc.chatsDue:
		if d.chat != "c1" || d.env != f.env {
			t.Fatalf("due %+v", d)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("never due")
	}
	select {
	case d := <-f.svc.chatsDue:
		t.Fatalf("due twice: %+v", d)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestRecentSessionsInPack(t *testing.T) {
	f := newFixture(t)
	c := f.newChat("acp-1")
	f.llm.reply = `{"facts": [], "summary": "Fixed the flaky login test in the api repo; the fix is on branch fix-login, not pushed."}`
	if err := f.svc.extractChat(f.ctx, chatDue{env: f.env, chat: c.ID}); err != nil {
		t.Fatal(err)
	}
	r := f.hook(f.sbAda.ID, map[string]any{"event": agenthook.SessionStart, "sessionId": "s2"})
	for _, want := range []string{"## Your recent sessions", "sandbox ada-dev (this one): Fixed the flaky login test", "2026-10-10"} {
		if !strings.Contains(r.Context, want) {
			t.Errorf("pack lacks %q:\n%s", want, r.Context)
		}
	}
	// The session's own summary is not news to it, and bob doesn't see ada's.
	if r := f.hook(f.sbAda.ID, map[string]any{"event": agenthook.SessionStart, "sessionId": "acp-1"}); strings.Contains(r.Context, "flaky login") {
		t.Fatalf("own summary in pack:\n%s", r.Context)
	}
	if r := f.hook(f.sbBob.ID, map[string]any{"event": agenthook.SessionStart, "sessionId": "b1"}); strings.Contains(r.Context, "flaky login") {
		t.Fatalf("bob sees ada's session:\n%s", r.Context)
	}
}

func TestSummaryParsing(t *testing.T) {
	_, summary, err := parseFacts(`{"facts": [], "summary": "  Worked on\n the   api. "}`, "")
	if err != nil || summary != "Worked on the api." {
		t.Fatalf("summary %q, %v", summary, err)
	}
	_, summary, err = parseFacts(`{"facts": [], "summary": "Used key sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123456789"}`, "")
	if err != nil || summary != "" {
		t.Fatalf("secret-like summary kept: %q, %v", summary, err)
	}
	_, summary, _ = parseFacts(`{"facts": [], "summary": "`+strings.Repeat("a", 2000)+`"}`, "")
	if n := len([]rune(summary)); n > maxSummary {
		t.Fatalf("summary of %d characters", n)
	}
	// Answers without a summary still parse.
	if _, summary, err := parseFacts(`{"facts": []}`, ""); err != nil || summary != "" {
		t.Fatalf("no summary: %q, %v", summary, err)
	}
}
