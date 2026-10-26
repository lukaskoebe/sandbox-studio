//go:build linux

package guest

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

func TestParseSessions(t *testing.T) {
	out := "main|1|2|1791627258|||1791627290\nclaude-2|0|1|1791627300|claude|\nchat-x|0|1|1791627301|codex|abc7\nold|0|1|1|opencode\nodd name|0|1|1||\nshort|0\n" +
		"web|0|1|1791627302|||1791627310|npm|412|dev server|/workspace/my|app\n" +
		"two|0|1|1791627303|||1791627311|a|b|bad|||/x\n"
	now := time.Unix(1791627400, 0)
	fg := func(pid int) ([]string, int64) {
		if pid != 412 {
			t.Errorf("foreground of %d", pid)
		}
		return []string{"npm run dev"}, 1791627350
	}
	want := []agentproto.Session{
		{Name: "main", Attached: 1, Windows: 2, Created: 1791627258, Activity: 1791627290},
		{Name: "claude-2", Windows: 1, Created: 1791627300, Harness: "claude"},
		{Name: "chat-x", Windows: 1, Created: 1791627301, Harness: "codex", Chat: "abc7"},
		{Name: "old", Windows: 1, Created: 1, Harness: "opencode"},
		{Name: "web", Windows: 1, Created: 1791627302, Activity: 1791627310, Command: "npm", Foreground: []string{"npm run dev"},
			Started: 1791627350, Path: "/workspace/my|app", Title: "dev server", Clock: 1791627400},
		// A "|" in the program's name shifts the fields: the session keeps its basics.
		{Name: "two", Windows: 1, Created: 1791627303, Activity: 1791627311},
	}
	if got := parseSessions(out, now, fg); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	if got := parseSessions("", now, fg); len(got) != 0 {
		t.Fatalf("empty output: %+v", got)
	}
}

// fakeProc writes a /proc with the processes in procs: pid → its stat fields after the
// name (state, ppid, pgrp, session, tty, tpgid, …, starttime as the 20th) and its cmdline.
func fakeProc(t *testing.T, uptime string, procs map[int][2]string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "uptime"), []byte(uptime), 0o644); err != nil {
		t.Fatal(err)
	}
	for pid, p := range procs {
		d := filepath.Join(dir, strconv.Itoa(pid))
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		stat := strconv.Itoa(pid) + " (a (b) c) " + p[0] + "\n"
		if err := os.WriteFile(filepath.Join(d, "stat"), []byte(stat), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "cmdline"), []byte(p[1]), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// stat is the stat fields after the name of a process with tpgid and starttime.
func stat(tpgid, starttime int) string {
	return "S 1 10 10 34816 " + strconv.Itoa(tpgid) + " 0 0 0 0 0 0 0 0 0 20 0 1 0 " + strconv.Itoa(starttime) + " 1000 100"
}

func TestForeground(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	// Booted 1000 s before now; npm started 400 s after boot, its title rewritten as node does.
	proc := fakeProc(t, "1000.42 3000.10\n", map[int][2]string{
		10: {stat(20, 100), "-bash\x00"},
		20: {stat(20, 40000), "npm run dev\x00\x00\x00"},
		30: {stat(30, 200), "/bin/bash\x00-l\x00"},
		40: {stat(41, 300), "zsh\x00"},
		41: {stat(41, 500), "bash\x00./build.sh\x00"},
		50: {stat(0, 600), "bash\x00"},
	})
	for _, c := range []struct {
		pid     int
		args    []string
		started int64
	}{
		{10, []string{"npm run dev"}, now.Unix() - 1000 + 400},
		{30, nil, 0}, // the shell at its prompt
		{40, []string{"bash", "./build.sh"}, now.Unix() - 1000 + 5}, // a script is a program
		{50, nil, 0}, // no terminal
		{60, nil, 0}, // gone
	} {
		args, started := foreground(proc, c.pid, now)
		if !slices.Equal(args, c.args) || started != c.started {
			t.Errorf("foreground(%d) = %q, %d; want %q, %d", c.pid, args, started, c.args, c.started)
		}
	}
}

func TestSplitCmdline(t *testing.T) {
	many := strings.Repeat("a\x00", 20)
	got := splitCmdline([]byte(many))
	if len(got) != maxForegroundArgs || got[len(got)-1] != "a …" {
		t.Fatalf("20 arguments: %q", got)
	}
	long := splitCmdline([]byte("vim\x00" + strings.Repeat("é", 200)))
	if len(long[1]) > maxForegroundArgSize+len("…") || !strings.HasSuffix(long[1], "…") || !utf8.ValidString(long[1]) {
		t.Fatalf("long argument: %q", long[1])
	}
}

func TestCheckTitle(t *testing.T) {
	for title, ok := range map[string]bool{"": true, "dev server": true, "-x": true, "a|b": false, "a\nb": false,
		"run;": false, strings.Repeat("x", agentproto.MaxTitle): true, strings.Repeat("é", agentproto.MaxTitle+1): false} {
		if err := agentproto.CheckTitle(title); (err == nil) != ok {
			t.Errorf("CheckTitle(%q) = %v", title, err)
		}
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
