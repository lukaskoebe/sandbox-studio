package sandboxes

import (
	"bufio"
	"context"
	"errors"
	"maps"
	"net"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

const configTestTimeout = 5 * time.Second

func TestConfigureGuestWaitsForACKAndReturnsGuestError(t *testing.T) {
	f := newCheckpointFixture(t)
	secrets := newConfigTestSecrets(map[string]string{"TOKEN": "placeholder"}, nil)
	guest, ctx := setupConfigTest(t, f, f.sandbox.ID, secrets)

	for _, guestErr := range []string{"", "guest rejected configuration"} {
		result := configureGuestAsync(f, ctx)
		call := receive(t, ctx, guest.calls, configTestTimeout, "guest configuration")
		if call.cfg.CA != "test CA PEM" || call.cfg.Env["TOKEN"] != "placeholder" {
			t.Fatalf("guest config = %+v", call.cfg)
		}
		if guestErr == "" {
			assertQuiet(t, ctx, result, "ConfigureGuest before the guest ACK")
		}
		call.reply <- configTestReply{err: guestErr}
		err := receive(t, ctx, result, configTestTimeout, "ConfigureGuest result")
		if guestErr == "" && err != nil {
			t.Fatalf("ConfigureGuest after ACK: %v", err)
		}
		if guestErr != "" && (err == nil || err.Error() != guestErr) {
			t.Fatalf("ConfigureGuest error = %v, want %q", err, guestErr)
		}
	}
}

func TestConfigureGuestRejectsInvalidEnvironmentOrSandboxWithoutDelivery(t *testing.T) {
	for _, name := range []string{"empty environment", "wrong environment", "missing sandbox"} {
		t.Run(name, func(t *testing.T) {
			f := newCheckpointFixture(t)
			sandboxID, envID := f.sandbox.ID, f.env.ID
			switch name {
			case "empty environment":
				envID = ""
			case "wrong environment":
				envID = "wrong-environment"
			case "missing sandbox":
				sandboxID = "missing"
			}
			guest, ctx := setupConfigTest(t, f, sandboxID, newConfigTestSecrets(nil, nil))
			err := f.manager.ConfigureGuest(ctx, envID, sandboxID)
			if !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("ConfigureGuest error = %v, want %v", err, store.ErrNotFound)
			}
			assertQuiet(t, ctx, guest.calls, "guest configuration delivery")
		})
	}
}

func TestConfigureGuestGateAndACKCancellationKeepSessionUsable(t *testing.T) {
	f := newCheckpointFixture(t)
	guest, ctx := setupConfigTest(t, f, f.sandbox.ID, newConfigTestSecrets(nil, nil))
	firstCtx, cancelFirst := context.WithCancel(ctx)
	defer cancelFirst()
	first := configureGuestAsync(f, firstCtx)
	_ = receive(t, ctx, guest.calls, configTestTimeout, "first guest configuration") // No ACK yet.

	secondCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	started, second := make(chan struct{}), make(chan error, 1)
	go func() {
		close(started)
		second <- f.manager.ConfigureGuest(secondCtx, f.env.ID, f.sandbox.ID)
	}()
	_ = receive(t, ctx, started, configTestTimeout, "second ConfigureGuest start")
	assertQuiet(t, ctx, second, "second ConfigureGuest while waiting for the gate")
	assertQuiet(t, ctx, guest.calls, "guest configuration delivery")
	cancel()
	if err := receive(t, ctx, second, 500*time.Millisecond, "cancelled second ConfigureGuest"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled second ConfigureGuest error = %v, want context.Canceled", err)
	}
	assertQuiet(t, ctx, first, "first ConfigureGuest after the second caller was cancelled")

	cancelFirst()
	if err := receive(t, ctx, first, 500*time.Millisecond, "cancelled first ConfigureGuest"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled first ConfigureGuest error = %v, want context.Canceled", err)
	}
	if _, ok := f.manager.Hub.Connected(f.sandbox.ID); !ok {
		t.Fatal("cancelling a configuration request disconnected the guest")
	}

	third := configureGuestAsync(f, ctx)
	call := receive(t, ctx, guest.calls, configTestTimeout, "post-cancellation guest configuration")
	call.reply <- configTestReply{}
	if err := receive(t, ctx, third, configTestTimeout, "post-cancellation ConfigureGuest result"); err != nil {
		t.Fatalf("ConfigureGuest after cancellation: %v", err)
	}
}

