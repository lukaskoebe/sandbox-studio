package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// onboardingOf reads the onboarding of envID as a map of each feature's happened and seen.
func onboardingOf(t *testing.T, h http.Handler, envID string) (Onboarding, map[string][2]bool) {
	t.Helper()
	rec := do(h, "GET", testOrigin+"/api/onboarding?env="+envID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get onboarding: %d %s", rec.Code, rec.Body)
	}
	o := decode[Onboarding](t, rec)
	m := map[string][2]bool{}
	for _, f := range o.Features {
		m[f.Key] = [2]bool{f.Happened, f.Seen}
	}
	return o, m
}

func TestOnboardingFollowsTheEnvironment(t *testing.T) {
	h, s := newTestServer(t)
	ctx := context.Background()
	env := newEnvironment(t, s, "work")

	o, m := onboardingOf(t, h, env.ID)
	if len(o.Features) != 5 || o.DoneAt != nil || o.Dismissed || o.TipsOff {
		t.Fatalf("fresh: %+v", o)
	}
	for k, v := range m {
		if v != [2]bool{} {
			t.Fatalf("fresh: %s is %v", k, v)
		}
	}

	// A provider's key and a forge's token are Studio's, not secrets the user added.
	createProvider(t, h, env, `{"name":"Anthropic","kind":"anthropic_api","apiKey":"`+providerKey+`"}`)
	if _, err := s.Store.CreateForge(ctx, store.Forge{ID: store.NewID(), EnvironmentID: env.ID, Name: "cb", Kind: "forgejo", BaseURL: "https://codeberg.org"},
		store.Secret{ID: store.NewID(), Name: "FORGE_CB_TOKEN", Sealed: []byte("x"), Hosts: []string{"codeberg.org"}}); err != nil {
		t.Fatal(err)
	}
	if _, m := onboardingOf(t, h, env.ID); m["secret"][0] {
		t.Fatal("a provider's key or a forge token counted as the user's secret")
	}

	sb := newSandbox(t, s, env, "first")
	if _, err := s.Store.CreateSecret(ctx, store.Secret{ID: store.NewID(), EnvironmentID: env.ID, Name: "STRIPE_KEY", Placeholder: "studio-placeholder-stripe", Sealed: []byte("x"), Hosts: []string{"api.stripe.com"}}); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"network", "git.push"} {
		if _, _, err := s.Store.RequestApproval(ctx, store.Approval{EnvironmentID: env.ID, SandboxID: sb.ID, Kind: kind, Subject: kind, Payload: []byte("{}")}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Store.AddBrowserAction(ctx, store.BrowserAction{EnvironmentID: env.ID, TeammateID: "t", SessionID: "s", Actor: "agent", Action: "open", Outcome: "ok"}); err != nil {
		t.Fatal(err)
	}
	o, m = onboardingOf(t, h, env.ID)
	for _, k := range []string{"network", "push", "browser", "secret"} {
		// Read once before, so these happened after the first read and still have tips.
		if m[k] != [2]bool{true, false} {
			t.Fatalf("%s: %v, want happened and not seen", k, m[k])
		}
	}
	if m["preview"] != [2]bool{} || o.DoneAt != nil {
		t.Fatalf("previews leave no record: %v, done %v", m["preview"], o.DoneAt)
	}

	// The page reports the preview; with it every feature is done.
	rec := do(h, "PUT", testOrigin+"/api/onboarding?env="+env.ID, `{"seen":["preview","network"],"dismissed":false,"tipsOff":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("put: %d %s", rec.Code, rec.Body)
	}
	o, m = onboardingOf(t, h, env.ID)
	if !m["preview"][1] || !m["network"][1] || !o.TipsOff || o.DoneAt == nil {
		t.Fatalf("after put: %+v", o)
	}

	// Showing the tips again brings the card back for the preview, which only a tip records.
	if rec := do(h, "PUT", testOrigin+"/api/onboarding?env="+env.ID, `{"seen":[],"dismissed":false,"tipsOff":false}`); rec.Code != http.StatusOK {
		t.Fatalf("put: %d %s", rec.Code, rec.Body)
	}
	if o, _ := onboardingOf(t, h, env.ID); o.TipsOff || o.DoneAt != nil {
		t.Fatalf("shown again: %+v", o)
	}

	if rec := do(h, "PUT", testOrigin+"/api/onboarding?env="+env.ID, `{"seen":["memory"],"dismissed":false,"tipsOff":false}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("an unknown feature: %d %s", rec.Code, rec.Body)
	}
}

func TestOnboardingFirstReadSkipsWhatAlreadyHappened(t *testing.T) {
	h, s := newTestServer(t)
	ctx := context.Background()
	env := newEnvironment(t, s, "work")
	sb := newSandbox(t, s, env, "first")
	if _, _, err := s.Store.RequestApproval(ctx, store.Approval{EnvironmentID: env.ID, SandboxID: sb.ID, Kind: "network.request", Subject: "x", Payload: []byte("{}")}); err != nil {
		t.Fatal(err)
	}
	_, m := onboardingOf(t, h, env.ID)
	if m["network"] != [2]bool{true, true} || m["push"] != [2]bool{} {
		t.Fatalf("an existing user's first read: %v", m)
	}
}
