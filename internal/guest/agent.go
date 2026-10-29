//go:build linux

package guest

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/hashicorp/yamux"
	"github.com/mdlayher/vsock"
	"golang.org/x/sys/unix"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/version"
)

// User is the unprivileged account that terminals run as.
const User = "agent"

// Workdir is where new terminals start.
const Workdir = "/workspace"

// tmuxConf is the server config for terminal sessions, written to tmuxConfPath at startup.
//
//go:embed tmux.conf
var tmuxConf []byte

const tmuxConfPath = "/run/studio-agent/tmux.conf"

// Agent serves host requests over the vsock channel.
type Agent struct {
	log      *slog.Logger
	user     *account
	configMu sync.Mutex
	shell    atomic.Pointer[string] // what Config.Shell last asked for
	// configured is closed once the first configuration is applied (see awaitConfig).
	configured             chan struct{}
	configMake, configDone sync.Once
	// browserTrust serializes and caches Chromium NSS CA preparation per browser agent.
	browserTrust browserTrustState

	sessMu sync.Mutex
	sess   *yamux.Session // the live host session, for relayed calls

	acp acpSlots // the running ACP agents, one per chat

	snapOnce sync.Once
	snap     *snapshots // the chats' turn snapshots

	fwdOnce sync.Once
	fwd     *forwarder // a browser VM's forwarded loopback ports
}

