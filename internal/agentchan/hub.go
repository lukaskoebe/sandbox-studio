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
	"strconv"
	"sync"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/version"
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
	// OnCall, if set before the first Listen, answers the calls a guest makes on its own
	// (agentproto.KindCall): memory hooks and tools. id is the sandbox whose socket the
	// call came in on, the only identity Studio trusts.
	OnCall func(ctx context.Context, id, method string, params json.RawMessage) (any, error)
	// OnForward, if set before the first Listen, connects a browser VM's forwarded loopback
	// port (agentproto.KindForward) to where it leads. id is the VM whose socket the stream
	// came in on.
	OnForward func(ctx context.Context, id string, port int) (net.Conn, error)

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
	ln, err := listen(path)
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

// Disconnect drops the current session of id but keeps listening, so the guest agent can
// connect again. Its open streams, such as terminals, end.
func (h *Hub) Disconnect(id string) {
	h.mu.Lock()
	c := h.sessions[id]
	delete(h.sessions, id)
	h.mu.Unlock()
	if c != nil {
		c.sess.Close()
	}
}

func (h *Hub) accept(id string, ln net.Listener) {
	for {
		nc, err := ln.Accept()
		if err != nil {
			// A client that hangs up while it is accepted fails Accept on Windows; only a
			// closed listener ends the loop.
			h.mu.Lock()
			open := h.listeners[id] == ln
			h.mu.Unlock()
			if !open || errors.Is(err, net.ErrClosed) {
				return
			}
			time.Sleep(50 * time.Millisecond)
			continue
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

	c := &conn{sess: sess, hello: hello, since: time.Now(), requestOpen: make(chan struct{}, 32)}
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

	h.acceptCalls(id, sess)
	h.mu.Lock()
	if h.sessions[id] == c {
		delete(h.sessions, id)
	}
	h.mu.Unlock()
	h.log.Info("guest agent disconnected", "sandbox", id)
}

// maxGuestCalls bounds the calls one guest has in flight; more wait in the guest's queue.
const maxGuestCalls = 8

// callTimeout bounds one call; hooks give up well before it (agentcall.HookDeadline).
const callTimeout = 2 * time.Minute

// maxGuestForwards bounds the forwarded connections one guest has open; more are refused.
const maxGuestForwards = 64

// acceptCalls serves the streams the guest opens after its hello until the session ends.
func (h *Hub) acceptCalls(id string, sess *yamux.Session) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	slots := make(chan struct{}, maxGuestCalls)
	forwards := make(chan struct{}, maxGuestForwards)
	for {
		st, err := sess.AcceptStream()
		if err != nil {
			return
		}
		select {
		case slots <- struct{}{}:
		case <-sess.CloseChan():
			st.Close()
			return
		}
		go func() {
			held := true
			release := func() {
				if held {
					held = false
					<-slots
				}
			}
			defer release()
			h.serveStream(ctx, id, st, release, forwards)
		}()
	}
}

// serveStream reads the header of a stream the guest opened and serves it. A forward gives
// its call slot back with release once its header is read: it lasts as long as the
// connection, under its own bound.
func (h *Hub) serveStream(ctx context.Context, id string, st net.Conn, release func(), forwards chan struct{}) {
	defer st.Close()
	st.SetReadDeadline(time.Now().Add(10 * time.Second))
	br := bufio.NewReader(st)
	var hdr agentproto.Header
	if err := agentproto.ReadJSONLineLimit(br, &hdr, 4096); err != nil {
		_ = agentproto.WriteJSONLine(st, agentproto.CallReply{Error: "expected a call"})
		return
	}
	switch hdr.Kind {
	case agentproto.KindCall:
		h.serveCall(ctx, id, st, br)
	case agentproto.KindForward:
		release()
		select {
		case forwards <- struct{}{}:
			defer func() { <-forwards }()
		default:
			_ = agentproto.WriteJSONLine(st, agentproto.Error{Error: "too many forwarded connections"})
			return
		}
		h.serveForward(ctx, id, st, br, hdr.Port)
	default:
		_ = agentproto.WriteJSONLine(st, agentproto.CallReply{Error: "expected a call"})
	}
}

func (h *Hub) serveCall(ctx context.Context, id string, st net.Conn, br *bufio.Reader) {
	var call agentproto.Call
	if err := agentproto.ReadJSONLineLimit(br, &call, agentproto.MaxCallLine); err != nil {
		_ = agentproto.WriteJSONLine(st, agentproto.CallReply{Error: "bad call: " + err.Error()})
		return
	}
	st.SetReadDeadline(time.Time{})
	if h.OnCall == nil {
		_ = agentproto.WriteJSONLine(st, agentproto.CallReply{Error: "calls are not available"})
		return
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	result, err := h.OnCall(ctx, id, call.Method, call.Params)
	var reply agentproto.CallReply
	if err != nil {
		reply.Error = err.Error()
	} else if reply.Result, err = json.Marshal(result); err != nil {
		reply = agentproto.CallReply{Error: "encoding the result failed"}
	}
	st.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_ = agentproto.WriteJSONLine(st, reply)
}

// serveForward connects a forwarded port of a browser VM and copies bytes both ways until
// either side ends.
func (h *Hub) serveForward(ctx context.Context, id string, st net.Conn, br *bufio.Reader, port int) {
	st.SetReadDeadline(time.Time{})
	if h.OnForward == nil || port <= 0 || port > 65535 {
		_ = agentproto.WriteJSONLine(st, agentproto.Error{Error: "forwards are not available"})
		return
	}
	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	up, err := h.OnForward(dctx, id, port)
	cancel()
	if err != nil {
		_ = agentproto.WriteJSONLine(st, agentproto.Error{Error: err.Error()})
		return
	}
	defer up.Close()
	if err := agentproto.WriteJSONLine(st, struct{}{}); err != nil {
		return
	}
	splice(st, br, up)
}

// splice copies a's bytes (read through ar, which may hold some already) to b and b's to a,
// until both directions end. A yamux Close only ends the writing side, so each direction
// closes its writer when its reader ends.
func splice(a net.Conn, ar io.Reader, b net.Conn) {
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(b, ar)
		closeWrite(b)
		close(done)
	}()
	_, _ = io.Copy(a, b)
	closeWrite(a)
	<-done
}

func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
		return
	}
	_ = c.Close()
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

