//go:build linux

package guest

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// readBody reads the single JSON line of a request into v, refusing lines over limit bytes.
func readBody(br *bufio.Reader, limit int, v any) error {
	line, err := bufio.NewReader(io.LimitReader(br, int64(limit)+1)).ReadBytes('\n')
	if len(line) > limit {
		return fmt.Errorf("request larger than %d bytes", limit)
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(line, v)
}

func replyError(st net.Conn, err error) error {
	if err != nil {
		return agentproto.WriteJSONLine(st, agentproto.Error{Error: err.Error()})
	}
	return agentproto.WriteJSONLine(st, struct{}{})
}

// homeFiles writes harness config files into the terminal user's home.
func (a *Agent) homeFiles(st net.Conn, br *bufio.Reader) error {
	var req agentproto.HomeFiles
	if err := readBody(br, agentproto.MaxHomeFilesLine, &req); err != nil {
		return replyError(st, err)
	}
	return replyError(st, writeHomeFiles(a.user.home, int(a.user.uid), int(a.user.gid), req.Files))
}

var harnessName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// checkStartSession validates a session request. The host is trusted, but the request
// becomes a command line and environment, so it is held to what Studio sends.
func checkStartSession(req agentproto.StartSession) error {
	if !validSessionName(req.Name) {
		return errors.New("invalid session name")
	}
	if !harnessName.MatchString(req.Harness) {
		return errors.New("invalid harness name")
	}
	if len(req.Command) == 0 || len(req.Command) > 32 {
		return errors.New("the command must have between 1 and 32 arguments")
	}
	for _, arg := range req.Command {
		if arg == "" || len(arg) > 4096 || strings.IndexByte(arg, 0) >= 0 || strings.HasSuffix(arg, ";") {
			return errors.New("invalid command argument")
		}
	}
	// env(1) would read these as an assignment or an option.
	if strings.Contains(req.Command[0], "=") || strings.HasPrefix(req.Command[0], "-") {
		return errors.New("invalid command")
	}
	if len(req.Env) > 32 {
		return errors.New("too many environment variables")
	}
	for name, value := range req.Env {
		if !envName.MatchString(name) || !envValue.MatchString(value) {
			return fmt.Errorf("invalid environment variable %q", name)
		}
	}
	return nil
}

// startCommand is the tmux command line for req: a detached session in the workspace whose
// command runs through a login shell, so it sees the same PATH and tools as a terminal.
// The session's variables are set by env(1) after the profile has run, so a secret of the
// same name exported by the profile can't replace them. The same tmux call marks the
// session as an agent session and keeps its pane after the harness exits, so a harness that
// dies right away leaves its output behind: tmux runs the commands of one call before it
// notices the exit (verify (S7) against the image's tmux). checkStartSession refuses arguments ending in ";", which tmux would
// read as a command separator.
func startCommand(req agentproto.StartSession) []string {
	args := []string{"-f", tmuxConfPath, "new-session", "-d", "-s", req.Name, "-c", Workdir,
		"bash", "-lc", `exec env "$@"`, "studio-session"}
	names := make([]string, 0, len(req.Env))
	for name := range req.Env {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		args = append(args, name+"="+req.Env[name])
	}
	args = append(args, req.Command...)
	return append(args,
		";", "set-option", "-w", "-t", "="+req.Name+":", "remain-on-exit", "on",
		";", "set-option", "-t", "="+req.Name, agentproto.HarnessOption, req.Harness)
}

// Starting a harness waits this long for it to exit right away before the session counts
// as started.
const (
	startGrace = 1500 * time.Millisecond
	startPoll  = 250 * time.Millisecond
	// maxExitOutput bounds the output of a failed harness quoted in the error.
	maxExitOutput = 600
)

// run runs a command as the terminal user and returns its combined output.
type run func(name string, args ...string) ([]byte, error)

func (a *Agent) run(name string, args ...string) ([]byte, error) {
	return a.command(name, args...).CombinedOutput()
}

func (a *Agent) startSession(st net.Conn, br *bufio.Reader) error {
	var req agentproto.StartSession
	if err := readBody(br, 64<<10, &req); err != nil {
		return replyError(st, err)
	}
	if err := checkStartSession(req); err != nil {
		return replyError(st, err)
	}
	if err := startHarness(a.run, time.Sleep, req); err != nil {
		var he harnessError
		if errors.As(err, &he) {
			return agentproto.WriteJSONLine(st, agentproto.Error{Error: err.Error(), Code: agentproto.CodeHarness})
		}
		return replyError(st, err)
	}
	return replyError(st, nil)
}

// harnessError is a harness that can't run: it is missing or exits right away. The host
// shows it to the user as is.
type harnessError string

func (e harnessError) Error() string { return string(e) }

// startHarness starts req's tmux session and waits startGrace for the harness to exit right
// away. A harness that does is reported with its exit status and last output lines, and its
// session is removed; one that doesn't gets remain-on-exit back off, so its session ends
// with it like any other.
func startHarness(run run, sleep func(time.Duration), req agentproto.StartSession) error {
	if _, err := run("tmux", "has-session", "-t", "="+req.Name); err == nil {
		return fmt.Errorf("a session named %s is already running", req.Name)
	}
	// An image built before the harnesses were added lacks them; the shell would exit at
	// once with "command not found".
	if _, err := run("bash", "-lc", `command -v -- "$1"`, "studio-session", req.Command[0]); err != nil {
		return harnessError(fmt.Sprintf("%s is not installed in this sandbox: its image predates the agent tools. "+
			"Rebuild the base image (make image) and create a new sandbox.", req.Harness))
	}
	if out, err := run("tmux", startCommand(req)...); err != nil {
		return fmt.Errorf("tmux: %s", strings.TrimSpace(string(out)))
	}
	pane := "=" + req.Name + ":"
	// exited reports the error of a harness whose pane is dead, after removing its session.
	exited := func() error {
		out, err := run("tmux", "display-message", "-p", "-t", pane, "#{pane_dead} #{pane_dead_status}")
		if err != nil {
			return harnessError(req.Harness + " exited right away")
		}
		dead, status := paneDead(string(out))
		if !dead {
			return nil
		}
		text, _ := run("tmux", "capture-pane", "-p", "-t", pane, "-S", "-50")
		_, _ = run("tmux", "kill-session", "-t", "="+req.Name)
		return exitError(req.Harness, status, string(text))
	}
	for waited := time.Duration(0); waited < startGrace; waited += startPoll {
		sleep(startPoll)
		if err := exited(); err != nil {
			return err
		}
	}
	// Back to the user's default, normally off. A harness that exited in between is caught
	// by the second look.
	if out, err := run("tmux", "set-option", "-w", "-u", "-t", pane, "remain-on-exit"); err != nil {
		return fmt.Errorf("tmux: %s", strings.TrimSpace(string(out)))
	}
	return exited()
}

// paneDead parses "#{pane_dead} #{pane_dead_status}". The status is empty when the
// harness was killed by a signal.
func paneDead(out string) (bool, string) {
	dead, status, _ := strings.Cut(strings.TrimSpace(out), " ")
	return dead == "1", status
}

// exitError describes a harness that exited right away by its status and the last lines it
// printed, cut to maxExitOutput. The command line isn't quoted: it carries the session's
// environment.
func exitError(harness, status, pane string) error {
	var lines []string
	for _, line := range strings.Split(pane, "\n") {
		line = strings.TrimRight(line, " \t\r")
		// tmux's own note below a dead pane.
		if line == "" || strings.HasPrefix(line, "Pane is dead") {
			continue
		}
		lines = append(lines, line)
	}
	text := strings.Join(lines, "\n")
	if r := []rune(text); len(r) > maxExitOutput {
		text = "…" + string(r[len(r)-maxExitOutput:])
	}
	what := "was killed by a signal"
	if status != "" {
		what = "exited right away (status " + status + ")"
	}
	if text == "" {
		return harnessError(fmt.Sprintf("%s %s without output", harness, what))
	}
	return harnessError(fmt.Sprintf("%s %s:\n%s", harness, what, text))
}
