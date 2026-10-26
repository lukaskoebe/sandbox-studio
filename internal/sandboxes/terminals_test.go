package sandboxes

import (
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/events"
)

func TestDescribeTerminals(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	ago := func(d time.Duration) int64 { return now.Add(-d).Unix() }
	sessions := []agentproto.Session{
		{Name: "main", Created: 1, Command: "bash", Path: "/workspace/webshop", Activity: ago(time.Second)},
		{Name: "2", Created: 2, Command: "node", Foreground: []string{"node", "/usr/local/bin/opencode"}, Path: "/workspace/webshop", Activity: ago(4 * time.Second)},
		{Name: "3", Created: 3, Command: "npm", Foreground: []string{"npm run dev"}, Path: "/workspace/webshop/web", Activity: ago(time.Second)},
		{Name: "4", Created: 4, Command: "bash", Path: "/workspace/webshop/api"},
		{Name: "5", Created: 5, Command: "bash", Path: "/workspace/other/webshop"},
		{Name: "claude-2", Created: 6, Harness: "claude", Command: "claude", Foreground: []string{"claude", "--mcp-config", "x"}, Activity: ago(time.Minute), Path: "/workspace"},
		{Name: "6", Created: 7, Title: "tests", Command: "make", Foreground: []string{"/usr/bin/make", "test", "-j8", "WHAT=./internal/everything/..."}, Path: "/workspace"},
		{Name: "7", Created: 8, Command: "bash", Path: "/home/agent"},
		{Name: "old", Created: 9}, // a guest before the pane fields
		{Name: "8", Created: 10, Command: "vim", Foreground: []string{"vim", "a-very-long-file-name-indeed.tsx"}, Path: "/workspace"},
	}
	want := []struct {
		label, program string
		state          TerminalState
	}{
		{"webshop", "", TerminalIdle},
		{"opencode", "opencode", TerminalWorking},
		{"npm run dev", "npm run dev", TerminalBusy},
		{"api", "", TerminalIdle},
		{"webshop 2", "", TerminalIdle},
		{"claude", "claude", TerminalIdle}, // a harness at its prompt
		{"tests", "make test -j8 WHAT=./in…", TerminalBusy},
		{"~", "", TerminalIdle},
		{"old", "", TerminalIdle},
		{"vim a-very-long-file-na…", "vim a-very-long-file-na…", TerminalBusy},
	}
	got := describeTerminals(sessions, now)
	for i, w := range want {
		if got[i].Label != w.label || got[i].Program != w.program || got[i].State != w.state {
			t.Errorf("%s: got %q, %q, %s; want %q, %q, %s", sessions[i].Name, got[i].Label, got[i].Program, got[i].State, w.label, w.program, w.state)
		}
	}
}

func TestDescribeTerminalsNumbersInCreationOrder(t *testing.T) {
	// tmux lists sessions by name; the older one keeps the plain label.
	got := describeTerminals([]agentproto.Session{
		{Name: "a", Created: 20, Path: "/workspace"},
		{Name: "b", Created: 10, Path: "/workspace"},
	}, time.Now())
	if got[0].Label != "workspace 2" || got[1].Label != "workspace" {
		t.Fatalf("labels %q, %q", got[0].Label, got[1].Label)
	}
}

func TestInHostClock(t *testing.T) {
	// The guest's clock is 100 s behind the host's.
	s := agentproto.Session{Activity: 900, Started: 800, Clock: 1000}.InHostClock(1100)
	if s.Activity != 1000 || s.Started != 900 || s.Clock != 0 {
		t.Fatalf("got %+v", s)
	}
	if s := (agentproto.Session{Activity: 900}).InHostClock(1100); s.Activity != 900 {
		t.Fatalf("a guest without a clock: %+v", s)
	}
}

func TestNoteTerminalsPublishesChanges(t *testing.T) {
	bus := &events.Bus{}
	ch, stop := bus.Subscribe()
	defer stop()
	m := &Manager{Bus: bus}
	count := func() int {
		n := 0
		for {
			select {
			case <-ch:
				n++
			default:
				return n
			}
		}
	}
	at := time.Now()
	busy := []Terminal{{Session: agentproto.Session{Name: "main", Activity: 1}, Label: "npm run dev", Program: "npm run dev", State: TerminalBusy}}
	m.noteTerminals("env", "sb", busy, at)
	if n := count(); n != 1 {
		t.Fatalf("first look: %d events", n)
	}
	printed := []Terminal{busy[0]}
	printed[0].Activity = 2
	m.noteTerminals("env", "sb", printed, at)
	if n := count(); n != 0 {
		t.Fatalf("new output alone: %d events", n)
	}
	idle := []Terminal{{Session: agentproto.Session{Name: "main"}, Label: "webshop", State: TerminalIdle}}
	m.noteTerminals("env", "sb", idle, at)
	if n := count(); n != 1 {
		t.Fatalf("the program ended: %d events", n)
	}
	m.forgetTerminals("sb")
	if n := count(); n != 1 {
		t.Fatalf("the guest left: %d events", n)
	}
}
