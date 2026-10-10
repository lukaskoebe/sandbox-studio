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
	specs map[string]runtime.Spec
}

func (r *specRecordingRuntime) Create(ctx context.Context, name string, spec runtime.Spec, socket string, labels map[string]string) error {
	r.specs[name] = spec
	return r.fakeSandboxRuntime.Create(ctx, name, spec, socket, labels)
}

func browserPersona(t *testing.T, f *checkpointFixture) store.Persona {
	t.Helper()
	if _, err := f.store.CreateProvider(f.ctx, store.Provider{ID: "sub", EnvironmentID: f.env.ID, Name: "sub", Kind: "claude_subscription"}, nil); err != nil {
		t.Fatal(err)
	}
	p, err := f.store.CreatePersona(f.ctx, store.Persona{EnvironmentID: f.env.ID, Name: "ada", Harness: "claude", ProviderID: "sub", GitName: "ada", GitEmail: "ada@agents.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// The browser VM runs the browser image, is one per persona and stays out of every public
// sandbox path: the API, terminals and previews (which would reach its DevTools port).
func TestBrowserVMIsPrivate(t *testing.T) {
	f := newCheckpointFixture(t)
	rt := &specRecordingRuntime{f.runtime, map[string]runtime.Spec{}}
	f.manager.Runtime = rt
	p := browserPersona(t, f)

	sb, err := f.manager.createBrowser(f.ctx, f.env.ID, p.ID, "sandbox-studio-browser:dev")
	if err != nil {
		t.Fatal(err)
	}
	if sb.Kind != store.SandboxKindBrowser || sb.PersonaID != p.ID || sb.Name != BrowserName(p.ID) {
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
