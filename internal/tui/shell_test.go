package tui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/lukaskoebe/sandbox-studio/internal/api"
	"github.com/lukaskoebe/sandbox-studio/internal/policy"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// fakeShellStudio serves one sandbox's terminal, its pending approvals and their decisions.
type fakeShellStudio struct {
	mu        sync.Mutex
	pending   []api.ApprovalView
	decided   map[string]Decision
	attaches  int
	connected chan struct{}
}

func newFakeShellStudio(t *testing.T) (*fakeShellStudio, *Client) {
	t.Helper()
	f := &fakeShellStudio{decided: map[string]Decision{}, connected: make(chan struct{}, 8)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/attach"):
			c, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer c.CloseNow()
			f.mu.Lock()
			f.attaches++
			f.mu.Unlock()
			f.connected <- struct{}{}
			_ = c.Write(r.Context(), websocket.MessageBinary, []byte("codex is ready\r\n"))
			for {
				if _, _, err := c.Read(r.Context()); err != nil {
					return
				}
			}
		case r.URL.Path == "/api/approvals":
			f.mu.Lock()
			defer f.mu.Unlock()
			_ = json.NewEncoder(w).Encode(f.pending)
		case strings.HasSuffix(r.URL.Path, "/decide"):
			var d Decision
			_ = json.NewDecoder(r.Body).Decode(&d)
			id := strings.Split(r.URL.Path, "/")[5]
			f.mu.Lock()
			f.decided[id] = d
			f.pending = nil
			f.mu.Unlock()
			_ = json.NewEncoder(w).Encode(api.ApprovalView{})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return f, &Client{addr: strings.TrimPrefix(srv.URL, "http://"), session: "test", http: srv.Client()}
}

// request has the sandbox's agent ask for a host.
func (f *fakeShellStudio) request(id, host string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pending = []api.ApprovalView{{
		Approval: store.Approval{ID: id, EnvironmentID: "env1", SandboxID: "sb1", Kind: "network", Status: "pending"},
		Network:  &policy.NetworkRequest{Host: host, Port: 443},
	}}
}

func (f *fakeShellStudio) decision(id string) (Decision, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.decided[id]
	return d, ok
}

func TestShellAnswersAnApprovalAndAttachesAgain(t *testing.T) {
	f, client := newFakeShellStudio(t)
	in, keys := io.Pipe()
	t.Cleanup(func() { keys.Close() })
	out := &syncBuffer{}
	done := make(chan error, 1)
	go func() { done <- Shell(context.Background(), client, "env1", "sb1", "codex", in, out) }()
	<-f.connected
	waitFor(t, "the terminal", func() bool { return strings.Contains(out.String(), "codex is ready") })

	// A request that comes in while attached names itself in the title, and Ctrl-] asks about it.
	f.request("ap1", "proxy.golang.org")
	waitFor(t, "the title", func() bool { return strings.Contains(out.String(), "wants proxy.golang.org:443 · Ctrl-] answers it") })
	if _, err := keys.Write([]byte{detachKey}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the question", func() bool { return strings.Contains(out.String(), "o  allow once") })
	if _, err := keys.Write([]byte("o")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the decision", func() bool { _, ok := f.decision("ap1"); return ok })
	if d, _ := f.decision("ap1"); d.Action != "allow_once" {
		t.Errorf("o decided %+v", d)
	}
	select {
	case <-f.connected: // attached again
	case <-time.After(5 * time.Second):
		t.Fatal("the terminal was not attached again")
	}

	// q at the question detaches, and leaves the approval waiting.
	f.request("ap2", "registry.npmjs.org")
	waitFor(t, "the second title", func() bool { return strings.Contains(out.String(), "wants registry.npmjs.org:443") })
	if _, err := keys.Write([]byte{detachKey}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the second question", func() bool { return strings.Count(out.String(), "o  allow once") == 2 })
	if _, err := keys.Write([]byte("q")); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("detach: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("q did not detach")
	}
	if _, ok := f.decision("ap2"); ok {
		t.Error("q decided the approval")
	}
}

func TestShellDetachesWhenNothingWaits(t *testing.T) {
	f, client := newFakeShellStudio(t)
	in, keys := io.Pipe()
	t.Cleanup(func() { keys.Close() })
	out := &syncBuffer{}
	done := make(chan error, 1)
	go func() { done <- Shell(context.Background(), client, "env1", "sb1", "main", in, out) }()
	<-f.connected
	if _, err := keys.Write([]byte{detachKey}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Ctrl-] did not detach")
	}
	if strings.Contains(out.String(), "allow once") {
		t.Errorf("asked about nothing:\n%s", out.String())
	}
}
