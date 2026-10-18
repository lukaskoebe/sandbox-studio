package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/coder/websocket"
	"golang.org/x/term"

	tea "charm.land/bubbletea/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/webauth"
)

// terminalName is the sandbox's tmux session t attaches to: the one the web workspace opens
// first, which it creates when it doesn't exist.
const terminalName = "main"

// detachKey is Ctrl-], which returns from an attached terminal to the TUI.
const detachKey = 0x1d

// resizeEvery is how often the attached terminal's size is checked. Polling works on every
// platform, where a window-change signal would not.
const resizeEvery = 250 * time.Millisecond

// approvalsEvery is how often an attached terminal asks whether an approval of its sandbox came
// in, and quietFor how long the sandbox's output must pause before the title is set, so that it
// never lands in the middle of the sandbox's own escape sequences.
const (
	approvalsEvery = 2 * time.Second
	quietFor       = 150 * time.Millisecond
)

// attachDone ends an attachment: err is why the terminal could not be shown, or nil when the
// user detached. waiting is an approval of the sandbox that came in while it was attached and
// still waited when the user detached.
type attachDone struct {
	id      string // the sandbox's
	sandbox string
	name    string // the terminal's
	waiting string
	err     error
}

// attachment is a sandbox's terminal, shown in this terminal. The TUI hands the screen, the keys
// and the resizes to the sandbox until the user presses Ctrl-]. It is a tea.ExecCommand, so
// Bubble Tea releases the terminal while it runs and takes it back afterwards.
type attachment struct {
	ctx     context.Context
	client  *Client
	env     string
	sandbox string
	name    string    // the tmux session
	in      io.Reader // the terminal's input, given by Bubble Tea
	out     io.Writer // the terminal's output, given by Bubble Tea
	// size is the terminal's size when it is not a terminal itself, such as in tests.
	size func() (cols, rows uint16)
	// watch has the attachment tell of the sandbox's new approvals, as the TUI's does.
	watch bool

	mu      sync.Mutex
	lastOut time.Time // when the sandbox last wrote to the terminal
	waiting string    // a new approval that waits, by its ID
}

func (a *attachment) SetStdin(r io.Reader)  { a.in = r }
func (a *attachment) SetStdout(w io.Writer) { a.out = w }
func (a *attachment) SetStderr(io.Writer)   {}

// Run shows the terminal until the user detaches. The sandbox's end of the terminal is shown
// too, and then any key returns; that key is not sent to the sandbox.
func (a *attachment) Run() error {
	fd, isTerm := terminalFD(a.in)
	cols, rows := a.terminalSize(fd, isTerm)
	conn, err := a.client.Attach(a.ctx, a.env, a.sandbox, firstNonEmpty(a.name, terminalName), cols, rows)
	if err != nil {
		return err
	}
	if isTerm {
		state, err := term.MakeRaw(fd)
		if err != nil {
			conn.CloseNow()
			return err
		}
		defer func() { _ = term.Restore(fd, state) }()
	}
	ctx, cancel := context.WithCancel(a.ctx)
	defer cancel()
	if a.watch {
		watchCtx, stop := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			defer close(done)
			a.watchApprovals(watchCtx)
		}()
		defer func() { // before the terminal is restored and handed back
			stop()
			<-done
		}()
	}

	// The sandbox's output goes to the terminal. When the sandbox ends the terminal, that is
	// shown, and the next key returns (the TUI doesn't send it on, since nothing is listening).
	detaching := make(chan struct{})
	ended := make(chan struct{})
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		defer close(ended)
		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				select {
				case <-detaching:
				default:
					fmt.Fprint(a.out, "\r\n[the terminal ended; press any key to return]")
				}
				return
			}
			if typ == websocket.MessageBinary {
				a.mu.Lock()
				_, _ = a.out.Write(data)
				a.lastOut = time.Now()
				a.mu.Unlock()
			}
		}
	}()
	go a.followSize(ctx, conn, fd, isTerm)

	// The keys go to the sandbox, except Ctrl-], which detaches.
	buf := make([]byte, 1024)
	for {
		n, err := a.in.Read(buf)
		select {
		case <-ended:
			detach(conn, detaching, readerDone)
			return nil
		default:
		}
		if err != nil {
			detach(conn, detaching, readerDone)
			return err
		}
		chunk := buf[:n]
		if i := bytes.IndexByte(chunk, detachKey); i >= 0 {
			if i > 0 {
				_ = conn.Write(ctx, websocket.MessageBinary, chunk[:i]) // detaching either way
			}
			detach(conn, detaching, readerDone)
			return nil
		}
		if err := conn.Write(ctx, websocket.MessageBinary, chunk); err != nil {
			detach(conn, detaching, readerDone)
			return errors.New("the terminal connection was lost")
		}
	}
}

