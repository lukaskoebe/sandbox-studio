package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// fakeField is an element of the fake page.
type fakeField struct {
	ref, role, name, typ, autocomplete string
}

// fakeBrowser stands in for agent-browser in a browser VM: it answers commands in
// agent-browser 0.39's --json format from a small page model and records what it got.
type fakeBrowser struct {
	mu           sync.Mutex
	url          string
	fields       []fakeField
	values       map[string]string // ref → value
	commands     []agentproto.BrowserCommand
	vms          []string // the VM each command went to
	failArgs     string   // a command (joined args) that fails, echoing its arguments
	blockArgs    string
	blockStarted chan struct{}
	blockRelease chan struct{}
	// lostArgs is a command whose reply is lost: it runs, then its reply fails with EOF, as
	// when the channel breaks. unreachable fails every command without running it.
	lostArgs    string
	unreachable bool
	dial        func(port int) (net.Conn, error)
	// downloads are the saved files by ephemeral slot, with their bodies by "slot/name".
	downloads map[int][]agentproto.BrowserDownload
	bodies    map[string]string
	// uploads are the copies that reached sandboxes, and uploadErr makes the next one fail.
	uploads   []fakeUpload
	uploadErr error
}

// fakeUpload is a file the fake sandboxes received.
type fakeUpload struct {
	sandbox string
	req     agentproto.UploadRequest
	body    string
}

func newFakeBrowser() *fakeBrowser {
	return &fakeBrowser{
		url: "https://example.com/login",
		fields: []fakeField{
			{"e1", "textbox", "Email", "email", "username"},
			{"e2", "textbox", "Password", "password", "current-password"},
			{"e3", "textbox", "API token", "text", "off"},
			{"e4", "button", "Sign in", "submit", ""},
		},
		values: map[string]string{"e1": "ada@example.com"},
	}
}

