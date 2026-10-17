package browser

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// The live view relays agent-browser's screencast WebSocket (AGENT_BROWSER_STREAM_PORT,
// cli/src/native/stream/websocket.rs in 0.39) between the UI and the browser VM, over the
// VM's channel like a terminal. The guest sends {"type":"frame","data":<base64 JPEG>,
// "metadata":{...}} and status messages, which go to the UI unchanged. From the UI only
// pacing messages (config, ack) always pass; mouse and keyboard input pass only while the
// user has taken over, rebuilt field by field so nothing else reaches the browser.

const (
	maxGuestMessage  = 4 << 20 // a JPEG frame, base64
	maxClientMessage = 16 << 10
)

// Relay bridges the UI's live-view socket and a session of the teammate's browser ("" for
// the persistent profile) until either ends.
func (s *Service) Relay(ctx context.Context, envID, teammateID, session string, client *websocket.Conn) error {
	t, err := s.streamTarget(ctx, envID, teammateID, session)
	if err != nil {
		return err
	}
	st, pg, sb, streamSession := t.st, t.pg, t.vm, t.session
	port := agentproto.BrowserStreamPort + pg.slot
	guest, err := s.dialStream(ctx, sb, port)
	if err != nil {
		if guest, err = s.startStream(ctx, st, pg, envID, sb, streamSession); err != nil {
			return err
		}
	}
	defer guest.CloseNow()
	guest.SetReadLimit(maxGuestMessage)
	client.SetReadLimit(maxClientMessage)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		defer cancel()
		last := time.Time{}
		for {
			typ, data, err := guest.Read(ctx)
			if err != nil {
				client.Close(websocket.StatusNormalClosure, "the browser stream ended")
				return
			}
			if typ != websocket.MessageText {
				continue
			}
			if !s.syncStreamURL(st, pg, envID, sb, streamSession, data) {
				return
			}
			if client.Write(ctx, websocket.MessageText, data) != nil {
				return
			}
			if now := s.now(); now.Sub(last) > 30*time.Second {
				// Someone watching keeps the browser from idling out.
				last = now
				s.mu.Lock()
				if s.streamCurrentLocked(st, pg, envID, sb, streamSession) {
					st.lastUsed = now
				}
				s.mu.Unlock()
			}
		}
	}()
	go func() {
		defer wg.Done()
		defer cancel()
		for {
			typ, data, err := client.Read(ctx)
			if err != nil {
				return
			}
			if typ != websocket.MessageText {
				continue
			}
			s.mu.Lock()
			if !s.streamCurrentLocked(st, pg, envID, sb, streamSession) {
				s.mu.Unlock()
				return
			}
			driving := pg.takeover
			if driving {
				st.lastUsed, pg.used = s.now(), s.now()
			}
			s.mu.Unlock()
			if msg := FilterInput(data, driving); msg != nil {
				if guest.Write(ctx, websocket.MessageText, msg) != nil {
					return
				}
			}
		}
	}()
	wg.Wait()
	return nil
}

// streamOf is a session of a teammate's browser whose stream can be read: its state and page,
// its VM's ID, and the run of the browser it belongs to.
type streamOf struct {
	st      *state
	pg      *page
	vm      string
	session string
}