func TestConfigureGuestPropagatesProviderFailuresWithoutDelivery(t *testing.T) {
	caErr, secretsErr := errors.New("CA provider unavailable"), errors.New("secrets provider unavailable")
	for _, tt := range []struct {
		name    string
		ca      configTestCA
		secrets *configTestSecrets
		want    error
	}{
		{"CA", configTestCA{err: caErr}, newConfigTestSecrets(nil, nil), caErr},
		{"secrets", configTestCA{cert: []byte("test CA")}, newConfigTestSecrets(nil, secretsErr), secretsErr},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newCheckpointFixture(t)
			guest, ctx := setupConfigTest(t, f, f.sandbox.ID, tt.secrets)
			f.manager.CA, f.manager.Secrets = tt.ca, tt.secrets
			err := f.manager.ConfigureGuest(ctx, f.env.ID, f.sandbox.ID)
			if !errors.Is(err, tt.want) {
				t.Fatalf("ConfigureGuest error = %v, want %v", err, tt.want)
			}
			assertQuiet(t, ctx, guest.calls, "guest configuration delivery")
		})
	}
}

func TestConfigureGuestSerializedPushReadsLatestPlaceholders(t *testing.T) {
	f := newCheckpointFixture(t)
	secrets := newConfigTestSecrets(map[string]string{"TOKEN": "placeholder-v1"}, nil)
	guest, ctx := setupConfigTest(t, f, f.sandbox.ID, secrets)
	first := configureGuestAsync(f, ctx)
	firstCall := receive(t, ctx, guest.calls, configTestTimeout, "first guest configuration")
	if got := firstCall.cfg.Env["TOKEN"]; got != "placeholder-v1" {
		t.Fatalf("first placeholder = %q, want placeholder-v1", got)
	}

	started, second := make(chan struct{}), make(chan error, 1)
	go func() {
		close(started)
		second <- f.manager.ConfigureGuest(ctx, f.env.ID, f.sandbox.ID)
	}()
	_ = receive(t, ctx, started, configTestTimeout, "second ConfigureGuest start")
	assertQuiet(t, ctx, second, "second ConfigureGuest while the first awaits its ACK")
	assertQuiet(t, ctx, guest.calls, "guest configuration delivery")
	secrets.set("TOKEN", "placeholder-v2")
	firstCall.reply <- configTestReply{}
	if err := receive(t, ctx, first, configTestTimeout, "first ConfigureGuest result"); err != nil {
		t.Fatalf("first ConfigureGuest after ACK: %v", err)
	}
	secondCall := receive(t, ctx, guest.calls, configTestTimeout, "second guest configuration")
	if got := secondCall.cfg.Env["TOKEN"]; got != "placeholder-v2" {
		t.Fatalf("second placeholder = %q, want latest placeholder-v2", got)
	}
	secondCall.reply <- configTestReply{}
	if err := receive(t, ctx, second, configTestTimeout, "second ConfigureGuest result"); err != nil {
		t.Fatalf("second ConfigureGuest after ACK: %v", err)
	}
}

type configTestCA struct {
	cert []byte
	err  error
}

func (ca configTestCA) CertPEM(context.Context, string) ([]byte, error) {
	return ca.cert, ca.err
}

type configTestSecrets struct {
	mu  sync.RWMutex
	env map[string]string
	err error
}

func newConfigTestSecrets(env map[string]string, err error) *configTestSecrets {
	return &configTestSecrets{env: maps.Clone(env), err: err}
}

