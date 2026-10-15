package browser

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// chat makes a chat of sb in state.
func (f *fixture) chat(sb store.Sandbox, state string) store.Chat {
	f.t.Helper()
	c, err := f.st.CreateChat(f.ctx, store.Chat{EnvironmentID: sb.EnvironmentID, SandboxID: sb.ID, TeammateID: sb.TeammateID, Harness: "claude"})
	if err != nil {
		f.t.Fatal(err)
	}
	if state != store.ChatIdle {
		if c, err = f.st.SetChatState(f.ctx, sb.EnvironmentID, c.ID, state); err != nil {
			f.t.Fatal(err)
		}
	}
	return c
}

// never is a Stopped for sandboxes that all keep running.
func never(context.Context, string, string) (bool, error) { return false, nil }

func (f *fixture) closeDetail(session string) string {
	f.t.Helper()
	list, _ := f.svc.Actions(f.ctx, f.env, f.ada.ID, session, 10)
	if len(list) == 0 || list[0].Action != "close" {
		return ""
	}
	return list[0].Detail
}

// A session opened during a chat's turn belongs to that chat and ends when the chat is
// deleted; a session opened when no chat, or more than one, ran belongs to its sandbox only.
func TestEphemeralSessionEndsWithItsChat(t *testing.T) {
	f := newFixture(t)
	// A teammate has at most two sessions, so the one opened without a chat is bob's.
	bobChat := f.chat(f.sbBob, store.ChatIdle)
	noChat := f.openEphemeral(f.sbBob, "https://example.com/none")
	running := f.chat(f.sbAda, store.ChatRunning)
	idle := f.chat(f.sbAda, store.ChatIdle)
	owned := f.openEphemeral(f.sbAda, "https://example.com/chat")
	other := f.chat(f.sbAda, store.ChatRunning)
	ambiguous := f.openEphemeral(f.sbAda, "https://example.com/two")
	if _, err := f.st.SetChatState(f.ctx, f.env, other.ID, store.ChatIdle); err != nil {
		t.Fatal(err)
	}
	sessions := func() []string { return append(f.sessionIDs(f.ada), f.sessionIDs(f.bob)...) }

	// Another chat's end, or an event for a live chat, ends nothing.
	for _, c := range []store.Chat{idle, bobChat} {
		if err := f.st.DeleteChat(f.ctx, f.env, c.ID); err != nil {
			t.Fatal(err)
		}
		f.svc.checkOwners(f.ctx, events.Event{Topic: events.TopicChats, EnvironmentID: f.env, ID: c.ID}, never)
	}
	f.svc.checkOwners(f.ctx, events.Event{Topic: events.TopicChats, EnvironmentID: f.env, ID: running.ID}, never)
	if ids := sessions(); len(ids) != 3 {
		t.Fatalf("sessions = %v, want all three", ids)
	}

	if err := f.st.DeleteChat(f.ctx, f.env, running.ID); err != nil {
		t.Fatal(err)
	}
	f.svc.checkOwners(f.ctx, events.Event{Topic: events.TopicChats, EnvironmentID: f.env, ID: running.ID}, never)
	ids := sessions()
	if slices.Contains(ids, owned) || !slices.Contains(ids, noChat) || !slices.Contains(ids, ambiguous) {
		t.Fatalf("sessions after the chat was deleted: %v; want %s gone, %s and %s kept", ids, owned, noChat, ambiguous)
	}
	if d := f.closeDetail(owned); d != whyChatEnded {
		t.Errorf("action log: %q", d)
	}
	if _, err := f.call(f.sbAda, "browser_snapshot", `{"session":"`+owned+`"}`); err == nil {
		t.Error("a session of a deleted chat still answers")
	}
}

// A chat whose agent session is gone for good (orphaned) ends its sessions too, also when
// only the periodic sweep sees it.
func TestEphemeralSessionEndsWithOrphanedChat(t *testing.T) {
	f := newFixture(t)
	c := f.chat(f.sbAda, store.ChatRunning)
	id := f.openEphemeral(f.sbAda, "https://example.com/a")
	if _, err := f.st.SetChatState(f.ctx, f.env, c.ID, store.ChatOrphaned); err != nil {
		t.Fatal(err)
	}
	f.svc.endIdleEphemeral(f.ctx, time.Hour)
	if ids := f.sessionIDs(f.ada); len(ids) != 0 {
		t.Fatalf("sessions after the chat was orphaned: %v", ids)
	}
	if d := f.closeDetail(id); d != whyChatEnded {
		t.Errorf("action log: %q", d)
	}
}

// When the sandbox that opened a session stops, its sessions end, whatever chat opened them;
// another sandbox's stay. A session the user drives stays.
func TestEphemeralSessionsEndWhenTheirSandboxStops(t *testing.T) {
	f := newFixture(t)
	f.chat(f.sbAda, store.ChatRunning)
	mine := f.openEphemeral(f.sbAda, "https://example.com/a")
	driven := f.openEphemeral(f.sbAda, "https://example.com/b")
	theirs := f.openEphemeral(f.sbBob, "https://example.com/c")
	if err := f.svc.SetTakeover(f.ctx, f.env, f.ada.ID, driven, true); err != nil {
		t.Fatal(err)
	}
	stopped := func(_ context.Context, envID, id string) (bool, error) {
		return envID == f.env && id == f.sbAda.ID, nil
	}
	sessions := func() []string { return append(f.sessionIDs(f.ada), f.sessionIDs(f.bob)...) }
	// An event of the other sandbox checks only that one.
	f.svc.checkOwners(f.ctx, events.Event{Topic: events.TopicSandboxes, EnvironmentID: f.env, ID: f.sbBob.ID}, stopped)
	if ids := sessions(); len(ids) != 3 {
		t.Fatalf("sessions = %v, want all three", ids)
	}
	f.svc.checkOwners(f.ctx, events.Event{Topic: events.TopicSandboxes, EnvironmentID: f.env, ID: f.sbAda.ID}, stopped)
	ids := sessions()
	if slices.Contains(ids, mine) || !slices.Contains(ids, driven) || !slices.Contains(ids, theirs) {
		t.Fatalf("sessions after the sandbox stopped: %v; want %s gone, %s and %s kept", ids, mine, driven, theirs)
	}
	if d := f.closeDetail(mine); d != whySandboxStopped {
		t.Errorf("action log: %q", d)
	}
}

// FollowOwners acts on the bus's events, and checks every session when it subscribes.
func TestFollowOwnersEndsSessionsOnEvents(t *testing.T) {
	f := newFixture(t)
	c := f.chat(f.sbAda, store.ChatRunning)
	id := f.openEphemeral(f.sbAda, "https://example.com/a")
	bus := &events.Bus{}
	ctx, cancel := context.WithCancel(f.ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.svc.FollowOwners(ctx, bus, never)
	}()
	defer func() { cancel(); <-done }()

	if err := f.st.DeleteChat(f.ctx, f.env, c.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for slices.Contains(f.sessionIDs(f.ada), id) {
		if time.Now().After(deadline) {
			t.Fatal("the session outlived its chat")
		}
		bus.Publish(events.Event{Topic: events.TopicChats, EnvironmentID: f.env, ID: c.ID})
		time.Sleep(10 * time.Millisecond)
	}
	if d := f.closeDetail(id); !strings.Contains(d, "chat") {
		t.Errorf("action log: %q", d)
	}
}
