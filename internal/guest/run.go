package guest

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// runEnvName matches the names of the placeholders a run may add: the vault's variable names.
var runEnvName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)

// serveRun runs one command for Studio and replies with what it printed (agentproto.KindRun).
func (a *Agent) serveRun(st net.Conn, br *bufio.Reader) error {
	var req agentproto.RunRequest
	if err := readBody(br, agentproto.MaxRunLine, &req); err != nil {
		return replyError(st, err)
	}
	if err := checkRun(req); err != nil {
		return replyError(st, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(req.TimeoutSec)*time.Second)
	defer cancel()
	res, err := a.runCommand(ctx, req)
	if err != nil {
		return replyError(st, err)
	}
	return agentproto.WriteJSONLine(st, res)
}

// checkRun refuses a run the guest won't start: a program outside RunCommands, too many or
// too large arguments and environment entries, a variable the system sets itself, or no
// time limit.
func checkRun(req agentproto.RunRequest) error {
	switch {
	case len(req.Args) == 0 || len(req.Args) > agentproto.MaxRunArgs:
		return fmt.Errorf("a run needs 1 to %d arguments", agentproto.MaxRunArgs)
	case !slices.Contains(agentproto.RunCommands, req.Args[0]):
		return fmt.Errorf("the guest doesn't run %q", req.Args[0])
	case len(req.Stdin) > agentproto.MaxRunStdin:
		return errors.New("the run's stdin is too large")
	case req.TimeoutSec < 1 || req.TimeoutSec > agentproto.MaxRunTimeout:
		return fmt.Errorf("the run's timeout must be 1 to %d seconds", agentproto.MaxRunTimeout)
	case len(req.Env) > agentproto.MaxRunEnv:
		return fmt.Errorf("a run takes at most %d environment variables", agentproto.MaxRunEnv)
	}
	for _, arg := range req.Args {
		if len(arg) > agentproto.MaxRunArg || strings.IndexByte(arg, 0) >= 0 {
			return errors.New("a run argument is too large or contains a NUL byte")
		}
	}
	for name, value := range req.Env {
		switch {
		case !runEnvName.MatchString(name):
			return fmt.Errorf("%q is not a variable name", name)
		case strings.HasPrefix(name, "LD_") || strings.HasPrefix(name, "DYLD_") || name == "PATH" || name == "HOME":
			return fmt.Errorf("a run may not set %s", name)
		case value == "" || len(value) > agentproto.MaxRunEnvLen || strings.IndexByte(value, 0) >= 0:
			return fmt.Errorf("the value of %s is empty, too large or contains a NUL byte", name)
		}
	}
	return nil
}

// runCommand runs req's command as the agent user, in the home, and collects its output.
func (a *Agent) runCommand(ctx context.Context, req agentproto.RunRequest) (agentproto.RunResult, error) {
	cmd := a.command(req.Args[0], req.Args[1:]...)
	for name, value := range req.Env {
		cmd.Env = append(cmd.Env, name+"="+value)
	}
	return execRun(ctx, cmd, req)
}

// execRun runs cmd with req's stdin and collects its output. cmd is killed when ctx ends.
func execRun(ctx context.Context, cmd *exec.Cmd, req agentproto.RunRequest) (agentproto.RunResult, error) {
	cmd.Stdin = strings.NewReader(req.Stdin)
	var stdout, stderr capped
	stdout.max, stderr.max = agentproto.MaxRunOutput, agentproto.MaxRunOutput
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Start(); err != nil {
		return agentproto.RunResult{}, fmt.Errorf("starting %s: %w", req.Args[0], err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-ctx.Done():
		cmd.Process.Kill()
		<-done
		return agentproto.RunResult{}, fmt.Errorf("%s timed out after %d seconds", req.Args[0], req.TimeoutSec)
	}
	return agentproto.RunResult{
		Stdout: stdout.buf.String(), Stderr: stderr.buf.String(), ExitCode: cmd.ProcessState.ExitCode(),
		Truncated: stdout.over || stderr.over,
	}, nil
}