func (s *configTestSecrets) Env(context.Context, store.Sandbox) (map[string]string, error) {
	if s.err != nil {
		return nil, s.err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return maps.Clone(s.env), nil
}

func (s *configTestSecrets) set(key, value string) {
	s.mu.Lock()
	s.env[key] = value
	s.mu.Unlock()
}

type configTestReply struct{ err string }
type configTestCall struct {
	cfg   agentproto.Config
	reply chan configTestReply
}

type configTestGuest struct {
	session *yamux.Session
	calls   chan configTestCall
	homes   chan []agentproto.HomeFile
}

func setupConfigTest(
	t *testing.T,
	f *checkpointFixture,
	guestID string,
	secrets *configTestSecrets,
) (*configTestGuest, context.Context) {
	t.Helper()
	f.manager.CA = configTestCA{cert: []byte("test CA PEM")}
	f.manager.Secrets = secrets
	guest := startConfigTestGuest(t, f, guestID)
	ctx, cancel := context.WithTimeout(context.Background(), configTestTimeout)
	t.Cleanup(cancel)
	return guest, ctx
}

func startConfigTestGuest(t *testing.T, f *checkpointFixture, id string) *configTestGuest {
	t.Helper()
	hub, socket := f.manager.Hub, f.paths.AgentSocket(id)
	if err := hub.Listen(id, socket); err != nil {
		t.Fatalf("listen for fake guest: %v", err)
	}
	nc, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatalf("dial fake guest socket: %v", err)
	}
	session, err := yamux.Client(nc, nil)
	if err != nil {
		_ = nc.Close()
		t.Fatalf("start fake guest yamux: %v", err)
	}
	g := &configTestGuest{session: session, calls: make(chan configTestCall, 8), homes: make(chan []agentproto.HomeFile, 8)}
	go g.accept()
	t.Cleanup(func() { _ = session.Close(); hub.Close(id) })
	stream, err := session.Open()
	if err != nil {
		t.Fatalf("open fake guest hello stream: %v", err)
	}
	_ = agentproto.WriteJSONLine(stream, agentproto.Header{Kind: agentproto.KindHello})
	_ = agentproto.WriteJSONLine(stream, agentproto.Hello{Version: "test", Arch: "amd64"})
	_ = stream.Close()
	ctx, cancel := context.WithTimeout(context.Background(), configTestTimeout)
	defer cancel()
	if err := hub.WaitConnected(ctx, id); err != nil {
		t.Fatalf("wait for fake guest connection: %v", err)
	}
	return g
}

func (g *configTestGuest) accept() {
	for {
		stream, err := g.session.Accept()
		if err != nil {
			return
		}
		go g.handleConfig(stream)
	}
}

func (g *configTestGuest) handleConfig(stream net.Conn) {
	defer stream.Close()
	br := bufio.NewReader(stream)
	var header agentproto.Header
	var cfg agentproto.Config
	if agentproto.ReadJSONLine(br, &header) == nil && header.Kind == agentproto.KindHomeFiles {
		var req agentproto.HomeFiles
		if agentproto.ReadJSONLine(br, &req) == nil {
			g.homes <- req.Files
			_ = agentproto.WriteJSONLine(stream, struct{}{})
		}
		return
	}
	if header.Kind != agentproto.KindConfig ||
		agentproto.ReadJSONLine(br, &cfg) != nil {
		return
	}
	call := configTestCall{cfg: cfg, reply: make(chan configTestReply, 1)}
	select {
	case g.calls <- call:
	case <-g.session.CloseChan():
		return
	}
	select {
	case reply := <-call.reply:
		var response any = struct{}{}
		if reply.err != "" {
			response = agentproto.Error{Error: reply.err}
		}
		_ = agentproto.WriteJSONLine(stream, response)
	case <-g.session.CloseChan():
	}
}

func configureGuestAsync(f *checkpointFixture, ctx context.Context) <-chan error {
	result := make(chan error, 1)
	go func() { result <- f.manager.ConfigureGuest(ctx, f.env.ID, f.sandbox.ID) }()
	return result
}

func receive[T any](t *testing.T, ctx context.Context, ch <-chan T, d time.Duration, what string) T {
	t.Helper()
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case value := <-ch:
		return value
	case <-timer.C:
		t.Fatalf("timed out waiting for %s after %s", what, d)
	case <-ctx.Done():
		t.Fatalf("test context ended waiting for %s: %v", what, ctx.Err())
	}
	var zero T
	return zero
}

func assertQuiet[T any](t *testing.T, ctx context.Context, ch <-chan T, phase string) {
	t.Helper()
	timer := time.NewTimer(40 * time.Millisecond)
	defer timer.Stop()
	select {
	case value := <-ch:
		t.Fatalf("unexpected %s: %v", phase, value)
	case <-timer.C:
	case <-ctx.Done():
		t.Fatalf("test context ended waiting for %s: %v", phase, ctx.Err())
	}
}