// snapshot renders the page like agent-browser's render_tree.
func (f *fakeBrowser) snapshot() string {
	var b strings.Builder
	for _, fl := range f.fields {
		q, _ := json.Marshal(fl.name)
		fmt.Fprintf(&b, "- %s %s [ref=%s]", fl.role, q, fl.ref)
		if v := f.values[fl.ref]; v != "" && v != fl.name {
			b.WriteString(": " + v)
		}
		b.WriteString("\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func ok(data any) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"success": true, "data": data, "error": nil})
	return b
}

func (f *fakeBrowser) answer(args []string) (any, error) {
	switch args[0] {
	case "get":
		switch args[1] {
		case "url":
			return map[string]any{"url": f.url}, nil
		case "attr":
			for _, fl := range f.fields {
				if "@"+fl.ref == args[2] {
					v := map[string]string{"type": fl.typ, "autocomplete": fl.autocomplete}[args[3]]
					return map[string]any{"value": v, "origin": f.url}, nil
				}
			}
			return nil, fmt.Errorf("Element not found: %s", args[2])
		case "text":
			// The page echoes what was typed into it, as pages do.
			var parts []string
			for _, v := range f.values {
				parts = append(parts, v)
			}
			return map[string]any{"text": "Welcome " + strings.Join(parts, " ")}, nil
		}
	case "open":
		f.url = args[1]
		f.values = map[string]string{} // a new page
		return map[string]any{"url": args[1], "title": "Example"}, nil
	case "snapshot":
		refs := map[string]any{}
		for _, fl := range f.fields {
			refs[fl.ref] = map[string]string{"role": fl.role, "name": fl.name}
		}
		return map[string]any{"snapshot": f.snapshot(), "origin": f.url, "refs": refs}, nil
	case "fill", "type":
		f.values[strings.TrimPrefix(args[1], "@")] = args[2]
		return map[string]any{"filled": args[1]}, nil
	case "close":
		return map[string]any{}, nil
	case "click", "press", "select", "scroll", "back", "forward", "reload", "wait":
		return map[string]any{}, nil
	}
	return nil, fmt.Errorf("unknown command %q", args[0])
}

func (f *fakeBrowser) Browser(ctx context.Context, vm string, cmd agentproto.BrowserCommand) (agentproto.BrowserResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, cmd)
	f.vms = append(f.vms, vm)
	var res agentproto.BrowserResult
	if f.unreachable {
		return res, io.EOF
	}
	if strings.Join(cmd.Args, " ") == f.blockArgs && f.blockStarted != nil && f.blockRelease != nil {
		started, release := f.blockStarted, f.blockRelease
		f.blockArgs = ""
		close(started)
		f.mu.Unlock()
		select {
		case <-release:
		case <-ctx.Done():
			f.mu.Lock()
			return res, ctx.Err()
		}
		f.mu.Lock()
	}
	if cmd.Args[0] == "batch" {
		var cmds [][]string
		if err := json.Unmarshal([]byte(cmd.Stdin), &cmds); err != nil {
			return res, err
		}
		var items []map[string]any
		for _, c := range cmds {
			data, err := f.answer(c)
			item := map[string]any{"command": c, "success": err == nil, "result": data, "error": nil}
			if err != nil {
				item["error"] = err.Error()
			}
			items = append(items, item)
		}
		res.Output, _ = json.Marshal(items)
		return res, nil
	}
	if strings.Join(cmd.Args, " ") == f.failArgs {
		res.Output, _ = json.Marshal(map[string]any{"success": false, "data": nil, "error": "failed: " + strings.Join(cmd.Args, " ")})
		return res, nil
	}
	data, err := f.answer(cmd.Args)
	if strings.Join(cmd.Args, " ") == f.lostArgs {
		f.lostArgs = ""
		return res, io.EOF
	}
	if err != nil {
		res.Output, _ = json.Marshal(map[string]any{"success": false, "data": nil, "error": err.Error()})
	} else {
		res.Output = ok(data)
	}
	if cmd.Screenshot {
		// The "pixels" show every visible value, the way a real screenshot would.
		var shot bytes.Buffer
		shot.WriteString("\xff\xd8JPEG " + f.url)
		for _, fl := range f.fields {
			if fl.typ != "password" {
				shot.WriteString(" " + f.values[fl.ref])
			}
		}
		res.Screenshot = shot.Bytes()
	}
	return res, nil
}

func (f *fakeBrowser) DialTCP(_ context.Context, _ string, port int) (net.Conn, error) {
	if f.dial == nil {
		return nil, fmt.Errorf("no stream")
	}
	return f.dial(port)
}

// saveDownload makes a file appear in a session's downloads folder of the fake VM.
func (f *fakeBrowser) saveDownload(slot int, name, body string, saved time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.downloads == nil {
		f.downloads, f.bodies = map[int][]agentproto.BrowserDownload{}, map[string]string{}
	}
	f.downloads[slot] = append(f.downloads[slot], agentproto.BrowserDownload{Name: name, Size: int64(len(body)), Modified: saved.Unix()})
	f.bodies[fmt.Sprintf("%d/%s", slot, name)] = body
}

func (f *fakeBrowser) BrowserDownloads(_ context.Context, _ string, req agentproto.BrowserDownloadsRequest) (agentproto.BrowserDownloadList, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return agentproto.BrowserDownloadList{Files: append([]agentproto.BrowserDownload(nil), f.downloads[req.Ephemeral]...)}, nil
}

func (f *fakeBrowser) BrowserDownload(_ context.Context, _ string, req agentproto.BrowserDownloadsRequest,
	fn func(agentproto.BrowserDownload, io.Reader) error) error {
	f.mu.Lock()
	var d *agentproto.BrowserDownload
	for i := range f.downloads[req.Ephemeral] {
		if f.downloads[req.Ephemeral][i].Name == req.Name {
			d = &f.downloads[req.Ephemeral][i]
		}
	}
	body := f.bodies[fmt.Sprintf("%d/%s", req.Ephemeral, req.Name)]
	f.mu.Unlock()
	if d == nil {
		return fmt.Errorf("no such download")
	}
	return fn(*d, strings.NewReader(body))
}

func (f *fakeBrowser) Upload(_ context.Context, sandboxID string, req agentproto.UploadRequest, body io.Reader) (agentproto.UploadReply, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return agentproto.UploadReply{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.uploadErr != nil {
		err, f.uploadErr = f.uploadErr, nil
		return agentproto.UploadReply{}, err
	}
	f.uploads = append(f.uploads, fakeUpload{sandbox: sandboxID, req: req, body: string(data)})
	return agentproto.UploadReply{Name: req.Name, Size: int64(len(data))}, nil
}

func (f *fakeBrowser) sent() []fakeUpload {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeUpload(nil), f.uploads...)
}

func (f *fakeBrowser) allCommands() []agentproto.BrowserCommand {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]agentproto.BrowserCommand(nil), f.commands...)
}

// fakeVMs keeps browser VM rows in the store and pretends they run.
type fakeVMs struct {
	st         *store.Store
	mu         sync.Mutex
	stopped    map[string]bool
	stops      int
	statusHook func()
}

