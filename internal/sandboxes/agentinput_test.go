package sandboxes

import (
	"bufio"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/yamux"

	"github.com/lukaskoebe/sandbox-studio/internal/agentchan"
	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// tmuxGuest is a guest agent with tmux sessions: it lists them and runs tmux's commands on
// them as far as SendToAgent uses them, evaluating if-shell's pane_current_command test
// against the session's command. leaveOn, when set, is the tmux command after which the
// program in the foreground of every session becomes a shell, as an agent quitting would.
type tmuxGuest struct {
	mu       sync.Mutex
	sessions []agentproto.Session
	ran      [][]string
	typed    map[string][]string // by session: what reached the program, paste or key
	leaveOn  string
}

func (g *tmuxGuest) connect(t *testing.T, hub *agentchan.Hub, id string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "tg")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s")
	if err := hub.Listen(id, socket); err != nil {
		t.Fatal(err)
	}
	nc, err := agentchan.Dial(context.Background(), socket)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := yamux.Client(nc, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	hello, err := sess.Open()
	if err != nil {
		t.Fatal(err)
	}
	_ = agentproto.WriteJSONLine(hello, agentproto.Header{Kind: agentproto.KindHello})
	_ = agentproto.WriteJSONLine(hello, agentproto.Hello{Version: "test", Arch: "amd64"})
	_ = hello.Close()
	go func() {
		for {
			st, err := sess.Accept()
			if err != nil {
				return
			}
			go g.answer(st)
		}
	}()
	if err := hub.WaitConnected(context.Background(), id); err != nil {
		t.Fatal(err)
	}
}

func (g *tmuxGuest) answer(st net.Conn) {
	defer st.Close()
	br := bufio.NewReader(st)
	var h agentproto.Header
	if agentproto.ReadJSONLine(br, &h) != nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	switch h.Kind {
	case agentproto.KindSessions:
		_ = agentproto.WriteJSONLine(st, g.sessions)
	case agentproto.KindRun:
		var req agentproto.RunRequest
		if agentproto.ReadJSONLine(br, &req) != nil {
			return
		}
		g.ran = append(g.ran, req.Args)
		g.run(req.Args[1:])
		if req.Args[1] == g.leaveOn {
			for i := range g.sessions {
				g.sessions[i].Command, g.sessions[i].Foreground = "bash", nil
			}
		}
		_ = agentproto.WriteJSONLine(st, agentproto.RunResult{})
	}
}

// run does what tmux would with args on the sessions, for the commands SendToAgent sends.
func (g *tmuxGuest) run(args []string) {
	target := func(args []string) string {
		for i, a := range args {
			if a == "-t" && i+1 < len(args) {
				return args[i+1]
			}
		}
		return ""
	}
	switch args[0] {
	case "paste-buffer":
		g.typed[target(args)] = append(g.typed[target(args)], "paste")
	case "send-keys":
		g.typed[target(args)] = append(g.typed[target(args)], args[len(args)-1])
	case "if-shell":
		// if-shell -F -t <session> '#{==:#{pane_current_command},<cmd>}' <then> [<else>]
		name, cond, rest := args[3], args[4], args[5:]
		want := strings.TrimSuffix(strings.TrimPrefix(cond, "#{==:#{pane_current_command},"), "}")
		for _, s := range g.sessions {
			if s.Name != name {
				continue
			}
			if s.Command == want {
				g.run(strings.Fields(rest[0]))
			} else if len(rest) > 1 {
				g.run(strings.Fields(rest[1]))
			}
		}
	}
}

func newTmuxGuest(t *testing.T, f *checkpointFixture, sessions ...agentproto.Session) *tmuxGuest {
	g := &tmuxGuest{sessions: sessions, typed: map[string][]string{}}
	g.connect(t, f.manager.Hub, f.sandbox.ID)
	return g
}

func TestSendToAgentTypesIntoAnAgentStudioStartedOrOneStartedByHand(t *testing.T) {
	f := newCheckpointFixture(t)
	g := newTmuxGuest(t, f,
		agentproto.Session{Name: "agent", Harness: "claude", Command: "claude"},
		agentproto.Session{Name: "main", Command: "opencode", Foreground: []string{"/usr/local/bin/opencode"}},
		agentproto.Session{Name: "shell", Command: "bash"},
		agentproto.Session{Name: "odd", Command: "a b", Foreground: []string{"opencode"}},
		agentproto.Session{Name: "x; kill-server", Command: "opencode", Foreground: []string{"opencode"}},
	)

	if err := f.manager.SendToAgent(f.ctx, f.env.ID, f.sandbox.ID, "agent", "fix it"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(g.typed["agent"], " "); got != "paste Enter" {
		t.Fatalf("the agent Studio started got %q, want the paste and Enter", got)
	}

	if err := f.manager.SendToAgent(f.ctx, f.env.ID, f.sandbox.ID, "main", "fix it"); err != nil {
		t.Fatalf("an agent started by hand: %v", err)
	}
	if got := strings.Join(g.typed["main"], " "); got != "paste Enter" {
		t.Fatalf("the agent started by hand got %q, want the paste and Enter", got)
	}
	for _, args := range g.ran {
		if args[1] != "load-buffer" && slices.Contains(args, "main") && args[1] != "if-shell" {
			t.Fatalf("tmux %v reached the terminal of an agent started by hand without checking it still runs", args)
		}
	}

	for _, name := range []string{"shell", "odd", "x; kill-server", "gone"} {
		g.ran = nil
		if err := f.manager.SendToAgent(f.ctx, f.env.ID, f.sandbox.ID, name, "rm -rf /"); !errors.Is(err, ErrNotAgentTerminal) {
			t.Fatalf("sending to %s = %v, want ErrNotAgentTerminal", name, err)
		}
		if len(g.ran) != 0 || len(g.typed[name]) != 0 {
			t.Fatalf("sending to %s ran %v", name, g.ran)
		}
	}
}

// An agent started by hand that quits between Studio's look and the paste leaves a shell:
// tmux pastes nothing there and presses no Enter, and the sender hears so.
func TestSendToAgentLeavesTheShellAloneWhenTheAgentQuit(t *testing.T) {
	f := newCheckpointFixture(t)
	g := newTmuxGuest(t, f,
		agentproto.Session{Name: "main", Command: "opencode", Foreground: []string{"opencode"}},
	)
	g.leaveOn = "load-buffer"

	if err := f.manager.SendToAgent(f.ctx, f.env.ID, f.sandbox.ID, "main", "fix it"); !errors.Is(err, ErrAgentLeft) {
		t.Fatalf("SendToAgent = %v, want ErrAgentLeft", err)
	}
	if len(g.typed["main"]) != 0 {
		t.Fatalf("the shell got %v", g.typed["main"])
	}
}
