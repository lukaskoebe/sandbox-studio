package login

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentchan"
	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// fakeVault keeps values in memory and rows in the real store, as the vault does.
type fakeVault struct {
	st      *store.Store
	mu      sync.Mutex
	values  map[string]string
	changed int
}

func newFakeVault(st *store.Store) *fakeVault {
	return &fakeVault{st: st, values: map[string]string{}}
}

func placeholder() string { return "studio-" + strings.ToLower(store.NewID()) }

func (v *fakeVault) Prepare(envID, name, value string, hosts []string, note string) (store.Secret, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	id := store.NewID()
	v.values[id] = value
	return store.Secret{ID: id, EnvironmentID: envID, Name: name, Sealed: []byte("sealed"), Hosts: hosts, Placeholder: placeholder(), Note: note}, nil
}

func (v *fakeVault) Create(ctx context.Context, sec store.Secret, value string) (store.Secret, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	sec.ID = store.NewID()
	sec.Sealed, sec.Placeholder = []byte("sealed"), placeholder()
	v.values[sec.ID] = value
	return v.st.CreateSecret(ctx, sec)
}

func (v *fakeVault) Update(ctx context.Context, envID, id string, value *string, hosts []string, note string, username *string) (store.Secret, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if value != nil {
		v.values[id] = *value
	}
	return store.Secret{ID: id, EnvironmentID: envID}, nil
}

func (v *fakeVault) Value(ctx context.Context, envID, id string) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	val, ok := v.values[id]
	if !ok {
		return "", store.ErrNotFound
	}
	return val, nil
}

func (v *fakeVault) Changed(envID string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.changed++
}

// value returns the value of a secret, for assertions.
func (v *fakeVault) value(id string) string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.values[id]
}

// fakeMachines records the system VMs a test starts and removes.
type fakeMachines struct {
	mu      sync.Mutex
	live    map[string]bool // name → running
	failOn  string          // a VM name whose start fails
	ensured []string
	removed []string
	stopped []string
}

func newFakeMachines() *fakeMachines { return &fakeMachines{live: map[string]bool{}} }

func (m *fakeMachines) EnsureSystem(ctx context.Context, envID, kind, name string) (store.Sandbox, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if name == m.failOn {
		return store.Sandbox{}, errors.New("no capacity")
	}
	m.live[name] = true
	m.ensured = append(m.ensured, name)
	return store.Sandbox{ID: "vm-" + name, EnvironmentID: envID, Kind: kind, Name: name}, nil
}

func (m *fakeMachines) RemoveSystem(ctx context.Context, envID, kind, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.live, name)
	m.removed = append(m.removed, name)
	return nil
}

func (m *fakeMachines) StopSystem(ctx context.Context, envID, kind, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.live, name)
	m.stopped = append(m.stopped, name)
	return nil
}

func (m *fakeMachines) running(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.live[name]
}

// fakeGuest answers the commands the logins run. The login's pane shows pane, and its command has
// exited when dead (the pane outlives it); gone is a pane that no longer exists. files are what
// `cat` finds in the VM's home.
type fakeGuest struct {
	mu        sync.Mutex
	pane      string
	dead      bool
	gone      bool
	files     map[string]string
	runs      []string
	typed     string // what send-keys typed into the login's pane, keys in angle brackets
	written   [][]agentproto.HomeFile
	connected map[string]bool
	// cli is the answer to a one-shot CLI call (claude or codex), and calls records the calls.
	cli   agentproto.RunResult
	clErr error
	calls []agentproto.RunRequest
}

func newFakeGuest() *fakeGuest {
	return &fakeGuest{files: map[string]string{}, connected: map[string]bool{}}
}

// setPane shows text in the login's pane; dead says whether the CLI has exited.
func (g *fakeGuest) setPane(text string, dead bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pane, g.dead = text, dead
}

func (g *fakeGuest) setFile(path, content string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.files[path] = content
}

func (g *fakeGuest) commands() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.runs...)
}

