package gitreview

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// git asking credentials for a host no forge covers posts one card that offers to add the
// forge; a covered host, the virtual remote and other protocols post none, and "Not now"
// keeps Studio from asking about the host again.
func TestCredentialOffersAForgeForTheHost(t *testing.T) {
	h := newHarness(t)
	sb, err := h.st.CreateSandbox(h.ctx, store.Sandbox{EnvironmentID: h.env.ID, Name: "api"})
	if err != nil {
		t.Fatal(err)
	}
	ask := func(args string) string {
		t.Helper()
		out, err := callTool(t, h, sb, "git_credential", args)
		if err != nil {
			t.Fatalf("git_credential %s: %v", args, err)
		}
		return out
	}

	forge, _ := url.Parse(h.f.BaseURL)
	for _, args := range []string{
		`{"protocol":"http","host":"example.com"}`,
		`{"protocol":"https","host":"git.studio.internal","path":"forge/o/r.git"}`,
		`{"protocol":"https","host":"user@example.com"}`,
	} {
		if out := ask(args); out != `{}` {
			t.Errorf("%s = %s", args, out)
		}
	}
	if out := ask(`{"protocol":"https","host":"` + forge.Hostname() + `"}`); !strings.Contains(out, `the forge \"forge\"`) {
		t.Errorf("a forge's host = %s", out)
	}
	if n := len(h.approvals(t, KindCredential, store.StatusPending)); n != 0 {
		t.Fatalf("%d cards before git asked about a new host", n)
	}

	out := ask(`{"protocol":"https","host":"GitHub.com","path":"acme/web.git"}`)
	if !strings.Contains(out, "no credentials for github.com") || !strings.Contains(out, "run the command again") {
		t.Fatalf("answer = %s", out)
	}
	ask(`{"protocol":"https","host":"github.com","path":"acme/web.git"}`)
	cards := h.approvals(t, KindCredential, store.StatusPending)
	if len(cards) != 1 {
		t.Fatalf("%d cards for one host", len(cards))
	}
	var p GitCredentialPayload
	if err := json.Unmarshal(cards[0].Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.Host != "github.com" || p.URL != "https://github.com/acme/web.git" || p.Kind != KindGitHub || p.SandboxName != "api" ||
		cards[0].Subject != "api needs access to github.com" {
		t.Fatalf("card %q, payload %+v", cards[0].Subject, p)
	}

	h.decide(t, cards[0], integrations.Dismiss, "")
	if out := ask(`{"protocol":"https","host":"github.com","path":"acme/web.git"}`); !strings.Contains(out, "chose not to") {
		t.Errorf("after Not now = %s", out)
	}
	if n := len(h.approvals(t, KindCredential, store.StatusPending)); n != 0 {
		t.Errorf("%d cards after Not now", n)
	}
	// Another host still asks.
	ask(`{"protocol":"https","host":"gitlab.com","path":"g/r.git"}`)
	if cards := h.approvals(t, KindCredential, store.StatusPending); len(cards) != 1 || !strings.Contains(cards[0].Subject, "gitlab.com") {
		t.Errorf("cards %+v", cards)
	}
}