func TestSecretEventsReconfigureOnlyTheirScope(t *testing.T) {
	alice := store.Sandbox{ID: "a", EnvironmentID: "work", TeammateID: "alice"}
	bob := store.Sandbox{ID: "b", EnvironmentID: "work", TeammateID: "bob"}
	unowned := store.Sandbox{ID: "u", EnvironmentID: "work"}
	elsewhere := store.Sandbox{ID: "x", EnvironmentID: "home", TeammateID: "alice"}
	all := []store.Sandbox{alice, bob, unowned, elsewhere}
	for _, tc := range []struct {
		name string
		e    events.Event
		want []string
	}{
		{"everything", events.Event{}, []string{"a", "b", "u", "x"}},
		{"environment", events.Event{EnvironmentID: "work"}, []string{"a", "b", "u"}},
		{"teammate", events.Event{EnvironmentID: "work", TeammateID: "alice"}, []string{"a"}},
		{"sandbox", events.Event{EnvironmentID: "work", SandboxID: "u"}, []string{"u"}},
	} {
		var got []string
		for _, sb := range all {
			if affected(tc.e, sb) {
				got = append(got, sb.ID)
			}
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: reconfigures %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A sandbox with an owner gets the owner's harness config in its home and the harness's
// environment in new shells, so the harness runs as configured in a plain terminal too. A
// teammate that can't run yet leaves both as they are.
func TestConfigureWritesTheOwnersHarnessConfig(t *testing.T) {
	f := newCheckpointFixture(t)
	tm, err := f.store.CreateTeammate(f.ctx, store.Teammate{EnvironmentID: f.env.ID, Name: "Iris", Harness: "opencode"})
	if err != nil {
		t.Fatal(err)
	}
	sb, err := f.store.CreateSandbox(f.ctx, store.Sandbox{EnvironmentID: f.env.ID, Name: "owned", TeammateID: tm.ID, CPUs: 1, MemoryMiB: 512, WorkspaceMiB: 1024, DockerMiB: 1024})
	if err != nil {
		t.Fatal(err)
	}
	f.sandbox = sb
	ready := true
	f.manager.SetHarnessConfig(func(_ context.Context, got store.Sandbox) ([]agentproto.HomeFile, map[string]string, error) {
		if got.ID != sb.ID {
			t.Errorf("harness config for %s", got.ID)
		}
		if !ready {
			return nil, nil, errors.New("provider needs a sign-in first")
		}
		return []agentproto.HomeFile{{Path: ".config/opencode/opencode.json", Content: "{}", Mode: 0o644}}, map[string]string{"PROVIDER_API_KEY": "placeholder-key"}, nil
	})
	guest, ctx := setupConfigTest(t, f, sb.ID, newConfigTestSecrets(map[string]string{"TOKEN": "placeholder"}, nil))

	result := configureGuestAsync(f, ctx)
	files := receive(t, ctx, guest.homes, configTestTimeout, "harness config")
	if len(files) != 1 || files[0].Path != ".config/opencode/opencode.json" {
		t.Fatalf("home files = %+v", files)
	}
	call := receive(t, ctx, guest.calls, configTestTimeout, "guest configuration")
	if call.cfg.Env["TOKEN"] != "placeholder" || call.cfg.Env["PROVIDER_API_KEY"] != "placeholder-key" {
		t.Fatalf("env = %v", call.cfg.Env)
	}
	call.reply <- configTestReply{}
	if err := receive(t, ctx, result, configTestTimeout, "ConfigureGuest result"); err != nil {
		t.Fatal(err)
	}

	ready = false
	result = configureGuestAsync(f, ctx)
	call = receive(t, ctx, guest.calls, configTestTimeout, "guest configuration without a harness config")
	if _, ok := call.cfg.Env["PROVIDER_API_KEY"]; ok || call.cfg.Env["TOKEN"] != "placeholder" {
		t.Fatalf("env = %v", call.cfg.Env)
	}
	assertQuiet(t, ctx, guest.homes, "home files without a harness config")
	call.reply <- configTestReply{}
	if err := receive(t, ctx, result, configTestTimeout, "ConfigureGuest result"); err != nil {
		t.Fatal(err)
	}
}