func (g *fakeGuest) Run(ctx context.Context, id string, req agentproto.RunRequest) (agentproto.RunResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	line := strings.Join(req.Args, " ")
	g.runs = append(g.runs, line)
	switch {
	case strings.HasPrefix(line, "tmux new-session"), strings.HasPrefix(line, "tmux set-option"):
		return agentproto.RunResult{}, nil
	case strings.HasPrefix(line, "tmux send-keys"):
		// -l types its last argument literally; anything else is a key, such as Enter.
		if req.Args[len(req.Args)-2] == "--" {
			g.typed += req.Args[len(req.Args)-1]
		} else {
			g.typed += "<" + req.Args[len(req.Args)-1] + ">"
		}
		return agentproto.RunResult{}, nil
	case strings.HasPrefix(line, "tmux capture-pane"):
		if g.gone {
			return agentproto.RunResult{ExitCode: 1, Stderr: "can't find session: login"}, nil
		}
		return agentproto.RunResult{Stdout: g.pane}, nil
	case strings.HasPrefix(line, "tmux display-message"):
		if g.dead {
			return agentproto.RunResult{Stdout: "1\n"}, nil
		}
		return agentproto.RunResult{Stdout: "0\n"}, nil
	case req.Args[0] == "claude" || req.Args[0] == "codex":
		g.calls = append(g.calls, req)
		return g.cli, g.clErr
	case strings.HasPrefix(line, "cat "):
		if content, ok := g.files[req.Args[1]]; ok {
			return agentproto.RunResult{Stdout: content}, nil
		}
		return agentproto.RunResult{ExitCode: 1, Stderr: "no such file"}, nil
	}
	return agentproto.RunResult{}, errors.New("unexpected command: " + line)
}

func (g *fakeGuest) OpenPTY(ctx context.Context, id, name string, cols, rows uint16) (*agentchan.PTY, error) {
	return nil, errors.New("the fake guest has no terminal")
}

func (g *fakeGuest) WriteHomeFiles(ctx context.Context, id string, files []agentproto.HomeFile) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.written = append(g.written, files)
	return nil
}

func (g *fakeGuest) Connected(id string) (agentproto.Hello, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return agentproto.Hello{}, g.connected[id]
}

// testEnv is a service over a real store and the fakes above.
type testEnv struct {
	st       *store.Store
	vault    *fakeVault
	vms      *fakeMachines
	guest    *fakeGuest
	svc      *Service
	env      string
	notified int
}

func newTestEnv(t *testing.T) *testEnv {
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
	e := &testEnv{st: st, vault: newFakeVault(st), vms: newFakeMachines(), guest: newFakeGuest(), env: env.ID}
	e.svc = New(st, e.vault, e.vms, e.guest, nil)
	e.svc.Poll = 5 * time.Millisecond
	e.svc.Timeout = 5 * time.Second
	e.svc.FreeMemory = func() (int, bool) { return 8192, true } // the test host's own doesn't count
	e.svc.Notify = func(string) { e.vault.mu.Lock(); e.notified++; e.vault.mu.Unlock() }
	return e
}

// provider creates a subscription provider without a login.
func (e *testEnv) provider(t *testing.T, name, kind string) store.Provider {
	t.Helper()
	p, err := e.st.CreateProvider(context.Background(), store.Provider{ID: store.NewID(), EnvironmentID: e.env, Name: name, Kind: kind}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// await waits until the provider's sign-in leaves the states it is in while running.
func (e *testEnv) await(t *testing.T, providerID string) View {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		v := e.svc.Status(providerID)
		if v.State == StateDone || v.State == StateFailed || v.State == StateIdle {
			return v
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("the sign-in did not end: %+v", e.svc.Status(providerID))
	return View{}
}

// authJSON is a Codex auth file for a fresh ChatGPT login that expires at expires.
func authJSON(t *testing.T, expires time.Time, refresh string) string {
	t.Helper()
	l := testLogin(t, expires)
	l.RefreshToken = refresh
	data, err := json.Marshal(codexAuth{Tokens: l.codexToken, LastRefresh: l.LastRefresh})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
