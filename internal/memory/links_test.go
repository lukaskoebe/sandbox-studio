package memory

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestLinkTargets(t *testing.T) {
	got := LinkTargets("See [[project/webshop]] and [[Deploys|how we ship]], again [[project/webshop]]; not [[]] or [[a\nb]].")
	if want := []string{"project/webshop", "deploys"}; !slices.Equal(got, want) {
		t.Fatalf("targets %q, want %q", got, want)
	}
	if s := FirstSentence("# Webshop\n\n- The shop runs on Next.js. It deploys nightly."); s != "The shop runs on Next.js." {
		t.Fatalf("first sentence %q", s)
	}
}

func TestPageLinks(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	page := func(scope, slug, compiled string) Page {
		t.Helper()
		p, err := f.svc.CreatePage(ctx, f.env, PageInput{Scope: scope, Slug: slug, Title: slug, Kind: slugKind(slug), Compiled: compiled}, UserOrigin)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	shop := page("teammate:a", "project/webshop", "The shop. It deploys with [[deploys]] and calls [[project/payments]].")
	deploys := page("shared", "topic/deploys", "Deploys run nightly.")
	page("teammate:a", "topic/notes", "Notes about [[project/webshop|the shop]].")
	page("teammate:b", "topic/b-notes", "B's view of [[deploys]].")
	f.fact(f.env, "teammate:a", KindFact, "Checkout moved to [[project/webshop]].", UserOrigin)
	f.fact(f.env, "teammate:a", KindDecision, "The shop uses Stripe.", UserOrigin, func(in *FactInput) { in.EntityIDs = []string{"project/webshop"} })
	f.fact(f.env, "teammate:a", KindFact, "Payments are slow on Mondays.", UserOrigin, func(in *FactInput) { in.EntityIDs = []string{"topic/deploys"} })

	got, err := f.svc.Page(ctx, f.env, shop.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Links) != 2 || !got.Links[0].Exists || got.Links[0].PageID != deploys.ID || got.Links[0].Summary != "Deploys run nightly." ||
		got.Links[1].Exists || got.Links[1].Target != "project/payments" {
		t.Fatalf("links %+v", got.Links)
	}
	// The notes page and the linking fact; the Stripe fact is one the page records.
	if len(got.LinkedFrom) != 2 || got.LinkedFrom[0].Via != "link" || got.LinkedFrom[1].Via != "link" {
		t.Fatalf("linked from %+v", got.LinkedFrom)
	}

	// A shared page is linked from every teammate: a's shop page, b's notes, a's fact by entity.
	got, err = f.svc.Page(ctx, f.env, deploys.ID)
	if err != nil {
		t.Fatal(err)
	}
	var vias []string
	for _, b := range got.LinkedFrom {
		vias = append(vias, b.Scope+" "+b.Type+" "+b.Via)
	}
	slices.Sort(vias)
	if want := []string{"teammate:a fact entity", "teammate:a page link", "teammate:b page link"}; !slices.Equal(vias, want) {
		t.Fatalf("linked from %q", vias)
	}

	// A timeline entry links its page.
	if _, err := f.svc.AppendTimeline(ctx, f.env, deploys.ID, TimelineInput{Text: "Moved off [[topic/old-ci]]."}, UserOrigin); err != nil {
		t.Fatal(err)
	}

	list, err := f.svc.Pages(ctx, f.env, "teammate:a")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.AddCounts(ctx, f.env, "teammate:a", list); err != nil {
		t.Fatal(err)
	}
	for _, p := range list {
		if p.Slug == "project/webshop" && (p.Counts.Facts != 1 || p.Counts.LinkedFrom != 2) {
			t.Fatalf("webshop counts %+v", *p.Counts)
		}
	}

	idx, err := f.svc.Index(ctx, f.env, "teammate:a")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(idx, "## Projects\n\n- [[project/webshop]] project/webshop: The shop.") || !strings.Contains(idx, "## Topics") {
		t.Fatalf("index:\n%s", idx)
	}

	// Lint: payments is wanted by a; old-ci by shared; a page untouched for 30 days with no
	// links is an orphan; three facts on an entity without a page.
	for i := 0; i < 3; i++ {
		f.fact(f.env, "teammate:a", KindFact, "Fact about the queue "+string(rune('a'+i)), UserOrigin, func(in *FactInput) { in.EntityIDs = []string{"project/queue"} })
	}
	f.svc.now = func() time.Time { return time.Now().Add(40 * 24 * time.Hour) }
	findings, err := f.svc.Lint(ctx, f.env, "teammate:a")
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, l := range findings {
		kinds = append(kinds, l.Kind+" "+l.Slug)
	}
	if want := []string{"wanted project/payments", "orphan topic/notes", "unpaged project/queue"}; !slices.Equal(kinds, want) {
		t.Fatalf("lint %q", kinds)
	}
	findings, err = f.svc.Lint(ctx, f.env, "shared")
	if err != nil || len(findings) != 1 || findings[0].Slug != "topic/old-ci" || findings[0].From[0].Slug != "topic/deploys" {
		t.Fatalf("shared lint %+v (%v)", findings, err)
	}
}
