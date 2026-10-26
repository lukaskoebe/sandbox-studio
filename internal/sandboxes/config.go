package sandboxes

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"sync/atomic"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

const configureTimeout = 30 * time.Second

// HarnessConfig is the owner teammate's harness setup for a sandbox: the config files for its
// home and the environment its harness needs, placeholders only (api.Server.HarnessConfig).
type HarnessConfig func(ctx context.Context, sb store.Sandbox) ([]agentproto.HomeFile, map[string]string, error)

// harnessConfigs holds the Manager's HarnessConfig, set once the API exists.
type harnessConfigs struct{ f atomic.Pointer[HarnessConfig] }

// SetHarnessConfig makes Configure write the owner teammate's harness config into the home and
// add its environment to new shells, so the harness also runs as configured in a plain terminal.
func (m *Manager) SetHarnessConfig(f HarnessConfig) { m.harness.f.Store(&f) }

// Configure sends a sandbox's guest agent its configuration: the CA the gateway intercepts
// TLS with, and the placeholders of the secrets the sandbox sees. With a HarnessConfig it also
// writes the owner teammate's harness config. It runs whenever the agent connects (see
// agentchan.Hub.OnConnect), and when the environment's secrets or teammates change.
func (m *Manager) Configure(sandboxID string) {
	ctx, cancel := context.WithTimeout(context.Background(), configureTimeout)
	defer cancel()
	if err := m.configure(ctx, "", sandboxID); err != nil {
		m.Log.Warn("configuring sandbox", "sandbox", sandboxID, "err", err)
	}
}

// ConfigureGuest waits for the guest to acknowledge its current CA and secret
// placeholders. Builders must call it after WaitReady and before running setup.
// An agent connection alone does not mean the configuration has been applied.
func (m *Manager) ConfigureGuest(ctx context.Context, envID, sandboxID string) error {
	if envID == "" {
		return store.ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, configureTimeout)
	defer cancel()
	return m.configure(ctx, envID, sandboxID)
}

func (m *Manager) configurationGate(sandboxID string) chan struct{} {
	gate, _ := m.configuring.LoadOrStore(sandboxID, make(chan struct{}, 1))
	return gate.(chan struct{})
}

func (m *Manager) configure(ctx context.Context, envID, sandboxID string) error {
	// One push at a time per sandbox, each reading the configuration once it holds the lock,
	// so the last push to arrive carries the latest state.
	gate := m.configurationGate(sandboxID)
	select {
	case gate <- struct{}{}:
		defer func() { <-gate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	var sb store.Sandbox
	var err error
	if envID == "" {
		sb, err = m.Store.LookupSandbox(ctx, sandboxID)
	} else {
		sb, err = m.Store.Sandbox(ctx, envID, sandboxID)
	}
	if err != nil {
		return err
	}
	cfg, err := m.config(ctx, sb)
	if err == nil {
		err = m.Hub.Configure(ctx, sandboxID, cfg)
	}
	return err
}

// FollowEnvironments reconfigures the connected sandboxes a secret change affects, until
// ctx ends.
func (m *Manager) FollowEnvironments(ctx context.Context, bus *events.Bus) {
	for ctx.Err() == nil {
		ch, unsubscribe := bus.Subscribe()
		m.follow(ctx, ch)
		unsubscribe()
		// The bus dropped us for falling behind, so changes may have been missed.
		m.configureAll(ctx, events.Event{})
	}
}

func (m *Manager) follow(ctx context.Context, ch <-chan events.Event) {
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-ch:
			if !ok {
				return
			}
			if (e.Topic == events.TopicSecrets || e.Topic == events.TopicTeammates) && e.EnvironmentID != "" {
				m.configureAll(ctx, e)
			}
		}
	}
}

