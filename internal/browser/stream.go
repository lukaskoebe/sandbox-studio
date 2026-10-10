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

// Relay bridges the UI's live-view socket and the persona's browser until either ends.
func (s *Service) Relay(ctx context.Context, envID, personaID string, client *websocket.Conn) error {
	st := s.state(envID, personaID)
	sb, status, err := s.VMs.BrowserStatus(ctx, envID, personaID)
	if err != nil {
		return err
	}
	if status != runtime.StatusRunning {
		return ErrNotLive
	}
	s.mu.Lock()
	st.vm = sb.ID
	if st.session == "" {
		st.session = store.NewID()
	}
	s.mu.Unlock()
	guest, err := s.dialStream(ctx, sb.ID)
	if err != nil {
		// agent-browser starts its daemon, and the stream with it, on the first command.
		if _, _, err := s.exec(ctx, st, agentBrowserGetURL); err != nil {
			return err
		}
		if guest, err = s.dialStream(ctx, sb.ID); err != nil {
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
			if client.Write(ctx, websocket.MessageText, data) != nil {
				return
			}
			if now := s.now(); now.Sub(last) > 30*time.Second {
				// Someone watching keeps the browser from idling out.
				last = now
				s.mu.Lock()
				st.lastUsed = now
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
			driving := st.takeover
			if driving {
				st.lastUsed = s.now()
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

// dialStream opens the screencast WebSocket in the VM through its channel. It sends no
// Origin header, which agent-browser's origin check accepts.
func (s *Service) dialStream(ctx context.Context, vm string) (*websocket.Conn, error) {
	conn, err := s.Runner.DialTCP(ctx, vm, agentproto.BrowserStreamPort)
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
	ws, _, err := websocket.Dial(dctx, "ws://127.0.0.1:"+strconv.Itoa(agentproto.BrowserStreamPort)+"/", &websocket.DialOptions{HTTPClient: hc})
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
		out = map[string]any{"type": "input_keyboard", "eventType": m.EventType, "key": m.Key, "code": m.Code,
			"windowsVirtualKeyCode": clampI(m.VK, 0, 255), "modifiers": clampI(m.Modifiers, 0, 15)}
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
