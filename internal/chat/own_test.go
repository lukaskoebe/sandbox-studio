package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/acp/acptest"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

const (
	ownStarted = `{"sessionUpdate":"async_task_spawned","asyncTaskId":"autonomous-turn-1","taskType":"turn","name":"Claude is working"}`
	ownText    = `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"The tests passed."}}`
)

func ownEnded(state string) json.RawMessage {
	return json.RawMessage(`{"sessionUpdate":"async_task_state_update","asyncTaskId":"autonomous-turn-1","state":"` + state + `","summary":"API Error: overloaded"}`)
}

// itemsOf are the kinds of the turn's items.
func itemsOf(log Log, turnID string) string {
	var out []Item
	for _, it := range log.Items {
		if it.TurnID == turnID {
			out = append(out, it)
		}
	}
	return kinds(out)
}

// Work the agent starts on its own, which it reports as an async task of the turn type, shows
// in a turn of its own: the chat runs until the task ends, and a message sent meanwhile goes
// next.
func TestWorkTheAgentStartsOnItsOwnHasATurn(t *testing.T) {
	a := &acptest.Agent{Steering: true, Turns: []acptest.Turn{{Steps: steps(working)}, {Steps: steps(noted)}}}
	f := newFixture(t, a)
	first := f.send(t, "work")
	f.wait(t, first.ID)
	a.Notify(json.RawMessage(ownStarted))
	a.Notify(json.RawMessage(ownText))
	log := f.until(t, "the agent's turn", func(l Log) bool {
		return len(l.Turns) == 2 && l.Chat.State == store.ChatRunning && strings.Contains(kinds(l.Items), "agent,agent")
	})
	own := log.Turns[1].ID
	d, err := f.s.Steer(context.Background(), f.sb.EnvironmentID, f.chat.ID, f.launch(), Prompt{Text: "and now"}, nil)
	if err != nil || d.Queued == nil {
		t.Fatalf("Steer = %+v, %v; want it queued behind the agent's turn", d, err)
	}
	a.Notify(ownEnded("completed"))
	log = f.until(t, "the queued message's turn", func(l Log) bool {
		return len(l.Turns) == 3 && l.Turns[2].EndedAt != nil && l.Chat.State == store.ChatIdle
	})
	if got := itemsOf(log, own); got != "agent" || log.Turns[1].StopReason != "end_turn" {
		t.Fatalf("the agent's turn: items %s, stop %q", got, log.Turns[1].StopReason)
	}
	if userTexts(log) != "work|and now" || itemsOf(log, log.Turns[2].ID) != "user,agent" {
		t.Fatalf("users %q, items %s", userTexts(log), kinds(log.Items))
	}
}

// A task that failed ends the agent's turn with what it said; a stop ends it stopped.
func TestWorkTheAgentStartsOnItsOwnEndsAsItsTaskDid(t *testing.T) {
	for _, tc := range []struct{ state, stop, marker string }{
		{"failed", StopInterrupted, MarkerError},
		{"stopped", "cancelled", MarkerStopped},
	} {
		t.Run(tc.state, func(t *testing.T) {
			a := &acptest.Agent{}
			f := newFixture(t, a)
			first := f.send(t, "work")
			f.wait(t, first.ID)
			a.Notify(json.RawMessage(ownStarted))
			a.Notify(json.RawMessage(ownText))
			f.until(t, "the agent's turn", func(l Log) bool { return len(l.Turns) == 2 && l.Chat.State == store.ChatRunning })
			if tc.state == "stopped" {
				if err := f.s.Cancel(context.Background(), f.sb.EnvironmentID, f.chat.ID); err != nil {
					t.Fatal(err)
				}
				if err := a.WaitForCall(context.Background(), "session/cancel"); err != nil {
					t.Fatal(err)
				}
			}
			a.Notify(ownEnded(tc.state))
			log := f.until(t, "the agent's turn ended", func(l Log) bool {
				return len(l.Turns) == 2 && l.Turns[1].EndedAt != nil && l.Chat.State == store.ChatIdle
			})
			if got := itemsOf(log, log.Turns[1].ID); got != "agent,marker:"+tc.marker || log.Turns[1].StopReason != tc.stop {
				t.Fatalf("items %s, stop %q", got, log.Turns[1].StopReason)
			}
			if tc.state == "failed" && !strings.Contains(log.Items[len(log.Items)-1].Marker.Text, "API Error: overloaded") {
				t.Fatalf("marker %+v", log.Items[len(log.Items)-1].Marker)
			}
		})
	}
}

// What the agent writes outside a turn with no task of its own is still dropped.
func TestUpdatesOutsideATurnAreDropped(t *testing.T) {
	a := &acptest.Agent{}
	f := newFixture(t, a)
	first := f.send(t, "work")
	f.wait(t, first.ID)
	a.Notify(json.RawMessage(ownText))
	second := f.send(t, "next")
	_, log := f.wait(t, second.ID)
	if len(log.Turns) != 2 || kinds(log.Items) != "user,user" {
		t.Fatalf("%d turns, items %s", len(log.Turns), kinds(log.Items))
	}
}
