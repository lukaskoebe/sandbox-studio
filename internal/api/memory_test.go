package api

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/memory"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func newMemoryServer(t *testing.T) (http.Handler, *Server) {
	t.Helper()
	h, s := newTestServer(t)
	s.Memory = memory.New(s.Store.DB(), &memory.FakeEmbedder{}, s.Log)
	return h, s
}

func TestMemoryAPI(t *testing.T) {
	h, s := newMemoryServer(t)
	env := newEnvironment(t, s, "work")
	base := testOrigin + "/api/environments/" + env.ID + "/memory"
	prov, err := s.Store.CreateProvider(t.Context(), store.Provider{ID: store.NewID(), EnvironmentID: env.ID, Name: "claude", Kind: "claude_subscription"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	dev, err := s.Store.CreatePersona(t.Context(), store.Persona{EnvironmentID: env.ID, Name: "dev", Harness: "claude", ProviderID: prov.ID, GitName: "Dev", GitEmail: "dev@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if rec := do(h, "POST", base+"/facts", `{"scope":"persona:nobody","kind":"fact","text":"x"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown persona: %d %s", rec.Code, rec.Body)
	}

	rec := do(h, "POST", base+"/facts", `{"scope":"persona:`+dev.ID+`","kind":"preference","text":"Use pnpm, not npm","attribute":"js.package_manager"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create fact: %d %s", rec.Code, rec.Body)
	}
	fact := decode[memory.Fact](t, rec)
	if fact.Tier != memory.TierUser || fact.AuthorPersona != "" || fact.Status != memory.StatusActive || len(fact.SourceIDs) != 1 {
		t.Fatalf("fact %+v", fact)
	}
	rec = do(h, "PUT", base+"/facts/"+fact.ID, `{"scope":"shared","kind":"preference","text":"Use pnpm everywhere"}`)
	if edited := decode[memory.Fact](t, rec); rec.Code != 200 || edited.Text != "Use pnpm everywhere" || edited.Scope != "persona:"+dev.ID {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}

	rec = do(h, "POST", base+"/pages", `{"scope":"shared","slug":"project/studio","title":"Studio","kind":"project","compiled":"Go and React.","alwaysLoad":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create page: %d %s", rec.Code, rec.Body)
	}
	page := decode[memory.Page](t, rec)
	if rec := do(h, "POST", base+"/pages/"+page.ID+"/timeline", `{"text":"Chose huma for the API"}`); rec.Code != http.StatusCreated {
		t.Fatalf("timeline: %d %s", rec.Code, rec.Body)
	}
	if got := decode[memory.Page](t, do(h, "GET", base+"/pages/"+page.ID, "")); len(got.Timeline) != 1 || got.Compiled != "Go and React." {
		t.Fatalf("page %+v", got)
	}
	if rec := do(h, "PUT", base+"/pages/"+page.ID, `{"title":"Studio","kind":"project","compiled":"Go, React and SQLite."}`); rec.Code != 200 {
		t.Fatalf("update page: %d %s", rec.Code, rec.Body)
	}

	scopes := decode[[]memory.Scope](t, do(h, "GET", base+"/scopes", ""))
	if len(scopes) != 2 || scopes[0].Scope != "shared" || scopes[1].Scope != "persona:"+dev.ID || scopes[1].Facts != 1 {
		t.Fatalf("scopes %+v", scopes)
	}
	if list := decode[[]memory.Fact](t, do(h, "GET", base+"/facts?scope=persona:"+dev.ID+"&status=active", "")); len(list) != 1 {
		t.Fatalf("facts %+v", list)
	}
	if list := decode[[]memory.Page](t, do(h, "GET", base+"/pages?scope=persona:"+dev.ID, "")); len(list) != 0 {
		t.Fatalf("pages %+v", list)
	}

	// Search: shared only without a persona, private plus shared with one.
	if hits := decode[[]memory.Hit](t, do(h, "GET", base+"/search?q=pnpm", "")); len(hits) != 0 {
		t.Fatalf("shared search found a private fact: %+v", hits)
	}
	hits := decode[[]memory.Hit](t, do(h, "GET", base+"/search?q=pnpm&persona="+dev.ID, ""))
	if len(hits) != 1 || hits[0].ID != fact.ID || hits[0].Why.Summary == "" || hits[0].Why.Vector != "not_ready" {
		t.Fatalf("search %+v", hits)
	}

	// Conflicts are created by consolidation; here directly through the service.
	other := decode[memory.Fact](t, do(h, "POST", base+"/facts", `{"scope":"shared","kind":"preference","text":"Use npm"}`))
	c, err := s.Memory.CreateConflict(t.Context(), env.ID, fact.ID, other.ID, "contradiction", "")
	if err != nil {
		t.Fatal(err)
	}
	if list := decode[[]memory.Conflict](t, do(h, "GET", base+"/conflicts?scope=shared", "")); len(list) != 1 || list[0].Status != "open" {
		t.Fatalf("conflicts %+v", list)
	}
	rec = do(h, "POST", base+"/conflicts/"+c.ID+"/resolve", `{"resolution":"keep_a","note":"pnpm it is"}`)
	if got := decode[memory.Conflict](t, rec); rec.Code != 200 || got.Status != "resolved" || got.FactB.Status != memory.StatusDisputed {
		t.Fatalf("resolve: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", base+"/conflicts/"+c.ID+"/resolve", `{"resolution":"keep_b"}`); rec.Code != http.StatusConflict {
		t.Fatalf("resolve twice: %d", rec.Code)
	}

	rec = do(h, "POST", base+"/facts/"+other.ID+"/retract", "")
	if got := decode[memory.Fact](t, rec); got.Status != memory.StatusRetracted {
		t.Fatalf("retract: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "DELETE", base+"/facts/"+other.ID, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete fact: %d", rec.Code)
	}
	if rec := do(h, "DELETE", base+"/pages/"+page.ID, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete page: %d", rec.Code)
	}
	if rec := do(h, "GET", base+"/pages/"+page.ID, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("deleted page: %d", rec.Code)
	}
}

func TestMemoryAPIValidation(t *testing.T) {
	h, s := newMemoryServer(t)
	env := newEnvironment(t, s, "work")
	base := testOrigin + "/api/environments/" + env.ID + "/memory"
	for _, tc := range []struct{ name, method, path, body string }{
		{"bad scope", "POST", "/facts", `{"scope":"team","kind":"fact","text":"x"}`},
		{"persona with a slash", "POST", "/facts", `{"scope":"persona:a/b","kind":"fact","text":"x"}`},
		{"bad kind", "POST", "/facts", `{"scope":"shared","kind":"opinion","text":"x"}`},
		{"empty text", "POST", "/facts", `{"scope":"shared","kind":"fact","text":""}`},
		{"blank text", "POST", "/facts", `{"scope":"shared","kind":"fact","text":"   "}`},
		{"confidence above 1", "POST", "/facts", `{"scope":"shared","kind":"fact","text":"x","confidence":2}`},
		{"bad slug", "POST", "/pages", `{"scope":"shared","slug":"Not A Slug","title":"x","kind":"topic"}`},
		{"bad page kind", "POST", "/pages", `{"scope":"shared","slug":"x","title":"x","kind":"diary"}`},
		{"over the core budget", "POST", "/pages", `{"scope":"shared","slug":"big","title":"x","kind":"topic","alwaysLoad":true,"compiled":"` + strings.Repeat("a", memory.CoreBudget+1) + `"}`},
		{"bad scope filter", "GET", "/facts?scope=nope", ""},
		{"bad status filter", "GET", "/facts?status=gone", ""},
		{"bad persona", "GET", "/search?q=x&persona=" + url.QueryEscape("a:b"), ""},
		{"empty query", "GET", "/search?q=", ""},
		{"limit too high", "GET", "/search?q=x&limit=500", ""},
	} {
		rec := do(h, tc.method, base+tc.path, tc.body)
		if rec.Code != http.StatusUnprocessableEntity && rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", tc.name, rec.Code, rec.Body)
		}
	}
	rec := do(h, "POST", base+"/pages", `{"scope":"shared","slug":"big","title":"x","kind":"topic","alwaysLoad":true,"compiled":"`+strings.Repeat("a", memory.CoreBudget+1)+`"}`)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "core") {
		t.Errorf("core budget: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", base+"/pages", `{"scope":"shared","slug":"x","title":"x","kind":"topic"}`); rec.Code != http.StatusCreated {
		t.Fatalf("page: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", base+"/pages", `{"scope":"shared","slug":"x","title":"y","kind":"topic"}`); rec.Code != http.StatusConflict {
		t.Errorf("duplicate slug: %d %s", rec.Code, rec.Body)
	}
}

func TestMemoryAPIGuardAndEnvironments(t *testing.T) {
	h, s := newMemoryServer(t)
	env := newEnvironment(t, s, "work")
	other := newEnvironment(t, s, "private")
	base := testOrigin + "/api/environments/" + env.ID + "/memory"
	otherBase := testOrigin + "/api/environments/" + other.ID + "/memory"

	// Cross-site writes and DNS rebinding are refused before reaching the handler.
	body := `{"scope":"shared","kind":"fact","text":"planted"}`
	if rec := do(h, "POST", base+"/facts", body, "Origin", "http://evil.example"); rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin: %d", rec.Code)
	}
	if rec := do(h, "POST", "http://attacker.example/api/environments/"+env.ID+"/memory/facts", body); rec.Code != http.StatusMisdirectedRequest {
		t.Fatalf("foreign host: %d", rec.Code)
	}
	if list := decode[[]memory.Fact](t, do(h, "GET", base+"/facts", "")); len(list) != 0 {
		t.Fatalf("planted: %+v", list)
	}

	if rec := do(h, "GET", testOrigin+"/api/environments/nope/memory/facts", ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown environment: %d", rec.Code)
	}
	if rec := do(h, "POST", testOrigin+"/api/environments/nope/memory/facts", body); rec.Code != http.StatusNotFound {
		t.Errorf("unknown environment write: %d", rec.Code)
	}

	// IDs of one environment are unknown in another.
	fact := decode[memory.Fact](t, do(h, "POST", base+"/facts", body, "Origin", testOrigin))
	page := decode[memory.Page](t, do(h, "POST", base+"/pages", `{"scope":"shared","slug":"p","title":"p","kind":"topic"}`))
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/facts/" + fact.ID, ""},
		{"PUT", "/facts/" + fact.ID, `{"kind":"fact","text":"x"}`},
		{"POST", "/facts/" + fact.ID + "/retract", ""},
		{"DELETE", "/facts/" + fact.ID, ""},
		{"GET", "/pages/" + page.ID, ""},
		{"PUT", "/pages/" + page.ID, `{"title":"x","kind":"topic"}`},
		{"POST", "/pages/" + page.ID + "/timeline", `{"text":"x"}`},
		{"DELETE", "/pages/" + page.ID, ""},
	} {
		if rec := do(h, tc.method, otherBase+tc.path, tc.body); rec.Code != http.StatusNotFound {
			t.Errorf("%s %s in another environment: %d %s", tc.method, tc.path, rec.Code, rec.Body)
		}
	}
	if hits := decode[[]memory.Hit](t, do(h, "GET", otherBase+"/search?q=planted", "")); len(hits) != 0 {
		t.Errorf("search leaked: %+v", hits)
	}
	if got := decode[memory.Fact](t, do(h, "GET", base+"/facts/"+fact.ID, "")); got.Text != "planted" || got.Status != memory.StatusActive {
		t.Errorf("fact changed from another environment: %+v", got)
	}
}
