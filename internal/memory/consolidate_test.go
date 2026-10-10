package memory

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func withEntity(e, attr string) func(*FactInput) {
	return func(in *FactInput) { in.EntityIDs, in.Attribute = []string{e}, attr }
}

func observed(t time.Time) func(*FactInput) { return func(in *FactInput) { in.ObservedAt = &t } }

func TestMergeFacts(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	keep := f.fact(f.env, "persona:a", KindFact, "Deploys use make ship", Origin{Tier: TierInferred, AuthorPersona: "a"}, withEntity("api", "deploy.command"))
	drop := f.fact(f.env, "persona:a", KindFact, "Deploys use make ship.", Origin{Tier: TierVerified, AuthorPersona: "a"}, withEntity("api", "deploy.command"))
	other := f.fact(f.env, "shared", KindFact, "Deploys use make ship", UserOrigin)
	if _, err := f.svc.MergeFacts(ctx, f.env, keep.ID, other.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cross-scope merge: %v", err)
	}
	ok, err := f.svc.MergeFacts(ctx, f.env, keep.ID, drop.ID)
	if err != nil || !ok {
		t.Fatalf("merge %v %v", ok, err)
	}
	got, _ := f.svc.Fact(ctx, f.env, keep.ID)
	if got.SupportCount != 2 || got.Tier != TierVerified || len(got.SourceIDs) != 2 {
		t.Fatalf("merged %+v", got)
	}
	if _, err := f.svc.Fact(ctx, f.env, drop.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("dropped fact: %v", err)
	}
	if ok, err := f.svc.MergeFacts(ctx, f.env, keep.ID, drop.ID); ok || err != nil {
		t.Fatalf("again %v %v", ok, err)
	}
}

func TestSupersedeWritesTimeline(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	day := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	old := f.fact(f.env, "shared", KindFact, "Prod database is Postgres 15", UserOrigin, withEntity("project/api", "db.version"), observed(day))
	nf := f.fact(f.env, "shared", KindFact, "Prod database is Postgres 16", UserOrigin, withEntity("project/api", "db.version"), observed(day.AddDate(0, 1, 0)))
	ok, err := f.svc.Supersede(ctx, f.env, old.ID, nf.ID, "upgraded in October")
	if err != nil || !ok {
		t.Fatalf("supersede %v %v", ok, err)
	}
	got, _ := f.svc.Fact(ctx, f.env, old.ID)
	if got.Status != StatusSuperseded || got.ValidUntil == nil || !got.ValidUntil.Equal(nf.ObservedAt) {
		t.Fatalf("old %+v", got)
	}
	if n, _ := f.svc.Fact(ctx, f.env, nf.ID); n.Supersedes != old.ID {
		t.Fatalf("link %+v", n)
	}
	p, err := f.svc.PageBySlug(ctx, f.env, "shared", "project/api")
	if err != nil || p.Kind != "project" {
		t.Fatalf("page %+v %v", p, err)
	}
	p, _ = f.svc.Page(ctx, f.env, p.ID)
	if len(p.Timeline) != 1 || !strings.Contains(p.Timeline[0].Text, "Postgres 16") || p.Timeline[0].FactID != nf.ID {
		t.Fatalf("timeline %+v", p.Timeline)
	}
	if ok, err := f.svc.Supersede(ctx, f.env, old.ID, nf.ID, ""); ok || err != nil {
		t.Fatalf("again %v %v", ok, err)
	}
	if p, _ = f.svc.Page(ctx, f.env, p.ID); len(p.Timeline) != 1 {
		t.Fatalf("idempotent timeline %d", len(p.Timeline))
	}
	// A shared fact is never superseded by a persona fact.
	pf := f.fact(f.env, "persona:a", KindFact, "Prod database is Postgres 17", UserOrigin, withEntity("project/api", "db.version"))
	if _, err := f.svc.Supersede(ctx, f.env, nf.ID, pf.ID, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("persona over shared: %v", err)
	}
}

