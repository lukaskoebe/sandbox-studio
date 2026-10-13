package tui

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
)

// syncBuffer is a buffer the attachment and the test write and read from different goroutines.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// waitFor polls until cond holds, failing the test after a few seconds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// fakeTerminal serves the attach websocket of one sandbox with the given behavior, and returns a
// client that reaches it.
func fakeTerminal(t *testing.T, serve func(context.Context, *websocket.Conn, *http.Request)) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		serve(r.Context(), c, r)
	}))
	t.Cleanup(srv.Close)
	return &Client{addr: strings.TrimPrefix(srv.URL, "http://"), session: "test"}
}

// attach starts a terminal attachment whose input is a pipe, and returns the pipe's writer, the
// output and the result of the run.
func attach(t *testing.T, client *Client, size func() (uint16, uint16)) (io.WriteCloser, *syncBuffer, <-chan error) {
	t.Helper()
	in, pw := io.Pipe()
	out := &syncBuffer{}
	a := &attachment{ctx: context.Background(), client: client, env: "env1", sandbox: "sb1", in: in, out: out, size: size}
	done := make(chan error, 1)
	go func() { done <- a.Run() }()
	t.Cleanup(func() { pw.Close() })
	return pw, out, done
}

func fixedSize(cols, rows uint16) func() (uint16, uint16) {
	return func() (uint16, uint16) { return cols, rows }
}

func TestAttachShowsTheSandboxAndDetachesWithCtrlBracket(t *testing.T) {
	var mu sync.Mutex
	var got bytes.Buffer
	client := fakeTerminal(t, func(ctx context.Context, c *websocket.Conn, _ *http.Request) {
		c.Write(ctx, websocket.MessageBinary, []byte("hello from the sandbox"))
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			if typ == websocket.MessageBinary {
				mu.Lock()
				got.Write(data)
				mu.Unlock()
				c.Write(ctx, websocket.MessageBinary, data) // the shell echoes
			}
		}
	})
	pw, out, done := attach(t, client, fixedSize(80, 24))
	waitFor(t, "the sandbox's output", func() bool { return strings.Contains(out.String(), "hello from the sandbox") })

	if _, err := pw.Write([]byte("ls\r")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the echo", func() bool { return strings.Contains(out.String(), "ls\r") })
	if _, err := pw.Write([]byte{detachKey}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("detach: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Ctrl-] did not detach")
	}
	mu.Lock()
	defer mu.Unlock()
	if got.String() != "ls\r" {
		t.Fatalf("the sandbox received %q; Ctrl-] must not reach it", got.String())
	}
}

func TestAttachForwardsResizes(t *testing.T) {
	sizes := make(chan string, 4)
	query := make(chan string, 1)
	client := fakeTerminal(t, func(ctx context.Context, c *websocket.Conn, r *http.Request) {
		query <- r.URL.RawQuery + "|" + r.Header.Get("Cookie")
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			if typ == websocket.MessageText {
				sizes <- string(data)
			}
		}
	})
	var calls atomic.Int32
	size := func() (uint16, uint16) {
		if calls.Add(1) == 1 {
			return 80, 24
		}
		return 120, 40
	}
	pw, _, done := attach(t, client, size)
	if q := <-query; q != "cols=80&rows=24|studio_session=test" {
		t.Fatalf("attach size and session: %q", q)
	}
	select {
	case msg := <-sizes:
		if msg != `{"cols":120,"rows":40,"type":"resize"}` {
			t.Fatalf("resize message: %s", msg)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the size change was not sent")
	}
	pw.Write([]byte{detachKey})
	<-done
}

// When the sandbox ends the terminal, the end is shown, and the next key returns to the TUI
// without going to the sandbox.
func TestAttachEndsWithTheSandboxAndAKeyReturns(t *testing.T) {
	client := fakeTerminal(t, func(ctx context.Context, c *websocket.Conn, _ *http.Request) {
		c.Write(ctx, websocket.MessageBinary, []byte("bye"))
		c.Close(websocket.StatusNormalClosure, "exited")
	})
	pw, out, done := attach(t, client, fixedSize(80, 24))
	waitFor(t, "the end of the terminal", func() bool {
		return strings.Contains(out.String(), "bye") && strings.Contains(out.String(), "[the terminal ended")
	})
	if _, err := pw.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("return after the end: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a key did not return after the end")
	}
}

func TestAttachRefusesASandboxThatIsNotRunning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "the sandbox is not running or still booting", http.StatusConflict)
	}))
	t.Cleanup(srv.Close)
	client := &Client{addr: strings.TrimPrefix(srv.URL, "http://"), session: "test"}
	a := &attachment{ctx: context.Background(), client: client, env: "env1", sandbox: "sb1", in: strings.NewReader(""), out: &syncBuffer{}, size: fixedSize(80, 24)}
	err := a.Run()
	if err == nil || !strings.Contains(err.Error(), "not running or still booting") {
		t.Fatalf("refusal: %v", err)
	}
}

func TestAttachOnlyStartsForARunningSandbox(t *testing.T) {
	m := sampleApp()
	m.cursor = 2 // docs-site, stopped
	if cmd := m.attachTo(m.selected()); cmd != nil || m.notice.text != "Start docs-site first, with s." {
		t.Fatalf("stopped sandbox: command %v, notice %q", cmd != nil, m.notice.text)
	}
	m.cursor = 0 // blub, running with an agent
	if cmd := m.attachTo(m.selected()); cmd == nil {
		t.Fatal("running sandbox did not start a terminal")
	}
	m.sandboxes[0].Status = runtime.Status("running")
	m.sandboxes[0].Agent = nil
	m.cursor = 0
	if cmd := m.attachTo(m.selected()); cmd != nil || m.notice.text != "blub is still starting." {
		t.Fatalf("starting sandbox: command %v, notice %q", cmd != nil, m.notice.text)
	}
}
