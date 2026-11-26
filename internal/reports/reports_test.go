package reports

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/updates"
)

func newService(t *testing.T) (*Service, store.Sandbox) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	env, err := st.CreateEnvironment(ctx, "Acme Payments")
	if err != nil {
		t.Fatal(err)
	}
	tm, err := st.CreateTeammate(ctx, store.Teammate{EnvironmentID: env.ID, Name: "Maximiliane", Harness: "claude",
		GitName: "Max Mustermann", GitEmail: "max@acme.example"})
	if err != nil {
		t.Fatal(err)
	}
	sb, err := st.CreateSandbox(ctx, store.Sandbox{EnvironmentID: env.ID, Name: "checkout-service", TeammateID: tm.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetPrivateHosts(ctx, env.ID, []store.PrivateHost{{Host: "*.corp.internal"}}); err != nil {
		t.Fatal(err)
	}
	return &Service{Store: st, Host: func() []Name { return []Name{{"lkoebe", "user"}, {"thinkpad-x1", "machine"}} }}, sb
}

func (s *Service) call(t *testing.T, sb store.Sandbox, title, body string) (Result, error) {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"title": title, "body": body})
	out, err := s.report(context.Background(), sb, raw)
	if err != nil {
		return Result{}, err
	}
	return out.(Result), nil
}

// A report reaches the user as a card with what could identify them taken out: the names
// Studio knows, the host user and machine, and the paths and hosts in the text. The footer
// says which Studio and harness it came from, and only allowing or dismissing settles it.
func TestAReportIsAnonymizedAndWaitsForTheUser(t *testing.T) {
	s, sb := newService(t)
	res, err := s.call(t, sb, "studio pr checks hangs in checkout-service",
		"Maximiliane ran `studio pr checks --wait` in /home/lkoebe/src and /workspace/checkout for git.corp.internal on thinkpad-x1; "+
			"Max Mustermann <max@acme.example> saw it hang.")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "offered" || res.Approval == "" || res.Replaced < 7 {
		t.Fatalf("result %+v", res)
	}
	a, err := s.Store.Approval(context.Background(), sb.EnvironmentID, res.Approval)
	if err != nil {
		t.Fatal(err)
	}
	var p Payload
	if err := json.Unmarshal(a.Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.Title != "studio pr checks hangs in <computer>" {
		t.Errorf("title %q", p.Title)
	}
	want := "<teammate> ran `studio pr checks --wait` in /home/<user>/src and /workspace/<project> for <host> on <machine>; <teammate> <<email>> saw it hang."
	if p.Body != want {
		t.Errorf("body\n got %q\nwant %q", p.Body, want)
	}
	for _, leak := range []string{"Maximiliane", "lkoebe", "thinkpad", "acme", "corp.internal", "checkout"} {
		if strings.Contains(strings.ToLower(p.Title+p.Body+p.Footer), strings.ToLower(leak)) {
			t.Errorf("%q is still in the report", leak)
		}
	}
	if !strings.Contains(p.Footer, "harness claude") || p.Repository != updates.Repository || p.SandboxName != "checkout-service" || p.TeammateName != "Maximiliane" {
		t.Errorf("payload %+v", p)
	}
	if a.Kind != Kind || a.Status != store.StatusPending {
		t.Fatalf("approval %+v", a)
	}
	if err := s.decide(context.Background(), a, integrations.Decision{Action: integrations.Allow}); err != nil {
		t.Fatal(err)
	}
	if a, _ = s.Store.Approval(context.Background(), sb.EnvironmentID, res.Approval); a.Status != store.StatusApproved {
		t.Errorf("status %s", a.Status)
	}
}

// Three reports may wait at once; a fourth is refused with a word for the agent, and one with
// the title of a waiting report replaces it.
func TestReportsAreCapped(t *testing.T) {
	s, sb := newService(t)
	if _, err := s.call(t, sb, "", "x"); !integrations.IsToolError(err) {
		t.Fatalf("an empty title: %v", err)
	}
	first, err := s.call(t, sb, "one", "a")
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.call(t, sb, "one", "b")
	if err != nil || again.Approval == first.Approval {
		t.Fatalf("the same title again: %+v %v", again, err)
	}
	for _, title := range []string{"two", "three"} {
		if _, err := s.call(t, sb, title, "x"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.call(t, sb, "four", "x"); !integrations.IsToolError(err) || !strings.Contains(err.Error(), "3 reports") {
		t.Fatalf("a fourth: %v", err)
	}
}
