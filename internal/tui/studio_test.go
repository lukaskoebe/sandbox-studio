package tui

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentchan"
	"github.com/lukaskoebe/sandbox-studio/internal/api"
	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/paths"
	"github.com/lukaskoebe/sandbox-studio/internal/policy"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/webauth"
)

// fakeVMs is the VM runtime of the tests: a VM is its status, which the tests set.
type fakeVMs struct {
	mu       sync.Mutex
	statuses map[string]runtime.Status
}

var _ sandboxes.SandboxRuntime = (*fakeVMs)(nil)

func (r *fakeVMs) set(name string, status runtime.Status) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.statuses[name] = status
}

func (r *fakeVMs) Create(_ context.Context, name string, _ runtime.Spec, _ string, _ map[string]string) error {
	r.set(name, runtime.StatusRunning)
	return nil
}

func (r *fakeVMs) Start(_ context.Context, name string) error {
	r.set(name, runtime.StatusRunning)
	return nil
}

func (r *fakeVMs) Stop(_ context.Context, name string) error {
	r.set(name, runtime.StatusStopped)
	return nil
}

func (r *fakeVMs) Pause(_ context.Context, name string) error {
	r.set(name, runtime.StatusSuspended)
	return nil
}

func (r *fakeVMs) Resume(_ context.Context, name string) error {
	r.set(name, runtime.StatusRunning)
	return nil
}

func (r *fakeVMs) Resize(context.Context, string, runtime.Size) (runtime.Resized, error) {
	return runtime.Resized{CPUs: runtime.AppliedNextStart, MemoryMiB: runtime.AppliedNextStart}, nil
}

func (r *fakeVMs) Remove(_ context.Context, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.statuses, name)
	return nil
}

func (r *fakeVMs) Status(_ context.Context, name string) (runtime.Status, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if status, ok := r.statuses[name]; ok {
		return status, nil
	}
	return runtime.StatusAbsent, nil
}

func (r *fakeVMs) Statuses(_ context.Context, prefix string) (map[string]runtime.Status, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]runtime.Status{}
	for name, status := range r.statuses {
		if strings.HasPrefix(name, prefix) {
			out[name] = status
		}
	}
	return out, nil
}

func (r *fakeVMs) CreateCheckpoint(context.Context, string, string, string) error { return nil }

func (r *fakeVMs) RemoveCheckpoint(context.Context, string, string) error { return nil }

func (r *fakeVMs) CheckpointRestoreSupported() bool { return false }

func (r *fakeVMs) RestoreCheckpoint(context.Context, string, string, string, runtime.Egress) error {
	return store.ErrNotFound
}

// fakeEgress hands out no network: the tests have no gateway.
type fakeEgress struct{}

func (fakeEgress) Attach(store.Sandbox) (runtime.Egress, error) { return runtime.Egress{}, nil }
func (fakeEgress) Detach(string)                                {}

// testStudio is an in-process Studio: the API behind the server's guards, over an in-memory
// catalog and a fake VM runtime. Nothing of it touches the machine's Studio.
type testStudio struct {
	Addr    string // host:port of the test server
	Control string // the control credential of the test installation
	Store   *store.Store
	VMs     *fakeVMs
	Bus     *events.Bus
	Env     store.Environment // the one environment of the catalog
}

// startStudio runs a test Studio with one environment named Default.
func startStudio(t *testing.T) *testStudio {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	env, err := st.CreateEnvironment(ctx, "Default")
	if err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{7}, 32)
	auth, err := webauth.New(key, func(string) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := &events.Bus{}
	vms := &fakeVMs{statuses: map[string]runtime.Status{}}
	mgr := &sandboxes.Manager{
		Store: st, Runtime: vms, Hub: agentchan.NewHub(log), Egress: fakeEgress{},
		Paths: paths.Paths{Data: t.TempDir()}, Log: log, Bus: bus,
	}
	s := &api.Server{Store: st, Sandboxes: mgr, Bus: bus, Log: log, Policy: &policy.Engine{Store: st, Bus: bus, Hold: time.Second}}
	mux := http.NewServeMux()
	s.Register(mux)
	srv := httptest.NewServer(api.Guard(auth.Middleware(mux)))
	t.Cleanup(srv.Close)
	return &testStudio{
		Addr: strings.TrimPrefix(srv.URL, "http://"), Control: webauth.ControlToken(key),
		Store: st, VMs: vms, Bus: bus, Env: env,
	}
}

// addSandbox records a sandbox of the test environment whose VM has the given status.
func (s *testStudio) addSandbox(t *testing.T, name string, status runtime.Status) store.Sandbox {
	t.Helper()
	sb, err := s.Store.CreateSandbox(context.Background(), store.Sandbox{EnvironmentID: s.Env.ID, Name: name, CPUs: 1})
	if err != nil {
		t.Fatal(err)
	}
	s.VMs.set(sandboxes.VMName(sb), status)
	return sb
}
