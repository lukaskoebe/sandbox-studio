package tui

import (
	"context"
	"encoding/json"
	"image/color"
	"io"
	"strings"
	"sync"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
	"github.com/coder/websocket"

	tea "charm.land/bubbletea/v2"
)

// A live terminal is a sandbox's terminal drawn inside Home's pane: its output is written into a
// terminal emulator, the emulator's screen is drawn into the pane's rows on every frame, and the
// keys go to it while the pane has them. Its connection is the attach websocket the full-screen
// attach uses, so tmux sizes the session to the pane while it shows.

// paneFrame is the shortest time between two redraws a terminal's output asks for: a program that
// prints without pause redraws the TUI 60 times a second, not once per write.
const paneFrame = 16 * time.Millisecond

// liveTerm is a sandbox's terminal, connected, and the emulator that keeps its screen.
type liveTerm struct {
	mu         sync.Mutex
	emu        *vt.Emulator
	conn       *websocket.Conn
	ctx        context.Context
	cancel     context.CancelFunc
	cols, rows int
	dirty      chan struct{} // the screen changed since the last frame
	ended      chan struct{} // the connection ended
	closing    bool          // the TUI closes it, so its end is no news
}

type (
	// paneDue is the end of the wait before the pane connects to what the cursor is on, so that
	// moving the cursor over the list doesn't open every terminal on the way.
	paneDue struct{ gen int }
	// paneOpened is the connection of the pane's terminal, or why it could not be made.
	paneOpened struct {
		gen  int
		term *liveTerm
		err  error
	}
	// paneOutput is output of the pane's terminal, which the next frame draws.
	paneOutput struct{ gen int }
	// paneEnded is the end of the pane's terminal: the sandbox closed it, or the connection was lost.
	paneEnded struct{ gen int }
)

// openTerm connects to a sandbox's terminal at a size and starts copying its output into a new
// emulator, and the emulator's answers (and the keys sent to it) back. light picks the default
// colors the emulator reports when a program asks.
func openTerm(ctx context.Context, client *Client, env, sandbox, name string, cols, rows int, light bool) (*liveTerm, error) {
	cols, rows = max(cols, 2), max(rows, 1)
	conn, err := client.Attach(ctx, env, sandbox, name, uint16(cols), uint16(rows))
	if err != nil {
		return nil, err
	}
	t := newLiveTerm(cols, rows, light)
	t.conn = conn
	t.ctx, t.cancel = context.WithCancel(ctx)
	go t.readOutput()
	go t.writeInput()
	return t, nil
}

// newLiveTerm is a terminal's emulator at a size, not yet connected.
func newLiveTerm(cols, rows int, light bool) *liveTerm {
	emu := vt.NewEmulator(cols, rows)
	fg, bg := color.Color(color.White), color.Color(color.Black)
	if light {
		fg, bg = color.Black, color.White
	}
	emu.SetDefaultForegroundColor(fg)
	emu.SetDefaultBackgroundColor(bg)
	emu.SetDefaultCursorColor(fg)
	return &liveTerm{emu: emu, cols: cols, rows: rows, dirty: make(chan struct{}, 1), ended: make(chan struct{})}
}

// readOutput writes the terminal's output into the emulator until the connection ends.
func (t *liveTerm) readOutput() {
	defer close(t.ended)
	for {
		typ, data, err := t.conn.Read(t.ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageBinary {
			continue
		}
		t.write(data)
	}
}

// write writes output into the emulator, and marks the screen changed.
func (t *liveTerm) write(data []byte) {
	t.mu.Lock()
	_, _ = t.emu.Write(data)
	t.mu.Unlock()
	select {
	case t.dirty <- struct{}{}:
	default:
	}
}

// writeInput sends what the emulator has for the terminal (the keys sent to it, and its answers to
// the program's queries) to the sandbox, until the emulator's input is closed. A write that fails
// is dropped: the reader sees the connection end.
func (t *liveTerm) writeInput() {
	buf := make([]byte, 4096)
	for {
		n, err := t.emu.Read(buf)
		if err != nil {
			return
		}
		if n > 0 && t.conn != nil {
			_ = t.conn.Write(t.ctx, websocket.MessageBinary, buf[:n])
		}
	}
}

// close ends the connection and the copying. The emulator's input pipe is closed, not the
// emulator, so a key sent after it is dropped instead of waiting for a reader.
func (t *liveTerm) close() {
	t.mu.Lock()
	t.closing = true
	t.mu.Unlock()
	if c, ok := t.emu.InputPipe().(io.Closer); ok {
		_ = c.Close()
	}
	if t.conn != nil {
		t.conn.Close(websocket.StatusNormalClosure, "detached")
	}
	if t.cancel != nil {
		t.cancel()
	}
}

// resize sets the terminal's size, in the emulator and in the sandbox.
func (t *liveTerm) resize(cols, rows int) {
	cols, rows = max(cols, 2), max(rows, 1)
	t.mu.Lock()
	if cols == t.cols && rows == t.rows {
		t.mu.Unlock()
		return
	}
	t.cols, t.rows = cols, rows
	t.emu.Resize(cols, rows)
	t.mu.Unlock()
	if t.conn == nil {
		return
	}
	msg, _ := json.Marshal(map[string]any{"type": "resize", "cols": cols, "rows": rows})
	conn, ctx := t.conn, t.ctx
	go func() { _ = conn.Write(ctx, websocket.MessageText, msg) }()
}

// key sends a key press to the terminal, as the emulator encodes it for the program's modes. The
// emulator writes it into its input pipe, which writeInput empties without the lock.
func (t *liveTerm) key(msg tea.KeyPressMsg) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.closing {
		t.emu.SendKey(uv.KeyPressEvent{Code: msg.Code, Mod: msg.Mod, Text: msg.Text})
	}
}

// paste sends pasted text, bracketed when the program asked for that.
func (t *liveTerm) paste(text string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.closing {
		t.emu.Paste(text)
	}
}

// waitOutput waits for the terminal's next output, or its end.
func (t *liveTerm) waitOutput(gen int) tea.Cmd {
	return func() tea.Msg {
		select {
		case <-t.dirty:
			time.Sleep(paneFrame) // what follows soon is drawn in the same frame
			return paneOutput{gen}
		case <-t.ended:
			return paneEnded{gen}
		}
	}
}

// lines draws the emulator's screen in w cells and h rows: each run of cells of one style as
// one styled piece, and an empty cell as a space.
func (t *liveTerm) lines(w, h int) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, 0, h)
	for y := 0; y < min(h, t.rows); y++ {
		var line, run strings.Builder
		var style uv.Style
		flush := func() {
			if run.Len() == 0 {
				return
			}
			line.WriteString(style.Styled(run.String()))
			run.Reset()
		}
		width := 0
		for x := 0; x < min(w, t.cols); x++ {
			c := t.emu.CellAt(x, y)
			if c == nil {
				flush()
				style = uv.Style{}
				run.WriteByte(' ')
				width++
				continue
			}
			if c.Width == 0 {
				continue // the second cell of a wide character
			}
			if width+c.Width > w {
				break
			}
			if !c.Style.Equal(&style) {
				flush()
				style = c.Style
			}
			content := c.Content
			if content == "" {
				content = " "
			}
			run.WriteString(content)
			width += c.Width
		}
		flush()
		out = append(out, strings.TrimRight(line.String(), " "))
	}
	return out
}

// cursor is where the terminal's cursor is, and whether the program shows it.
func (t *liveTerm) cursor() (x, y int, shown bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	p := t.emu.CursorPosition()
	return p.X, p.Y, !t.emu.CursorHidden()
}
