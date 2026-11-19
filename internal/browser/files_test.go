package browser

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// cdpCall is a command the fake page's debugging socket got.
type cdpCall struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// While the live view is open, Studio watches its page with the file chooser intercepted: a
// request for files reaches the view only while the user drives, the paths it sends back go
// to the input that asked, once, and only from the pastes folder; a drop goes to the point
// it names; and the agent's handback note names the files.
func TestRelayFiles(t *testing.T) {
	f := newFixture(t)
	stream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		_ = c.Write(r.Context(), websocket.MessageText, []byte(`{"type":"frame","data":"AAAA","metadata":{}}`))
		for {
			if _, _, err := c.Read(r.Context()); err != nil {
				return
			}
		}
	}))
	defer stream.Close()
	calls := make(chan cdpCall, 32)
	page := make(chan *websocket.Conn, 1)
	devtools := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h, _, _ := net.SplitHostPort(r.Host); h != "127.0.0.1" {
			http.Error(w, "Host header is specified and is not an IP address or localhost.", http.StatusInternalServerError)
			return
		}
		switch r.URL.Path {
		case "/json/list":
			_ = json.NewEncoder(w).Encode([]map[string]string{{"id": "T1", "type": "page", "url": "about:blank"}})
		case "/devtools/page/T1":
			if r.Header.Get("Origin") != "" {
				http.Error(w, "an Origin", http.StatusForbidden)
				return
			}
			c, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer c.CloseNow()
			page <- c
			for {
				_, data, err := c.Read(r.Context())
				if err != nil {
					return
				}
				var m struct {
					ID int64 `json:"id"`
					cdpCall
				}
				if json.Unmarshal(data, &m) != nil {
					t.Errorf("not JSON: %s", data)
					return
				}
				calls <- m.cdpCall
				reply, _ := json.Marshal(map[string]any{"id": m.ID, "result": map[string]any{}})
				if c.Write(r.Context(), websocket.MessageText, reply) != nil {
					return
				}
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer devtools.Close()
	f.fb.devtoolsPort = 9222
	f.fb.dial = func(port int) (net.Conn, error) {
		switch port {
		case agentproto.BrowserStreamPort:
			return net.Dial("tcp", strings.TrimPrefix(stream.URL, "http://"))
		case 9222:
			return net.Dial("tcp", strings.TrimPrefix(devtools.URL, "http://"))
		}
		return nil, errors.New("nothing listens there")
	}
	f.mustCall(f.sbAda, agentproto.MethodBrowserSnapshot, `{}`)
	ui := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		_ = f.svc.Relay(r.Context(), f.env, f.ada.ID, "", c)
	}))
	defer ui.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ui.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()

	next := func() cdpCall {
		t.Helper()
		select {
		case call := <-calls:
			return call
		case <-ctx.Done():
			t.Fatal("the page's socket got no command")
		}
		return cdpCall{}
	}
	if call := next(); call.Method != "Page.enable" {
		t.Fatalf("first command %+v", call)
	}
	if call := next(); call.Method != "Page.setInterceptFileChooserDialog" || string(call.Params) != `{"enabled":true}` {
		t.Fatalf("second command %+v", call)
	}
	cdp := <-page
	// studio reads the view's socket until a message of Studio's own, past the frames.
	studio := func() map[string]any {
		t.Helper()
		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				t.Fatalf("the view's socket: %v", err)
			}
			var m map[string]any
			if json.Unmarshal(data, &m) == nil && strings.HasPrefix(m["type"].(string), "studio_") {
				return m
			}
		}
	}
	send := func(msg string) map[string]any {
		t.Helper()
		if err := c.Write(ctx, websocket.MessageText, []byte(msg)); err != nil {
			t.Fatal(err)
		}
		return studio()
	}
	chooser := func(mode string, node int) {
		t.Helper()
		ev, _ := json.Marshal(map[string]any{"method": "Page.fileChooserOpened", "params": map[string]any{"frameId": "F", "mode": mode, "backendNodeId": node}})
		if err := cdp.Write(ctx, websocket.MessageText, ev); err != nil {
			t.Fatal(err)
		}
	}

	// The agent drives: its click on a file input gets nothing, as before. (Called here, as
	// an event the socket sent now could be read after the takeover below.)
	st := f.svc.state(f.env, f.ada.ID)
	f.svc.mu.Lock()
	vm, session := st.vm, st.session
	f.svc.mu.Unlock()
	idle := &pageWatch{}
	f.svc.pageEvent(ctx, st, &st.page, f.env, vm, session, nil, idle, "Page.fileChooserOpened", json.RawMessage(`{"mode":"selectSingle","backendNodeId":7}`))
	if idle.chooser != nil {
		t.Error("a request for files while the agent drives was kept")
	}
	if err := f.svc.SetTakeover(f.ctx, f.env, f.ada.ID, "", true); err != nil {
		t.Fatal(err)
	}
	chooser("selectMultiple", 42)
	asked := studio()
	if asked["type"] != "studio_file_chooser" || asked["multiple"] != true || asked["id"] == "" {
		t.Fatalf("the view was told %v", asked)
	}
	id := asked["id"].(string)

	if r := send(`{"type":"studio_files","chooser":"` + id + `","paths":["/etc/passwd"]}`); r["ok"] != false {
		t.Errorf("a file outside the pastes went to the page: %v", r)
	}
	if r := send(`{"type":"studio_files","chooser":"` + id + `","paths":["/tmp/studio-pastes/a.txt","/tmp/studio-pastes/b (2).png"]}`); r["ok"] != true {
		t.Fatalf("choosing files: %v", r)
	}
	if call := next(); call.Method != "DOM.setFileInputFiles" ||
		string(call.Params) != `{"backendNodeId":42,"files":["/tmp/studio-pastes/a.txt","/tmp/studio-pastes/b (2).png"]}` {
		t.Fatalf("set files %+v", call)
	}
	if r := send(`{"type":"studio_files","chooser":"` + id + `","paths":["/tmp/studio-pastes/a.txt"]}`); r["ok"] != false {
		t.Errorf("a request was answered twice: %v", r)
	}

	if r := send(`{"type":"studio_files","drop":{"x":10,"y":20.5},"paths":["/tmp/studio-pastes/a.txt"]}`); r["ok"] != true || r["dropped"] != true {
		t.Fatalf("dropping files: %v", r)
	}
	for _, typ := range []string{"dragEnter", "dragOver", "drop"} {
		call := next()
		var p struct {
			Type string  `json:"type"`
			X    float64 `json:"x"`
			Y    float64 `json:"y"`
			Data struct {
				Files []string `json:"files"`
				Mask  int      `json:"dragOperationsMask"`
			} `json:"data"`
		}
		_ = json.Unmarshal(call.Params, &p)
		if call.Method != "Input.dispatchDragEvent" || p.Type != typ || p.X != 10 || p.Y != 20.5 ||
			strings.Join(p.Data.Files, ",") != "/tmp/studio-pastes/a.txt" || p.Data.Mask != dragOperations {
			t.Fatalf("%s: %s %s", typ, call.Method, call.Params)
		}
	}
	if r := send(`{"type":"studio_files","drop":{"x":-1,"y":0},"paths":["/tmp/studio-pastes/a.txt"]}`); r["ok"] != false {
		t.Errorf("a drop off the page went through: %v", r)
	}

	if err := f.svc.SetTakeover(f.ctx, f.env, f.ada.ID, "", false); err != nil {
		t.Fatal(err)
	}
	if r := send(`{"type":"studio_files","drop":{"x":1,"y":1},"paths":["/tmp/studio-pastes/a.txt"]}`); r["ok"] != false ||
		r["error"] != ErrFilesNeedTakeover.Error() {
		t.Errorf("a drop while the agent drives: %v", r)
	}
	f.svc.mu.Lock()
	note := st.handback
	f.svc.mu.Unlock()
	if !strings.Contains(note, "They gave the page files from their computer: a.txt, b (2).png (chosen); a.txt (dropped).") {
		t.Errorf("handback note %q", note)
	}
}