// New prepares an agent; it fails if the terminal user does not exist.
func New(log *slog.Logger) (*Agent, error) {
	acct, err := lookupAccount(User)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll("/run/studio-agent", 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(tmuxConfPath, tmuxConf, 0o644); err != nil {
		return nil, err
	}
	return &Agent{log: log, user: acct}, nil
}

// Run dials the host and serves sessions until ctx is done, reconnecting with backoff.
func (a *Agent) Run(ctx context.Context) error {
	go func() {
		// serveCalls returns only errors, so err is set whenever it returns.
		if err := a.serveCalls(ctx); ctx.Err() == nil {
			a.log.Error("call socket failed; memory hooks and tools are unavailable", "err", err)
		}
	}()
	backoff := 200 * time.Millisecond
	for ctx.Err() == nil {
		start := time.Now()
		err := a.serveOnce(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Since(start) > 10*time.Second {
			backoff = 200 * time.Millisecond
		}
		a.log.Warn("host channel closed; reconnecting", "err", err, "in", backoff)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
		}
		backoff = min(backoff*2, 5*time.Second)
	}
	return ctx.Err()
}

// maxChannelWrite bounds one write to the host. msb's vsock breaks the channel when a single
// guest write is larger than 32 KiB (docs/guest-agent.md), and yamux writes a frame's body
// in one call, up to a stream window of 256 KiB. Smaller writes each stay under the limit.
const maxChannelWrite = 16 << 10

// channelConn is the guest's end of the host channel. Its writes are split into pieces of at
// most maxChannelWrite bytes; reads and Close are the connection's.
type channelConn struct{ net.Conn }

func (c channelConn) Write(p []byte) (int, error) {
	n := 0
	for len(p) > 0 {
		m, err := c.Conn.Write(p[:min(len(p), maxChannelWrite)])
		n += m
		if err != nil {
			return n, err
		}
		p = p[m:]
	}
	return n, nil
}

// hostSession runs the guest's yamux session over the host channel conn.
func hostSession(conn net.Conn) (*yamux.Session, error) {
	return yamux.Client(channelConn{conn}, yamux.DefaultConfig())
}

func (a *Agent) serveOnce(ctx context.Context) error {
	conn, err := vsock.Dial(vsock.Host, agentproto.VsockPort, nil)
	if err != nil {
		return fmt.Errorf("dial host: %w", err)
	}
	sess, err := hostSession(conn)
	if err != nil {
		conn.Close()
		return err
	}
	defer sess.Close()
	go func() {
		<-ctx.Done()
		sess.Close()
	}()

	if err := a.sendHello(sess); err != nil {
		return err
	}
	a.log.Info("connected to host")
	a.setSession(sess)
	for {
		st, err := sess.Accept()
		if err != nil {
			return err
		}
		go a.handle(st)
	}
}

func (a *Agent) sendHello(sess *yamux.Session) error {
	st, err := sess.Open()
	if err != nil {
		return err
	}
	defer st.Close()
	host, _ := os.Hostname()
	if err := agentproto.WriteJSONLine(st, agentproto.Header{Kind: agentproto.KindHello}); err != nil {
		return err
	}
	return agentproto.WriteJSONLine(st, agentproto.Hello{Version: version.Version, Arch: runtime.GOARCH, Hostname: host})
}

func (a *Agent) handle(st net.Conn) {
	defer st.Close()
	br := bufio.NewReader(st)
	var h agentproto.Header
	if err := agentproto.ReadJSONLine(br, &h); err != nil {
		a.log.Warn("bad stream header", "err", err)
		return
	}
	var err error
	switch h.Kind {
	case agentproto.KindPTY:
		err = a.servePTY(st, br, h)
	case agentproto.KindTCP:
		err = servTCP(st, br, h.Port)
	case agentproto.KindSessions:
		err = a.replySessions(st)
	case agentproto.KindPorts:
		err = replyPorts(st)
	case agentproto.KindDisk:
		err = a.replyDisks(st)
	case agentproto.KindKill:
		err = a.killSession(st, h.Session)
	case agentproto.KindTitle:
		err = a.setTitle(st, br, h.Session)
	case agentproto.KindConfig:
		err = a.configure(st, br)
	case agentproto.KindHomeFiles:
		err = a.homeFiles(st, br)
	case agentproto.KindHomeDir:
		err = a.homeDir(st, br)
	case agentproto.KindStartSession:
		err = a.startSession(st, br)
	case agentproto.KindBrowser:
		err = a.browser(st, br)
	case agentproto.KindBrowserDownloads:
		err = a.browserDownloads(st, br)
	case agentproto.KindACP:
		err = a.serveACP(st, br)
	case agentproto.KindSnapshot:
		err = a.snapshot(st, br)
	case agentproto.KindFiles:
		err = a.files(st, br)
	case agentproto.KindUpload:
		err = a.upload(st, br)
	case agentproto.KindPaste:
		err = a.paste(st, br)
	case agentproto.KindRun:
		err = a.serveRun(st, br)
	case agentproto.KindForwards:
		err = a.forwards(st, br)
	default:
		err = agentproto.WriteJSONLine(st, agentproto.Error{Error: "unknown stream kind " + strconv.Quote(h.Kind)})
	}
	if err != nil && !errors.Is(err, io.EOF) {
		a.log.Warn("stream failed", "kind", h.Kind, "err", err)
	}
}

// configure applies the environment's configuration pushed by Studio.
func (a *Agent) configure(st net.Conn, br *bufio.Reader) error {
	var cfg agentproto.Config
	if err := agentproto.ReadJSONLine(br, &cfg); err != nil {
		return err
	}
	a.configMu.Lock()
	defer a.configMu.Unlock()
	if err := applyConfig("/", cfg, updateCACertificates); err != nil {
		a.log.Warn("applying the environment's configuration failed", "err", err)
		return agentproto.WriteJSONLine(st, agentproto.Error{Error: err.Error()})
	}
	a.shell.Store(&cfg.Shell)
	a.markConfigured()
	return agentproto.WriteJSONLine(st, struct{}{})
}

func (a *Agent) markConfigured() { a.configDone.Do(func() { close(a.configuredCh()) }) }

func (a *Agent) configuredCh() chan struct{} {
	a.configMake.Do(func() { a.configured = make(chan struct{}) })
	return a.configured
}

// configWait bounds how long a new terminal waits for the first configuration.
const configWait = 15 * time.Second

// awaitConfig waits until Studio's first configuration is applied, or configWait has passed.
// Studio pushes it right after the agent connects, but a terminal tab that was open
// reconnects as soon as the agent does. On a fresh root disk, as after a rebase, a shell
// started before then finds no secrets in the profile hook and keeps that environment,
// and starts bash where the user picked zsh.
func (a *Agent) awaitConfig() {
	select {
	case <-a.configuredCh():
	case <-time.After(configWait):
		a.log.Warn("starting a terminal before Studio sent the configuration", "waited", configWait)
	}
}

// --- terminals ------------------------------------------------------------------------

func (a *Agent) servePTY(st net.Conn, br *bufio.Reader, h agentproto.Header) error {
	name := h.Session
	if !validSessionName(name) {
		return agentproto.WriteFrame(st, agentproto.FrameExit, []byte("invalid session name"))
	}
	a.awaitConfig()
	shell := a.terminalShell()
	cmd := a.command("tmux", terminalCommand(name, shell)...)
	cmd.Env = append(cmd.Env, "SHELL="+shell) // the later of two values wins
	cmd.Dir = Workdir
	size := &pty.Winsize{Cols: max(h.Cols, 20), Rows: max(h.Rows, 5)}
	f, err := pty.StartWithSize(cmd, size)
	if err != nil {
		return agentproto.WriteFrame(st, agentproto.FrameExit, []byte(err.Error()))
	}
	defer f.Close()

	var wmu sync.Mutex
	write := func(typ byte, p []byte) error {
		wmu.Lock()
		defer wmu.Unlock()
		return agentproto.WriteFrame(st, typ, p)
	}
	go func() {
		var clicks afterClick
		for {
			typ, p, err := agentproto.ReadFrame(br)
			if err != nil {
				// Host went away: detach by killing the tmux client, the session lives on.
				cmd.Process.Signal(syscall.SIGHUP)
				return
			}
			switch typ {
			case agentproto.FrameData:
				_, _ = f.Write(clicks.input(p, time.Now())) // a terminal that is gone ends the read loop below
			case agentproto.FrameResize:
				var r agentproto.Resize
				if json.Unmarshal(p, &r) == nil && r.Cols > 0 && r.Rows > 0 {
					_ = pty.Setsize(f, &pty.Winsize{Cols: r.Cols, Rows: r.Rows})
				}
			}
		}
	}()
	buf := make([]byte, 32*1024)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			if werr := write(agentproto.FrameData, buf[:n]); werr != nil {
				cmd.Process.Signal(syscall.SIGHUP)
				break
			}
		}
		if err != nil {
			break
		}
	}
	code := 0
	if err := cmd.Wait(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
	}
	return write(agentproto.FrameExit, []byte(strconv.Itoa(code)))
}

