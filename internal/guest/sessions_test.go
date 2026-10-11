//go:build linux

package guest

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

func TestParseSessions(t *testing.T) {
	out := "main|1|2|1791627258||\nclaude-2|0|1|1791627300|claude|\nchat-x|0|1|1791627301|codex|abc7\nold|0|1|1|opencode\nodd name|0|1|1||\nshort|0\n"
	want := []agentproto.Session{
		{Name: "main", Attached: 1, Windows: 2, Created: 1791627258},
		{Name: "claude-2", Windows: 1, Created: 1791627300, Harness: "claude"},
		{Name: "chat-x", Windows: 1, Created: 1791627301, Harness: "codex", Chat: "abc7"},
		{Name: "old", Windows: 1, Created: 1, Harness: "opencode"},
	}
	if got := parseSessions(out); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if got := parseSessions(""); len(got) != 0 {
		t.Fatalf("empty output: %+v", got)
	}
}

var startReq = agentproto.StartSession{Name: "opencode", Harness: "opencode",
	Command: []string{"opencode"}, Env: map[string]string{"B": "2", "A": "studio-1"}}

// fakeTmux answers startHarness's commands: the binary lookup, then display-message with
// panes in turn (the last one repeats). It records every command.
type fakeTmux struct {
	missing bool
	panes   []string
	capture string
	calls   []string
}

func (f *fakeTmux) run(name string, args ...string) ([]byte, error) {
	call := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)
	switch {
	case name == "bash":
		if f.missing {
			return nil, errors.New("exit status 1")
		}
		return []byte("/usr/local/bin/opencode\n"), nil
	case len(args) > 0 && args[0] == "has-session":
		return []byte("can't find session"), errors.New("exit status 1")
	case len(args) > 0 && args[0] == "display-message":
		pane := f.panes[0]
		if len(f.panes) > 1 {
			f.panes = f.panes[1:]
		}
		return []byte(pane), nil
	case len(args) > 0 && args[0] == "capture-pane":
		return []byte(f.capture), nil
	}
	return nil, nil
}

func (f *fakeTmux) ran(prefix string) bool {
	return slices.ContainsFunc(f.calls, func(c string) bool { return strings.HasPrefix(c, prefix) })
}

func TestStartHarness(t *testing.T) {
	var slept time.Duration
	sleep := func(d time.Duration) { slept += d }

	// Not installed: nothing starts.
	f := &fakeTmux{missing: true}
	err := startHarness(f.run, sleep, startReq)
	var he harnessError
	if !errors.As(err, &he) || !strings.HasPrefix(err.Error(), "opencode is not installed in this sandbox") ||
		f.ran("tmux -f") || f.calls[1] != `bash -lc command -v -- "$1" studio-session opencode` {
		t.Fatalf("missing: %v %q", err, f.calls)
	}

	// Exits right away: the error has the status and the output, and the session is gone.
	f = &fakeTmux{panes: []string{"0 \n", "1 1\n"},
		capture: "booting\nError: no provider configured\n\n\nPane is dead (status 1, Sat Oct 10 12:00:00 2026)\n\n"}
	err = startHarness(f.run, sleep, startReq)
	if !errors.As(err, &he) || err.Error() != "opencode exited right away (status 1):\nbooting\nError: no provider configured" ||
		!f.ran("tmux kill-session -t =opencode") || f.ran("tmux set-option -w -u") {
		t.Fatalf("exited: %v %q", err, f.calls)
	}

	// Runs: after the grace, remain-on-exit goes back to the default.
	f, slept = &fakeTmux{panes: []string{"0 \n"}}, 0
	if err := startHarness(f.run, sleep, startReq); err != nil || slept != startGrace ||
		!f.ran("tmux set-option -w -u -t =opencode: remain-on-exit") || f.ran("tmux kill-session") {
		t.Fatalf("running: %v %v %q", err, slept, f.calls)
	}

	// Exits just as remain-on-exit is turned off: still caught.
	f = &fakeTmux{panes: []string{"0 ", "0 ", "0 ", "0 ", "0 ", "0 ", "1 \n"}}
	if err := startHarness(f.run, sleep, startReq); err == nil || err.Error() != "opencode was killed by a signal without output" {
		t.Fatalf("late exit: %v", err)
	}
}

func TestExitErrorBounded(t *testing.T) {
	err := exitError("codex", "2", strings.Repeat("x", 2000)+"\nlast line\n")
	if n := len([]rune(err.Error())); n > maxExitOutput+60 || !strings.HasSuffix(err.Error(), "last line") ||
		!strings.Contains(err.Error(), "(status 2):\n…") {
		t.Fatalf("%d %q", n, err)
	}
}
