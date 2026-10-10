package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

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

func TestMemoryDreamAPI(t *testing.T) {
	h, s := newMemoryServer(t)
	s.AgentMem = agentmem.New(s.Store, s.Memory, s.Vault, s.Log)
	s.Integrations = append(s.Integrations, s.AgentMem)
	env := newEnvironment(t, s, "work")
	base := testOrigin + "/api/environments/" + env.ID + "/memory"
	ctx := t.Context()
	prov, _ := s.Store.CreateProvider(ctx, store.Provider{ID: store.NewID(), EnvironmentID: env.ID, Name: "claude", Kind: "claude_subscription"}, nil)
	dev, err := s.Store.CreatePersona(ctx, store.Persona{EnvironmentID: env.ID, Name: "dev", Harness: "claude", ProviderID: prov.ID, GitName: "Dev", GitEmail: "dev@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	scope := memory.PersonaScope(dev.ID)

	if rec := do(h, "POST", base+"/dream", `{"scope":"persona:nobody"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown persona: %d %s", rec.Code, rec.Body)
	}
	rec := do(h, "POST", base+"/dream", `{"scope":"`+scope+`"}`)
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"trigger":"manual"`) {
		t.Fatalf("dream: %d %s", rec.Code, rec.Body)
	}
	for i := 0; ; i++ {
		runs := decode[[]agentmem.DreamRun](t, do(h, "GET", base+"/dreams?scope="+scope, ""))
		if len(runs) == 1 && runs[0].Status == agentmem.DreamDone {
			break
		}
		if i > 500 {
			t.Fatalf("runs %+v", runs)
		}
		time.Sleep(10 * time.Millisecond)
	}

	a, _ := s.Memory.CreateFact(ctx, env.ID, memory.FactInput{Scope: scope, Kind: memory.KindFact, Text: "Use tabs"}, memory.Origin{Tier: memory.TierInferred, AuthorPersona: dev.ID})
	b, _ := s.Memory.CreateFact(ctx, env.ID, memory.FactInput{Scope: memory.SharedScope, Kind: memory.KindFact, Text: "Use spaces"}, memory.Origin{Tier: memory.TierUser})
	c, err := s.Memory.CreateConflict(ctx, env.ID, a.ID, b.ID, "contradiction", "indentation differs")
	if err != nil {
		t.Fatal(err)
	}
	d := decode[agentmem.ConflictDetail](t, do(h, "GET", base+"/conflicts/"+c.ID, ""))
	if d.ID != c.ID || len(d.SourcesA) != 1 || len(d.SourcesB) != 1 || d.Reason != "indentation differs" {
		t.Fatalf("detail %+v", d)
	}
	if rec := do(h, "POST", base+"/conflicts/"+c.ID+"/resolve", `{"resolution":"edit"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("edit without text: %d", rec.Code)
	}

	if rec := do(h, "POST", base+"/facts/"+a.ID+"/promote", ""); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("promote a disputed fact: %d", rec.Code)
	}
	p, _ := s.Memory.CreateFact(ctx, env.ID, memory.FactInput{Scope: scope, Kind: memory.KindFact, Text: "CI is Buildkite"}, memory.Origin{Tier: memory.TierInferred, AuthorPersona: dev.ID})
	rec = do(h, "POST", base+"/facts/"+p.ID+"/promote", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"memoryShare":{`) {
		t.Fatalf("promote: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", base+"/facts/"+b.ID+"/promote", ""); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("promote shared: %d %s", rec.Code, rec.Body)
	}
}
