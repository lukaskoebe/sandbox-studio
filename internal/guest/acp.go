//go:build linux

package guest

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// acpKillGrace is how long an ACP process group gets to drain after its leader exits or
// receives SIGTERM before remaining group members receive SIGKILL.
var acpKillGrace = 5 * time.Second

// acpEnvValue admits the fixed paths of the ACP environment (CLAUDE_CODE_EXECUTABLE,
// CODEX_PATH) besides placeholders. The values travel as env(1) arguments, never through
// a shell or the profile, so "/" is harmless here.
var acpEnvValue = regexp.MustCompile(`^[A-Za-z0-9._/-]*$`)

// checkStartACP validates an ACP request like checkStartSession.
func checkStartACP(req agentproto.StartACP) error {
	if !validSessionName(req.Chat) {
		return errors.New("invalid chat ID")
	}
	if err := checkStartSession(agentproto.StartSession{Name: req.Chat, Harness: req.Harness, Command: req.Command}); err != nil {
		return err
	}
	if len(req.Env) > 32 {
		return errors.New("too many environment variables")
	}
	for name, value := range req.Env {
		if !envName.MatchString(name) || !acpEnvValue.MatchString(value) {
			return fmt.Errorf("invalid environment variable %q", name)
		}
	}
	return nil
}

// acpArgs is the command line of req: a login shell in the workspace that execs env(1)
// with the chat's variables, so they win over the profile's like a tmux session's.
func acpArgs(req agentproto.StartACP) []string {
	args := []string{"-lc", `exec env "$@"`, "studio-acp"}
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

// acpSlots admits at most MaxACPProcesses agents, one per chat.
type acpSlots struct {
	mu    sync.Mutex
	chats map[string]bool
}

func (s *acpSlots) acquire(chat string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.chats == nil {
		s.chats = map[string]bool{}
	}
	if s.chats[chat] {
		return fmt.Errorf("the agent of chat %s is already running", chat)
	}
	if len(s.chats) >= agentproto.MaxACPProcesses {
		return errFull{}
	}
	s.chats[chat] = true
	return nil
}

// errFull says every slot is taken.
type errFull struct{}

func (errFull) Error() string {
	return fmt.Sprintf("at most %d chats can run at once in a sandbox", agentproto.MaxACPProcesses)
}

func (s *acpSlots) release(chat string) {
	s.mu.Lock()
	delete(s.chats, chat)
	s.mu.Unlock()
}

// serveACP runs the ACP agent of one chat on st.
func (a *Agent) serveACP(st net.Conn, br *bufio.Reader) error {
	var req agentproto.StartACP
	if err := readBody(br, 64<<10, &req); err != nil {
		return replyError(st, err)
	}
	if err := checkStartACP(req); err != nil {
		return replyError(st, err)
	}
	if _, err := a.run("bash", "-lc", `command -v -- "$1"`, "studio-acp", req.Command[0]); err != nil {
		return agentproto.WriteJSONLine(st, agentproto.Error{Code: agentproto.CodeHarness,
			Error: fmt.Sprintf("%s's chat adapter is not installed in this sandbox: its image predates chats. "+
				"Rebase it onto the current base: Rebase in the computer menu keeps /workspace and the home.", req.Harness)})
	}
	cmd := a.command("bash", acpArgs(req)...)
	cmd.Dir = Workdir
	return runACP(&a.acp, st, br, cmd, req.Chat, "/run/studio-agent/acp-"+req.Chat+".log")
}

// runACP starts cmd for chat, replies {} and then pipes the stream to its stdin and its
// stdout to the stream. When the host closes the stream the agent's stdin is closed and
// its process group gets SIGTERM, then SIGKILL after acpKillGrace. Leader exit is monitored
// from startup so descendants that keep stdout or stderr open are also stopped. cmd must
// start a new process group (Setsid or Setpgid).
func runACP(slots *acpSlots, st net.Conn, br *bufio.Reader, cmd *exec.Cmd, chat, logPath string) error {
	if err := slots.acquire(chat); err != nil {
		var full errFull
		if errors.As(err, &full) {
			return agentproto.WriteJSONLine(st, agentproto.Error{Error: err.Error(), Code: agentproto.CodeFull})
		}
		return replyError(st, err)
	}
	defer slots.release(chat)

	log, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return replyError(st, err)
	}
	defer log.Close()
	cmd.Stderr = &cappedWriter{w: log, left: agentproto.MaxACPLog}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return replyError(st, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return replyError(st, err)
	}
	if err := cmd.Start(); err != nil {
		return replyError(st, err)
	}
	outputDrained := make(chan struct{})
	p := &acpProcess{
		cmd: cmd, grace: acpKillGrace, done: make(chan struct{}),
		leaderDone: make(chan struct{}), stopRequested: make(chan struct{}),
	}
	// Observe the leader from startup. A descendant can keep stdout open after the
	// leader exits, which would otherwise leave the output copy waiting forever.
	go p.monitor(outputDrained)
	if err := replyError(st, nil); err != nil {
		close(outputDrained)
		_ = st.Close()
		_ = stdin.Close()
		_ = stopAndWait(p) // the failed reply is the error to return
		return err
	}

	// Host → agent. The stream ending (host closed it, or the channel died) stops the agent.
	hostToAgentDone := make(chan struct{})
	go func() {
		defer close(hostToAgentDone)
		_, _ = io.Copy(stdin, br)
		_ = stdin.Close()
		p.stop()
	}()
	// Agent → host until the agent closes its stdout, normally by exiting.
	_, streamErr := io.Copy(st, stdout)
	close(outputDrained)
	// A closed stdout is the end of this ACP stream even if the process or one of its
	// children is still alive. Close the other direction, then stop and reap the group.
	_ = st.Close()
	err = stopAndWait(p)
	<-hostToAgentDone
	if streamErr != nil {
		return fmt.Errorf("chat %s: forwarding ACP output: %w", chat, streamErr)
	}
	if err != nil {
		return fmt.Errorf("chat %s: the ACP agent exited: %w", chat, err)
	}
	return nil
}

