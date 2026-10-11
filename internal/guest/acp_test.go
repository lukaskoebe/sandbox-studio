//go:build linux

package guest

import (
	"bufio"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
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
	io.WriteString(host, "ping\n")
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
		go runACP(&slots, guest, bufio.NewReader(guest), cmd, chat, filepath.Join(t.TempDir(), "log"))
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
