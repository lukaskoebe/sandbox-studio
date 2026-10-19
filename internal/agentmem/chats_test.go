package agentmem

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/acp"
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
	start := time.Now().Add(time.Hour) // the store's times are the wall clock's; the chat is idle by now
	f.now = start
	c, _ := f.st.CreateChat(f.ctx, store.Chat{EnvironmentID: f.env, SandboxID: f.sbAda.ID, TeammateID: f.ada.ID, Harness: "claude"})
	f.turn(c, "hi", "Hello!")
	if err := f.svc.extractChat(f.ctx, chatDue{env: f.env, chat: c.ID}); err != nil || f.llm.n() != 0 {
		t.Fatalf("short chat: %v, %d calls", err, f.llm.n())
	}
	if seq, _ := f.st.ChatMemorySeq(f.ctx, c.ID); seq != 0 {
		t.Fatalf("cursor moved past a short user turn: %d", seq)
	}
	// The sweep leaves it for chatShortWait, then looks again.
	f.now = start.Add(time.Hour)
	if due := f.svc.sweepChats(f.ctx); len(due) != 0 {
		t.Fatalf("swept a short chat at once: %+v", due)
	}
	f.now = start.Add(chatShortWait)
	if due := f.svc.sweepChats(f.ctx); len(due) != 1 || due[0].chat != c.ID {
		t.Fatalf("swept %+v", due)
	}
	// With more conversation it is extracted, under chat-<id>.
	f.turn(c, chatUser, "Sure.")
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
	if due := f.svc.sweepChats(f.ctx); len(due) != 0 {
		t.Fatalf("still to remember: %+v", due)
	}
}

// A failed call leaves the cursor where it was: the sweep tries again after chatRetry, then four
// times as long, and gives up after maxChatAttempts.
func TestChatExtractionRetries(t *testing.T) {
	f := newFixture(t)
	start := time.Now().Add(time.Hour)
	f.now = start
	c := f.newChat("acp-1")
	f.llm.err = errors.New("overloaded")
	swept := func(want bool) {
		t.Helper()
		due := f.svc.sweepChats(f.ctx)
		if got := len(due) == 1 && due[0].chat == c.ID; got != want || len(due) > 1 {
			t.Fatalf("at +%s swept %+v", f.now.Sub(start), due)
		}
	}
	swept(true)
	if err := f.svc.extractChat(f.ctx, chatDue{env: f.env, chat: c.ID}); err == nil || !strings.Contains(err.Error(), "attempt 1 of 3") {
		t.Fatalf("first attempt: %v", err)
	}
	if seq, _ := f.st.ChatMemorySeq(f.ctx, c.ID); seq != 0 {
		t.Fatalf("the cursor moved on a failed call: %d", seq)
	}
	swept(false)
	f.now = start.Add(chatRetry)
	swept(true)
	if err := f.svc.extractChat(f.ctx, chatDue{env: f.env, chat: c.ID}); err == nil {
		t.Fatal("second attempt passed")
	}
	f.now = start.Add(chatRetry + 3*chatRetry)
	swept(false)
	f.now = start.Add(chatRetry + 4*chatRetry)
	swept(true)
	if err := f.svc.extractChat(f.ctx, chatDue{env: f.env, chat: c.ID}); err == nil || !strings.Contains(err.Error(), "given up after 3 attempts") {
		t.Fatalf("third attempt: %v", err)
	}
	if seq, _ := f.st.ChatMemorySeq(f.ctx, c.ID); seq == 0 {
		t.Fatal("the cursor stayed after the last attempt")
	}
	f.now = f.now.Add(24 * time.Hour)
	swept(false)
	if f.llm.n() != 3 {
		t.Fatalf("%d calls", f.llm.n())
	}

	// Once the cursor moves, the count starts over.
	f.turn(c, strings.Repeat("The staging database needs the VPN. ", 10), "Noted.")
	if err := f.svc.extractChat(f.ctx, chatDue{env: f.env, chat: c.ID}); err == nil || !strings.Contains(err.Error(), "attempt 1 of 3") {
		t.Fatalf("after giving up: %v", err)
	}
	f.llm.err = nil
	if err := f.svc.extractChat(f.ctx, chatDue{env: f.env, chat: c.ID}); err != nil {
		t.Fatal(err)
	}
	swept(false)
}

