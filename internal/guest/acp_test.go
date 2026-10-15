//go:build linux

package guest

import (
	"bufio"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

func TestCheckStartACP(t *testing.T) {
	ok := agentproto.StartACP{Chat: "c0123456789ab", Harness: "claude", Command: []string{"claude-agent-acp"},
		Env: map[string]string{"CLAUDE_CODE_EXECUTABLE": "/usr/local/bin/claude", "ANTHROPIC_API_KEY": "studio-0123"}}
	if err := checkStartACP(ok); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*agentproto.StartACP){
		"chat":      func(r *agentproto.StartACP) { r.Chat = "../x" },
		"harness":   func(r *agentproto.StartACP) { r.Harness = "Claude" },
		"command":   func(r *agentproto.StartACP) { r.Command = []string{"A=b", "sh"} },
		"separator": func(r *agentproto.StartACP) { r.Command = []string{"x", "y;"} },
		"value":     func(r *agentproto.StartACP) { r.Env = map[string]string{"X": "$(id)"} },
		"space":     func(r *agentproto.StartACP) { r.Env = map[string]string{"X": "a b"} },
		"name":      func(r *agentproto.StartACP) { r.Env = map[string]string{"x": "1"} },
	} {
		r := ok
		mutate(&r)
		if checkStartACP(r) == nil {
			t.Errorf("%s: accepted %+v", name, r)
		}
	}
}

func TestACPArgs(t *testing.T) {
	got := acpArgs(agentproto.StartACP{Command: []string{"codex-acp"}, Env: map[string]string{"NO_BROWSER": "1", "CODEX_PATH": "/usr/local/bin/codex"}})
	want := []string{"-lc", `exec env "$@"`, "studio-acp", "CODEX_PATH=/usr/local/bin/codex", "NO_BROWSER=1", "codex-acp"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q", got)
	}
}

// startACP runs script under runACP and returns the host's end after the ack.
func startACP(t *testing.T, slots *acpSlots, chat, script string) (net.Conn, *bufio.Reader, chan error, string) {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	host, guest := net.Pipe()
	logPath := filepath.Join(t.TempDir(), "acp.log")
	done := make(chan error, 1)
	go func() { done <- runACP(slots, guest, bufio.NewReader(guest), cmd, chat, logPath) }()
	br := bufio.NewReader(host)
	ack, err := br.ReadString('\n')
	if err != nil || ack != "{}\n" {
		t.Fatalf("ack = %q, %v", ack, err)
	}
	return host, br, done, logPath
}

func TestACPPipesAndExits(t *testing.T) {
	var slots acpSlots
	host, br, done, _ := startACP(t, &slots, "c1", `read line; echo "got $line"`)
	if _, err := io.WriteString(host, "ping\n"); err != nil {
		t.Fatal(err)
	}
	if line, _ := br.ReadString('\n'); line != "got ping\n" {
		t.Fatalf("line = %q", line)
	}
	// The agent exited: the stream ends.
	if _, err := br.ReadByte(); err != io.EOF {
		t.Fatalf("stream after exit: %v", err)
	}
	<-done
	if len(slots.chats) != 0 {
		t.Fatalf("slot kept: %v", slots.chats)
	}
}

// Closing the stream stops the agent, even one that ignores its stdin, and the whole
// process group with it.
func TestACPKilledWhenStreamCloses(t *testing.T) {
	var slots acpSlots
	pidFile := filepath.Join(t.TempDir(), "child")
	host, _, done, logPath := startACP(t, &slots, "c1",
		`sleep 60 & echo $! > `+pidFile+`; trap 'echo terminated >&2; kill $!; exit 0' TERM; while :; do sleep 0.05; done`)
	waitFile(t, pidFile)
	host.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the agent outlived its stream")
	}
	if b, _ := os.ReadFile(logPath); !strings.Contains(string(b), "terminated") {
		t.Fatalf("log = %q", b)
	}
}

func TestACPKilledAfterGrace(t *testing.T) {
	old := acpKillGrace
	acpKillGrace = 100 * time.Millisecond
	defer func() { acpKillGrace = old }()
	var slots acpSlots
	ready := filepath.Join(t.TempDir(), "ready")
	host, _, done, _ := startACP(t, &slots, "c1", `trap '' TERM; touch `+ready+`; while :; do sleep 0.05; done`)
	waitFile(t, ready)
	host.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("SIGKILL never came")
	}
}