// configureAll configures the connected sandboxes a secrets event covers: those of its
// environment, narrowed to one teammate's or one sandbox, or every sandbox for the zero
// event.
func (m *Manager) configureAll(ctx context.Context, e events.Event) {
	all, err := m.Store.AllSandboxes(ctx)
	if err != nil {
		m.Log.Warn("configuring sandboxes", "err", err)
		return
	}
	for _, sb := range all {
		if !affected(e, sb) {
			continue
		}
		if _, ok := m.Hub.Connected(sb.ID); ok {
			go m.Configure(sb.ID)
		}
	}
}

// affected tells whether a secrets event covers sb.
func affected(e events.Event, sb store.Sandbox) bool {
	return (e.EnvironmentID == "" || sb.EnvironmentID == e.EnvironmentID) &&
		(e.TeammateID == "" || sb.TeammateID == e.TeammateID) &&
		(e.SandboxID == "" || sb.ID == e.SandboxID)
}

func (m *Manager) config(ctx context.Context, sb store.Sandbox) (agentproto.Config, error) {
	ca, err := m.CA.CertPEM(ctx, sb.EnvironmentID)
	if err != nil {
		return agentproto.Config{}, err
	}
	env, err := m.Secrets.Env(ctx, sb)
	if err != nil {
		return agentproto.Config{}, err
	}
	// Which sandbox this is, for scripts and the agent's shell; names are slugs, so they
	// pass the guest's check of what a value may hold.
	env = maps.Clone(env)
	if env == nil {
		env = map[string]string{}
	}
	env["STUDIO_SANDBOX_ID"], env["STUDIO_SANDBOX_NAME"] = sb.ID, sb.Name
	if f := m.harness.f.Load(); f != nil && sb.TeammateID != "" && Public(sb) {
		// A teammate that can't run yet (no provider, no sign-in) leaves the home as it is.
		files, henv, err := (*f)(ctx, sb)
		switch {
		case err != nil:
			m.Log.Debug("no harness config for the sandbox", "sandbox", sb.ID, "err", err)
		case m.Hub.WriteHomeFiles(ctx, sb.ID, files) != nil:
			m.Log.Info("writing the harness config", "sandbox", sb.ID)
		default:
			maps.Copy(env, henv)
		}
	}
	ts, err := m.TerminalSetting(ctx)
	if err != nil {
		return agentproto.Config{}, err
	}
	return agentproto.Config{CA: string(ca), Env: env, Shell: ts.Shell}, nil
}

// settingTerminal stores the shell sandboxes' terminals start; absent means bash.
const settingTerminal = "terminal.v1"

// TerminalSetting is how the terminals of every sandbox start (Settings → General).
type TerminalSetting struct {
	Shell string `json:"shell" enum:"bash,zsh" doc:"The shell new terminals start. Agents' sessions run bash either way, and a sandbox whose image lacks zsh starts bash"`
}

// TerminalSetting reads the stored setting.
func (m *Manager) TerminalSetting(ctx context.Context) (TerminalSetting, error) {
	raw, err := m.Store.Setting(ctx, settingTerminal)
	if errors.Is(err, store.ErrNotFound) {
		return TerminalSetting{Shell: "bash"}, nil
	}
	if err != nil {
		return TerminalSetting{}, err
	}
	var ts TerminalSetting
	if err := json.Unmarshal(raw, &ts); err != nil {
		return TerminalSetting{}, err
	}
	if ts.Shell != "zsh" {
		ts.Shell = "bash"
	}
	return ts, nil
}

// SetTerminalSetting stores ts and sends it to the connected sandboxes, whose next new
// terminal starts the shell.
func (m *Manager) SetTerminalSetting(ctx context.Context, ts TerminalSetting) error {
	if ts.Shell != "bash" && ts.Shell != "zsh" {
		return errors.New("the shell is bash or zsh")
	}
	raw, err := json.Marshal(ts)
	if err == nil {
		err = m.Store.SetSetting(ctx, settingTerminal, raw)
	}
	if err == nil {
		m.configureAll(ctx, events.Event{})
	}
	return err
}
