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
		if arg == "" || len(arg) > 4096 || strings.IndexByte(arg, 0) >= 0 {
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
// same name exported by the profile can't replace them.
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
	return append(args, req.Command...)
}

func (a *Agent) startSession(st net.Conn, br *bufio.Reader) error {
	var req agentproto.StartSession
	if err := readBody(br, 64<<10, &req); err != nil {
		return replyError(st, err)
	}
	if err := checkStartSession(req); err != nil {
		return replyError(st, err)
	}
	if a.command("tmux", "has-session", "-t", "="+req.Name).Run() == nil {
		return replyError(st, fmt.Errorf("a session named %s is already running", req.Name))
	}
	if out, err := a.command("tmux", startCommand(req)...).CombinedOutput(); err != nil {
		return replyError(st, fmt.Errorf("tmux: %s", strings.TrimSpace(string(out))))
	}
	// The option marks the session as an agent session in list-sessions. If the harness
	// already exited, the session is gone and there's nothing to mark.
	if out, err := a.command("tmux", "set-option", "-t", "="+req.Name, agentproto.HarnessOption, req.Harness).CombinedOutput(); err != nil {
		return replyError(st, fmt.Errorf("the session ended right away: %s", strings.TrimSpace(string(out))))
	}
	return replyError(st, nil)
}