func TestConflictResolutions(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	pair := func() (Fact, Fact, Conflict) {
		a := f.fact(f.env, "persona:a", KindPreference, "Use tabs for indentation", Origin{Tier: TierInferred, AuthorPersona: "a"}, withEntity("style", "indent"))
		b := f.fact(f.env, "persona:a", KindPreference, "Use spaces for indentation", UserOrigin, withEntity("style", "indent"))
		c, created, err := f.svc.OpenConflict(ctx, f.env, a.ID, b.ID, "contradiction", "tabs vs spaces")
		if err != nil || !created || c.FactA.Status != StatusDisputed || c.FactB.Status != StatusDisputed {
			t.Fatalf("open %+v %v %v", c, created, err)
		}
		again, created, err := f.svc.OpenConflict(ctx, f.env, b.ID, a.ID, "contradiction", "")
		if err != nil || created || again.ID != c.ID {
			t.Fatalf("idempotent open %+v %v %v", again, created, err)
		}
		return a, b, c
	}

	_, _, c := pair()
	r, err := f.svc.ResolveConflict(ctx, f.env, c.ID, ConflictResolution{Resolution: "keep_both", TextA: "In Go code, use tabs", TextB: "In YAML, use spaces"})
	if err != nil || r.FactA.Status != StatusActive || r.FactB.Status != StatusActive || r.FactA.Text != "In Go code, use tabs" || r.FactA.Tier != TierUser {
		t.Fatalf("keep_both %+v %v", r, err)
	}
	if hits := f.search(f.env, SearchRequest{Query: "yaml", Persona: "a"}); !contains(hits, r.FactB.ID) {
		t.Fatalf("rewritten text not searchable: %v", ids(hits))
	}

	a, b, c := pair()
	r, err = f.svc.ResolveConflict(ctx, f.env, c.ID, ConflictResolution{Resolution: "edit", Text: "Use the formatter's default indentation"})
	if err != nil || r.FactA.Status != StatusSuperseded || r.FactB.Status != StatusSuperseded {
		t.Fatalf("edit %+v %v", r, err)
	}
	hits := f.search(f.env, SearchRequest{Query: "formatter indentation", Persona: "a"})
	if len(hits) == 0 || hits[0].Fact == nil || hits[0].Fact.Text != "Use the formatter's default indentation" || hits[0].Fact.Tier != TierUser || contains(hits, a.ID) || contains(hits, b.ID) {
		t.Fatalf("after edit %v", ids(hits))
	}
	if _, err := f.svc.ResolveConflict(ctx, f.env, c.ID, ConflictResolution{Resolution: "edit"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("edit without text: %v", err)
	}
	if _, err := f.svc.ResolveConflict(ctx, f.env, c.ID, ConflictResolution{Resolution: "keep_a", Text: "x"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("keep_a with text: %v", err)
	}

	// A fact stays disputed while another open conflict disputes it.
	a, b, c = pair()
	x := f.fact(f.env, "persona:a", KindPreference, "Indent with two spaces", UserOrigin, withEntity("style", "indent"))
	if _, _, err := f.svc.OpenConflict(ctx, f.env, b.ID, x.ID, "contradiction", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ResolveConflict(ctx, f.env, c.ID, ConflictResolution{Resolution: "keep_b"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.svc.Fact(ctx, f.env, b.ID); got.Status != StatusDisputed {
		t.Fatalf("b still in a conflict: %s", got.Status)
	}
	if got, _ := f.svc.Fact(ctx, f.env, a.ID); got.Status != StatusRetracted {
		t.Fatalf("a: %s", got.Status)
	}
}

func TestSharedWinsAtRecall(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	shared := f.fact(f.env, "shared", KindFact, "Releases are cut on Thursday", Origin{Tier: TierInferred}, withEntity("release", "release.day"))
	mine := f.fact(f.env, "persona:a", KindFact, "Releases are cut on Monday mornings after standup", UserOrigin, withEntity("release", "release.day"))
	if _, err := f.svc.CreateConflict(ctx, f.env, shared.ID, mine.ID, "contradiction", ""); err != nil {
		t.Fatal(err)
	}
	// "monday standup" only matches the persona fact; the shared one is pulled in above it.
	hits := f.search(f.env, SearchRequest{Query: "monday standup", Persona: "a"})
	if len(hits) < 2 || hits[0].ID != shared.ID || hits[1].ID != mine.ID {
		t.Fatalf("order %v", ids(hits))
	}
	if !hits[1].Disputed || hits[1].OverriddenBy != shared.ID || !strings.Contains(hits[1].Why.Summary, "overridden") || hits[0].Disputed {
		t.Fatalf("markers %+v / %+v", hits[0], hits[1])
	}
	// Another persona doesn't see the conflict's persona fact at all.
	if hits := f.search(f.env, SearchRequest{Query: "monday standup", Persona: "b"}); contains(hits, mine.ID) {
		t.Fatalf("persona b sees %v", ids(hits))
	}
}

func TestCompileEntityPage(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	scope := "persona:a"
	f.fact(f.env, scope, KindFact, "The API is written in Go", UserOrigin, withEntity("project/api", ""))
	if res, err := f.svc.CompileEntityPage(ctx, f.env, scope, "project/api"); err != nil || res.PageID != "" {
		t.Fatalf("one fact makes no page: %+v %v", res, err)
	}
	f.fact(f.env, scope, KindFact, "The API listens on port 8080", UserOrigin, withEntity("project/api", ""))
	res, err := f.svc.CompileEntityPage(ctx, f.env, scope, "project/api")
	if err != nil || !res.Created || !res.Changed {
		t.Fatalf("compile %+v %v", res, err)
	}
	p, _ := f.svc.Page(ctx, f.env, res.PageID)
	if !strings.Contains(p.Compiled, "port 8080") || !strings.Contains(p.Compiled, "written in Go") {
		t.Fatalf("compiled %q", p.Compiled)
	}
	if again, _ := f.svc.CompileEntityPage(ctx, f.env, scope, "project/api"); again.Changed {
		t.Fatal("recompiling without changes changed the page")
	}

	// The user adds a section above and edits the block; both survive the next compile.
	edited := "## Notes\n\nOwned by the platform team.\n\n" + strings.Replace(p.Compiled, "port 8080", "port 9090 (user)", 1)
	if _, err := f.svc.UpdatePage(ctx, f.env, p.ID, PageInput{Title: p.Title, Kind: p.Kind, Compiled: edited}, UserOrigin); err != nil {
		t.Fatal(err)
	}
	f.fact(f.env, scope, KindFact, "The API uses SQLite", UserOrigin, withEntity("project/api", ""))
	if res, err = f.svc.CompileEntityPage(ctx, f.env, scope, "project/api"); err != nil || !res.Changed {
		t.Fatalf("recompile %+v %v", res, err)
	}
	p, _ = f.svc.Page(ctx, f.env, p.ID)
	if !strings.HasPrefix(p.Compiled, "## Notes\n\nOwned by the platform team.") || !strings.Contains(p.Compiled, "port 9090 (user)") ||
		!strings.Contains(p.Compiled, "uses SQLite") || strings.Count(p.Compiled, "<!-- studio:compiled") != 1 || p.Tier != TierUser {
		t.Fatalf("user text lost: %q tier %s", p.Compiled, p.Tier)
	}

	// A core page that would outgrow the budget is left alone.
	core, err := f.svc.CreatePage(ctx, f.env, PageInput{Scope: scope, Slug: "topic/big", Title: "big", Kind: "topic", AlwaysLoad: true,
		Compiled: strings.Repeat("x", CoreBudget-10)}, UserOrigin)
	if err != nil {
		t.Fatal(err)
	}
	f.fact(f.env, scope, KindFact, "Big fact one", UserOrigin, withEntity("big", ""))
	res, err = f.svc.CompileEntityPage(ctx, f.env, scope, "big")
	if err != nil || res.Changed || !strings.Contains(res.Note, "core pages") {
		t.Fatalf("budget %+v %v", res, err)
	}
	if got, _ := f.svc.Page(ctx, f.env, core.ID); got.Compiled != core.Compiled {
		t.Fatal("core page changed over budget")
	}
}