// streamTarget finds the running browser VM of a teammate's session, adopting it as the
// browser's VM.
func (s *Service) streamTarget(ctx context.Context, envID, teammateID, session string) (streamOf, error) {
	st := s.state(envID, teammateID)
	// Watching must not wait for an agent command or approval. Remember this run so a
	// concurrent Stop cannot be undone by adopting the VM from a stale status response.
	s.mu.Lock()
	previousVM, previousSession := st.vm, st.session
	pg := st.pageFor(session)
	s.mu.Unlock()
	if pg == nil {
		return streamOf{}, ErrNoSession
	}
	sb, status, err := s.VMs.BrowserStatus(ctx, envID, teammateID)
	if err != nil {
		return streamOf{}, err
	}
	if status != runtime.StatusRunning {
		return streamOf{}, ErrNotLive
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.streamCurrentLocked(st, pg, envID, previousVM, previousSession) {
		return streamOf{}, ErrNotLive
	}
	if st.vm != "" && st.vm != sb.ID {
		st.reset()
	}
	if st.pageFor(session) != pg {
		return streamOf{}, ErrNoSession
	}
	st.vm = sb.ID
	if st.session == "" {
		st.session = store.NewID()
	}
	return streamOf{st: st, pg: pg, vm: sb.ID, session: st.session}, nil
}

const (
	frameFresh = 2 * time.Second // how old a relayed frame Frame still returns
	frameWait  = 5 * time.Second // how long Frame reads a stream for a frame
)

// Frame is the latest screencast frame of a session of the teammate's browser as a JPEG. While
// a live view watches, it is the frame last relayed; otherwise Frame opens the session's
// stream and reads one. Neither counts as using the browser, so a picture on show doesn't keep
// the browser from idling out, and a browser with no page yet has no frame (ErrNotLive).
func (s *Service) Frame(ctx context.Context, envID, teammateID, session string) ([]byte, error) {
	st := s.state(envID, teammateID)
	s.mu.Lock()
	pg := st.pageFor(session)
	var cached []byte
	hidden := pg != nil && pg.visible
	if pg != nil && pg.frame != nil && s.now().Sub(pg.frameAt) < frameFresh {
		cached = pg.frame
	}
	s.mu.Unlock()
	switch {
	case pg == nil:
		return nil, ErrNoSession
	case hidden:
		return nil, ErrHidden
	case cached != nil:
		return frameJPEG(cached)
	}
	t, err := s.streamTarget(ctx, envID, teammateID, session)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, frameWait)
	defer cancel()
	guest, err := s.dialStream(ctx, t.vm, agentproto.BrowserStreamPort+t.pg.slot)
	if err != nil {
		return nil, ErrNotLive
	}
	defer guest.CloseNow()
	guest.SetReadLimit(maxGuestMessage)
	for {
		typ, data, err := guest.Read(ctx)
		if err != nil {
			return nil, ErrNotLive
		}
		if typ != websocket.MessageText {
			continue
		}
		if kind, _, _ := parseStream(data); kind != "frame" {
			continue
		}
		s.mu.Lock()
		current := s.streamCurrentLocked(t.st, t.pg, envID, t.vm, t.session)
		hidden := t.pg.visible
		if current && !hidden {
			t.pg.frame, t.pg.frameAt = data, s.now()
		}
		s.mu.Unlock()
		switch {
		case !current:
			return nil, ErrNotLive
		case hidden:
			return nil, ErrHidden
		}
		return frameJPEG(data)
	}
}

// frameJPEG is the JPEG of a stream's frame message.
func frameJPEG(data []byte) ([]byte, error) {
	var m struct {
		Data []byte `json:"data"` // base64, which encoding/json decodes
	}
	if err := json.Unmarshal(data, &m); err != nil || len(m.Data) == 0 {
		return nil, errors.New("the browser sent a frame that could not be read")
	}
	return m.Data, nil
}

// Only bootstrapping the daemon needs the command slot. An existing stream is readable
// even while an agent command is waiting for approval.
func (s *Service) startStream(ctx context.Context, st *state, pg *page, envID, vmID, sessionID string) (*websocket.Conn, error) {
	select {
	case st.run <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-st.run }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	current := s.streamCurrentLocked(st, pg, envID, vmID, sessionID)
	s.mu.Unlock()
	if !current {
		return nil, ErrNotLive
	}
	sb, status, err := s.VMs.BrowserStatus(ctx, envID, st.teammate)
	if err != nil || status != runtime.StatusRunning || sb.ID != vmID {
		return nil, ErrNotLive
	}
	// A queued Start may already have brought up the daemon.
	port := agentproto.BrowserStreamPort + pg.slot
	if guest, err := s.dialStream(ctx, vmID, port); err == nil {
		return guest, nil
	}
	if _, _, err := s.exec(ctx, st, pg, agentBrowserGetURL); err != nil {
		return nil, err
	}
	return s.dialStream(ctx, vmID, port)
}

func streamPageURL(data []byte) (string, bool) {
	_, url, ok := parseStream(data)
	return url, ok
}

// parseStream reads a message of the guest's stream: its type, and the page's URL if it tells
// one.
func parseStream(data []byte) (typ, url string, ok bool) {
	var message struct {
		Type string `json:"type"`
		URL  string `json:"url"`
		Tabs []struct {
			URL    string `json:"url"`
			Active bool   `json:"active"`
		} `json:"tabs"`
	}
	if json.Unmarshal(data, &message) != nil {
		return "", "", false
	}
	if message.Type == "url" {
		return message.Type, message.URL, message.URL != ""
	}
	if message.Type != "tabs" {
		return message.Type, "", false
	}
	var activeURL string
	activeCount := 0
	for _, tab := range message.Tabs {
		if tab.Active {
			if tab.URL == "" {
				return message.Type, "", false
			}
			activeURL = tab.URL
			activeCount++
		}
	}
	return message.Type, activeURL, activeCount == 1
}

func (s *Service) streamCurrentLocked(st *state, pg *page, envID, vmID, sessionID string) bool {
	return st != nil && s.states[st.teammate] == st && st.env == envID && st.vm == vmID && st.session == sessionID &&
		st.pageFor(pg.id) == pg
}

