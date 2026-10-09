// Package agentchan is the host side of the guest-agent channel: one listening
// socket per sandbox, a yamux session per connected guest, and typed calls on top.
package agentchan

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// ErrNotConnected means the sandbox's guest agent has no live session.
var ErrNotConnected = errors.New("guest agent not connected")

// ErrRequestsBusy bounds control requests if a guest is unresponsive.
var ErrRequestsBusy = errors.New("too many pending guest requests")

// Hub tracks guest-agent sessions by sandbox ID.
type Hub struct {
	// OnConnect, if set before the first Listen, runs (in its own goroutine) whenever a
	// guest agent connects.
	OnConnect func(id string)

	log *slog.Logger

	mu        sync.Mutex
	listeners map[string]net.Listener
	sessions  map[string]*conn
	waiters   map[string][]chan struct{}
}

type conn struct {
	sess        *yamux.Session
	hello       agentproto.Hello
	since       time.Time
	exportOpen  chan struct{}
	requestOpen chan struct{}
}

// NewHub returns an empty hub.
func NewHub(log *slog.Logger) *Hub {
	return &Hub{
		log:       log,
		listeners: map[string]net.Listener{},
		sessions:  map[string]*conn{},
		waiters:   map[string][]chan struct{}{},
	}
}

// Listen starts accepting the guest agent of sandbox id on a Unix socket at path.
// It is idempotent: an existing listener for id is kept.
func (h *Hub) Listen(id, path string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.listeners[id]; ok {
		return nil
	}
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	h.listeners[id] = ln
	go h.accept(id, ln)
	return nil
}

// Close stops listening for id and drops its session.
func (h *Hub) Close(id string) {
	h.mu.Lock()
	ln := h.listeners[id]
	c := h.sessions[id]
	delete(h.listeners, id)
	delete(h.sessions, id)
	h.mu.Unlock()
	if ln != nil {
		ln.Close()
	}
	if c != nil {
		c.sess.Close()
	}
}

func (h *Hub) accept(id string, ln net.Listener) {
	for {
		nc, err := ln.Accept()
		if err != nil {
			return
		}
		go h.serve(id, nc)
	}
}

func (h *Hub) serve(id string, nc net.Conn) {
	sess, err := yamux.Server(nc, yamux.DefaultConfig())
	if err != nil {
		nc.Close()
		return
	}
	// The guest opens a hello stream first.
	st, err := sess.AcceptStreamWithContext(timeoutCtx(10 * time.Second))
	if err != nil {
		sess.Close()
		return
	}
	br := bufio.NewReader(st)
	var hdr agentproto.Header
	var hello agentproto.Hello
	if err := agentproto.ReadJSONLine(br, &hdr); err != nil || hdr.Kind != agentproto.KindHello ||
		agentproto.ReadJSONLine(br, &hello) != nil {
		h.log.Warn("guest agent sent no hello", "sandbox", id)
		sess.Close()
		return
	}
	st.Close()

	c := &conn{sess: sess, hello: hello, since: time.Now(), exportOpen: make(chan struct{}, 1), requestOpen: make(chan struct{}, 32)}
	h.mu.Lock()
	old := h.sessions[id]
	if old != nil && !old.sess.IsClosed() {
		h.mu.Unlock()
		h.log.Debug("rejected duplicate guest agent connection while current session is live", "sandbox", id)
		_ = sess.Close()
		// The guest retries with capped backoff. If the old session has not yet
		// reported closed here, the newcomer is rejected and recovery waits for
		// a later retry after the old session's closure is observed.
		return
	}
	h.sessions[id] = c
	waiters := h.waiters[id]
	delete(h.waiters, id)
	h.mu.Unlock()
	if old != nil {
		old.sess.Close()
	}
	for _, w := range waiters {
		close(w)
	}
	h.log.Info("guest agent connected", "sandbox", id, "version", hello.Version, "arch", hello.Arch)
	if h.OnConnect != nil {
		go h.OnConnect(id)
	}

	<-sess.CloseChan()
	h.mu.Lock()
	if h.sessions[id] == c {
		delete(h.sessions, id)
	}
	h.mu.Unlock()
	h.log.Info("guest agent disconnected", "sandbox", id)
}

// Connected reports whether the guest agent of id is connected, and its hello.
func (h *Hub) Connected(id string) (agentproto.Hello, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	c, ok := h.sessions[id]
	if !ok {
		return agentproto.Hello{}, false
	}
	return c.hello, true
}

// WaitConnected blocks until the guest agent of id connects or ctx ends.
func (h *Hub) WaitConnected(ctx context.Context, id string) error {
	h.mu.Lock()
	if _, ok := h.sessions[id]; ok {
		h.mu.Unlock()
		return nil
	}
	ch := make(chan struct{})
	h.waiters[id] = append(h.waiters[id], ch)
	h.mu.Unlock()
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *Hub) open(ctx context.Context, id string, hdr agentproto.Header) (net.Conn, error) {
	h.mu.Lock()
	c, ok := h.sessions[id]
	h.mu.Unlock()
	if !ok {
		return nil, ErrNotConnected
	}
	st, err := c.sess.Open()
	if err != nil {
		return nil, err
	}
	if dl, ok := ctx.Deadline(); ok {
		st.SetWriteDeadline(dl)
	}
	if err := agentproto.WriteJSONLine(st, hdr); err != nil {
		st.Close()
		return nil, err
	}
	st.SetWriteDeadline(time.Time{})
	return st, nil
}

