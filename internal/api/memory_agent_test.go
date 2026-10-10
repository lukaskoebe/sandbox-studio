package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/agentmem"
	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/memory"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func TestMemoryShareApproval(t *testing.T) {
	h, s := newMemoryServer(t)
	base := testOrigin + "/api/environments/"
	if rec := do(h, "GET", base+newEnvironment(t, s, "x").ID+"/memory/usage", ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("usage without the service: %d", rec.Code)
	}
	s.AgentMem = agentmem.New(s.Store, s.Memory, s.Vault, s.Log)
	s.Integrations = append(s.Integrations, s.AgentMem)
	env := newEnvironment(t, s, "work")
	base += env.ID
	ctx := t.Context()
	prov, _ := s.Store.CreateProvider(ctx, store.Provider{ID: store.NewID(), EnvironmentID: env.ID, Name: "claude", Kind: "claude_subscription"}, nil)
	dev, err := s.Store.CreatePersona(ctx, store.Persona{EnvironmentID: env.ID, Name: "dev", Harness: "claude", ProviderID: prov.ID, GitName: "Dev", GitEmail: "dev@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	sb, err := s.Store.CreateSandbox(ctx, store.Sandbox{EnvironmentID: env.ID, Name: "dev-1", CPUs: 1, PersonaID: dev.ID})
	if err != nil {
		t.Fatal(err)
	}
	share := func(text string) string {
		res, err := s.AgentMem.HandleCall(ctx, sb.ID, agentproto.MethodShare, json.RawMessage(`{"text":"`+text+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		return res.(map[string]any)["approvalId"].(string)
	}

	id := share("CI runs on the self-hosted runner")
	rec := do(h, "GET", base+"/approvals?status=pending", "")
	if !strings.Contains(rec.Body.String(), `"memoryShare":{"personaId":"`+dev.ID) {
		t.Fatalf("inbox: %s", rec.Body)
	}
	if rec := do(h, "POST", base+"/approvals/"+id+"/decide", `{"action":"allow"}`); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"approved"`) {
		t.Fatalf("allow: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", base+"/approvals/"+id+"/decide", `{"action":"allow"}`); rec.Code != http.StatusConflict {
		t.Fatalf("second decision: %d %s", rec.Code, rec.Body)
	}
	shared, _ := s.Memory.Facts(ctx, env.ID, memory.FactFilter{Scope: memory.SharedScope})
	if len(shared) != 1 || shared[0].AuthorPersona != dev.ID || shared[0].SupportCount != 1 {
		t.Fatalf("shared: %+v", shared)
	}

	denied := share("Nobody reviews on Mondays")
	if rec := do(h, "POST", base+"/approvals/"+denied+"/decide", `{"action":"deny"}`); rec.Code != http.StatusOK {
		t.Fatalf("deny: %d %s", rec.Code, rec.Body)
	}
	if shared, _ := s.Memory.Facts(ctx, env.ID, memory.FactFilter{Scope: memory.SharedScope}); len(shared) != 1 {
		t.Fatalf("denied share written: %+v", shared)
	}

	rec = do(h, "GET", base+"/memory/sessions", "")
	sessions := decode[[]agentmem.SessionView](t, rec)
	if len(sessions) != 1 || sessions[0].Writes != 2 {
		t.Fatalf("sessions: %s", rec.Body)
	}
	rec = do(h, "GET", base+"/memory/sessions/"+sessions[0].ID+"/log", "")
	if l := decode[[]agentmem.LogEntry](t, rec); len(l) != 2 || l[0].ItemID != id {
		t.Fatalf("log: %s", rec.Body)
	}
	rec = do(h, "GET", base+"/memory/usage", "")
	if u := decode[agentmem.UsageView](t, rec); u.Budget.MaxCalls == 0 || len(u.Models) != 1 || u.Models[0].Model != "" {
		t.Fatalf("usage: %s", rec.Body)
	}
}
