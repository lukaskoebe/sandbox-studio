package sandboxinfo

import (
	"context"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// sandbox_info names the sandbox and its teammate, links each port, and lists the rules that
// cover the sandbox: its own, its teammate's and the environment's, not another sandbox's,
// browser-only or expired ones.
func TestInfoDescribesTheCallersSandbox(t *testing.T) {
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
	if _, err := st.CreateProvider(ctx, store.Provider{ID: "sub", EnvironmentID: env.ID, Name: "sub", Kind: "claude_subscription"}, nil); err != nil {
		t.Fatal(err)
	}
	ada, err := st.CreateTeammate(ctx, store.Teammate{EnvironmentID: env.ID, Name: "ada", Harness: "claude", ProviderID: "sub", GitName: "ada", GitEmail: "ada@agents.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	sb, err := st.CreateSandbox(ctx, store.Sandbox{EnvironmentID: env.ID, Name: "api", TeammateID: ada.ID})
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateSandbox(ctx, store.Sandbox{EnvironmentID: env.ID, Name: "other"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Hour)
	for _, r := range []store.Rule{
		{Host: "mine.example", SandboxID: sb.ID, Action: store.ActionAllow},
		{Host: "ada.example", TeammateID: ada.ID, Action: store.ActionProxy},
		{Host: "all.example", Ports: []int{443}, Action: store.ActionDeny, Method: "POST", PathPrefix: "/admin/*"},
		{Host: "other.example", SandboxID: other.ID, Action: store.ActionAllow},
		{Host: "browser.example", Action: store.ActionAllow, BrowserOnly: true},
		{Host: "expired.example", Action: store.ActionAllow, ExpiresAt: &past},
	} {
		r.EnvironmentID = env.ID
		if _, err := st.CreateRule(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	var levelFor string
	s := &Service{
		Store: st,
		Ports: func(_ context.Context, id string) ([]agentproto.Port, error) {
			if id != sb.ID {
				t.Errorf("ports of %s", id)
			}
			return []agentproto.Port{{Port: 5173}}, nil
		},
		Level: func(_ context.Context, envID, teammateID string) (string, error) {
			levelFor = teammateID
			return store.LevelGuarded, nil
		},
		Now: func() time.Time { return now },
	}

	res, err := s.info(ctx, sb, nil)
	if err != nil {
		t.Fatal(err)
	}
	info := res.(Info)
	if info.Sandbox != (Named{sb.ID, "api"}) || info.Teammate == nil || *info.Teammate != (Named{ada.ID, "ada"}) {
		t.Errorf("sandbox %+v, teammate %+v", info.Sandbox, info.Teammate)
	}
	if len(info.Ports) != 1 || info.Ports[0] != (Port{5173, "http://localhost:5173/"}) {
		t.Errorf("ports = %+v", info.Ports)
	}
	if levelFor != ada.ID || info.Network.Level != store.LevelGuarded || info.Network.Meaning == "" {
		t.Errorf("level %q (%q) for teammate %q", info.Network.Level, info.Network.Meaning, levelFor)
	}
	scopes := map[string]Rule{}
	for _, r := range info.Network.Rules {
		scopes[r.Host] = r
	}
	if r := scopes["mine.example"]; r.Scope != "sandbox" || r.Action != "allow" {
		t.Errorf("sandbox rule = %+v", r)
	}
	if r := scopes["ada.example"]; r.Scope != "teammate" || r.Action != actions[store.ActionProxy] {
		t.Errorf("teammate rule = %+v", r)
	}
	if r := scopes["all.example"]; r.Scope != "environment" || r.Action != "deny" || r.Method != "POST" || r.Path != "/admin/*" || len(r.Ports) != 1 {
		t.Errorf("environment rule = %+v", r)
	}
	for _, host := range []string{"other.example", "browser.example", "expired.example"} {
		if _, ok := scopes[host]; ok {
			t.Errorf("%s is listed", host)
		}
	}
	if info.Network.Rules[0].Host != "mine.example" {
		t.Errorf("the sandbox's own rule comes first, got %+v", info.Network.Rules[0])
	}
}
