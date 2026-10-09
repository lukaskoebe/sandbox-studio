package sandboxes

import (
	"context"
	"sync"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/events"
)

const configureTimeout = 30 * time.Second

// Configure sends a sandbox's guest agent its environment's configuration: the CA the
// gateway intercepts TLS with, and the secrets' placeholders. It runs whenever the agent
// connects (see agentchan.Hub.OnConnect).
func (m *Manager) Configure(sandboxID string) {
	// One push at a time per sandbox, each reading the configuration once it holds the lock,
	// so the last push to arrive carries the latest state.
	mu, _ := m.configuring.LoadOrStore(sandboxID, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), configureTimeout)
	defer cancel()
	sb, err := m.Store.LookupSandbox(ctx, sandboxID)
	if err != nil {
		m.Log.Warn("configuring sandbox", "sandbox", sandboxID, "err", err)
		return
	}
	cfg, err := m.config(ctx, sb.EnvironmentID)
	if err == nil {
		err = m.Hub.Configure(ctx, sandboxID, cfg)
	}
	if err != nil {
		m.Log.Warn("configuring sandbox", "sandbox", sandboxID, "err", err)
	}
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