// request opens a stream, sends hdr and body (unless nil) and decodes the single JSON
// reply into v.
func (h *Hub) request(ctx context.Context, id string, hdr agentproto.Header, body, v any) (retErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	h.mu.Lock()
	c, ok := h.sessions[id]
	h.mu.Unlock()
	if !ok {
		return ErrNotConnected
	}
	st, release, err := openLimitedStream(ctx, c.requestOpen, c.sess.Open, ErrRequestsBusy)
	if err != nil {
		return err
	}
	defer release()
	defer st.Close()
	if dl, ok := ctx.Deadline(); ok {
		st.SetDeadline(dl)
	}
	// A caller can cancel before its deadline, including while waiting for a
	// configuration ACK. Yamux Close is a half-close, so also expire reads.
	// Both operations affect only this stream, leaving terminals connected.
	stopCancel := context.AfterFunc(ctx, func() {
		_ = st.SetDeadline(time.Now())
		_ = st.Close()
	})
	defer stopCancel()
	defer func() {
		if err := ctx.Err(); err != nil {
			retErr = err
		}
	}()
	if err := agentproto.WriteJSONLine(st, hdr); err != nil {
		return err
	}
	if body != nil {
		if err := agentproto.WriteJSONLine(st, body); err != nil {
			return err
		}
	}
	const maxReplyBytes = 1 << 20
	line, err := bufio.NewReader(io.LimitReader(st, maxReplyBytes+1)).ReadBytes('\n')
	if len(line) > maxReplyBytes {
		return errors.New("guest control reply exceeds 1 MiB")
	}
	if err != nil {
		return err
	}
	var e agentproto.Error
	if json.Unmarshal(line, &e) == nil && e.Error != "" {
		return errors.New(e.Error)
	}
	return json.Unmarshal(line, v)
}

// Sessions lists the guest's tmux sessions.
func (h *Hub) Sessions(ctx context.Context, id string) ([]agentproto.Session, error) {
	var out []agentproto.Session
	err := h.request(ctx, id, agentproto.Header{Kind: agentproto.KindSessions}, nil, &out)
	return out, err
}

// KillSession ends a tmux session in the guest, closing its terminals.
func (h *Hub) KillSession(ctx context.Context, id, name string) error {
	var ok struct{}
	return h.request(ctx, id, agentproto.Header{Kind: agentproto.KindKill, Session: name}, nil, &ok)
}

// Ports lists TCP ports listening in the guest.
func (h *Hub) Ports(ctx context.Context, id string) ([]agentproto.Port, error) {
	var out []agentproto.Port
	err := h.request(ctx, id, agentproto.Header{Kind: agentproto.KindPorts}, nil, &out)
	return out, err
}

// Configure sends a sandbox its environment's configuration.
func (h *Hub) Configure(ctx context.Context, id string, cfg agentproto.Config) error {
	var ok struct{}
	return h.request(ctx, id, agentproto.Header{Kind: agentproto.KindConfig}, cfg, &ok)
}

// DialTCP connects to a loopback port inside the guest.
func (h *Hub) DialTCP(ctx context.Context, id string, port int) (net.Conn, error) {
	if port <= 0 || port > 65535 {
		return nil, fmt.Errorf("invalid port %s", strconv.Itoa(port))
	}
	return h.open(ctx, id, agentproto.Header{Kind: agentproto.KindTCP, Port: port})
}

// PTY is an attached terminal: Read returns output, Write sends input.
type PTY struct {
	st   net.Conn
	wmu  sync.Mutex
	pend []byte
	exit chan string
}

// OpenPTY attaches to (or creates) the tmux session name in the guest.
func (h *Hub) OpenPTY(ctx context.Context, id, name string, cols, rows uint16) (*PTY, error) {
	st, err := h.open(ctx, id, agentproto.Header{Kind: agentproto.KindPTY, Session: name, Cols: cols, Rows: rows})
	if err != nil {
		return nil, err
	}
	return &PTY{st: st, exit: make(chan string, 1)}, nil
}

// Read returns terminal output. It returns io.EOF after the shell exits.
func (p *PTY) Read(b []byte) (int, error) {
	for len(p.pend) == 0 {
		typ, payload, err := agentproto.ReadFrame(p.st)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return 0, fmt.Errorf("terminal channel ended without exit frame: %w", io.ErrUnexpectedEOF)
			}
			return 0, err
		}
		switch typ {
		case agentproto.FrameData:
			p.pend = payload
		case agentproto.FrameExit:
			select {
			case p.exit <- string(payload):
			default:
			}
			return 0, io.EOF
		}
	}
	n := copy(b, p.pend)
	p.pend = p.pend[n:]
	return n, nil
}

// Write sends terminal input.
func (p *PTY) Write(b []byte) (int, error) {
	p.wmu.Lock()
	defer p.wmu.Unlock()
	if err := agentproto.WriteFrame(p.st, agentproto.FrameData, b); err != nil {
		return 0, err
	}
	return len(b), nil
}

// Resize changes the terminal size.
func (p *PTY) Resize(cols, rows uint16) error {
	b, _ := json.Marshal(agentproto.Resize{Cols: cols, Rows: rows})
	p.wmu.Lock()
	defer p.wmu.Unlock()
	return agentproto.WriteFrame(p.st, agentproto.FrameResize, b)
}

// Close detaches; the tmux session keeps running in the guest.
func (p *PTY) Close() error { return p.st.Close() }

func timeoutCtx(d time.Duration) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	go func() {
		<-ctx.Done()
		cancel()
	}()
	return ctx
}