// ConnectedIDs returns the sandboxes whose guest agents are connected, in no order.
func (h *Hub) ConnectedIDs() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	ids := make([]string, 0, len(h.sessions))
	for id := range h.sessions {
		ids = append(ids, id)
	}
	return ids
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
func (h *Hub) request(ctx context.Context, id string, hdr agentproto.Header, body, v any) error {
	return h.requestLimit(ctx, id, hdr, body, v, 1<<20)
}

// requestLimit is request with replies of up to maxReplyBytes.
func (h *Hub) requestLimit(ctx context.Context, id string, hdr agentproto.Header, body, v any, maxReplyBytes int) (retErr error) {
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
	line, err := bufio.NewReader(io.LimitReader(st, int64(maxReplyBytes)+1)).ReadBytes('\n')
	if len(line) > maxReplyBytes {
		return fmt.Errorf("guest control reply exceeds %d MiB", maxReplyBytes>>20)
	}
	if err != nil {
		return err
	}
	var e agentproto.Error
	if json.Unmarshal(line, &e) == nil && e.Error != "" {
		if e.Code == agentproto.CodeHarness {
			return HarnessError(e.Error)
		}
		return errors.New(e.Error)
	}
	return json.Unmarshal(line, v)
}

// Sessions lists the guest's tmux sessions, with their times in the host's clock.
func (h *Hub) Sessions(ctx context.Context, id string) ([]agentproto.Session, error) {
	var out []agentproto.Session
	err := h.request(ctx, id, agentproto.Header{Kind: agentproto.KindSessions}, nil, &out)
	now := time.Now().Unix()
	for i := range out {
		out[i] = out[i].InHostClock(now)
	}
	return out, err
}

// KillSession ends a tmux session in the guest, closing its terminals.
func (h *Hub) KillSession(ctx context.Context, id, name string) error {
	var ok struct{}
	return h.request(ctx, id, agentproto.Header{Kind: agentproto.KindKill, Session: name}, nil, &ok)
}

// SetSessionTitle names a tmux session in the guest, or removes its name for "".
func (h *Hub) SetSessionTitle(ctx context.Context, id, name, title string) error {
	var ok struct{}
	return h.request(ctx, id, agentproto.Header{Kind: agentproto.KindTitle, Session: name}, agentproto.SessionTitle{Title: title}, &ok)
}

// Snapshot takes, compares, restores or collects turn snapshots of the guest's workspace.
// The guest keeps them; only the reply's listing and diffs come to the host.
func (h *Hub) Snapshot(ctx context.Context, id string, req agentproto.SnapshotRequest) (agentproto.SnapshotReply, error) {
	var out agentproto.SnapshotReply
	err := h.requestLimit(ctx, id, agentproto.Header{Kind: agentproto.KindSnapshot}, req, &out, agentproto.MaxGuestReply)
	return out, err
}

