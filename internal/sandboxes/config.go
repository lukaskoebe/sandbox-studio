package sandboxes

import (
	"context"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

const configureTimeout = 30 * time.Second

// Configure sends a sandbox's guest agent its environment's configuration: the CA the
// gateway intercepts TLS with, and the secrets' placeholders. It runs whenever the agent
// connects (see agentchan.Hub.OnConnect).
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
	cfg, err := m.config(ctx, sb.EnvironmentID)
	if err == nil {
		err = m.Hub.Configure(ctx, sandboxID, cfg)
	}
	return err
}

// FollowEnvironments reconfigures an environment's connected sandboxes whenever its secrets
// change, until ctx ends.
func (m *Manager) FollowEnvironments(ctx context.Context, bus *events.Bus) {
	for ctx.Err() == nil {
		ch, unsubscribe := bus.Subscribe()
		m.follow(ctx, ch)
		unsubscribe()
		// The bus dropped us for falling behind, so changes may have been missed.
		m.configureAll(ctx, "")
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
			if e.Topic == events.TopicSecrets && e.EnvironmentID != "" {
				m.configureAll(ctx, e.EnvironmentID)
			}
		}
	}
}

// configureAll configures the connected sandboxes of envID, or of every environment.
func (m *Manager) configureAll(ctx context.Context, envID string) {
	all, err := m.Store.AllSandboxes(ctx)
	if err != nil {
		m.Log.Warn("configuring sandboxes", "err", err)
		return
	}
	for _, sb := range all {
		if envID != "" && sb.EnvironmentID != envID {
			continue
		}
		if _, ok := m.Hub.Connected(sb.ID); ok {
			go m.Configure(sb.ID)
		}
	}
}

func (m *Manager) config(ctx context.Context, envID string) (agentproto.Config, error) {
	ca, err := m.CA.CertPEM(ctx, envID)
	if err != nil {
		return agentproto.Config{}, err
	}
	env, err := m.Secrets.Env(ctx, envID)
	if err != nil {
		return agentproto.Config{}, err
	}
	return agentproto.Config{CA: string(ca), Env: env}, nil
}
