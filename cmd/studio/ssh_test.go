package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// fakeSSHStudio is a Studio with sandboxes; a lifecycle action makes one run.
type fakeSSHStudio struct {
	sandboxes map[string][]sandboxes.View // by environment
	actions   []string
}

func (f *fakeSSHStudio) Environments(context.Context) ([]store.Environment, error) {
	var out []store.Environment
	for id := range f.sandboxes {
		out = append(out, store.Environment{ID: id})
	}
	return out, nil
}

func (f *fakeSSHStudio) Sandboxes(_ context.Context, env string) ([]sandboxes.View, error) {
	return f.sandboxes[env], nil
}

func (f *fakeSSHStudio) Lifecycle(_ context.Context, env, id, action string) (sandboxes.View, error) {
	f.actions = append(f.actions, action+" "+id)
	for i, sb := range f.sandboxes[env] {
		if sb.ID == id {
			f.sandboxes[env][i].Status = runtime.StatusRunning
			return f.sandboxes[env][i], nil
		}
	}
	return sandboxes.View{}, store.ErrNotFound
}

func sshView(env, id, name string, status runtime.Status) sandboxes.View {
	return sandboxes.View{Sandbox: store.Sandbox{ID: id, EnvironmentID: env, Name: name, Generation: 2}, Status: status}
}

func TestSSHSandboxFindsAndStartsTheSandbox(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		host, want, action string
	}{
		{"api.studio", "a1", ""},
		{"A1.STUDIO", "a1", ""},
		{"web.studio", "w1", "start w1"},
		{"w1.studio", "w1", "start w1"},
		{"docs.studio", "d1", "resume d1"},
		{"dup-x1.studio", "x1", ""},
		{"dup-x2", "x2", ""},
	} {
		f := &fakeSSHStudio{sandboxes: map[string][]sandboxes.View{
			"e1": {
				sshView("e1", "a1", "api", runtime.StatusRunning),
				sshView("e1", "w1", "web", runtime.StatusStopped),
				sshView("e1", "d1", "docs", runtime.StatusSuspended),
				sshView("e1", "x1", "dup", runtime.StatusRunning),
			},
			"e2": {sshView("e2", "x2", "dup", runtime.StatusRunning)},
		}}
		var said []string
		sb, err := sshSandbox(ctx, f, tc.host, time.Second, func(s string) { said = append(said, s) })
		if err != nil || sb.ID != tc.want {
			t.Fatalf("%s: sandbox %q, %v; want %q", tc.host, sb.ID, err, tc.want)
		}
		if got := strings.Join(f.actions, ","); got != tc.action {
			t.Fatalf("%s: actions %q, want %q", tc.host, got, tc.action)
		}
		if (tc.action == "") != (len(said) == 0) {
			t.Fatalf("%s: said %q", tc.host, said)
		}
		if sandboxes.VMName(sb) != "ss-"+tc.want+"-g2" {
			t.Fatalf("%s: VM %s", tc.host, sandboxes.VMName(sb))
		}
	}
}

func TestSSHSandboxRefuses(t *testing.T) {
	ctx := context.Background()
	f := &fakeSSHStudio{sandboxes: map[string][]sandboxes.View{
		"e1": {sshView("e1", "x1", "dup", runtime.StatusRunning), sshView("e1", "f1", "broken", "failed"),
			sshView("e1", "s1", "slow", runtime.StatusStarting)},
		"e2": {sshView("e2", "x2", "dup", runtime.StatusRunning)},
	}}
	say := func(string) {}
	for host, want := range map[string]string{
		"nope.studio":   "Studio has no sandbox nope",
		"dup.studio":    "dup-x1.studio",
		"broken.studio": "can't start (failed)",
		"slow.studio":   "still starting",
	} {
		_, err := sshSandbox(ctx, f, host, 10*time.Millisecond, say)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: %v, want an error with %q", host, err, want)
		}
	}
	if len(f.actions) != 0 {
		t.Fatalf("actions %v", f.actions)
	}
}