// watchApprovals asks for the pending approvals every approvalsEvery while the terminal is
// attached. While one that came in since waits for the sandbox, the terminal's title names it and
// says how to answer it, the bell rings once for each, and the detach is told of it; the title
// it replaced comes back when none waits, or when the terminal is let go. A chat's permission
// requests are left to the chat.
func (a *attachment) watchApprovals(ctx context.Context) {
	initial := map[string]bool{} // what waited already when the terminal was attached
	rung := map[string]bool{}
	first := true
	shown, want := "", "" // the title set, and the one to set
	bell := false
	setTitle := func(force bool) { // only while the sandbox's output pauses
		a.mu.Lock()
		defer a.mu.Unlock()
		if want == shown || (!force && time.Since(a.lastOut) < quietFor) {
			return
		}
		switch {
		case shown == "":
			fmt.Fprint(a.out, "\x1b[22;0t") // keep the title to bring back
		case want == "":
			fmt.Fprint(a.out, "\x1b[23;0t") // the title kept
		}
		if want != "" {
			fmt.Fprint(a.out, "\x1b]2;"+want+"\a")
		}
		if bell {
			fmt.Fprint(a.out, "\a")
			bell = false
		}
		shown = want
	}
	defer func() {
		want = ""
		setTitle(true)
	}()
	ticker := time.NewTicker(approvalsEvery)
	defer ticker.Stop()
	for {
		rctx, cancel := requestContext(ctx)
		list, err := a.client.PendingApprovals(rctx)
		cancel()
		if err == nil {
			waiting, title := "", ""
			for _, ap := range list { // newest first
				switch {
				case ap.SandboxID != a.sandbox || ap.ChatPermission != nil:
					continue
				case first:
					initial[ap.ID] = true
					continue
				case initial[ap.ID] || waiting != "":
					continue
				}
				waiting = ap.ID
				title = "▲ " + kindTitle(ap.Kind) + " waits · Ctrl-] answers it"
				if isNetwork(ap) {
					title = "▲ " + firstNonEmpty(approvalSource(ap), "the sandbox") + " wants " + hostOf(ap) + " · Ctrl-] answers it"
				}
				if !rung[ap.ID] {
					rung[ap.ID], bell = true, true
				}
			}
			first = false
			want = title
			a.mu.Lock()
			a.waiting = waiting
			a.mu.Unlock()
		}
		setTitle(false)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// detach ends the connection. The reader is told first, so that the close isn't taken for the
// sandbox ending the terminal; then it is waited for, briefly.
func detach(conn *websocket.Conn, detaching chan struct{}, readerDone <-chan struct{}) {
	close(detaching)
	conn.Close(websocket.StatusNormalClosure, "detached")
	select {
	case <-readerDone:
	case <-time.After(2 * time.Second):
	}
}

// followSize sends the terminal's size to the sandbox whenever it changes.
func (a *attachment) followSize(ctx context.Context, conn *websocket.Conn, fd int, isTerm bool) {
	var lastCols, lastRows uint16
	ticker := time.NewTicker(resizeEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		cols, rows := a.terminalSize(fd, isTerm)
		if cols == lastCols && rows == lastRows {
			continue
		}
		lastCols, lastRows = cols, rows
		msg, _ := json.Marshal(map[string]any{"type": "resize", "cols": cols, "rows": rows})
		if err := conn.Write(ctx, websocket.MessageText, msg); err != nil {
			return
		}
	}
}

// terminalSize is the size of the terminal, or of a. size when there is no terminal.
func (a *attachment) terminalSize(fd int, isTerm bool) (uint16, uint16) {
	if isTerm {
		if cols, rows, err := term.GetSize(fd); err == nil && cols > 0 && rows > 0 {
			return uint16(cols), uint16(rows)
		}
	}
	if a.size != nil {
		return a.size()
	}
	return defaultWidth, defaultHeight
}

// terminalFD is the file descriptor of an input that is a terminal, and whether it is one.
func terminalFD(r io.Reader) (int, bool) {
	f, ok := r.(*os.File)
	if !ok {
		return -1, false
	}
	fd := int(f.Fd())
	return fd, term.IsTerminal(fd)
}

// Attach opens a sandbox's terminal over the attach websocket, at the size given. The session is
// created when it doesn't exist.
func (c *Client) Attach(ctx context.Context, env, id, name string, cols, rows uint16) (*websocket.Conn, error) {
	url := fmt.Sprintf("ws://%s/api/environments/%s/sandboxes/%s/terminals/%s/attach?cols=%d&rows=%d", c.addr, env, id, name, cols, rows)
	header := http.Header{}
	if c.session != "" {
		header.Set("Cookie", (&http.Cookie{Name: webauth.SessionCookie, Value: c.session}).String())
	}
	conn, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		if resp != nil {
			defer resp.Body.Close()
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			return nil, apiError(resp.StatusCode, body)
		}
		return nil, err
	}
	conn.SetReadLimit(1 << 20)
	return conn, nil
}

// AttachTerminal shows a running sandbox's terminal by its name in this one, as the TUI does,
// until the user detaches with Ctrl-] or the sandbox ends the terminal.
func AttachTerminal(ctx context.Context, c *Client, env, sandbox, name string) error {
	return (&attachment{ctx: ctx, client: c, env: env, sandbox: sandbox, name: name, in: os.Stdin, out: os.Stdout}).Run()
}

// attachTo shows a sandbox's terminal by its name. Only a running sandbox has terminals; the
// others say what to do.
func (m *app) attachTo(sb *sandboxes.View, name string) tea.Cmd {
	if sb == nil {
		return nil
	}
	switch phaseOf(*sb) {
	case phaseReady:
	case phaseStopped, phaseSuspended:
		m.setNotice("Start "+sb.Name+" first, with s.", false)
		return nil
	case phaseFailed:
		m.setNotice(sb.Name+" could not be created. Delete it with d.", true)
		return nil
	default:
		m.setNotice(sb.Name+" is still starting.", false)
		return nil
	}
	sandbox, id := sb.Name, sb.ID
	at := &attachment{ctx: m.ctx, client: m.client, env: m.env.ID, sandbox: sb.ID, name: name, watch: true}
	return tea.Exec(at, func(err error) tea.Msg {
		at.mu.Lock()
		defer at.mu.Unlock()
		return attachDone{id: id, sandbox: sandbox, name: name, waiting: at.waiting, err: err}
	})
}