// A chat over the daily budget waits for the next day's, without counting as a failure.
func TestChatExtractionWaitsForTheBudget(t *testing.T) {
	f := newFixture(t)
	start := time.Now().Add(time.Hour)
	f.now = start
	c := f.newChat("acp-1")
	f.svc.Budget = Budget{}
	if err := f.svc.extractChat(f.ctx, chatDue{env: f.env, chat: c.ID}); err != nil || f.llm.n() != 0 {
		t.Fatalf("over budget: %v, %d calls", err, f.llm.n())
	}
	y, m, d := start.UTC().Date()
	tomorrow := time.Date(y, m, d+1, 0, 0, 0, 0, time.UTC)
	f.now = tomorrow.Add(-time.Second)
	if due := f.svc.sweepChats(f.ctx); len(due) != 0 {
		t.Fatalf("swept on the same day: %+v", due)
	}
	f.now = tomorrow
	if due := f.svc.sweepChats(f.ctx); len(due) != 1 {
		t.Fatalf("not swept the next day: %+v", due)
	}
	f.svc.Budget = DefaultBudget
	if err := f.svc.extractChat(f.ctx, chatDue{env: f.env, chat: c.ID}); err != nil || f.llm.n() != 1 {
		t.Fatalf("the next day: %v, %d calls", err, f.llm.n())
	}
}

// A call the host has no memory for is no call: it isn't charged and waits for the next sweep.
func TestChatExtractionWaitsForMemory(t *testing.T) {
	f := newFixture(t)
	start := time.Now().Add(time.Hour)
	f.now = start
	c := f.newChat("acp-1")
	f.llm.err = ErrLowMemory
	if err := f.svc.extractChat(f.ctx, chatDue{env: f.env, chat: c.ID}); err != nil {
		t.Fatal(err)
	}
	if u, _ := f.svc.usage(f.ctx, f.env); u.Calls != 0 {
		t.Fatalf("charged %+v", u)
	}
	if due := f.svc.sweepChats(f.ctx); len(due) != 0 {
		t.Fatalf("swept at once: %+v", due)
	}
	f.now = start.Add(chatSweep)
	if due := f.svc.sweepChats(f.ctx); len(due) != 1 {
		t.Fatalf("not swept after chatSweep: %+v", due)
	}
	f.llm.err = nil
	if err := f.svc.extractChat(f.ctx, chatDue{env: f.env, chat: c.ID}); err != nil {
		t.Fatal(err)
	}
	if seq, _ := f.st.ChatMemorySeq(f.ctx, c.ID); seq == 0 {
		t.Fatal("the cursor stayed")
	}
}