// Files lists a directory or reads a file of the guest's workspace.
func (h *Hub) Files(ctx context.Context, id string, req agentproto.FilesRequest) (agentproto.FilesReply, error) {
	var out agentproto.FilesReply
	err := h.requestLimit(ctx, id, agentproto.Header{Kind: agentproto.KindFiles}, req, &out, agentproto.MaxGuestReply)
	return out, err
}

// NameTakenError is the guest's refusal of an upload whose name is taken in its folder. The
// message is meant for the user.
type NameTakenError string

func (e NameTakenError) Error() string { return string(e) }

// Upload streams body into a folder of the guest's workspace as req describes. body must hold
// exactly req.Size bytes. The guest replies once the file is in place, under the name it returns.
func (h *Hub) Upload(ctx context.Context, id string, req agentproto.UploadRequest, body io.Reader) (agentproto.UploadReply, error) {
	var out agentproto.UploadReply
	if req.Size < 0 || req.Size > agentproto.MaxUploadBytes {
		return out, fmt.Errorf("a file is at most %d MiB", agentproto.MaxUploadBytes>>20)
	}
	if len(req.Dir) > agentproto.MaxUploadDir || len(req.Name) > agentproto.MaxUploadName {
		return out, errors.New("the folder or the file name is too long")
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	h.mu.Lock()
	c, ok := h.sessions[id]
	h.mu.Unlock()
	if !ok {
		return out, ErrNotConnected
	}
	st, release, err := openLimitedStream(ctx, c.requestOpen, c.sess.Open, ErrRequestsBusy)
	if err != nil {
		return out, err
	}
	defer release()
	defer st.Close()
	if dl, ok := ctx.Deadline(); ok {
		st.SetDeadline(dl)
	}
	stopCancel := context.AfterFunc(ctx, func() {
		_ = st.SetDeadline(time.Now())
		_ = st.Close()
	})
	defer stopCancel()
	kind := agentproto.KindUpload
	if req.Paste {
		kind = agentproto.KindPaste
	}
	if err := agentproto.WriteJSONLine(st, agentproto.Header{Kind: kind}); err != nil {
		return out, err
	}
	if err := agentproto.WriteJSONLine(st, req); err != nil {
		return out, err
	}
	// The guest reads the whole body even when it refuses the file, so this copy completes
	// before any reply.
	if _, err := io.CopyN(st, body, req.Size); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return out, ctxErr
		}
		return out, err
	}
	line, err := bufio.NewReader(io.LimitReader(st, 1<<20+1)).ReadBytes('\n')
	if ctxErr := ctx.Err(); ctxErr != nil {
		return out, ctxErr
	}
	if err != nil {
		return out, err
	}
	var e agentproto.Error
	if json.Unmarshal(line, &e) == nil && e.Error != "" {
		if e.Code == agentproto.CodeExists {
			return out, NameTakenError(e.Error)
		}
		return out, errors.New(e.Error)
	}
	return out, json.Unmarshal(line, &out)
}

// WriteHomeFiles writes harness config files into the terminal user's home in the guest.
func (h *Hub) WriteHomeFiles(ctx context.Context, id string, files []agentproto.HomeFile) error {
	// An older guest would write a Keys file whole over the harness's own, so it gets none.
	if hello, _ := h.Connected(id); hello.Version != version.Version {
		kept := files[:0:0]
		for _, f := range files {
			if !f.Keys {
				kept = append(kept, f)
			}
		}
		if len(kept) == 0 {
			return nil
		}
		files = kept
	}
	var ok struct{}
	return h.request(ctx, id, agentproto.Header{Kind: agentproto.KindHomeFiles}, agentproto.HomeFiles{Files: files}, &ok)
}

// HarnessError is a session start the guest refused because of the harness itself: it is
// not installed or exits right away. The message is meant for the user.
type HarnessError string

func (e HarnessError) Error() string { return string(e) }

// StartSession starts a detached tmux session running an agent harness in the guest.
func (h *Hub) StartSession(ctx context.Context, id string, req agentproto.StartSession) error {
	var ok struct{}
	return h.request(ctx, id, agentproto.Header{Kind: agentproto.KindStartSession}, req, &ok)
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

// Forwards has a browser VM's guest listen on loopback at exactly ports; see
// agentproto.KindForwards.
func (h *Hub) Forwards(ctx context.Context, id string, ports []int) error {
	if len(ports) > agentproto.MaxForwards {
		ports = ports[:agentproto.MaxForwards]
	}
	var ok struct{}
	return h.request(ctx, id, agentproto.Header{Kind: agentproto.KindForwards}, agentproto.Forwards{Ports: ports}, &ok)
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
