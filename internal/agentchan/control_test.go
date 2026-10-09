package agentchan

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

const controlTestTimeout = 5 * time.Second

func TestRequestRejectsOversizedRepliesWithoutDroppingSession(t *testing.T) {
	tests := []struct {
		name  string
		reply []byte
	}{
		{"unterminated", bytes.Repeat([]byte{'x'}, (1<<20)+1)},
		{"newline terminated", append(bytes.Repeat([]byte{'x'}, 1<<20), '\n')},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newControlTestHarness(t)
			result := h.sessionsAsync()
			call := h.nextCall(t)
			call.reply <- tt.reply
			got := h.nextResult(t, result)
			if got.err == nil || !strings.Contains(got.err.Error(), "exceeds 1 MiB") {
				t.Fatalf("oversized reply error = %v, want size-limit error", got.err)
			}
			if _, ok := h.hub.Connected(h.id); !ok {
				t.Fatal("oversized control reply dropped the guest session")
			}

			good := h.sessionsAsync()
			goodCall := h.nextCall(t)
			goodCall.reply <- []byte("[]\n")
			if got := h.nextResult(t, good); got.err != nil || len(got.sessions) != 0 {
				t.Fatalf("valid request after oversized reply = %+v", got)
			}
		})
	}
}

func TestRequestGateRejectsThirtyThirdHeldRequestAndRecovers(t *testing.T) {
	h := newControlTestHarness(t)
	results := make([]<-chan sessionsResult, 32)
	calls := make([]*controlCall, 32)
	for i := range results {
		results[i] = h.sessionsAsync()
	}
	for i := range calls {
		calls[i] = h.nextCall(t)
		if calls[i].header.Kind != agentproto.KindSessions {
			t.Fatalf("held request %d kind = %q, want %q", i, calls[i].header.Kind, agentproto.KindSessions)
		}
	}

	if _, err := h.hub.Sessions(h.ctx, h.id); !errors.Is(err, ErrRequestsBusy) {
		t.Fatalf("33rd pending Sessions error = %v, want ErrRequestsBusy", err)
	}
	for _, call := range calls {
		call.reply <- []byte("[]\n")
	}
	for i, result := range results {
		if got := h.nextResult(t, result); got.err != nil {
			t.Fatalf("released request %d: %v", i, got.err)
		}
	}

	good := h.sessionsAsync()
	goodCall := h.nextCall(t)
	goodCall.reply <- []byte("[]\n")
	if got := h.nextResult(t, good); got.err != nil {
		t.Fatalf("request after gate release: %v", got.err)
	}
}

type sessionsResult struct {
	sessions []agentproto.Session
	err      error
}

type controlCall struct {
	header agentproto.Header
	reply  chan []byte
}

type controlFakeGuest struct {
	session    *yamux.Session
	calls      chan *controlCall
	acceptDone chan struct{}
	handlers   sync.WaitGroup
}

type controlTestHarness struct {
	hub      *Hub
	guest    *controlFakeGuest
	id       string
	ctx      context.Context
	cancel   context.CancelFunc
	requests sync.WaitGroup
}

func newControlTestHarness(t *testing.T) *controlTestHarness {
	t.Helper()
	dir, err := os.MkdirTemp("", "ac-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	hub, id := NewHub(slog.New(slog.NewTextHandler(io.Discard, nil))), "control"
	socket := filepath.Join(dir, "s")
	if err := hub.Listen(id, socket); err != nil {
		t.Fatalf("listen for fake guest: %v", err)
	}
	t.Cleanup(func() { hub.Close(id) })
	nc, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatalf("dial fake guest socket: %v", err)
	}
	session, err := yamux.Client(nc, nil)
	if err != nil {
		_ = nc.Close()
		t.Fatalf("start fake guest yamux: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), controlTestTimeout)
	h := &controlTestHarness{
		hub: hub, id: id, ctx: ctx, cancel: cancel,
		guest: &controlFakeGuest{session: session, calls: make(chan *controlCall, 64), acceptDone: make(chan struct{})},
	}
	go h.guest.accept()
	t.Cleanup(func() { h.cleanup(t) })
	h.hello(t)
	if err := hub.WaitConnected(ctx, id); err != nil {
		t.Fatalf("wait for fake guest connection: %v", err)
	}
	return h
}

func (h *controlTestHarness) hello(t *testing.T) {
	t.Helper()
	stream, err := h.guest.session.Open()
	if err != nil {
		t.Fatalf("open fake guest hello stream: %v", err)
	}
	if err := agentproto.WriteJSONLine(stream, agentproto.Header{Kind: agentproto.KindHello}); err != nil {
		t.Fatalf("write fake guest hello header: %v", err)
	}
	if err := agentproto.WriteJSONLine(stream, agentproto.Hello{Version: "test", Arch: "amd64"}); err != nil {
		t.Fatalf("write fake guest hello: %v", err)
	}
	_ = stream.Close()
}

func (h *controlTestHarness) cleanup(t *testing.T) {
	h.cancel()
	_ = h.guest.session.Close()
	h.hub.Close(h.id)
	done := make(chan struct{})
	go func() {
		<-h.guest.acceptDone
		h.guest.handlers.Wait()
		h.requests.Wait()
		close(done)
	}()
	timer := time.NewTimer(controlTestTimeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		t.Errorf("fake guest handlers or Hub requests did not stop after session close")
	}
}

func (g *controlFakeGuest) accept() {
	defer close(g.acceptDone)
	for {
		stream, err := g.session.Accept()
		if err != nil {
			return
		}
		g.handlers.Add(1)
		go func() {
			defer g.handlers.Done()
			g.handle(stream)
		}()
	}
}

func (g *controlFakeGuest) handle(stream net.Conn) {
	defer stream.Close()
	var header agentproto.Header
	if err := agentproto.ReadJSONLine(bufio.NewReader(stream), &header); err != nil {
		return
	}
	call := &controlCall{header: header, reply: make(chan []byte, 1)}
	select {
	case g.calls <- call:
	case <-g.session.CloseChan():
		return
	}
	select {
	case reply := <-call.reply:
		_, _ = io.Copy(stream, bytes.NewReader(reply))
	case <-g.session.CloseChan():
	}
}

func (h *controlTestHarness) sessionsAsync() <-chan sessionsResult {
	result := make(chan sessionsResult, 1)
	h.requests.Add(1)
	go func() {
		defer h.requests.Done()
		got, err := h.hub.Sessions(h.ctx, h.id)
		result <- sessionsResult{sessions: got, err: err}
	}()
	return result
}

func (h *controlTestHarness) nextCall(t *testing.T) *controlCall {
	t.Helper()
	select {
	case call := <-h.guest.calls:
		return call
	case <-h.ctx.Done():
		t.Fatalf("timed out waiting for guest control request: %v", h.ctx.Err())
		return nil
	}
}

func (h *controlTestHarness) nextResult(t *testing.T, result <-chan sessionsResult) sessionsResult {
	t.Helper()
	select {
	case got := <-result:
		return got
	case <-h.ctx.Done():
		t.Fatalf("timed out waiting for Hub.Sessions: %v", h.ctx.Err())
		return sessionsResult{}
	}
}
