package memory

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

type fixture struct {
	t    *testing.T
	st   *store.Store
	svc  *Service
	emb  *FakeEmbedder
	env  string
	env2 string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	e1, err := st.CreateEnvironment(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	e2, err := st.CreateEnvironment(ctx, "private")
	if err != nil {
		t.Fatal(err)
	}
	emb := &FakeEmbedder{Aliases: map[string]string{"node": "javascript", "npm": "javascript", "ship": "deploy", "backend": "api"}}
	return &fixture{t: t, st: st, svc: New(st.DB(), emb, nil), emb: emb, env: e1.ID, env2: e2.ID}
}

func (f *fixture) fact(env, scope, kind, text string, o Origin, mod ...func(*FactInput)) Fact {
	f.t.Helper()
	in := FactInput{Scope: scope, Kind: kind, Text: text}
	for _, m := range mod {
		m(&in)
	}
	fact, err := f.svc.CreateFact(context.Background(), env, in, o)
	if err != nil {
		f.t.Fatal(err)
	}
	return fact
}

func (f *fixture) embedAll() {
	f.t.Helper()
	if _, err := f.svc.EmbedPending(context.Background()); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) search(env string, req SearchRequest) []Hit {
	f.t.Helper()
	hits, err := f.svc.Search(context.Background(), env, req)
	if err != nil {
		f.t.Fatal(err)
	}
	return hits
}

func ids(hits []Hit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.ID
	}
	return out
}

func contains(hits []Hit, id string) bool {
	for _, h := range hits {
		if h.ID == id {
			return true
		}
	}
	return false
}

