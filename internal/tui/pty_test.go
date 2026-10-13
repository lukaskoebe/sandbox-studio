//go:build !windows

package tui

import (
	"bytes"
	"context"
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
)

// TestTerminalSmoke runs the TUI in a pseudo-terminal, as a child process of this test binary,
// against a test Studio. It shows Home with the sandboxes, and q quits it.
func TestTerminalSmoke(t *testing.T) {
	s := startStudio(t)
	s.addSandbox(t, "blub", runtime.StatusStopped)

	cmd := exec.Command(os.Args[0], "-test.run=^TestTUIChild$")
	cmd.Env = append(os.Environ(), "STUDIO_TUI_CHILD=1", "STUDIO_TUI_ADDR="+s.Addr, "STUDIO_TUI_CONTROL="+s.Control, "TERM=xterm-256color")
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
	for !strings.Contains(seen(), "Sandbox Studio") || !strings.Contains(seen(), "blub") {
		if time.Now().After(deadline) {
			t.Fatalf("Home did not show; the terminal said:\n%q", seen())
		}
		time.Sleep(50 * time.Millisecond)
	}

	if _, err := ptmx.Write([]byte("q")); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the TUI exited with %v; the terminal said:\n%q", err, seen())
		}
	case <-time.After(15 * time.Second):
		cmd.Process.Kill()
		t.Fatal("the TUI did not quit on q")
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
		c.Write(ctx, websocket.MessageBinary, []byte("sandbox ready"))
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
