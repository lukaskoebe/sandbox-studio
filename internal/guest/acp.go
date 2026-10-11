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

// acpKillGrace is how long an ACP agent gets between SIGTERM and SIGKILL once its stream
// closed.
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
		return fmt.Errorf("at most %d chats can run at once in a sandbox", agentproto.MaxACPProcesses)
	}
	s.chats[chat] = true
	return nil
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
				"Rebuild the base image (make image) and create a new sandbox.", req.Harness)})
	}
	cmd := a.command("bash", acpArgs(req)...)
	cmd.Dir = Workdir
	return runACP(&a.acp, st, br, cmd, req.Chat, "/run/studio-agent/acp-"+req.Chat+".log")
}

// runACP starts cmd for chat, replies {} and then pipes the stream to its stdin and its
// stdout to the stream. When the host closes the stream the agent's stdin is closed and
// its process group gets SIGTERM, then SIGKILL after acpKillGrace; when the agent exits
// the stream is closed. cmd must start a new process group (Setsid or Setpgid).
func runACP(slots *acpSlots, st net.Conn, br *bufio.Reader, cmd *exec.Cmd, chat, logPath string) error {
	if err := slots.acquire(chat); err != nil {
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
	p := &acpProcess{cmd: cmd, grace: acpKillGrace}
	if err := replyError(st, nil); err != nil {
		p.stop()
		p.wait()
		return err
	}

	// Host → agent. The stream ending (host closed it, or the channel died) stops the agent.
	go func() {
		io.Copy(stdin, br)
		stdin.Close()
		p.stop()
	}()
	// Agent → host until the agent closes its stdout, normally by exiting.
	io.Copy(st, stdout)
	err = p.wait()
	st.Close()
	if err != nil {
		return fmt.Errorf("chat %s: the ACP agent exited: %w", chat, err)
	}
	return nil
}

// acpProcess signals an agent's process group only while its leader is unreaped, so its
// PID can't have been reused.
type acpProcess struct {
	cmd    *exec.Cmd
	grace  time.Duration
	mu     sync.Mutex
	exited bool
	done   chan struct{}
	once   sync.Once
}

func (p *acpProcess) doneCh() chan struct{} {
	p.once.Do(func() { p.done = make(chan struct{}) })
	return p.done
}

func (p *acpProcess) signal(sig syscall.Signal) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.exited {
		syscall.Kill(-p.cmd.Process.Pid, sig)
	}
}

// stop sends SIGTERM to the process group and SIGKILL after acpKillGrace unless the
// agent exited in between.
func (p *acpProcess) stop() {
	p.signal(syscall.SIGTERM)
	select {
	case <-p.doneCh():
	case <-time.After(p.grace):
		p.signal(syscall.SIGKILL)
	}
}

// wait waits for the agent to exit without reaping it, marks it exited and then reaps it.
func (p *acpProcess) wait() error {
	var info unix.Siginfo
	for {
		err := unix.Waitid(unix.P_PID, p.cmd.Process.Pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
		if err != unix.EINTR {
			break
		}
	}
	p.mu.Lock()
	p.exited = true
	p.mu.Unlock()
	close(p.doneCh())
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