func TestValidateScope(t *testing.T) {
	for _, ok := range []string{"shared", "persona:abc", "persona:A_b-9"} {
		if err := ValidateScope(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "Shared", "persona:", "persona:a b", "persona:a/b", "team", "persona:" + strings.Repeat("a", 65)} {
		if err := ValidateScope(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestMigrationAndFTSStayInSync(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	fact := f.fact(f.env, "shared", KindFact, "Postgres listens on port 5433", UserOrigin)
	if hits := f.search(f.env, SearchRequest{Query: "postgres"}); len(hits) != 1 {
		t.Fatalf("hits %v", ids(hits))
	}
	// Editing replaces the chunk; the old words must leave the index.
	if _, err := f.svc.UpdateFact(ctx, f.env, fact.ID, FactInput{Kind: KindFact, Text: "MySQL listens on port 3306"}, UserOrigin); err != nil {
		t.Fatal(err)
	}
	if hits := f.search(f.env, SearchRequest{Query: "postgres"}); len(hits) != 0 {
		t.Fatalf("stale index: %v", ids(hits))
	}
	// Deleting cascades to chunks, vectors and the FTS index.
	f.embedAll()
	if err := f.svc.DeleteFact(ctx, f.env, fact.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	db := f.st.DB()
	if err := db.QueryRow("SELECT (SELECT COUNT(*) FROM memory_chunks) + (SELECT COUNT(*) FROM memory_embeddings) + (SELECT COUNT(*) FROM memory_fts WHERE memory_fts MATCH 'mysql')").Scan(&n); err != nil || n != 0 {
		t.Fatalf("leftovers %d %v", n, err)
	}
	// Deleting the environment removes its memory.
	f.fact(f.env, "shared", KindFact, "something", UserOrigin)
	if _, err := db.Exec("DELETE FROM environments WHERE id = ?", f.env); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM memory_facts").Scan(&n); err != nil || n != 0 {
		t.Fatalf("facts left %d %v", n, err)
	}
}

func TestFactLifecycleRecordsAuthorAndSource(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	o := Origin{Tier: TierInferred, AuthorPersona: "p1", SessionRef: "sess-1", Evidence: "user said: use pnpm"}
	fact := f.fact(f.env, "persona:p1", KindPreference, "Use pnpm, not npm", o, func(in *FactInput) {
		in.Attribute, in.EntityIDs = "js.package_manager", []string{"user"}
	})
	if fact.AuthorPersona != "p1" || fact.Tier != TierInferred || fact.Status != StatusActive || fact.SupportCount != 1 ||
		len(fact.SourceIDs) != 1 || fact.Attribute != "js.package_manager" || fact.EntityIDs[0] != "user" || fact.Confidence != 1 {
		t.Fatalf("fact %+v", fact)
	}
	src, err := f.svc.Source(ctx, f.env, fact.SourceIDs[0])
	if err != nil || src.Kind != TierInferred || src.AuthorPersona != "p1" || src.SessionRef != "sess-1" || src.Evidence != o.Evidence {
		t.Fatalf("source %+v %v", src, err)
	}
	// A UI edit makes it user tier and adds a source.
	edited, err := f.svc.UpdateFact(ctx, f.env, fact.ID, FactInput{Scope: "shared", Kind: KindPreference, Text: "Use pnpm everywhere"}, UserOrigin)
	if err != nil || edited.Tier != TierUser || edited.Scope != "persona:p1" || len(edited.SourceIDs) != 2 || edited.Text != "Use pnpm everywhere" {
		t.Fatalf("edited %+v %v", edited, err)
	}
	retracted, err := f.svc.SetFactStatus(ctx, f.env, fact.ID, StatusRetracted)
	if err != nil || retracted.Status != StatusRetracted {
		t.Fatalf("retract %+v %v", retracted, err)
	}
	if _, err := f.svc.CreateFact(ctx, f.env, FactInput{Scope: "shared", Kind: "opinion", Text: "x"}, UserOrigin); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad kind: %v", err)
	}
	if _, err := f.svc.CreateFact(ctx, f.env, FactInput{Scope: "shared", Kind: KindFact, Text: "x"}, Origin{Tier: "gossip"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad tier: %v", err)
	}
	if _, err := f.svc.CreateFact(ctx, f.env, FactInput{Scope: "shared", Kind: KindFact, Text: "x"}, Origin{Tier: TierUser, AuthorPersona: "a b"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad persona: %v", err)
	}
}

func TestEnvironmentIsolation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	fact := f.fact(f.env, "shared", KindFact, "The deploy key lives in the vault", UserOrigin)
	page, err := f.svc.CreatePage(ctx, f.env, PageInput{Scope: "shared", Slug: "infra", Title: "Infra", Kind: "topic", Compiled: "deploy notes"}, UserOrigin)
	if err != nil {
		t.Fatal(err)
	}
	f.embedAll()
	other := f.fact(f.env2, "shared", KindFact, "unrelated", UserOrigin)
	if _, err := f.svc.CreateConflict(ctx, f.env, fact.ID, other.ID, "contradiction", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-environment conflict: %v", err)
	}
	if hits := f.search(f.env2, SearchRequest{Query: "deploy vault"}); len(hits) != 0 {
		t.Fatalf("leak via search: %v", ids(hits))
	}
	if _, err := f.svc.Fact(ctx, f.env2, fact.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get: %v", err)
	}
	if _, err := f.svc.UpdateFact(ctx, f.env2, fact.ID, FactInput{Kind: KindFact, Text: "x"}, UserOrigin); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update: %v", err)
	}
	if _, err := f.svc.SetFactStatus(ctx, f.env2, fact.ID, StatusRetracted); !errors.Is(err, ErrNotFound) {
		t.Fatalf("status: %v", err)
	}
	if err := f.svc.DeleteFact(ctx, f.env2, fact.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete: %v", err)
	}
	if _, err := f.svc.Page(ctx, f.env2, page.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("page: %v", err)
	}
	if _, err := f.svc.AppendTimeline(ctx, f.env2, page.ID, TimelineInput{Text: "x"}, UserOrigin); !errors.Is(err, ErrNotFound) {
		t.Fatalf("timeline: %v", err)
	}
	if _, err := f.svc.AppendTimeline(ctx, f.env, page.ID, TimelineInput{Text: "x", FactID: other.ID}, UserOrigin); !errors.Is(err, ErrInvalid) {
		t.Fatalf("timeline link across environments: %v", err)
	}
	if _, err := f.svc.CreateFact(ctx, f.env2, FactInput{Scope: "shared", Kind: KindFact, Text: "x", Supersedes: fact.ID}, UserOrigin); !errors.Is(err, ErrNotFound) {
		t.Fatalf("supersede across environments: %v", err)
	}
	for _, env := range []string{f.env, f.env2} {
		facts, _ := f.svc.Facts(ctx, env, FactFilter{})
		pages, _ := f.svc.Pages(ctx, env, "")
		scopes, _ := f.svc.Scopes(ctx, env)
		if len(facts) != 1 || (env == f.env) != (len(pages) == 1) || len(scopes) != 1 {
			t.Fatalf("env %s: %d facts, %d pages, %v", env, len(facts), len(pages), scopes)
		}
	}
}

func TestPersonaScopeIsolation(t *testing.T) {
	f := newFixture(t)
	a := f.fact(f.env, "persona:a", KindPreference, "Prefers tabs over spaces in Go code", Origin{Tier: TierUser, AuthorPersona: "a"})
	b := f.fact(f.env, "persona:b", KindPreference, "Prefers spaces over tabs in Go code", Origin{Tier: TierUser, AuthorPersona: "b"})
	shared := f.fact(f.env, "shared", KindFact, "Go code is formatted with gofmt, which uses tabs", UserOrigin)
	for _, ready := range []bool{false, true} {
		if ready {
			f.embedAll()
		}
		hitsA := f.search(f.env, SearchRequest{Query: "tabs spaces go code", Persona: "a"})
		if !contains(hitsA, a.ID) || !contains(hitsA, shared.ID) || contains(hitsA, b.ID) {
			t.Fatalf("persona a (vectors %v): %v", ready, ids(hitsA))
		}
		hitsB := f.search(f.env, SearchRequest{Query: "tabs spaces go code", Persona: "b"})
		if !contains(hitsB, b.ID) || contains(hitsB, a.ID) {
			t.Fatalf("persona b: %v", ids(hitsB))
		}
		sharedOnly := f.search(f.env, SearchRequest{Query: "tabs spaces go code"})
		if len(sharedOnly) != 1 || sharedOnly[0].ID != shared.ID {
			t.Fatalf("shared only: %v", ids(sharedOnly))
		}
	}
	scopes, err := f.svc.Scopes(context.Background(), f.env)
	if err != nil || len(scopes) != 3 || scopes[0].Scope != "shared" || scopes[1].Scope != "persona:a" {
		t.Fatalf("scopes %+v %v", scopes, err)
	}
	if _, err := f.svc.Search(context.Background(), f.env, SearchRequest{Query: "x", Persona: "a:b"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad persona: %v", err)
	}
}

func TestBM25Ranking(t *testing.T) {
	f := newFixture(t)
	best := f.fact(f.env, "shared", KindFact, "Deploy the API with make deploy; deploy only after the integration tests pass", UserOrigin)
	weak := f.fact(f.env, "shared", KindFact, "The API documentation lives in docs", UserOrigin)
	f.fact(f.env, "shared", KindFact, "Lunch is at noon", UserOrigin)
	hits := f.search(f.env, SearchRequest{Query: "deploy api"})
	if len(hits) != 2 || hits[0].ID != best.ID || hits[1].ID != weak.ID {
		t.Fatalf("ranking %v", ids(hits))
	}
	if w := hits[0].Why; w.BM25Rank != 1 || w.VectorRank != 0 || w.Vector != "not_ready" || !strings.Contains(w.Summary, "keyword match #1") {
		t.Fatalf("why %+v", w)
	}
	// Stemming: "deploying" finds "deploy".
	if hits := f.search(f.env, SearchRequest{Query: "deploying"}); len(hits) != 1 || hits[0].ID != best.ID {
		t.Fatalf("stemmed %v", ids(hits))
	}
	if _, err := f.svc.Search(context.Background(), f.env, SearchRequest{Query: " -- "}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty query: %v", err)
	}
}

func TestVectorRankingAndRRF(t *testing.T) {
	f := newFixture(t)
	// Only the vector side can connect "node" with "javascript" (an alias in the fake).
	semantic := f.fact(f.env, "shared", KindPreference, "Use pnpm for javascript projects", UserOrigin)
	lexical := f.fact(f.env, "shared", KindPreference, "Which editor? Prefer helix", UserOrigin)
	f.embedAll()
	hits := f.search(f.env, SearchRequest{Query: "node package manager"})
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	var sem *Hit
	for i := range hits {
		if hits[i].ID == semantic.ID {
			sem = &hits[i]
		}
	}
	if sem == nil || sem.Why.VectorRank == 0 || sem.Why.BM25Rank != 0 || sem.Why.Vector != "used" || sem.Why.Similarity <= 0 {
		t.Fatalf("semantic hit %+v in %v", sem, ids(hits))
	}

	// A document found by both rankers beats ones found by either alone.
	both := f.fact(f.env, "shared", KindPreference, "Prefer pnpm for node javascript tooling", UserOrigin)
	f.embedAll()
	hits = f.search(f.env, SearchRequest{Query: "pnpm node"})
	if hits[0].ID != both.ID || hits[0].Why.BM25Rank == 0 || hits[0].Why.VectorRank == 0 {
		t.Fatalf("fusion: %+v", hits)
	}
	want := 1.0/float64(rrfK+hits[0].Why.BM25Rank) + 1.0/float64(rrfK+hits[0].Why.VectorRank)
	if d := hits[0].Why.RRF - want; d > 1e-6 || d < -1e-6 {
		t.Fatalf("rrf %v, want %v", hits[0].Why.RRF, want)
	}
	_ = lexical
}

func TestTierAndRecencyBoosts(t *testing.T) {
	f := newFixture(t)
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	f.svc.now = func() time.Time { return now }
	at := func(d time.Duration) func(*FactInput) {
		return func(in *FactInput) { t := now.Add(-d); in.ObservedAt = &t }
	}
	// Same words, different tiers: the user-stated fact wins.
	inferred := f.fact(f.env, "shared", KindFact, "The staging database is called orbit", Origin{Tier: TierInferred})
	user := f.fact(f.env, "shared", KindFact, "The staging database is called orbit", UserOrigin)
	hits := f.search(f.env, SearchRequest{Query: "staging database orbit"})
	if hits[0].ID != user.ID || hits[1].ID != inferred.ID || hits[0].Why.TierBoost != 1.3 || hits[1].Why.TierBoost != 1.0 {
		t.Fatalf("tier: %+v", hits)
	}

	// Same age and tier: an old event decays faster than an old preference.
	event := f.fact(f.env, "shared", KindEvent, "Released version seven of quasar", UserOrigin, at(60*24*time.Hour))
	pref := f.fact(f.env, "shared", KindPreference, "Released version seven of quasar", UserOrigin, at(60*24*time.Hour))
	hits = f.search(f.env, SearchRequest{Query: "quasar released"})
	if hits[0].ID != pref.ID || hits[1].ID != event.ID {
		t.Fatalf("decay: %v", ids(hits))
	}
	if w := hits[1].Why; w.HalfLifeDays != 14 || w.AgeDays != 60 || w.RecencyBoost > 0.6 {
		t.Fatalf("event why %+v", w)
	}
	if w := hits[0].Why; w.HalfLifeDays != 365 || w.RecencyBoost < 0.9 {
		t.Fatalf("preference why %+v", w)
	}
	// A fresh event beats an old one.
	fresh := f.fact(f.env, "shared", KindEvent, "Released version seven of quasar", UserOrigin)
	hits = f.search(f.env, SearchRequest{Query: "quasar released"})
	if hits[0].ID != fresh.ID {
		t.Fatalf("fresh: %v", ids(hits))
	}
}

func TestStatusFiltering(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	now := time.Now()
	old := f.fact(f.env, "shared", KindFact, "The deploy command is make ship", UserOrigin)
	newer := f.fact(f.env, "shared", KindFact, "The deploy command is make release", UserOrigin, func(in *FactInput) { in.Supersedes = old.ID })
	if got, _ := f.svc.Fact(ctx, f.env, old.ID); got.Status != StatusSuperseded || newer.Supersedes != old.ID {
		t.Fatalf("old %+v new %+v", got, newer)
	}
	expired := f.fact(f.env, "shared", KindFact, "The deploy command was make legacy", UserOrigin, func(in *FactInput) {
		past := now.Add(-time.Hour)
		in.ValidUntil = &past
	})
	retracted := f.fact(f.env, "shared", KindFact, "The deploy command is make oops", UserOrigin)
	if _, err := f.svc.SetFactStatus(ctx, f.env, retracted.ID, StatusRetracted); err != nil {
		t.Fatal(err)
	}
	a := f.fact(f.env, "persona:x", KindFact, "The deploy command is make alpha", UserOrigin)
	b := f.fact(f.env, "shared", KindFact, "The deploy command is make beta", UserOrigin)
	conflict, err := f.svc.CreateConflict(ctx, f.env, a.ID, b.ID, "contradiction", "two commands")
	if err != nil || conflict.FactA.Status != StatusDisputed || conflict.FactB.Status != StatusDisputed || conflict.Status != "open" {
		t.Fatalf("conflict %+v %v", conflict, err)
	}

	hits := f.search(f.env, SearchRequest{Query: "deploy command", Persona: "x", Limit: 50})
	if contains(hits, old.ID) || contains(hits, expired.ID) || contains(hits, retracted.ID) || !contains(hits, newer.ID) {
		t.Fatalf("default: %v", ids(hits))
	}
	for _, h := range hits {
		if (h.ID == a.ID || h.ID == b.ID) != h.Disputed {
			t.Fatalf("disputed marker on %s: %+v", h.ID, h)
		}
		if h.Disputed && !strings.Contains(h.Why.Summary, "disputed") {
			t.Fatalf("summary %q", h.Why.Summary)
		}
	}
	hits = f.search(f.env, SearchRequest{Query: "deploy command", IncludeSuperseded: true, Limit: 50})
	if !contains(hits, old.ID) || !contains(hits, expired.ID) || contains(hits, retracted.ID) {
		t.Fatalf("with superseded: %v", ids(hits))
	}
	for _, h := range hits {
		if (h.ID == old.ID || h.ID == expired.ID) != h.Superseded {
			t.Fatalf("superseded marker on %s", h.ID)
		}
	}
	// Superseding only works within one scope.
	if _, err := f.svc.CreateFact(ctx, f.env, FactInput{Scope: "persona:x", Kind: KindFact, Text: "y", Supersedes: b.ID}, UserOrigin); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cross-scope supersede: %v", err)
	}

	resolved, err := f.svc.ResolveConflict(ctx, f.env, conflict.ID, "keep_b", "beta is current")
	if err != nil || resolved.Status != "resolved" || resolved.Resolution != "keep_b" || resolved.ResolvedAt == nil || resolved.Note != "beta is current" {
		t.Fatalf("resolve %+v %v", resolved, err)
	}
	if _, err := f.svc.ResolveConflict(ctx, f.env, conflict.ID, "keep_a", ""); !errors.Is(err, ErrExists) {
		t.Fatalf("twice: %v", err)
	}
	if _, err := f.svc.ResolveConflict(ctx, f.env2, conflict.ID, "keep_a", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other env: %v", err)
	}
	if _, err := f.svc.ResolveConflict(ctx, f.env, conflict.ID, "maybe", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad resolution: %v", err)
	}
	list, err := f.svc.Conflicts(ctx, f.env, "persona:x")
	if err != nil || len(list) != 1 {
		t.Fatalf("list %v %v", list, err)
	}
	if list, _ := f.svc.Conflicts(ctx, f.env, "persona:y"); len(list) != 0 {
		t.Fatalf("other scope %v", list)
	}
}

func TestCoreBudget(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	page := func(slug string, n int, core bool) (Page, error) {
		return f.svc.CreatePage(ctx, f.env, PageInput{Scope: "shared", Slug: slug, Title: slug, Kind: "topic", Compiled: strings.Repeat("ä", n), AlwaysLoad: core}, UserOrigin)
	}
	first, err := page("first", 3000, true)
	if err != nil {
		t.Fatal(err)
	}
	// Characters, not bytes: 1000 "ä" fit exactly.
	if _, err := page("exact", 1000, true); err != nil {
		t.Fatal(err)
	}
	_, err = page("over", 1, true)
	if !errors.Is(err, ErrCoreBudget) || !strings.Contains(err.Error(), "first (3000)") || !strings.Contains(err.Error(), "at most 0") {
		t.Fatalf("over budget: %v", err)
	}
	// Not core: no limit. Other scopes and environments have their own budget.
	if _, err := page("big", 5000, false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CreatePage(ctx, f.env, PageInput{Scope: "persona:p", Slug: "self", Title: "Self", Kind: "persona-self", Compiled: strings.Repeat("x", 4000), AlwaysLoad: true}, UserOrigin); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CreatePage(ctx, f.env2, PageInput{Scope: "shared", Slug: "x", Title: "x", Kind: "topic", Compiled: strings.Repeat("x", 4000), AlwaysLoad: true}, UserOrigin); err != nil {
		t.Fatal(err)
	}
	// Updates count the page once, not twice.
	if _, err := f.svc.UpdatePage(ctx, f.env, first.ID, PageInput{Title: "first", Kind: "topic", Compiled: strings.Repeat("b", 3000), AlwaysLoad: true}, UserOrigin); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.UpdatePage(ctx, f.env, first.ID, PageInput{Title: "first", Kind: "topic", Compiled: strings.Repeat("b", 3001), AlwaysLoad: true}, UserOrigin); !errors.Is(err, ErrCoreBudget) {
		t.Fatalf("grow: %v", err)
	}
	// Unmarking frees the budget.
	if _, err := f.svc.UpdatePage(ctx, f.env, first.ID, PageInput{Title: "first", Kind: "topic", Compiled: "short"}, UserOrigin); err != nil {
		t.Fatal(err)
	}
	if _, err := page("now-fits", 3000, true); err != nil {
		t.Fatal(err)
	}
	scopes, _ := f.svc.Scopes(ctx, f.env)
	if scopes[0].CoreChars != 4000 || scopes[0].CoreLimit != CoreBudget {
		t.Fatalf("scopes %+v", scopes)
	}
}

func TestPagesAndTimeline(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	p, err := f.svc.CreatePage(ctx, f.env, PageInput{Scope: "persona:p", Slug: "project/studio", Title: "Sandbox Studio", Kind: "project",
		Compiled: "Go backend.\n\nReact frontend with TanStack Router."}, Origin{Tier: TierInferred, AuthorPersona: "p"})
	if err != nil || p.AuthorPersona != "p" || p.Tier != TierInferred || len(p.Timeline) != 0 {
		t.Fatalf("page %+v %v", p, err)
	}
	if _, err := f.svc.CreatePage(ctx, f.env, PageInput{Scope: "persona:p", Slug: "project/studio", Title: "x", Kind: "project"}, UserOrigin); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate slug: %v", err)
	}
	if _, err := f.svc.CreatePage(ctx, f.env, PageInput{Scope: "persona:p", Slug: "Bad Slug", Title: "x", Kind: "project"}, UserOrigin); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad slug: %v", err)
	}
	fact := f.fact(f.env, "persona:p", KindDecision, "Chose SQLite FTS5 for keyword search", UserOrigin)
	sharedFact := f.fact(f.env, "shared", KindDecision, "shared thing", UserOrigin)
	day := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	e1, err := f.svc.AppendTimeline(ctx, f.env, p.ID, TimelineInput{Text: "Decided on hybrid search with zeppelin fusion", FactID: fact.ID, At: &day}, Origin{Tier: TierUser, AuthorPersona: "p"})
	if err != nil || !e1.At.Equal(day) || e1.FactID != fact.ID || e1.AuthorPersona != "p" {
		t.Fatalf("entry %+v %v", e1, err)
	}
	if _, err := f.svc.AppendTimeline(ctx, f.env, p.ID, TimelineInput{Text: "first entry"}, UserOrigin); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.AppendTimeline(ctx, f.env, p.ID, TimelineInput{Text: "x", FactID: sharedFact.ID}, UserOrigin); !errors.Is(err, ErrInvalid) {
		t.Fatalf("link to another scope: %v", err)
	}
	got, err := f.svc.Page(ctx, f.env, p.ID)
	if err != nil || len(got.Timeline) != 2 || got.Timeline[0].ID != e1.ID {
		t.Fatalf("timeline %+v %v", got.Timeline, err)
	}
	// Append-only at the database level too.
	if _, err := f.st.DB().Exec("UPDATE memory_timeline SET text = 'rewritten' WHERE id = ?", e1.ID); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("update allowed: %v", err)
	}
	// Timeline entries and compiled truth are searchable and resolve to the page.
	for _, q := range []string{"zeppelin", "tanstack"} {
		hits := f.search(f.env, SearchRequest{Query: q, Persona: "p"})
		if len(hits) != 1 || hits[0].Type != "page" || hits[0].ID != p.ID || hits[0].Page.Title != "Sandbox Studio" {
			t.Fatalf("%s: %+v", q, hits)
		}
	}
	// Deleting the fact keeps the entry but clears the link.
	if err := f.svc.DeleteFact(ctx, f.env, fact.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = f.svc.Page(ctx, f.env, p.ID)
	if got.Timeline[0].FactID != "" {
		t.Fatalf("link kept: %+v", got.Timeline[0])
	}
	if err := f.svc.DeletePage(ctx, f.env, p.ID); err != nil {
		t.Fatal(err)
	}
	if hits := f.search(f.env, SearchRequest{Query: "zeppelin", Persona: "p"}); len(hits) != 0 {
		t.Fatalf("deleted page found: %v", ids(hits))
	}
}

func TestEmbeddingFallbackAndReembed(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a := f.fact(f.env, "shared", KindFact, "kestrel nests on the roof", UserOrigin)
	hits := f.search(f.env, SearchRequest{Query: "kestrel"})
	if len(hits) != 1 || hits[0].Why.Vector != "not_ready" {
		t.Fatalf("before activation: %+v", hits)
	}
	f.embedAll()
	b := f.fact(f.env, "shared", KindFact, "kestrel hunts mice", UserOrigin)
	hits = f.search(f.env, SearchRequest{Query: "kestrel"})
	for _, h := range hits {
		want := map[string]string{a.ID: "used", b.ID: "not_embedded"}[h.ID]
		if h.Why.Vector != want {
			t.Fatalf("%s: %q, want %q", h.ID, h.Why.Vector, want)
		}
	}
	f.embedAll()

	// A new model re-embeds everything; vectors of the old model are ignored meanwhile.
	emb2 := &FakeEmbedder{Name: "fake-v2", Dims: 32}
	svc2 := New(f.st.DB(), emb2, nil)
	pending, err := svc2.pendingChunks(ctx, 100)
	if err != nil || len(pending) != 2 {
		t.Fatalf("pending %v %v", pending, err)
	}
	if n, err := svc2.EmbedPending(ctx); err != nil || n != 2 {
		t.Fatalf("re-embed %d %v", n, err)
	}
	var models, dims int
	if err := f.st.DB().QueryRow("SELECT COUNT(DISTINCT model), MAX(dims) FROM memory_embeddings").Scan(&models, &dims); err != nil || models != 1 || dims != 32 {
		t.Fatalf("models %d dims %d %v", models, dims, err)
	}
}

func TestWorkerEmbedsInBackground(t *testing.T) {
	f := newFixture(t)
	failing := &FakeEmbedder{Fail: errors.New("offline")}
	svc := New(f.st.DB(), failing, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// An empty memory never activates the embedder.
	if n, err := svc.EmbedPending(ctx); n != 0 || err != nil || failing.Ready() {
		t.Fatalf("idle activation: %d %v", n, err)
	}
	// Preparation errors surface and leave search on BM25.
	if _, err := svc.CreateFact(ctx, f.env, FactInput{Scope: "shared", Kind: KindFact, Text: "heron"}, UserOrigin); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.EmbedPending(ctx); err == nil {
		t.Fatal("prepare error swallowed")
	}

	go f.svc.Run(ctx)
	fact := f.fact(f.env, "shared", KindFact, "osprey fishes in the lake", UserOrigin)
	deadline := time.Now().Add(5 * time.Second)
	for {
		var n int
		f.st.DB().QueryRow("SELECT COUNT(*) FROM memory_embeddings e JOIN memory_chunks c ON c.id = e.chunk_id WHERE c.fact_id = ?", fact.ID).Scan(&n)
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("not embedded")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Overflowing the bounded queue never blocks writers; the sweep catches up.
	for i := range QueueSize + 10 {
		f.fact(f.env, "shared", KindFact, "bulk fact "+strings.Repeat("x", i%5+1), UserOrigin)
	}
	for {
		pending, _ := f.svc.pendingChunks(ctx, 1)
		if len(pending) == 0 {
			break
		}
		if time.Now().After(deadline.Add(10 * time.Second)) {
			t.Fatal("sweep did not catch up")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestQuantize(t *testing.T) {
	emb := &FakeEmbedder{}
	a := emb.embed("the quick brown fox")
	b := emb.embed("the lazy brown dog")
	var exact float64
	for i := range a {
		exact += float64(a[i] * b[i])
	}
	scale, q := quantize(b)
	if got := dotInt8(a, scale, q); got < exact-0.02 || got > exact+0.02 {
		t.Fatalf("dot %v, want %v", got, exact)
	}
	if scale, q := quantize(make([]float32, 4)); scale != 0 || len(q) != 4 {
		t.Fatal("zero vector")
	}
}

func TestSplitChunks(t *testing.T) {
	if got := splitChunks("a\n\nb"); len(got) != 1 || got[0] != "a\n\nb" {
		t.Fatalf("%q", got)
	}
	long := strings.Repeat("word ", 600) // 3000 runes
	got := splitChunks("intro\n\n" + long)
	if len(got) < 3 {
		t.Fatalf("%d chunks", len(got))
	}
	for _, c := range got {
		if n := len([]rune(c)); n > maxChunkRunes {
			t.Fatalf("chunk of %d runes", n)
		}
	}
	if splitChunks("  \n\n ") != nil {
		t.Fatal("blank")
	}
}