// stopAndWait asks the leader to stop, waits for the startup monitor to kill any remaining
// group members, and then performs the sole cmd.Wait after stream copying has finished.
func stopAndWait(p *acpProcess) error {
	p.stop()
	return p.wait()
}

// acpProcess signals an agent's process group only while its leader is unreaped, so its
// PID can't have been reused.
type acpProcess struct {
	cmd           *exec.Cmd
	grace         time.Duration
	mu            sync.Mutex
	leaderExited  bool
	exited        bool
	done          chan struct{}
	leaderDone    chan struct{}
	stopRequested chan struct{}
	stopOnce      sync.Once
}

func (p *acpProcess) signal(sig syscall.Signal) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.exited && !p.leaderExited {
		syscall.Kill(-p.cmd.Process.Pid, sig)
	}
}

// stop requests shutdown, sends SIGTERM while the leader is still running, and escalates
// only if the leader has not exited within the grace period. The monitor handles remaining
// descendants once it has observed leader exit.
func (p *acpProcess) stop() {
	p.stopOnce.Do(func() { close(p.stopRequested) })
	p.signal(syscall.SIGTERM)
	timer := time.NewTimer(p.grace)
	defer timer.Stop()
	select {
	case <-p.leaderDone:
	case <-timer.C:
		select {
		case <-p.leaderDone:
		default:
			p.signal(syscall.SIGKILL)
		}
	}
}

// monitor observes leader exit from startup without reaping it. It lets the stdout copy
// drain normally, but kills the group after the grace period if a descendant still holds
// stdout or stderr. Once output is drained or shutdown was requested, it kills immediately.
func (p *acpProcess) monitor(outputDrained <-chan struct{}) {
	var info unix.Siginfo
	var waitErr error
	for {
		waitErr = unix.Waitid(unix.P_PID, p.cmd.Process.Pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
		if waitErr != unix.EINTR {
			break
		}
	}
	if waitErr != nil {
		// Without Waitid confirmation, the process-group ID is not safe to signal.
		// Wake the sole waiter so cmd.Wait can report the underlying failure.
		p.mu.Lock()
		p.leaderExited = true
		close(p.leaderDone)
		p.exited = true
		close(p.done)
		p.mu.Unlock()
		return
	}

	p.mu.Lock()
	p.leaderExited = true
	close(p.leaderDone)
	p.mu.Unlock()

	timer := time.NewTimer(p.grace)
	defer timer.Stop()
	select {
	case <-outputDrained:
	case <-p.stopRequested:
	case <-timer.C:
	}

	// Waitid(WNOWAIT) leaves the exited leader as a zombie, pinning its PID and
	// process-group ID until the sole cmd.Wait below reaps it.
	p.mu.Lock()
	syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
	p.exited = true
	close(p.done)
	p.mu.Unlock()
}

func (p *acpProcess) wait() error {
	<-p.done
	return p.cmd.Wait()
}

// cappedWriter writes up to left bytes and drops the rest, so a chatty agent can't fill
// /run.
type cappedWriter struct {
	w    io.Writer
	left int
}

func (c *cappedWriter) Write(p []byte) (int, error) {
	n := len(p)
	if c.left <= 0 {
		return n, nil
	}
	if len(p) > c.left {
		p = p[:c.left]
	}
	m, err := c.w.Write(p)
	c.left -= m
	if err != nil {
		c.left = 0
	}
	return n, nil
}