func (s *Service) syncStreamURL(st *state, pg *page, envID, vmID, sessionID string, data []byte) bool {
	typ, url, ok := parseStream(data)
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.streamCurrentLocked(st, pg, envID, vmID, sessionID) {
		return false
	}
	if typ == "frame" {
		pg.frame, pg.frameAt = data, s.now()
		if pg.visible {
			pg.frame = nil
		}
	}
	if ok {
		pg.observedURL(url)
		st.lastUsed = s.now()
	}
	return true
}

// dialStream opens a session's screencast WebSocket in the VM through its channel. It
// sends no Origin header, which agent-browser's origin check accepts.
func (s *Service) dialStream(ctx context.Context, vm string, port int) (*websocket.Conn, error) {
	conn, err := s.Runner.DialTCP(ctx, vm, port)
	if err != nil {
		return nil, err
	}
	var once sync.Once
	dial := func(context.Context, string, string) (net.Conn, error) {
		var c net.Conn
		once.Do(func() { c = conn })
		if c == nil {
			return nil, errors.New("the stream connection was used")
		}
		return c, nil
	}
	hc := &http.Client{Transport: &http.Transport{DialContext: dial, DisableKeepAlives: true}}
	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(dctx, "ws://127.0.0.1:"+strconv.Itoa(port)+"/", &websocket.DialOptions{HTTPClient: hc})
	if err != nil {
		conn.Close()
		return nil, err
	}
	return ws, nil
}

var (
	mouseEvents = []string{"mousePressed", "mouseReleased", "mouseMoved", "mouseWheel"}
	mouseButton = []string{"none", "left", "middle", "right", "back", "forward"}
	keyEvents   = []string{"keyDown", "keyUp", "rawKeyDown", "char"}
	pacing      = []string{"push", "ack"}
)

// FilterInput rebuilds one message from the UI for the guest, or returns nil to drop it.
// Input passes only while the user drives.
func FilterInput(data []byte, driving bool) []byte {
	var m struct {
		Type       string  `json:"type"`
		MaxFPS     *int    `json:"maxFps"`
		Pacing     string  `json:"pacing"`
		Seq        *int64  `json:"seq"`
		EventType  string  `json:"eventType"`
		X          float64 `json:"x"`
		Y          float64 `json:"y"`
		Button     string  `json:"button"`
		ClickCount int     `json:"clickCount"`
		DeltaX     float64 `json:"deltaX"`
		DeltaY     float64 `json:"deltaY"`
		Modifiers  int     `json:"modifiers"`
		Key        string  `json:"key"`
		Code       string  `json:"code"`
		Text       *string `json:"text"`
		VK         int     `json:"windowsVirtualKeyCode"`
	}
	if json.Unmarshal(data, &m) != nil {
		return nil
	}
	var out map[string]any
	switch m.Type {
	case "config":
		out = map[string]any{"type": "config"}
		if m.MaxFPS != nil && *m.MaxFPS >= 1 && *m.MaxFPS <= 60 {
			out["maxFps"] = *m.MaxFPS
		}
		if slices.Contains(pacing, m.Pacing) {
			out["pacing"] = m.Pacing
		}
	case "ack":
		if m.Seq == nil || *m.Seq < 0 {
			return nil
		}
		out = map[string]any{"type": "ack", "seq": *m.Seq}
	case "input_mouse":
		if !driving || !slices.Contains(mouseEvents, m.EventType) {
			return nil
		}
		button := m.Button
		if !slices.Contains(mouseButton, button) {
			button = "none"
		}
		out = map[string]any{"type": "input_mouse", "eventType": m.EventType, "x": clampF(m.X, 0, 10000), "y": clampF(m.Y, 0, 10000),
			"button": button, "clickCount": clampI(m.ClickCount, 0, 3), "deltaX": clampF(m.DeltaX, -10000, 10000),
			"deltaY": clampF(m.DeltaY, -10000, 10000), "modifiers": clampI(m.Modifiers, 0, 15)}
	case "input_keyboard":
		if !driving || !slices.Contains(keyEvents, m.EventType) || len(m.Key) > 32 || len(m.Code) > 32 {
			return nil
		}
		out = map[string]any{"type": "input_keyboard", "eventType": m.EventType,
			"windowsVirtualKeyCode": clampI(m.VK, 0, 255), "modifiers": clampI(m.Modifiers, 0, 15)}
		// agent-browser's char event is text-only; omit absent optional CDP strings.
		if m.Key != "" {
			out["key"] = m.Key
		}
		if m.Code != "" {
			out["code"] = m.Code
		}
		if m.Text != nil && len(*m.Text) <= 8 {
			out["text"] = *m.Text
		}
	default:
		return nil
	}
	b, _ := json.Marshal(out)
	return b
}

func clampF(v, lo, hi float64) float64 { return max(lo, min(hi, v)) }
func clampI(v, lo, hi int) int         { return max(lo, min(hi, v)) }
