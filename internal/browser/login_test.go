package browser

import (
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/gateway"
	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// A teammate's browser login is named in the snapshot of a page of its host, with its username,
// and fill_credential fills it there; another teammate's login is neither named nor filled.
func TestBrowserLoginIsOfferedAndFilled(t *testing.T) {
	f := newFixture(t)
	login, err := f.st.CreateSecret(f.ctx, store.Secret{ID: "login-ada", EnvironmentID: f.env, TeammateID: f.ada.ID, Name: "EXAMPLE_COM", Sealed: []byte("sealed"),
		Hosts: []string{"example.com"}, Placeholder: "studio-login-ada", BrowserOnly: true, Username: "ada@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	f.keys[login.ID] = "ada-password"
	other, err := f.st.CreateSecret(f.ctx, store.Secret{ID: "login-bob", EnvironmentID: f.env, TeammateID: f.bob.ID, Name: "BOB_SHOP", Sealed: []byte("sealed"),
		Hosts: []string{"example.com"}, Placeholder: "studio-login-bob", BrowserOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	f.keys[other.ID] = "bob-password"

	snap, err := f.call(f.sbAda, agentproto.MethodBrowserSnapshot, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	text, _ := snap.(string)
	if !strings.Contains(text, `saved logins for this site, for browser_fill_credential: EXAMPLE_COM (username "ada@example.com")`) ||
		strings.Contains(text, "BOB_SHOP") || strings.Contains(text, "ada-password") {
		t.Errorf("snapshot:\n%s", text)
	}

	if _, err := f.call(f.sbAda, agentproto.MethodBrowserFillCredential, `{"ref":"e2","name":"BOB_SHOP"}`); err == nil ||
		!strings.Contains(err.Error(), `no credential named "BOB_SHOP"`) {
		t.Errorf("bob's login: %v", err)
	}
	go func() {
		a := f.pending(KindCredential)
		for _, k := range f.svc.ApprovalKinds() {
			if k.Kind == KindCredential {
				if err := k.Decide(f.ctx, a, integrations.Decision{Action: integrations.Allow}); err != nil {
					t.Error(err)
				}
			}
		}
	}()
	if _, err := f.call(f.sbAda, agentproto.MethodBrowserFillCredential, `{"ref":"e2","name":"EXAMPLE_COM"}`); err != nil {
		t.Fatal(err)
	}
	f.fb.mu.Lock()
	got := f.fb.values["e2"]
	f.fb.mu.Unlock()
	if got != "ada-password" {
		t.Errorf("filled %q", got)
	}
}

// A browser login the user lets agents use is filled without asking, and the action log says
// it was filled.
func TestBrowserLoginAgentsMayUseIsFilledWithoutAsking(t *testing.T) {
	f := newFixture(t)
	login, err := f.st.CreateSecret(f.ctx, store.Secret{ID: "login-ada", EnvironmentID: f.env, TeammateID: f.ada.ID, Name: "EXAMPLE_COM", Sealed: []byte("sealed"),
		Hosts: []string{"example.com"}, Placeholder: "studio-login-ada", BrowserOnly: true, Username: "ada", AgentMayUse: true})
	if err != nil {
		t.Fatal(err)
	}
	f.keys[login.ID] = "ada-password"
	if _, err := f.call(f.sbAda, agentproto.MethodBrowserSnapshot, `{}`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.call(f.sbAda, agentproto.MethodBrowserFillCredential, `{"ref":"e2","name":"EXAMPLE_COM"}`); err != nil {
		t.Fatal(err)
	}
	f.fb.mu.Lock()
	got := f.fb.values["e2"]
	f.fb.mu.Unlock()
	if got != "ada-password" {
		t.Errorf("filled %q", got)
	}
	if list, _ := f.st.Approvals(f.ctx, f.env, store.StatusPending, 10); len(list) != 0 {
		t.Errorf("asked: %+v", list)
	}
	actions, err := f.st.BrowserActions(f.ctx, f.env, f.ada.ID, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) == 0 || actions[0].Action != "fill_credential" || actions[0].Outcome != "ok" {
		t.Errorf("action log: %+v", actions)
	}
}

// A page asking for Basic credentials shows in the browser's status while its host still asks.
func TestSignInShowsInStatus(t *testing.T) {
	f := newFixture(t)
	if _, err := f.call(f.sbAda, agentproto.MethodBrowserSnapshot, `{}`); err != nil {
		t.Fatal(err)
	}
	vm, err := f.st.BrowserSandbox(f.ctx, f.env, f.ada.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.svc.SignInSeen(vm, "Example.com", &gateway.BasicAsk{Realm: "Staging", Login: "EXAMPLE_COM", Refused: true})
	st, err := f.svc.Status(f.ctx, f.env, f.ada.ID)
	if err != nil {
		t.Fatal(err)
	}
	if st.SignIn == nil || *st.SignIn != (SignIn{Host: "example.com", Realm: "Staging", Login: "EXAMPLE_COM", Refused: true}) {
		t.Fatalf("sign-in %+v", st.SignIn)
	}
	f.svc.SignInSeen(vm, "example.com", nil)
	if st, _ := f.svc.Status(f.ctx, f.env, f.ada.ID); st.SignIn != nil {
		t.Fatalf("sign-in after the page loaded: %+v", st.SignIn)
	}
}
