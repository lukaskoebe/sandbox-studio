//go:build !windows

package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"

	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// TestTerminalSmoke runs the TUI in a pseudo-terminal, as a child process of this test binary,
// against a test Studio. It shows Home with the sandboxes, l opens Activity, a opens the
// approvals, where y allows the waiting host with a rule on the server, and q quits it.
func TestTerminalSmoke(t *testing.T) {
	ctx := context.Background()
	s := startStudio(t)
	sb := s.addSandbox(t, "blub", runtime.StatusRunning)
	payload, _ := json.Marshal(map[string]any{"host": "registry.npmjs.org", "port": 443, "sandboxName": sb.Name, "patterns": []string{"registry.npmjs.org"}})
	if _, _, err := s.Store.RequestApproval(ctx, store.Approval{EnvironmentID: s.Env.ID, SandboxID: sb.ID, Kind: "network", Subject: "registry.npmjs.org:443", Payload: payload}); err != nil {
		t.Fatal(err)
	}
	tui := startTUI(t, s, defaultWidth, defaultHeight)
	tui.waitFor("Home", "Sandbox Studio", "blub")

	// l opens Activity; q returns to Home.
	tui.write("l")
	tui.waitFor("Activity", "Activity")
	tui.write("q")

	// a opens the approvals; y allows the host, which the server keeps as a rule.
	tui.write("a")
	tui.waitFor("the approval", "registry.npmjs.org")
	tui.write("y")
	deadline := time.Now().Add(15 * time.Second)
	for {
		rules, err := s.Store.Rules(ctx, s.Env.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(rules) == 1 && rules[0].Host == "registry.npmjs.org" && rules[0].Action == "allow" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no allow rule after y: %+v; the terminal said:\n%q", rules, tui.seen())
		}
		time.Sleep(50 * time.Millisecond)
	}
	tui.write("q")
	tui.write("q")
	tui.quit()
}

// TestTerminalSmokeWide runs the visual look on a wide 256-color terminal: Home splits into the
// list and the selected sandbox's detail, and a click on the Activity tab opens the log.
func TestTerminalSmokeWide(t *testing.T) {
	s := startStudio(t)
	s.addSandbox(t, "blub", runtime.StatusStopped)
	tui := startTUI(t, s, 140, 40)
	tui.waitFor("the wide Home", "◆", "Studio", "SANDBOXES", "blub", "HISTORY")

	// A click on the tab, in SGR mouse coordinates that count from 1, acts as l.
	x := len([]rune(" ◆ Studio  Default ▾   Sandboxes   Approvals   "))
	tui.write(fmt.Sprintf("\x1b[<0;%d;1M\x1b[<0;%d;1m", x+2, x+2))
	tui.waitFor("Activity", "connections and decisions")
	tui.write("q")
	tui.write("q")
	tui.quit()
}

// ptyTUI is the TUI running in a pseudo-terminal, and what it has drawn so far.
type ptyTUI struct {
	t      *testing.T
	cmd    *exec.Cmd
	ptmx   *os.File
	mu     sync.Mutex
	screen bytes.Buffer
}

// startTUI runs the TUI against s in a pseudo-terminal of cols×rows that says it has 256 colors.
func startTUI(t *testing.T, s *testStudio, cols, rows uint16) *ptyTUI {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestTUIChild$")
	cmd.Env = append(os.Environ(), "STUDIO_TUI_CHILD=1", "STUDIO_TUI_ADDR="+s.Addr, "STUDIO_TUI_CONTROL="+s.Control, "TERM=xterm-256color")
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: rows, Cols: cols})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ptmx.Close() })
	p := &ptyTUI{t: t, cmd: cmd, ptmx: ptmx}
	go func() {
		chunk := make([]byte, 4096)
		for {
			n, err := ptmx.Read(chunk)
			p.mu.Lock()
			p.screen.Write(chunk[:n])
			p.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	return p
}

// seen is all the TUI has written to the terminal.
func (p *ptyTUI) seen() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.screen.String()
}

// write types into the terminal.
func (p *ptyTUI) write(s string) {
	p.t.Helper()
	if _, err := p.ptmx.Write([]byte(s)); err != nil {
		p.t.Fatal(err)
	}
}