func validSessionName(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

// sessionFormat separates fields with "|": session names can't contain it, and tmux 3.5
// prints tabs in format output as "_". The pane variables are those of the active pane of
// the session's window. The pane's directory comes last, as it may contain "|"; titles
// can't (agentproto.CheckTitle).
const sessionFormat = "#{session_name}|#{session_attached}|#{session_windows}|#{session_created}|#{" + agentproto.HarnessOption + "}|#{" + agentproto.ChatOption + "}|#{window_activity}" +
	"|#{pane_current_command}|#{pane_pid}|#{" + agentproto.TitleOption + "}|#{pane_current_path}"

func (a *Agent) replySessions(st net.Conn) error {
	out, err := a.command("tmux", "list-sessions", "-F", sessionFormat).Output()
	sessions := []agentproto.Session{}
	if err == nil { // tmux exits non-zero when no server is running: no sessions.
		now := time.Now()
		sessions = parseSessions(string(out), now, func(pid int) ([]string, int64) { return foreground("/proc", pid, now) })
	}
	return agentproto.WriteJSONLine(st, sessions)
}

// parseSessions reads list-sessions output in sessionFormat, at now. fg answers what runs in
// the foreground of a pane's terminal, and since when (foreground). Sessions with names
// Studio couldn't address are skipped. Agents before the chat tag print five fields, those
// before the activity six, and those before the pane seven.
func parseSessions(out string, now time.Time, fg func(pid int) ([]string, int64)) []agentproto.Session {
	sessions := []agentproto.Session{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.SplitN(line, "|", 11)
		if len(f) < 5 || !validSessionName(f[0]) {
			continue
		}
		chat := ""
		if len(f) >= 6 && validSessionName(f[5]) {
			chat = f[5]
		}
		var activity int64
		if len(f) >= 7 {
			activity, _ = strconv.ParseInt(f[6], 10, 64)
		}
		att, _ := strconv.Atoi(f[1])
		win, _ := strconv.Atoi(f[2])
		created, _ := strconv.ParseInt(f[3], 10, 64)
		s := agentproto.Session{Name: f[0], Attached: att, Windows: win, Created: created, Harness: f[4], Chat: chat, Activity: activity}
		if len(f) == 11 {
			// A "|" in the program's name would shift the fields: then the pid doesn't parse.
			if pid, err := strconv.Atoi(f[8]); err == nil && pid > 0 {
				s.Command, s.Title, s.Path, s.Clock = f[7], f[9], f[10], now.Unix()
				s.Foreground, s.Started = fg(pid)
			}
		}
		sessions = append(sessions, s)
	}
	return sessions
}

func (a *Agent) killSession(st net.Conn, name string) error {
	if !validSessionName(name) {
		return agentproto.WriteJSONLine(st, agentproto.Error{Error: "invalid session name"})
	}
	// "=" makes tmux match the name exactly instead of by prefix.
	if out, err := a.command("tmux", "kill-session", "-t", "="+name).CombinedOutput(); err != nil {
		return agentproto.WriteJSONLine(st, agentproto.Error{Error: strings.TrimSpace(string(out))})
	}
	return agentproto.WriteJSONLine(st, struct{}{})
}

// --- previews -------------------------------------------------------------------------

func servTCP(st net.Conn, br *bufio.Reader, port int) error {
	if port <= 0 || port > 65535 {
		return fmt.Errorf("invalid port %d", port)
	}
	up, err := dialLoopback(port)
	if err != nil {
		return err
	}
	defer up.Close()
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(up, br)
		if c, ok := up.(*net.TCPConn); ok {
			_ = c.CloseWrite()
		}
		close(done)
	}()
	_, _ = io.Copy(st, up)
	// A yamux Close only ends our side. Without it the host never learns that the server
	// dropped an idle keep-alive connection, reuses it, and the request hangs.
	st.Close()
	<-done
	return nil
}

