package secretcheck

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

type harness struct {
	ctx context.Context
	st  *store.Store
	env store.Environment
	sb  store.Sandbox
	svc *Service
	now time.Time
}

func newHarness(t *testing.T) *harness {
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
	sb, err := st.CreateSandbox(ctx, store.Sandbox{EnvironmentID: env.ID, Name: "api"})
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{ctx: ctx, st: st, env: env, sb: sb, now: time.Unix(1_800_000_000, 0)}
	h.svc = &Service{Store: st, Now: func() time.Time { return h.now }}
	return h
}

func (h *harness) secret(t *testing.T, name string) store.Secret {
	t.Helper()
	sec, err := h.st.CreateSecret(h.ctx, store.Secret{EnvironmentID: h.env.ID, Name: name, Hosts: []string{"registry.npmjs.org"},
		Placeholder: "studio-" + name, Sealed: []byte("x")})
	if err != nil {
		t.Fatal(err)
	}
	return sec
}

// cards waits for the background raise and returns the environment's secret.rejected cards.
func (h *harness) cards(t *testing.T, want int) []store.Approval {
	t.Helper()
	var out []store.Approval
	for i := range 100 {
		list, err := h.st.Approvals(h.ctx, h.env.ID, store.StatusPending, 100)
		if err != nil {
			t.Fatal(err)
		}
		out = out[:0]
		for _, a := range list {
			if a.Kind == Kind {
				out = append(out, a)
			}
		}
		if len(out) >= want && want > 0 || want == 0 && i == 5 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(out) != want {
		t.Fatalf("%d cards, want %d", len(out), want)
	}
	return out
}

// Three 401s within ten minutes raise one card naming the secret and host; fewer, or ones
// spread out, raise none, and "Not now" keeps Studio quiet about the pair for a day.
func TestRepeatedRejectionsRaiseACard(t *testing.T) {
	h := newHarness(t)
	sec := h.secret(t, "NPM_TOKEN")
	reject := func(n int, every time.Duration) {
		for range n {
			h.svc.Rejected(h.sb, "registry.npmjs.org", []string{"NPM_TOKEN"})
			h.now = h.now.Add(every)
		}
	}

	reject(2, time.Minute)
	h.now = h.now.Add(11 * time.Minute) // the first two are out of the window by the next one
	reject(2, time.Minute)
	h.cards(t, 0)
	reject(1, 0)
	cards := h.cards(t, 1)
	if cards[0].Subject != "registry.npmjs.org refused the secret NPM_TOKEN" {
		t.Errorf("subject %q", cards[0].Subject)
	}
	var p RejectedPayload
	if err := json.Unmarshal(cards[0].Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.SecretID != sec.ID || p.Name != "NPM_TOKEN" || p.Host != "registry.npmjs.org" || p.SandboxName != "api" || p.ProviderID != "" {
		t.Errorf("payload %+v", p)
	}

	if err := h.svc.decide(h.ctx, cards[0], integrations.Decision{Action: integrations.Dismiss}); err != nil {
		t.Fatal(err)
	}
	reject(3, time.Second)
	h.cards(t, 0)
	h.now = h.now.Add(25 * time.Hour)
	reject(3, time.Second)
	h.cards(t, 1)
}

// A provider's API key points the card at the provider; a subscription's token is the
// sign-in's business and raises none.
func TestProviderSecrets(t *testing.T) {
	h := newHarness(t)
	api, err := h.st.CreateProvider(h.ctx, store.Provider{ID: "p1", EnvironmentID: h.env.ID, Name: "Anthropic", Kind: "anthropic_api"},
		&store.Secret{ID: "s1", Name: "ANTHROPIC_API_KEY", Hosts: []string{"api.anthropic.com"}, Placeholder: "studio-a", Sealed: []byte("x")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.CreateProvider(h.ctx, store.Provider{ID: "p2", EnvironmentID: h.env.ID, Name: "Claude", Kind: "claude_subscription"},
		&store.Secret{ID: "s2", Name: "CLAUDE_CODE_OAUTH_TOKEN", Hosts: []string{"api.anthropic.com"}, Placeholder: "studio-c", Sealed: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		h.svc.Rejected(h.sb, "api.anthropic.com", []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"})
	}
	time.Sleep(50 * time.Millisecond) // both raises have run
	cards := h.cards(t, 1)
	var p RejectedPayload
	if err := json.Unmarshal(cards[0].Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.Name != "ANTHROPIC_API_KEY" || p.ProviderID != api.ID || p.ProviderName != "Anthropic" {
		t.Errorf("payload %+v", p)
	}
}