// waitFor waits until the terminal has shown each of want.
func (p *ptyTUI) waitFor(what string, want ...string) {
	p.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		all := true
		for _, w := range want {
			all = all && strings.Contains(p.seen(), w)
		}
		if all {
			return
		}
		if time.Now().After(deadline) {
			p.t.Fatalf("%s did not show; the terminal said:\n%q", what, p.seen())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// quit waits for the TUI to exit, after the keys that quit it.
func (p *ptyTUI) quit() {
	p.t.Helper()
	done := make(chan error, 1)
	go func() { done <- p.cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			p.t.Fatalf("the TUI exited with %v; the terminal said:\n%q", err, p.seen())
		}
	case <-time.After(15 * time.Second):
		p.cmd.Process.Kill()
		p.t.Fatal("the TUI did not quit on q")
	}
}

// TestTUIChild is the TUI process of TestTerminalSmoke. It runs only when the parent sets
// STUDIO_TUI_CHILD, and exits with the TUI's result.
func TestTUIChild(t *testing.T) {
	if os.Getenv("STUDIO_TUI_CHILD") != "1" {
		t.Skip("runs as the TUI process of TestTerminalSmoke")
	}
	err := Run(context.Background(), Options{Addr: os.Getenv("STUDIO_TUI_ADDR"), Control: os.Getenv("STUDIO_TUI_CONTROL")})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

// TestAttachInATerminal runs an attachment on a real pseudo-terminal, as the TUI does. Input
// reaches the sandbox without a newline, which only raw mode allows, and Ctrl-] detaches.
func TestAttachInATerminal(t *testing.T) {
	received := make(chan string, 1)
	client := fakeTerminal(t, func(ctx context.Context, c *websocket.Conn, _ *http.Request) {
		_ = c.Write(ctx, websocket.MessageBinary, []byte("sandbox ready"))
		var got bytes.Buffer
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				received <- got.String()
				return
			}
			if typ == websocket.MessageBinary {
				got.Write(data)
			}
		}
	})

	cmd := exec.Command(os.Args[0], "-test.run=^TestAttachChild$")
	cmd.Env = append(os.Environ(), "STUDIO_TUI_ATTACH=1", "STUDIO_TUI_ATTACH_ADDR="+client.addr, "TERM=xterm-256color")
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: defaultHeight, Cols: defaultWidth})
	if err != nil {
		t.Fatal(err)
	}
	defer ptmx.Close()
	var mu sync.Mutex
	var screen bytes.Buffer
	go func() {
		chunk := make([]byte, 4096)
		for {
			n, err := ptmx.Read(chunk)
			mu.Lock()
			screen.Write(chunk[:n])
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	seen := func() string {
		mu.Lock()
		defer mu.Unlock()
		return screen.String()
	}
	deadline := time.Now().Add(15 * time.Second)
	for !strings.Contains(seen(), "sandbox ready") {
		if time.Now().After(deadline) {
			t.Fatalf("the sandbox's output did not reach the terminal: %q", seen())
		}
		time.Sleep(50 * time.Millisecond)
	}

	if _, err := ptmx.Write([]byte("ab")); err != nil {
		t.Fatal(err)
	}
	if _, err := ptmx.Write([]byte{detachKey}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the attachment exited with %v; the terminal said:\n%q", err, seen())
		}
	case <-time.After(15 * time.Second):
		cmd.Process.Kill()
		t.Fatal("Ctrl-] did not detach in the terminal")
	}
	select {
	case got := <-received:
		if got != "ab" {
			t.Fatalf("the sandbox received %q, want %q (raw input, without the Ctrl-])", got, "ab")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the sandbox's end of the terminal was not closed")
	}
}

// TestAttachChild is the attachment process of TestAttachInATerminal: it runs with the pseudo-terminal
// as its terminal, and exits with the attachment's result.
func TestAttachChild(t *testing.T) {
	if os.Getenv("STUDIO_TUI_ATTACH") != "1" {
		t.Skip("runs as the attachment process of TestAttachInATerminal")
	}
	a := &attachment{
		ctx: context.Background(), client: &Client{addr: os.Getenv("STUDIO_TUI_ATTACH_ADDR"), session: "test"},
		env: "env1", sandbox: "sb1", in: os.Stdin, out: os.Stdout,
	}
	if err := a.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}
