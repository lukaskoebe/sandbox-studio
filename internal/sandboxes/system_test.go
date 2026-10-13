package sandboxes

import (
	"errors"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// A system VM is hidden like a browser VM, boots with the system resources and is configured
// with the environment before anything uses it; stop and remove are idempotent.
func TestSystemVMLifecycle(t *testing.T) {
	f := newCheckpointFixture(t)
	rt := &specRecordingRuntime{fakeSandboxRuntime: f.runtime, specs: map[string]runtime.Spec{}}
	f.manager.Runtime = rt
	const kind, name = store.SandboxKindClaudeUtility, "utility-prov"

	sb, err := f.manager.CreateSystem(f.ctx, f.env.ID, kind, name)
	if err != nil {
		t.Fatal(err)
	}
	if Public(sb) || sb.Kind != kind || sb.TeammateID != "" {
		t.Fatalf("system VM row = %+v; want a hidden %s row without an owner", sb, kind)
	}
	if spec := rt.specs[VMName(sb)]; spec.WorkspaceMiB != uint32(systemResources.WorkspaceMiB) || spec.BaseImage != "" {
		t.Fatalf("system VM spec = %+v; want the system resources and the default image", spec)
	}
	if _, err := f.manager.CreateSystem(f.ctx, f.env.ID, kind, name); !errors.Is(err, store.ErrExists) {
		t.Fatalf("a second VM named %s: %v, want ErrExists", name, err)
	}

	guest, ctx := setupConfigTest(t, f, sb.ID, newConfigTestSecrets(map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "studio-x"}, nil))
	result := make(chan browserEnsureResult, 1)
	go func() {
		got, err := f.manager.EnsureSystem(ctx, f.env.ID, kind, name)
		result <- browserEnsureResult{sandbox: got, err: err}
	}()
	call := receive(t, ctx, guest.calls, configTestTimeout, "system VM guest configuration")
	if call.cfg.Env["CLAUDE_CODE_OAUTH_TOKEN"] != "studio-x" {
		t.Fatalf("system VM config = %+v", call.cfg)
	}
	call.reply <- configTestReply{}
	out := receive(t, ctx, result, configTestTimeout, "EnsureSystem result")
	if out.err != nil || out.sandbox.ID != sb.ID {
		t.Fatalf("EnsureSystem = %+v, %v; want the existing VM", out.sandbox, out.err)
	}
	if f.runtime.countCall("create:"+VMName(sb)) != 1 {
		t.Fatal("EnsureSystem created a second VM for an existing row")
	}

	if err := f.manager.StopSystem(ctx, f.env.ID, kind, name); err != nil {
		t.Fatal(err)
	}
	if _, st, err := f.manager.SystemStatus(ctx, f.env.ID, kind, name); err != nil || st != runtime.StatusStopped {
		t.Fatalf("status after stop: %v, %v", st, err)
	}
	if err := f.manager.RemoveSystem(ctx, f.env.ID, kind, name); err != nil {
		t.Fatal(err)
	}
	if err := f.manager.RemoveSystem(ctx, f.env.ID, kind, name); err != nil {
		t.Fatalf("removing a removed VM: %v", err)
	}
	if _, err := f.store.SystemSandbox(ctx, f.env.ID, kind, name); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("row after remove: %v", err)
	}
	if err := f.manager.StopSystem(ctx, f.env.ID, kind, name); err != nil {
		t.Fatalf("stopping a removed VM: %v", err)
	}
}