func TestACPNaturalLeaderExitKillsChildHoldingOutput(t *testing.T) {
	old := acpKillGrace
	acpKillGrace = 500 * time.Millisecond
	defer func() { acpKillGrace = old }()

	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	leaderExited := filepath.Join(dir, "leader-exited")
	childReady := filepath.Join(dir, "child-ready")
	childActivity := filepath.Join(dir, "child-activity")
	stopChild := filepath.Join(dir, "stop-child")
	// The leader exits naturally. Its child ignores TERM while retaining both output
	// pipes, so the host-facing copy cannot reach EOF until the monitor kills the group.
	// The stop marker is test cleanup if a failed assertion leaves the child behind.
	script := `(
		trap '' TERM
		touch "` + childReady + `"
		while [ ! -e "` + stopChild + `" ]; do
			printf x >> "` + childActivity + `"
			printf 'child queued output\n'
			printf 'stubborn child still alive\n' >&2
			sleep 0.02
		done
	) &
	trap 'touch "` + leaderExited + `"' EXIT
	printf 'leader queued output\n'
	touch "` + ready + `"
	exit 0`
	cmd := exec.Command("sh", "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var slots acpSlots
	host, guest := net.Pipe()
	defer host.Close()
	hostReader := bufio.NewReader(host)
	done := make(chan error, 1)
	go func() {
		done <- runACP(&slots, guest, bufio.NewReader(guest), cmd, "c1", filepath.Join(dir, "acp.log"))
	}()
	defer func() { _ = os.WriteFile(stopChild, nil, 0o600) }()
	if ack, err := hostReader.ReadString('\n'); err != nil || ack != "{}\n" {
		t.Fatalf("ack = %q, %v", ack, err)
	}
	var output strings.Builder
	readDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(&output, hostReader)
		readDone <- err
	}()
	waitFile(t, ready)
	waitFile(t, childReady)
	waitFile(t, leaderExited)
	waitFile(t, childActivity)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runACP error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runACP remained blocked by the child's inherited stdout/stderr")
	}
	select {
	case err := <-readDone:
		if err != nil {
			t.Fatalf("read host output: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("host output reader did not finish")
	}
	if !strings.Contains(output.String(), "leader queued output\n") || !strings.Contains(output.String(), "child queued output\n") {
		t.Fatalf("queued output was truncated: %q", output.String())
	}
	if cmd.ProcessState == nil {
		t.Fatal("leader was not reaped")
	}
	waitACPGroupStopped(t, cmd.Process.Pid)
	before, err := os.ReadFile(childActivity)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	after, err := os.ReadFile(childActivity)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("child kept running after runACP returned: heartbeat grew from %d to %d bytes", len(before), len(after))
	}
	if len(slots.chats) != 0 {
		t.Fatalf("slot kept after leader exit: %v", slots.chats)
	}
}