// dialLoopback connects to port on IPv4 loopback, then IPv6: dev servers that listen on
// "localhost" (Vite, for one) often bind only ::1.
func dialLoopback(port int) (net.Conn, error) {
	p := strconv.Itoa(port)
	up, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", p), 5*time.Second)
	if err == nil {
		return up, nil
	}
	if up, err6 := net.DialTimeout("tcp", net.JoinHostPort("::1", p), 5*time.Second); err6 == nil {
		return up, nil
	}
	return nil, err
}

func (a *Agent) replyDisks(st net.Conn) error {
	var disks []agentproto.Disk
	seen := map[uint64]bool{}
	for _, d := range []struct{ name, dir string }{{agentproto.DiskHome, a.user.home}, {agentproto.DiskWorkspace, Workdir},
		{agentproto.DiskDocker, agentproto.DockerDir}} {
		var fs unix.Stat_t
		if unix.Stat(d.dir, &fs) != nil || seen[fs.Dev] {
			continue // a workspace on the home's disk is the home
		}
		seen[fs.Dev] = true
		if free, total, err := diskSpace(d.dir)(); err == nil {
			disks = append(disks, agentproto.Disk{Name: d.name, Free: free, Total: total})
		}
	}
	return agentproto.WriteJSONLine(st, disks)
}

func replyPorts(st net.Conn) error {
	ports, err := listeningPorts()
	if err != nil {
		return agentproto.WriteJSONLine(st, agentproto.Error{Error: err.Error()})
	}
	return agentproto.WriteJSONLine(st, ports)
}

// listeningPorts parses /proc/net/tcp{,6} for sockets in LISTEN state.
func listeningPorts() ([]agentproto.Port, error) {
	seen := map[int]bool{}
	var ports []agentproto.Port
	for _, file := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		b, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n")[1:] {
			f := strings.Fields(line)
			if len(f) < 4 || f[3] != "0A" {
				continue
			}
			_, hexPort, ok := strings.Cut(f[1], ":")
			if !ok {
				continue
			}
			p, err := strconv.ParseInt(hexPort, 16, 32)
			if err != nil || seen[int(p)] {
				continue
			}
			seen[int(p)] = true
			ports = append(ports, agentproto.Port{Port: int(p)})
		}
	}
	return ports, nil
}

// --- running as the terminal user -----------------------------------------------------

type account struct {
	uid, gid uint32
	groups   []uint32
	home     string
}

func lookupAccount(name string) (*account, error) {
	u, err := user.Lookup(name)
	if err != nil {
		return nil, err
	}
	uid, _ := strconv.ParseUint(u.Uid, 10, 32)
	gid, _ := strconv.ParseUint(u.Gid, 10, 32)
	acct := &account{uid: uint32(uid), gid: uint32(gid), home: u.HomeDir}
	gids, _ := u.GroupIds()
	for _, g := range gids {
		if n, err := strconv.ParseUint(g, 10, 32); err == nil {
			acct.groups = append(acct.groups, uint32(n))
		}
	}
	return acct, nil
}

// command builds a command that runs as the terminal user with a login-like environment.
func (a *Agent) command(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
		Credential: &syscall.Credential{
			Uid: a.user.uid, Gid: a.user.gid, Groups: a.user.groups,
		},
	}
	env := []string{
		"HOME=" + a.user.home,
		"USER=" + User,
		"LOGNAME=" + User,
		"SHELL=/bin/bash",
		"TERM=xterm-256color",
		"LANG=en_US.UTF-8",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	}
	env = append(env, toolEnv...)
	// Secrets come first so that the system's own variables win; Studio refuses secret names
	// that would shadow them anyway.
	env = append(env, readEnvironmentFile(envFile)...)
	cmd.Env = append(env, readEnvironmentFile("/etc/environment")...)
	cmd.Dir = a.user.home
	return cmd
}

func readEnvironmentFile(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var env []string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		env = append(env, line)
	}
	return env
}
