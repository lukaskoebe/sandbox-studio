package browser

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// The live view relays frames from the VM's stream and input to it only while the user drives.
func TestRelay(t *testing.T) {
	f := newFixture(t)
	got := make(chan string, 16)
	guest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "" {
			t.Error("the relay sent an Origin header")
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		c.Write(r.Context(), websocket.MessageText, []byte(`{"type":"frame","data":"AAAA","metadata":{}}`))
		for {
			_, data, err := c.Read(r.Context())
			if err != nil {
				return
			}
			got <- string(data)
		}
	}))
	defer guest.Close()
	f.fb.dial = func(port int) (net.Conn, error) {
		if port != agentproto.BrowserStreamPort {
			t.Errorf("port %d", port)
		}
		return net.Dial("tcp", strings.TrimPrefix(guest.URL, "http://"))
	}
	f.mustCall(f.sbAda, agentproto.MethodBrowserSnapshot, `{}`)
	ui := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		f.svc.Relay(r.Context(), f.env, f.ada.ID, c)
	}))
	defer ui.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ui.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	_, frame, err := c.Read(ctx)
	if err != nil || !strings.Contains(string(frame), `"frame"`) {
		t.Fatalf("frame %s %v", frame, err)
	}
	click := `{"type":"input_mouse","eventType":"mousePressed","x":1,"y":2,"button":"left","clickCount":1}`
	c.Write(ctx, websocket.MessageText, []byte(click))
	c.Write(ctx, websocket.MessageText, []byte(`{"type":"ack","seq":1}`))
	if m := <-got; !strings.Contains(m, `"ack"`) {
		t.Errorf("input passed while the agent drives: %s", m)
	}
	if err := f.svc.SetTakeover(f.ctx, f.env, f.ada.ID, true); err != nil {
		t.Fatal(err)
	}
	c.Write(ctx, websocket.MessageText, []byte(click))
	select {
	case m := <-got:
		if !strings.Contains(m, "mousePressed") {
			t.Errorf("got %s", m)
		}
	case <-ctx.Done():
		t.Fatal("input did not pass while the user drives")
	}
}