// waitACPGroupStopped allows zombies, which cannot run or hold streams, but waits until
// no live process remains in the ACP process group.
func waitACPGroupStopped(t *testing.T, pgid int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		active := false
		entries, err := os.ReadDir("/proc")
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if _, err := strconv.Atoi(entry.Name()); err != nil {
				continue
			}
			stat, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "stat"))
			if err != nil {
				continue
			}
			closeParen := strings.LastIndexByte(string(stat), ')')
			if closeParen < 0 {
				continue
			}
			fields := strings.Fields(string(stat[closeParen+1:]))
			if len(fields) < 3 || fields[0] == "Z" || fields[0] == "X" {
				continue
			}
			group, err := strconv.Atoi(fields[2])
			if err == nil && group == pgid {
				active = true
				break
			}
		}
		if !active {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("live processes remain in ACP process group %d", pgid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A failed host write ends the stream and must stop the process even when the
// host-to-agent read is still blocked and the agent ignores SIGTERM.
func TestACPHostOutputFailureStopsAgent(t *testing.T) {
	old := acpKillGrace
	acpKillGrace = 100 * time.Millisecond
	defer func() { acpKillGrace = old }()

	var slots acpSlots
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command("sh", "-c", `trap '' TERM; while [ ! -e "`+ready+`" ]; do sleep 0.01; done; printf 'agent output\n'; while :; do sleep 0.05; done`)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	peer, guest := net.Pipe()
	defer peer.Close()
	writeErr := errors.New("host output failed")
	st := &failAgentOutputConn{Conn: guest, ack: make(chan []byte, 1), readStarted: make(chan struct{}), err: writeErr}
	logPath := filepath.Join(t.TempDir(), "acp.log")
	done := make(chan error, 1)
	go func() { done <- runACP(&slots, st, bufio.NewReader(st), cmd, "c1", logPath) }()

	select {
	case ack := <-st.ack:
		if string(ack) != "{}\n" {
			t.Fatalf("ack = %q", ack)
		}
	case <-time.After(time.Second):
		t.Fatal("initial acknowledgement was not written")
	}
	select {
	case <-st.readStarted:
	case <-time.After(time.Second):
		t.Fatal("host-to-agent read did not block on the stream")
	}
	if err := os.WriteFile(ready, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, writeErr) {
			t.Fatalf("runACP error = %v, want host output failure", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("agent was not stopped after the host output failed")
	}
	if cmd.ProcessState == nil {
		t.Fatal("agent process was not reaped")
	}
	if len(slots.chats) != 0 {
		t.Fatalf("slot kept after output failure: %v", slots.chats)
	}
}

func TestACPAckFailureStopsAgent(t *testing.T) {
	old := acpKillGrace
	acpKillGrace = 100 * time.Millisecond
	defer func() { acpKillGrace = old }()

	var slots acpSlots
	cmd := exec.Command("sh", "-c", `trap '' TERM; while :; do sleep 0.05; done`)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	peer, guest := net.Pipe()
	defer peer.Close()
	writeErr := errors.New("ack write failed")
	st := failACPWriteConn{Conn: guest, err: writeErr}
	logPath := filepath.Join(t.TempDir(), "acp.log")
	done := make(chan error, 1)
	go func() {
		done <- runACP(&slots, st, bufio.NewReader(st), cmd, "c1", logPath)
	}()
	select {
	case err := <-done:
		if !errors.Is(err, writeErr) {
			t.Fatalf("runACP error = %v, want acknowledgement write failure", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("agent was not stopped after acknowledgement failure")
	}
	if cmd.ProcessState == nil {
		t.Fatal("agent process was not reaped")
	}
	if len(slots.chats) != 0 {
		t.Fatalf("slot kept after acknowledgement failure: %v", slots.chats)
	}
}

// Closing stdout is the end of the ACP stream. Even if the process remains
// alive and the host input direction is blocked, it is stopped after the grace.
func TestACPEOFStopsAgentAndReleasesSlot(t *testing.T) {
	old := acpKillGrace
	acpKillGrace = 100 * time.Millisecond
	defer func() { acpKillGrace = old }()

	var slots acpSlots
	cmd := exec.Command("sh", "-c", `trap '' TERM; exec 1>&-; while :; do sleep 0.05; done`)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	peer, guest := net.Pipe()
	defer peer.Close()
	logPath := filepath.Join(t.TempDir(), "acp.log")
	done := make(chan error, 1)
	go func() { done <- runACP(&slots, guest, bufio.NewReader(guest), cmd, "c1", logPath) }()
	br := bufio.NewReader(peer)
	if ack, err := br.ReadString('\n'); err != nil || ack != "{}\n" {
		t.Fatalf("ack = %q, %v", ack, err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("agent that ignored termination unexpectedly exited cleanly")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("agent remained alive after closing stdout")
	}
	if cmd.ProcessState == nil {
		t.Fatal("agent process was not reaped")
	}
	if len(slots.chats) != 0 {
		t.Fatalf("slot kept after stdout EOF: %v", slots.chats)
	}
}

type failAgentOutputConn struct {
	net.Conn
	mu          sync.Mutex
	w           int
	ack         chan []byte
	readStarted chan struct{}
	readOnce    sync.Once
	err         error
}

type failACPWriteConn struct {
	net.Conn
	err error
}

func (c failACPWriteConn) Write([]byte) (int, error) { return 0, c.err }

func (c *failAgentOutputConn) Read(p []byte) (int, error) {
	c.readOnce.Do(func() { close(c.readStarted) })
	return c.Conn.Read(p)
}

func (c *failAgentOutputConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.w++
	if c.w == 1 {
		c.ack <- append([]byte(nil), p...)
		return len(p), nil
	}
	return 0, c.err
}

func TestACPSlots(t *testing.T) {
	var slots acpSlots
	var hosts []net.Conn
	for _, chat := range []string{"c1", "c2", "c3", "c4"} {
		host, _, _, _ := startACP(t, &slots, chat, `while :; do sleep 0.05; done`)
		hosts = append(hosts, host)
	}
	defer func() {
		for _, h := range hosts {
			h.Close()
		}
	}()
	for chat, want := range map[string]string{"c5": "at most 4 chats", "c2": "already running"} {
		cmd := exec.Command("true")
		host, guest := net.Pipe()
		go func() {
			_ = runACP(&slots, guest, bufio.NewReader(guest), cmd, chat, filepath.Join(t.TempDir(), "log"))
		}()
		line, _ := bufio.NewReader(host).ReadString('\n')
		host.Close()
		if !strings.Contains(line, want) || cmd.Process != nil {
			t.Errorf("%s: reply %q, started %v", chat, line, cmd.Process != nil)
		}
	}
}

func TestCappedWriter(t *testing.T) {
	var b strings.Builder
	w := &cappedWriter{w: &b, left: 5}
	for _, s := range []string{"abc", "defg", "hij"} {
		if n, err := w.Write([]byte(s)); n != len(s) || err != nil {
			t.Fatal(n, err)
		}
	}
	if b.String() != "abcde" {
		t.Fatalf("got %q", b.String())
	}
}

func waitFile(t *testing.T, path string) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s never appeared", path)
}
