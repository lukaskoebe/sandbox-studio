package chat

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/acp/acptest"
)

const (
	working = `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Working"}}`
	noted   = `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Noted"}}`
)

// until waits for the chat's log to satisfy ok.
func (f *fixture) until(t *testing.T, what string, ok func(Log) bool) Log {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		log, err := f.s.Get(context.Background(), f.sb.EnvironmentID, f.chat.ID)
		if err != nil {
			t.Fatal(err)
		}
		if ok(log) {
			return log
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("never: %s", what)
	return Log{}
}

func userTexts(log Log) string {
	var out []string
	for _, it := range log.Items {
		if it.User != nil {
			out = append(out, it.User.Text)
		}
	}
	return strings.Join(out, "|")
}

func steeredTexts(a *acptest.Agent) string {
	var out []string
	for _, p := range a.Steered() {
		var blocks []struct{ Text string }
		_ = json.Unmarshal(p, &blocks)
		for _, b := range blocks {
			out = append(out, b.Text)
		}
	}
	return strings.Join(out, "|")
}

// steer sends text while the first turn runs, once the agent wrote into it.
func (f *fixture) steer(t *testing.T, text string) Delivery {
	t.Helper()
	f.until(t, "the agent wrote", func(l Log) bool { return strings.Contains(kinds(l.Items), "agent") })
	d, err := f.s.Steer(context.Background(), f.sb.EnvironmentID, f.chat.ID, f.launch(), Prompt{Text: text}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// A message sent while a turn runs goes into that turn with _session/steering, and shows in
// it where the agent took it.
func TestSteeringGoesIntoTheRunningTurn(t *testing.T) {
	a := &acptest.Agent{Steering: true, Turns: []acptest.Turn{{Steps: []acptest.Step{
		{Update: json.RawMessage(working)}, {WaitSteer: true}, {Update: json.RawMessage(noted)},
	}}, {}}}
	f := newFixture(t, a)
	first := f.send(t, "work")
	if d := f.steer(t, "use tabs"); !d.Steered || d.Queued != nil {
		t.Fatalf("Steer = %+v; want steered", d)
	}
	_, log := f.wait(t, first.ID)
	if len(log.Turns) != 1 || kinds(log.Items) != "user,agent,user,agent" || userTexts(log) != "work|use tabs" {
		t.Fatalf("%d turns, items %s, users %q; want one turn with the steered message after Working", len(log.Turns), kinds(log.Items), userTexts(log))
	}
	if got := steeredTexts(a); got != "use tabs" {
		t.Fatalf("steered %q", got)
	}
	if n := strings.Count(strings.Join(a.Called(), ","), "session/prompt"); n != 1 {
		t.Fatalf("%d prompts; want the steered message to go as steering only", n)
	}

	// An idle chat starts a turn with it.
	if d, err := f.s.Steer(context.Background(), f.sb.EnvironmentID, f.chat.ID, f.launch(), Prompt{Text: "next"}, nil); err != nil || d.Steered || d.Queued != nil {
		t.Fatalf("Steer on an idle chat = %+v, %v; want a new turn", d, err)
	}
	f.until(t, "the second turn", func(l Log) bool { return len(l.Turns) == 2 && strings.HasSuffix(userTexts(l), "|next") })
}

// OpenCode takes a session/prompt sent while a turn runs into that turn; the turn ends once
// both are answered.
func TestSteeringJoinsTheRunningTurn(t *testing.T) {
	a := &acptest.Agent{Joins: true, Turns: []acptest.Turn{{Steps: []acptest.Step{
		{Update: json.RawMessage(working)}, {WaitSteer: true}, {Pause: 50 * time.Millisecond}, {Update: json.RawMessage(noted)},
	}}}}
	f := newFixture(t, a)
	first := f.send(t, "work")
	if d := f.steer(t, "use tabs"); !d.Steered {
		t.Fatalf("Steer = %+v; want steered", d)
	}
	_, log := f.wait(t, first.ID)
	if len(log.Turns) != 1 || kinds(log.Items) != "user,agent,user,agent" || userTexts(log) != "work|use tabs" {
		t.Fatalf("%d turns, items %s, users %q; want one turn with the joined message", len(log.Turns), kinds(log.Items), userTexts(log))
	}
	if got := steeredTexts(a); got != "use tabs" {
		t.Fatalf("joined %q", got)
	}
	if log.Turns[0].StopReason != "end_turn" {
		t.Fatalf("stop reason %q", log.Turns[0].StopReason)
	}
}

// A message steered once the agent's turn ended, before Studio heard so, goes as the next turn.
func TestSteeringAfterTheAgentsTurnGoesNext(t *testing.T) {
	release := make(chan struct{})
	var prompts atomic.Int32
	a := &acptest.Agent{Steering: true, Turns: []acptest.Turn{{}, {}}, BeforePrompt: func() {
		if prompts.Add(1) == 1 {
			<-release
		}
	}}
	f := newFixture(t, a)
	first := f.send(t, "work")
	if err := a.WaitForCall(context.Background(), "session/prompt"); err != nil {
		t.Fatal(err)
	}
	d, err := f.s.Steer(context.Background(), f.sb.EnvironmentID, f.chat.ID, f.launch(), Prompt{Text: "and then"}, nil)
	if err != nil || d.Steered || d.Queued == nil {
		t.Fatalf("Steer = %+v, %v; want it queued", d, err)
	}
	close(release)
	f.wait(t, first.ID)
	f.until(t, "the queued message's turn", func(l Log) bool { return len(l.Turns) == 2 && userTexts(l) == "work|and then" })
	if got := steeredTexts(a); got != "" {
		t.Fatalf("steered %q; want it sent as a prompt", got)
	}
}

// When the agent starts a turn of its own for a steered message, as Codex does when its turn
// just ended, the chat's turn goes on until the agent is quiet.
func TestSteeringIntoATurnTheAgentStartedEndsWhenQuiet(t *testing.T) {
	defer func(d time.Duration) { quietAfter = d }(quietAfter)
	quietAfter = 300 * time.Millisecond
	release := make(chan struct{})
	a := &acptest.Agent{Steering: true, SteerStartsTurn: true, Turns: []acptest.Turn{
		{Steps: []acptest.Step{{Pause: 100 * time.Millisecond}, {Update: json.RawMessage(noted)}}}, {},
	}}
	a.BeforePrompt = func() { <-release }
	f := newFixture(t, a)
	first := f.send(t, "work")
	if err := a.WaitForCall(context.Background(), "session/prompt"); err != nil {
		t.Fatal(err)
	}
	d, err := f.s.Steer(context.Background(), f.sb.EnvironmentID, f.chat.ID, f.launch(), Prompt{Text: "and then"}, nil)
	if err != nil || !d.Steered {
		t.Fatalf("Steer = %+v, %v; want steered", d, err)
	}
	close(release)
	start := time.Now()
	_, log := f.wait(t, first.ID)
	if len(log.Turns) != 1 || !slices.Equal(strings.Split(kinds(log.Items), ","), []string{"user", "user", "agent"}) {
		t.Fatalf("%d turns, items %s; want one turn with the agent's answer to the steered message", len(log.Turns), kinds(log.Items))
	}
	if time.Since(start) < quietAfter {
		t.Fatal("the turn ended before the agent went quiet")
	}
}