// The sweep reads a few chats at a time, the oldest first, and only idle ones.
func TestSweepChatsInBatches(t *testing.T) {
	f := newFixture(t)
	var ids []string
	for i := range chatSweepBatch + 2 {
		ids = append(ids, f.newChat(fmt.Sprintf("acp-%d", i)).ID)
		time.Sleep(1100 * time.Millisecond) // updated_at is in seconds
	}
	f.now = time.Now()
	if due := f.svc.sweepChats(f.ctx); len(due) != 0 {
		t.Fatalf("swept chats idle for less than ChatIdle: %+v", due)
	}
	f.now = time.Now().Add(time.Hour)
	due := f.svc.sweepChats(f.ctx)
	if len(due) != chatSweepBatch {
		t.Fatalf("swept %d chats", len(due))
	}
	for i, d := range due {
		if d.chat != ids[i] {
			t.Fatalf("swept out of order: %+v", due)
		}
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
	// Wide margins: the second call must land inside the window even on a loaded machine.
	f.svc.ChatIdle = 300 * time.Millisecond
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
	case <-time.After(400 * time.Millisecond):
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

// A choice for the task at hand never becomes a preference or decision, however the model
// labels it: an answer to the agent's question, or a choice said for now, is kept only as an
// event. A rule the user states, or a project decision, stays.
func TestOneOffChoicesAreNoPreferences(t *testing.T) {
	transcript := `assistant: The login test is flaky. Should I fix it now or defer the fix?
user: defer
assistant: [question] Which database should the api use?
user: [answer] {"db":"postgres"}
assistant: Which package manager?
user: use npm this time
assistant: Should I run the tests before pushing?
user: always run the tests before you push, in every repo
user: skip the tests for now, just push it
user: I prefer small commits with one-line messages
`
	fact := func(kind, reach, text, evidence, replyTo string, entities ...string) string {
		b, _ := json.Marshal(map[string]any{"text": text, "kind": kind, "tier": "user", "reach": reach,
			"entities": append([]string{}, entities...), "attribute": "", "evidence": evidence, "reply_to": replyTo})
		return string(b)
	}
	for _, tc := range []struct {
		name, fact string
		kept       bool
	}{
		{"an answer labeled general", fact("preference", "general", "The user prefers deferring fixes.", "defer", "Should I fix it now or defer the fix?"), false},
		{"an answer without reply_to", fact("preference", "general", "The user prefers deferring fixes.", "defer", ""), false},
		{"an answer labeled task", fact("decision", "task", "Defer the flaky test fix.", "defer", "Should I fix it now or defer the fix?"), false},
		{"the same answer as an event", fact("event", "task", "The user chose to defer the fix of the flaky login test.", "defer", "Should I fix it now or defer the fix?", "api"), true},
		{"said for this time", fact("preference", "general", "The user prefers npm.", "use npm this time", ""), false},
		{"said for now", fact("decision", "general", "Skip tests before pushing.", "skip the tests for now, just push it", ""), false},
		{"an answer about a project", fact("decision", "general", "The api uses Postgres.", `{"db":"postgres"}`, "Which database should the api use?", "api"), true},
		{"an answer stated as a rule", fact("preference", "general", "Always run the tests before pushing.", "always run the tests before you push, in every repo", "Should I run the tests before pushing?"), true},
		{"a stated preference", fact("preference", "general", "The user prefers small commits with one-line messages.", "I prefer small commits with one-line messages", ""), true},
		{"no reach from an older model", fact("preference", "", "The user prefers small commits with one-line messages.", "I prefer small commits with one-line messages", ""), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			facts, _, err := parseFacts(`{"facts": [`+tc.fact+`], "summary": ""}`, transcript)
			if err != nil {
				t.Fatal(err)
			}
			if kept := len(facts) == 1; kept != tc.kept {
				t.Fatalf("kept %v, want %v: %+v", kept, tc.kept, facts)
			}
		})
	}
	if _, _, err := parseFacts(`{"facts": [`+fact("fact", "everywhere", "x", "defer", "")+`], "summary": ""}`, transcript); err == nil {
		t.Fatal("an unknown reach was accepted")
	}
}

// The agent's questions and permission asks reach the extraction with the user's answers.
func TestChatTranscriptKeepsQuestionsWithTheirAnswers(t *testing.T) {
	item := func(kind string, body any) store.ChatItem {
		b, _ := json.Marshal(body)
		return store.ChatItem{Kind: kind, Body: b}
	}
	got := chatTranscript([]store.ChatItem{
		item(chat.KindQuestion, chat.QuestionBody{Message: "Fix it now or later?", Action: acp.ElicitAccept, Answer: json.RawMessage(`{"when":"later"}`)}),
		item(chat.KindPermission, chat.PermissionBody{ToolCall: acp.ToolCallUpdate{Title: "git push"},
			Options: []acp.PermissionOption{{OptionID: "a", Name: "Allow once"}, {OptionID: "r", Name: "Reject"}}, Outcome: "a"}),
		item(chat.KindQuestion, chat.QuestionBody{Message: "Still waiting?"}),
	})
	want := "assistant: [question] Fix it now or later?\nuser: [answer] {\"when\":\"later\"}\nassistant: [asks to run] git push\nuser: [chose] Allow once\nassistant: [question] Still waiting?\n"
	if got != want {
		t.Fatalf("transcript\n%s\nwant\n%s", got, want)
	}
}
