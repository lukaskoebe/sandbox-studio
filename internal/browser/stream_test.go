package browser

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
		for _, m := range []string{
			`{"type":"frame","data":"AAAA","metadata":{}}`,
			`{"type":"tabs","tabs":[{"url":"https://clicked.example/path","active":true}]}`,
			`{"type":"url","url":"https://redirected.example/path"}`,
		} {
			if err := c.Write(r.Context(), websocket.MessageText, []byte(m)); err != nil {
				t.Error(err)
				return
			}
		}
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
	// An agent waiting for approval holds the command slot. Watching must still work.
	st := f.svc.state(f.env, f.ada.ID)
	st.run <- struct{}{}
	release := sync.OnceFunc(func() { <-st.run })
	defer release()
	ui := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		if err := f.svc.Relay(r.Context(), f.env, f.ada.ID, "", c); err != nil {
			t.Error(err)
		}
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
	_, tabs, err := c.Read(ctx)
	if err != nil || !strings.Contains(string(tabs), `"type":"tabs"`) {
		t.Fatalf("tabs %s %v", tabs, err)
	}
	_, changedURL, err := c.Read(ctx)
	if err != nil || !strings.Contains(string(changedURL), `"type":"url"`) {
		t.Fatalf("URL %s %v", changedURL, err)
	}
	if status, err := f.svc.Status(f.ctx, f.env, f.ada.ID); err != nil || status.URL != "https://redirected.example/path" {
		t.Errorf("stream did not update browser status: %+v, %v", status, err)
	}
	release()
	click := `{"type":"input_mouse","eventType":"mousePressed","x":1,"y":2,"button":"left","clickCount":1}`
	if err := c.Write(ctx, websocket.MessageText, []byte(click)); err != nil {
		t.Fatal(err)
	}
	if err := c.Write(ctx, websocket.MessageText, []byte(`{"type":"ack","seq":1}`)); err != nil {
		t.Fatal(err)
	}
	if m := <-got; !strings.Contains(m, `"ack"`) {
		t.Errorf("input passed while the agent drives: %s", m)
	}
	if err := f.svc.SetTakeover(f.ctx, f.env, f.ada.ID, "", true); err != nil {
		t.Fatal(err)
	}
	if err := c.Write(ctx, websocket.MessageText, []byte(click)); err != nil {
		t.Fatal(err)
	}
	select {
	case m := <-got:
		if !strings.Contains(m, "mousePressed") {
			t.Errorf("got %s", m)
		}
	case <-ctx.Done():
		t.Fatal("input did not pass while the user drives")
	}
}

func TestActiveTabsURLAndStaleStream(t *testing.T) {
	valid := []byte(`{"type":"tabs","tabs":[{"url":"https://other.example","active":false},{"url":"about:blank","active":true}]}`)
	if got, ok := streamPageURL(valid); !ok || got != "about:blank" {
		t.Fatalf("active URL = %q, %v", got, ok)
	}
	urlEvent := []byte(`{"type":"url","url":"https://clicked.example/#next"}`)
	if got, ok := streamPageURL(urlEvent); !ok || got != "https://clicked.example/#next" {
		t.Fatalf("URL event = %q, %v", got, ok)
	}
	for _, raw := range []string{
		`not json`,
		`{"type":"frame","url":"https://ignored.example"}`,
		`{"type":"url"}`,
		`{"type":"url","url":42}`,
		`{"type":"tabs","tabs":[]}`,
		`{"type":"tabs","tabs":[{"url":"https://one.example","active":true},{"url":"https://two.example","active":true}]}`,
		`{"type":"tabs","tabs":[{"active":true}]}`,
		`{"type":"tabs","tabs":[{"url":"https://bad.example","active":"yes"}]}`,
	} {
		if _, ok := streamPageURL([]byte(raw)); ok {
			t.Errorf("malformed or unrelated message accepted: %s", raw)
		}
	}

	f := newFixture(t)
	f.mustCall(f.sbAda, agentproto.MethodBrowserSnapshot, `{}`)
	st := f.svc.state(f.env, f.ada.ID)
	f.svc.mu.Lock()
	vmID, sessionID := st.vm, st.session
	st.visible = true
	f.svc.mu.Unlock()
	f.svc.syncStreamURL(st, &st.page, f.env, vmID, sessionID, valid)
	if status, err := f.svc.Status(f.ctx, f.env, f.ada.ID); err != nil || status.URL != "about:blank" {
		t.Fatalf("active tab URL not applied: %+v, %v", status, err)
	}
	f.svc.syncStreamURL(st, &st.page, f.env, vmID, sessionID, urlEvent)
	f.svc.mu.Lock()
	if !st.visible || len(st.refs) != 0 {
		t.Error("a URL observation must invalidate refs without unmasking a visible credential")
	}
	st.reset()
	st.vm = vmID
	st.session = "replacement-session"
	f.svc.mu.Unlock()
	f.svc.syncStreamURL(st, &st.page, f.env, vmID, sessionID, valid)
	f.svc.syncStreamURL(st, &st.page, f.env, vmID, sessionID, urlEvent)
	f.svc.mu.Lock()
	got := st.url
	f.svc.mu.Unlock()
	if got != "" {
		t.Errorf("stale stream changed reset browser URL to %q", got)
	}
}

// A frame is the latest one relayed while it is fresh, else one read from the stream; neither
// counts as use, and none shows while a credential does.
func TestFrame(t *testing.T) {
	f := newFixture(t)
	dials := 0
	guest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		for _, m := range []string{
			`{"type":"tabs","tabs":[{"url":"https://example.com/","active":true}]}`,
			`{"type":"frame","data":"/9j/","metadata":{}}`,
		} {
			if c.Write(r.Context(), websocket.MessageText, []byte(m)) != nil {
				return
			}
		}
		_, _, _ = c.Read(r.Context()) // until the reader goes
	}))
	defer guest.Close()
	f.fb.dial = func(int) (net.Conn, error) {
		dials++
		return net.Dial("tcp", strings.TrimPrefix(guest.URL, "http://"))
	}
	if _, err := f.svc.Frame(f.ctx, f.env, f.ada.ID, "eph-0000000f"); !errors.Is(err, ErrNoSession) {
		t.Errorf("an unknown session: %v", err)
	}
	f.mustCall(f.sbAda, agentproto.MethodBrowserSnapshot, `{}`)
	st := f.svc.state(f.env, f.ada.ID)
	f.svc.mu.Lock()
	used := st.lastUsed
	f.svc.mu.Unlock()
	for range 2 {
		jpeg, err := f.svc.Frame(f.ctx, f.env, f.ada.ID, "")
		if err != nil || string(jpeg) != "\xff\xd8\xff" {
			t.Fatalf("frame %q, %v", jpeg, err)
		}
	}
	if dials != 1 {
		t.Errorf("the stream was opened %d times, want once: the second frame is the first", dials)
	}
	f.svc.mu.Lock()
	if !st.lastUsed.Equal(used) {
		t.Error("a frame counted as using the browser")
	}
	st.frameAt = st.frameAt.Add(-time.Minute)
	f.svc.mu.Unlock()
	if _, err := f.svc.Frame(f.ctx, f.env, f.ada.ID, ""); err != nil || dials != 2 {
		t.Errorf("a stale frame: %d dials, %v", dials, err)
	}
	f.svc.mu.Lock()
	st.visible = true
	f.svc.mu.Unlock()
	if _, err := f.svc.Frame(f.ctx, f.env, f.ada.ID, ""); !errors.Is(err, ErrHidden) {
		t.Errorf("a frame showed while a credential does: %v", err)
	}
}
