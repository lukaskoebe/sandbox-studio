//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image/jpeg"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/lukaskoebe/sandbox-studio/internal/agentchan"
	"github.com/lukaskoebe/sandbox-studio/internal/browser"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// browserVMs keeps one browser VM row, always running, for the broker.
type browserVMs struct{ row store.Sandbox }

func (v browserVMs) EnsureBrowser(context.Context, string, string, string) (store.Sandbox, error) {
	return v.row, nil
}
func (v browserVMs) StopBrowser(context.Context, string, string) error   { return nil }
func (v browserVMs) RemoveBrowser(context.Context, string, string) error { return nil }
func (v browserVMs) BrowserStatus(context.Context, string, string) (store.Sandbox, runtime.Status, error) {
	return v.row, runtime.StatusRunning, nil
}

type browserFixture struct {
	t        *testing.T
	ctx      context.Context
	st       *store.Store
	env      string
	teammate string
	agent    *Guest // the guest of the teammate's agent sandbox
	vm       *Guest // the guest of its browser VM
	svc      *browser.Service
}

// newBrowserFixture runs the real broker and hub against fake guests of an agent sandbox
// and of its teammate's browser VM.
func newBrowserFixture(t *testing.T) *browserFixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	env, err := st.CreateEnvironment(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateProvider(ctx, store.Provider{ID: "sub", EnvironmentID: env.ID, Name: "sub", Kind: "claude_subscription"}, nil); err != nil {
		t.Fatal(err)
	}
	tm, err := st.CreateTeammate(ctx, store.Teammate{EnvironmentID: env.ID, Name: "ada", Harness: "claude", ProviderID: "sub", GitName: "ada", GitEmail: "ada@agents.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	sb, err := st.CreateSandbox(ctx, store.Sandbox{EnvironmentID: env.ID, Name: "ada-dev", CPUs: 1, TeammateID: tm.ID})
	if err != nil {
		t.Fatal(err)
	}
	vmRow, err := st.CreateBrowserSandbox(ctx, store.Sandbox{EnvironmentID: env.ID, TeammateID: tm.ID, Name: "browser-" + tm.ID, CPUs: 1})
	if err != nil {
		t.Fatal(err)
	}
	// Unix socket paths are short; t.TempDir's may be too long.
	dir, err := os.MkdirTemp("", "e2e")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	hub := agentchan.NewHub(log)
	f := &browserFixture{t: t, ctx: ctx, st: st, env: env.ID, teammate: tm.ID}
	f.svc = &browser.Service{Store: st, VMs: browserVMs{vmRow}, Runner: hub, Image: "browser:e2e", Dir: filepath.Join(dir, "shots"),
		Log: log, Hold: 500 * time.Millisecond}
	hub.OnCall = f.svc.HandleCall
	hub.OnForward = f.svc.DialForward
	rt := NewRuntime("base:e2e", log)
	t.Cleanup(func() {
		rt.Remove(ctx, "a")
		rt.Remove(ctx, "b")
	})
	for i, row := range []store.Sandbox{sb, vmRow} {
		name := []string{"a", "b"}[i]
		sock := filepath.Join(dir, name+".sock")
		if err := hub.Listen(row.ID, sock); err != nil {
			t.Fatal(err)
		}
		if err := rt.Create(ctx, name, runtime.Spec{}, sock, map[string]string{"studio.sandbox-id": row.ID, "studio.sandbox-name": row.Name}); err != nil {
			t.Fatal(err)
		}
		wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := hub.WaitConnected(wctx, row.ID)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
	}
	f.agent, _ = rt.Guest(sb.ID)
	f.vm, _ = rt.BrowserGuest(tm.ID)
	if f.agent == nil || f.vm == nil {
		t.Fatal("no guests")
	}
	return f
}

// call makes a tool call from the agent sandbox's guest and waits for its answer.
func (f *browserFixture) call(method, params string) Call {
	f.t.Helper()
	id, done := f.agent.Call(method, json.RawMessage(params))
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		f.t.Fatalf("%s did not return", method)
	}
	c, _ := f.agent.CallState(id)
	return c
}

func (f *browserFixture) result(c Call) string {
	f.t.Helper()
	if c.Error != "" {
		f.t.Fatalf("%s: %s", c.Method, c.Error)
	}
	var s string
	if json.Unmarshal(c.Result, &s) != nil {
		return string(c.Result)
	}
	return s
}

func (f *browserFixture) tab(slot int) BrowserSession {
	f.t.Helper()
	b, ok := f.vm.BrowserState()
	if !ok {
		f.t.Fatal("the browser VM got no browser stream")
	}
	for _, s := range b.Sessions {
		if s.Slot == slot {
			return s
		}
	}
	f.t.Fatalf("slot %d is not started: %+v", slot, b.Sessions)
	return BrowserSession{}
}

func TestBrowserToolsThroughTheChannel(t *testing.T) {
	f := newBrowserFixture(t)
	if got := f.result(f.call("browser_open", `{"url":"https://example.com/login"}`)); !strings.Contains(got, "opened https://example.com/login (Sign in · example.com)") {
		t.Fatalf("open: %s", got)
	}
	snap := f.result(f.call("browser_snapshot", `{}`))
	if !strings.Contains(snap, `textbox "Password" [ref=e2]`) || !strings.Contains(snap, `button "Sign in" [ref=e4]`) {
		t.Fatalf("snapshot: %s", snap)
	}
	// Clicking needs the user: the call returns while the approval waits.
	if c := f.call("browser_click", `{"ref":"e4"}`); !strings.Contains(c.Error, "waiting for the user") {
		t.Fatalf("click without a rule: %+v", c)
	}
	list, err := f.st.Approvals(f.ctx, f.env, store.StatusPending, 10)
	if err != nil || len(list) != 1 || list[0].Kind != browser.KindAction {
		t.Fatalf("approvals %+v %v", list, err)
	}
	actions, err := f.svc.Actions(f.ctx, f.env, f.teammate, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	var opened store.BrowserAction
	for _, a := range actions {
		if a.Action == "open" {
			opened = a
		}
	}
	if opened.Screenshot == "" || opened.Actor != "agent" {
		t.Fatalf("open logged as %+v", opened)
	}
	path, err := f.svc.ShotPath(f.ctx, f.env, f.teammate, opened.ID)
	if err != nil {
		t.Fatal(err)
	}
	shot, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jpeg.Decode(bytes.NewReader(shot)); err != nil {
		t.Errorf("the screenshot is no JPEG: %v", err)
	}

	// An ephemeral session is its own tab, in slot 1, and ends on browser_close.
	c := f.call("browser_open", `{"url":"https://example.org/","ephemeral":true}`)
	var eph struct {
		Session string `json:"session"`
	}
	if json.Unmarshal(c.Result, &eph); eph.Session == "" {
		t.Fatalf("ephemeral open: %+v", c)
	}
	if tab := f.tab(1); tab.URL != "https://example.org/" || f.tab(0).URL != "https://example.com/login" {
		t.Errorf("tabs %+v %+v", tab, f.tab(0))
	}
	if snap := f.result(f.call("browser_snapshot", `{"session":"`+eph.Session+`"}`)); !strings.Contains(snap, "page: https://example.org/") {
		t.Errorf("ephemeral snapshot: %s", snap)
	}
	if got := f.result(f.call("browser_close", `{"session":"`+eph.Session+`"}`)); !strings.Contains(got, "closed ephemeral session") {
		t.Errorf("close: %s", got)
	}
	if b, _ := f.vm.BrowserState(); len(b.Sessions) != 1 {
		t.Errorf("sessions after close: %+v", b.Sessions)
	}
}

// localhost in the browser is the sandbox that drives it: the ports it listens on when it
// calls, and no others.
func TestBrowserLocalhostIsTheDrivingSandbox(t *testing.T) {
	f := newBrowserFixture(t)
	want := "(Preview of sandbox " + f.agent.id + " on port 3000, path /app)"
	if got := f.result(f.call("browser_open", `{"url":"http://localhost:3000/app"}`)); !strings.Contains(got, want) {
		t.Fatalf("open localhost:3000: %s", got)
	}
	// A dev server started since is there by the next call, and one that stopped is gone.
	f.agent.SetPorts([]int{5173})
	if got := f.result(f.call("browser_open", `{"url":"http://127.0.0.1:5173/"}`)); !strings.Contains(got, "on port 5173, path /)") {
		t.Fatalf("open 127.0.0.1:5173: %s", got)
	}
	if got := f.result(f.call("browser_open", `{"url":"http://localhost:3000/"}`)); !strings.Contains(got, "localhost:3000 refused to connect") {
		t.Fatalf("open the stopped localhost:3000: %s", got)
	}
	// Only the browser VM's channel reaches a forward, and only on a forwarded port.
	if _, err := f.svc.DialForward(f.ctx, f.agent.id, 5173); !errors.Is(err, browser.ErrNoForward) {
		t.Fatalf("a forward from the agent sandbox: %v", err)
	}
	if _, err := f.svc.DialForward(f.ctx, f.vm.id, 3000); !errors.Is(err, browser.ErrNoForward) {
		t.Fatalf("a forward of a port nothing listens on: %v", err)
	}
}

func TestBrowserHistoryAndLiveView(t *testing.T) {
	f := newBrowserFixture(t)
	if err := f.svc.Start(f.ctx, f.env, f.teammate); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SetTakeover(f.ctx, f.env, f.teammate, "", true); err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"https://example.com/a", "https://example.com/b"} {
		if st, err := f.svc.Navigate(f.ctx, f.env, f.teammate, "", u); err != nil || st.URL != u {
			t.Fatalf("navigate %s: %+v %v", u, st, err)
		}
	}
	for _, step := range []struct{ to, want string }{{"back", "https://example.com/a"}, {"back", "about:blank"},
		{"forward", "https://example.com/a"}, {"reload", "https://example.com/a"}, {"forward", "https://example.com/b"}} {
		if st, err := f.svc.Go(f.ctx, f.env, f.teammate, "", step.to); err != nil || st.URL != step.want {
			t.Fatalf("%s: %+v %v, want %s", step.to, st, err, step.want)
		}
	}

	ui := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		f.svc.Relay(r.Context(), f.env, f.teammate, "", c)
	}))
	defer ui.Close()
	ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ui.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	type message struct {
		Type     string         `json:"type"`
		URL      string         `json:"url"`
		Data     string         `json:"data"`
		Metadata map[string]any `json:"metadata"`
	}
	read := func(typ string) message {
		t.Helper()
		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				t.Fatalf("waiting for %s: %v", typ, err)
			}
			var m message
			json.Unmarshal(data, &m)
			if m.Type == typ {
				return m
			}
		}
	}
	if m := read("url"); m.URL != "https://example.com/b" {
		t.Errorf("url %+v", m)
	}
	frame := read("frame")
	img, err := base64.StdEncoding.DecodeString(frame.Data)
	if err != nil {
		t.Fatal(err)
	}
	if cfg, err := jpeg.DecodeConfig(bytes.NewReader(img)); err != nil || cfg.Width != viewW || frame.Metadata["deviceWidth"] != float64(viewW) {
		t.Fatalf("frame %v %+v %v", cfg, frame.Metadata, err)
	}
	// The user clicks into the email field, types and submits the form.
	for _, msg := range []string{
		`{"type":"input_mouse","eventType":"mousePressed","x":100,"y":120,"button":"left","clickCount":1}`,
		`{"type":"input_keyboard","eventType":"char","text":"a"}`,
		`{"type":"input_keyboard","eventType":"keyDown","key":"Enter","code":"Enter","text":"\r","windowsVirtualKeyCode":13}`,
	} {
		if err := c.Write(ctx, websocket.MessageText, []byte(msg)); err != nil {
			t.Fatal(err)
		}
	}
	// Changes coalesce: the views in between may be skipped.
	for {
		if m := read("url"); m.URL == "https://example.com/welcome" {
			break
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		st, err := f.svc.Status(f.ctx, f.env, f.teammate)
		if err == nil && st.URL == "https://example.com/welcome" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the status did not follow the stream: %+v %v", st, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if b, _ := f.vm.BrowserState(); len(b.Inputs) != 3 || b.Sessions[0].Watchers != 1 {
		t.Errorf("browser state %+v", b)
	}
}