// A file for the page goes to the browser VM's pastes, under a free name, only while the
// user drives.
func TestPasteFile(t *testing.T) {
	f := newFixture(t)
	f.mustCall(f.sbAda, agentproto.MethodBrowserSnapshot, `{}`)
	if _, err := f.svc.PasteFile(f.ctx, f.env, f.ada.ID, "", "a.txt", 2, strings.NewReader("hi")); !errors.Is(err, ErrFilesNeedTakeover) {
		t.Fatalf("while the agent drives: %v", err)
	}
	if _, err := f.svc.PasteFile(f.ctx, f.env, f.ada.ID, "nope", "a.txt", 2, strings.NewReader("hi")); !errors.Is(err, ErrNoSession) {
		t.Fatalf("an unknown session: %v", err)
	}
	if err := f.svc.SetTakeover(f.ctx, f.env, f.ada.ID, "", true); err != nil {
		t.Fatal(err)
	}
	p, err := f.svc.PasteFile(f.ctx, f.env, f.ada.ID, "", "a.txt", 2, strings.NewReader("hi"))
	if err != nil || p != "/tmp/studio-pastes/a.txt" {
		t.Fatalf("pasted to %q, %v", p, err)
	}
	st := f.svc.state(f.env, f.ada.ID)
	f.svc.mu.Lock()
	vm := st.vm
	f.svc.mu.Unlock()
	f.fb.mu.Lock()
	defer f.fb.mu.Unlock()
	if len(f.fb.uploads) != 1 {
		t.Fatalf("uploads %+v", f.fb.uploads)
	}
	u := f.fb.uploads[0]
	if u.sandbox != vm || !u.req.Paste || u.req.Conflict != agentproto.ConflictKeep || u.req.Name != "a.txt" || u.body != "hi" {
		t.Errorf("upload %+v, browser VM %s", u, vm)
	}
}