func (v *fakeVMs) EnsureBrowser(ctx context.Context, envID, teammateID, image string) (store.Sandbox, error) {
	sb, err := v.st.BrowserSandbox(ctx, envID, teammateID)
	if err != nil {
		sb, err = v.st.CreateBrowserSandbox(ctx, store.Sandbox{EnvironmentID: envID, TeammateID: teammateID, Name: "browser-" + teammateID, CPUs: 1})
	}
	v.mu.Lock()
	delete(v.stopped, teammateID)
	v.mu.Unlock()
	return sb, err
}

func (v *fakeVMs) StopBrowser(_ context.Context, _, teammateID string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.stopped == nil {
		v.stopped = map[string]bool{}
	}
	v.stopped[teammateID] = true
	v.stops++
	return nil
}

func (v *fakeVMs) RemoveBrowser(ctx context.Context, envID, teammateID string) error {
	sb, err := v.st.BrowserSandbox(ctx, envID, teammateID)
	if err != nil {
		return nil
	}
	return v.st.DeleteSandbox(ctx, envID, sb.ID)
}

func (v *fakeVMs) BrowserStatus(ctx context.Context, envID, teammateID string) (store.Sandbox, runtime.Status, error) {
	sb, err := v.st.BrowserSandbox(ctx, envID, teammateID)
	if err != nil {
		return sb, runtime.StatusAbsent, nil
	}
	v.mu.Lock()
	stopped, hook := v.stopped[teammateID], v.statusHook
	v.mu.Unlock()
	if hook != nil {
		hook()
	}
	if stopped {
		return sb, runtime.StatusStopped, nil
	}
	return sb, runtime.StatusRunning, nil
}

type fakeKeys map[string]string // secret ID → value

func (k fakeKeys) Value(_ context.Context, _, id string) (string, error) {
	v, ok := k[id]
	if !ok {
		return "", fmt.Errorf("no secret %s", id)
	}
	return v, nil
}

// syncBuffer is a log sink safe for concurrent writes.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type fixture struct {
	t        *testing.T
	ctx      context.Context
	st       *store.Store
	env      string
	ada, bob store.Teammate
	sbAda    store.Sandbox // an agent sandbox of ada
	sbAda2   store.Sandbox // another one
	sbBob    store.Sandbox
	sbPlain  store.Sandbox // no teammate
	fb       *fakeBrowser
	vms      *fakeVMs
	keys     fakeKeys
	logs     *syncBuffer
	svc      *Service
	notified chan string
}

func newFixture(t *testing.T) *fixture {
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
	f := &fixture{t: t, ctx: ctx, st: st, env: env.ID, fb: newFakeBrowser(), keys: fakeKeys{}, logs: &syncBuffer{}, notified: make(chan string, 64)}
	teammate := func(name string) store.Teammate {
		p, err := st.CreateTeammate(ctx, store.Teammate{EnvironmentID: env.ID, Name: name, Harness: "claude", ProviderID: "sub", GitName: name, GitEmail: name + "@agents.invalid"})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	f.ada, f.bob = teammate("ada"), teammate("bob")
	sandbox := func(name, teammate string) store.Sandbox {
		s, err := st.CreateSandbox(ctx, store.Sandbox{EnvironmentID: env.ID, Name: name, CPUs: 1, TeammateID: teammate})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	f.sbAda, f.sbAda2, f.sbBob, f.sbPlain = sandbox("ada-dev", f.ada.ID), sandbox("ada-two", f.ada.ID), sandbox("bob-dev", f.bob.ID), sandbox("plain", "")
	f.vms = &fakeVMs{st: st}
	f.svc = &Service{
		Store: st, VMs: f.vms, Runner: f.fb, Keys: f.keys, Image: "browser:test", Dir: filepath.Join(t.TempDir(), "browser"),
		Log:       slog.New(slog.NewTextHandler(f.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Notify:    func(env string) { f.notified <- env },
		Hold:      2 * time.Second,
		PauseWait: 300 * time.Millisecond,
	}
	return f
}

func (f *fixture) call(sb store.Sandbox, method string, params string) (any, error) {
	f.t.Helper()
	return f.svc.HandleCall(f.ctx, sb.ID, method, json.RawMessage(params))
}

func (f *fixture) mustCall(sb store.Sandbox, method string, params string) any {
	f.t.Helper()
	res, err := f.call(sb, method, params)
	if err != nil {
		f.t.Fatalf("%s: %v", method, err)
	}
	return res
}

// pending waits for a pending approval of a kind.
func (f *fixture) pending(kind string) store.Approval {
	f.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		list, err := f.st.Approvals(f.ctx, f.env, store.StatusPending, 50)
		if err != nil {
			f.t.Fatal(err)
		}
		for _, a := range list {
			if a.Kind == kind {
				return a
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	f.t.Fatalf("no pending %s approval", kind)
	return store.Approval{}
}
