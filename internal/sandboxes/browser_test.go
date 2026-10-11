package sandboxes

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

type specRecordingRuntime struct {
	*fakeSandboxRuntime
	specs    map[string]runtime.Spec
	onCreate func(string)
}

func (r *specRecordingRuntime) Create(ctx context.Context, name string, spec runtime.Spec, socket string, labels map[string]string) error {
	r.specs[name] = spec
	err := r.fakeSandboxRuntime.Create(ctx, name, spec, socket, labels)
	if r.onCreate != nil {
		r.onCreate(name)
	}
	return err
}

type browserEnsureResult struct {
	sandbox store.Sandbox
	err     error
}

func browserTeammate(t *testing.T, f *checkpointFixture) store.Teammate {
	t.Helper()
	if _, err := f.store.CreateProvider(f.ctx, store.Provider{ID: "sub", EnvironmentID: f.env.ID, Name: "sub", Kind: "claude_subscription"}, nil); err != nil {
		t.Fatal(err)
	}
	p, err := f.store.CreateTeammate(f.ctx, store.Teammate{EnvironmentID: f.env.ID, Name: "ada", Harness: "claude", ProviderID: "sub", GitName: "ada", GitEmail: "ada@agents.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEnsureBrowserConfiguresConnectedGuestBeforeReturning(t *testing.T) {
	f := newCheckpointFixture(t)
	rt := &specRecordingRuntime{fakeSandboxRuntime: f.runtime, specs: map[string]runtime.Spec{}}
	f.manager.Runtime = rt
	p := browserTeammate(t, f)
	sb, err := f.manager.createBrowser(f.ctx, f.env.ID, p.ID, "sandbox-studio-browser:dev")
	if err != nil {
		t.Fatal(err)
	}
	guest, ctx := setupConfigTest(t, f, sb.ID, newConfigTestSecrets(map[string]string{"TOKEN": "placeholder"}, nil))

	result := make(chan browserEnsureResult, 1)
	go func() {
		got, err := f.manager.EnsureBrowser(ctx, f.env.ID, p.ID, "ignored")
		result <- browserEnsureResult{sandbox: got, err: err}
	}()
	call := receive(t, ctx, guest.calls, configTestTimeout, "browser guest configuration")
	if call.cfg.CA != "test CA PEM" || call.cfg.Env["TOKEN"] != "placeholder" {
		t.Fatalf("browser guest config = %+v", call.cfg)
	}
	assertQuiet(t, ctx, result, "EnsureBrowser before the guest configuration ACK")
	call.reply <- configTestReply{err: "guest rejected configuration"}
	out := receive(t, ctx, result, configTestTimeout, "EnsureBrowser rejection result")
	if out.err == nil || out.err.Error() != "guest rejected configuration" {
		t.Fatalf("EnsureBrowser rejection error = %v, want guest rejection", out.err)
	}
	if f.runtime.countCall("create:"+VMName(sb)) != 1 || f.runtime.countCall("start:"+VMName(sb)) != 0 {
		t.Fatal("configuration rejection triggered another browser VM create or start")
	}

	result = make(chan browserEnsureResult, 1)
	go func() {
		got, err := f.manager.EnsureBrowser(ctx, f.env.ID, p.ID, "ignored")
		result <- browserEnsureResult{sandbox: got, err: err}
	}()
	call = receive(t, ctx, guest.calls, configTestTimeout, "browser guest configuration retry")
	if call.cfg.CA != "test CA PEM" || call.cfg.Env["TOKEN"] != "placeholder" {
		t.Fatalf("browser guest retry config = %+v", call.cfg)
	}
	assertQuiet(t, ctx, result, "EnsureBrowser before the retry configuration ACK")
	call.reply <- configTestReply{}
	out = receive(t, ctx, result, configTestTimeout, "EnsureBrowser retry result")
	if out.err != nil || out.sandbox.ID != sb.ID {
		t.Fatalf("EnsureBrowser retry = %+v, %v", out.sandbox, out.err)
	}
}

func TestEnsureBrowserWaitsForNewGuestThenConfiguresIt(t *testing.T) {
	f := newCheckpointFixture(t)
	created := make(chan string, 1)
	rt := &specRecordingRuntime{
		fakeSandboxRuntime: f.runtime,
		specs:              map[string]runtime.Spec{},
		onCreate:           func(name string) { created <- name },
	}
	f.manager.Runtime = rt
	p := browserTeammate(t, f)
	f.manager.CA = configTestCA{cert: []byte("test CA PEM")}
	f.manager.Secrets = newConfigTestSecrets(nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), configTestTimeout)
	defer cancel()

	result := make(chan browserEnsureResult, 1)
	go func() {
		got, err := f.manager.EnsureBrowser(ctx, f.env.ID, p.ID, "sandbox-studio-browser:dev")
		result <- browserEnsureResult{sandbox: got, err: err}
	}()
	createdVM := receive(t, ctx, created, configTestTimeout, "browser VM creation")
	sb, err := f.store.BrowserSandbox(ctx, f.env.ID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if createdVM != VMName(sb) {
		t.Fatalf("created VM = %q, want %q", createdVM, VMName(sb))
	}
	assertQuiet(t, ctx, result, "EnsureBrowser before the new guest connects")

	guest := startConfigTestGuest(t, f, sb.ID)
	call := receive(t, ctx, guest.calls, configTestTimeout, "new browser guest configuration")
	if call.cfg.CA != "test CA PEM" {
		t.Fatalf("new browser guest config = %+v", call.cfg)
	}
	assertQuiet(t, ctx, result, "EnsureBrowser before the new guest configuration ACK")
	call.reply <- configTestReply{}
	out := receive(t, ctx, result, configTestTimeout, "EnsureBrowser result")
	if out.err != nil || out.sandbox.ID != sb.ID {
		t.Fatalf("EnsureBrowser = %+v, %v", out.sandbox, out.err)
	}
}

// The browser VM runs the browser image, is one per teammate and stays out of every public
// sandbox path: the API, terminals and previews (which would reach its DevTools port).
func TestBrowserVMIsPrivate(t *testing.T) {
	f := newCheckpointFixture(t)
	rt := &specRecordingRuntime{fakeSandboxRuntime: f.runtime, specs: map[string]runtime.Spec{}}
	f.manager.Runtime = rt
	p := browserTeammate(t, f)

	sb, err := f.manager.createBrowser(f.ctx, f.env.ID, p.ID, "sandbox-studio-browser:dev")
	if err != nil {
		t.Fatal(err)
	}
	if sb.Kind != store.SandboxKindBrowser || sb.TeammateID != p.ID || sb.Name != BrowserName(p.ID) {
		t.Fatalf("browser row %+v", sb)
	}
	if spec := rt.specs[VMName(sb)]; spec.BaseImage != "sandbox-studio-browser:dev" || spec.Image != nil {
		t.Fatalf("browser VM spec %+v", spec)
	}
	again, err := f.manager.createBrowser(f.ctx, f.env.ID, p.ID, "sandbox-studio-browser:dev")
	if err != nil || again.ID != sb.ID || f.runtime.countCall("create:"+VMName(sb)) != 1 {
		t.Fatalf("second create: %+v %v", again, err)
	}

	if _, err := f.manager.PublicSandbox(f.ctx, f.env.ID, sb.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("PublicSandbox = %v", err)
	}
	views, err := f.manager.List(f.ctx, f.env.ID)
	if err != nil || len(views) != 1 || views[0].ID != f.sandbox.ID {
		t.Errorf("List = %+v, %v", views, err)
	}
	if _, err := f.manager.DialPreviewTCP(f.ctx, sb.ID, 9223); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("DialPreviewTCP = %v", err)
	}
	if _, err := f.manager.OpenTerminal(f.ctx, f.env.ID, sb.ID, "main", 80, 24); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("OpenTerminal = %v", err)
	}
	if err := f.manager.Delete(f.ctx, f.env.ID, sb.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Delete = %v", err)
	}

	// Stopped, it is started again on the next use; the agent never connects here.
	f.runtime.setStatus(VMName(sb), runtime.StatusStopped)
	ctx, cancel := context.WithTimeout(f.ctx, 50*time.Millisecond)
	defer cancel()
	if _, err := f.manager.EnsureBrowser(ctx, f.env.ID, p.ID, "x"); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("EnsureBrowser = %v", err)
	}
	if f.runtime.countCall("start:"+VMName(sb)) != 1 {
		t.Error("the stopped browser VM was not started")
	}

	if err := f.manager.RemoveBrowser(f.ctx, f.env.ID, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.BrowserSandbox(f.ctx, f.env.ID, p.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("browser row after remove: %v", err)
	}
}
