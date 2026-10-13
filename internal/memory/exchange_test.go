package memory

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

var exportTime = time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)

// A scope exports and imports back into another environment: pages and active facts, with
// their entities, attribute and context; superseded, retracted and disputed facts stay home.
func TestExportImportRoundTrip(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	compiled := "Deployed with make ship.\n\n## fact\n---\nbytes: 3\n---\nnot a section\n<!-- studio:compiled sha=00000000 -->"
	if _, err := f.svc.CreatePage(ctx, f.env, PageInput{Scope: SharedScope, Slug: "project/api", Title: "API\nwith a line break", Kind: "project",
		Compiled: compiled, AlwaysLoad: true}, UserOrigin); err != nil {
		t.Fatal(err)
	}
	observed := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	f.fact(f.env, SharedScope, KindDecision, "Ship with make ship", UserOrigin, func(in *FactInput) {
		in.EntityIDs = []string{"project/api"}
		in.Attribute = "deploy.command"
		in.ObservedAt = &observed
	})
	f.fact(f.env, SharedScope, KindFact, "Staging is on port 5433", UserOrigin, func(in *FactInput) { in.Context = "staging" })
	old := f.fact(f.env, SharedScope, KindFact, "The api runs on Go 1.25", UserOrigin)
	f.fact(f.env, SharedScope, KindFact, "The api runs on Go 1.26", UserOrigin, func(in *FactInput) { in.Supersedes = old.ID })
	retracted := f.fact(f.env, SharedScope, KindFact, "Gone", UserOrigin)
	if _, err := f.svc.SetFactStatus(ctx, f.env, retracted.ID, StatusRetracted); err != nil {
		t.Fatal(err)
	}

	md, err := f.svc.Export(ctx, f.env, SharedScope, exportTime)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(md, "---\nstudio-memory: 1\nscope: shared\nexported: 2026-10-13T09:00:00Z\n---\n") {
		t.Fatalf("front matter:\n%s", md)
	}
	for _, not := range []string{"Go 1.25", "Gone"} {
		if strings.Contains(md, not) {
			t.Errorf("export has %q", not)
		}
	}

	res, err := f.svc.Import(ctx, f.env2, SharedScope, md)
	if err != nil || res.Pages != 1 || res.Facts != 3 || res.Skipped != 0 || len(res.Notes) != 0 {
		t.Fatalf("import %+v %v", res, err)
	}
	pages, _ := f.svc.Pages(ctx, f.env2, SharedScope)
	if len(pages) != 1 || pages[0].Slug != "project/api" || pages[0].Compiled != compiled || !pages[0].AlwaysLoad || pages[0].Tier != TierDocument || pages[0].Title != "API with a line break" {
		t.Fatalf("pages %+v", pages)
	}
	facts, _ := f.svc.Facts(ctx, f.env2, FactFilter{Scope: SharedScope, Status: StatusActive})
	byText := map[string]Fact{}
	for _, fa := range facts {
		byText[fa.Text] = fa
	}
	if d := byText["Ship with make ship"]; d.Kind != KindDecision || d.Attribute != "deploy.command" || len(d.EntityIDs) != 1 || d.EntityIDs[0] != "project/api" ||
		!d.ObservedAt.Equal(observed) || d.Tier != TierDocument {
		t.Fatalf("decision %+v", d)
	}
	if s := byText["Staging is on port 5433"]; s.Context != "staging" {
		t.Fatalf("context lost: %+v", s)
	}
	if _, ok := byText["The api runs on Go 1.26"]; !ok || len(facts) != 3 {
		t.Fatalf("facts %+v", facts)
	}

	// Again: the scope has all of it already.
	if res, err := f.svc.Import(ctx, f.env2, SharedScope, md); err != nil || res.Pages != 0 || res.Facts != 0 || res.Skipped != 4 {
		t.Fatalf("second import %+v %v", res, err)
	}
	// Other scopes stay out of it.
	if _, err := f.svc.Import(ctx, f.env2, "teammate:nobody", md); err == nil || !errors.Is(err, ErrInvalid) {
		t.Fatalf("import into another scope: %v", err)
	}
}

// A file is refused whole when any part of it is off, and nothing is written then.
func TestImportIsStrict(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	header := "---\nstudio-memory: 1\nscope: shared\nexported: 2026-10-13T09:00:00Z\n---\n"
	fact := func(extra, body string) string {
		return "\n## fact\n---\nkind: fact\ntier: user\nbytes: " + strconv.Itoa(len(body)) + "\n" + extra + "---\n" + body + "\n"
	}
	good := header + fact("", "Works")
	if res, err := f.svc.Import(ctx, f.env, SharedScope, good); err != nil || res.Facts != 1 {
		t.Fatalf("good file: %+v %v", res, err)
	}
	bad := map[string]string{
		"wrong scope":           strings.Replace(good, "scope: shared", "scope: teammate:x", 1),
		"unknown format":        strings.Replace(good, "studio-memory: 1", "studio-memory: 2", 1),
		"unknown header key":    strings.Replace(good, "exported:", "author: x\nexported:", 1),
		"no front matter":       "## fact\n",
		"unclosed header":       "---\nstudio-memory: 1\nscope: shared\nexported: 2026-10-13T09:00:00Z\n",
		"bad time":              strings.Replace(good, "2026-10-13T09:00:00Z", "yesterday", 1),
		"unknown section":       header + "\n## note\n---\n---\n",
		"unknown key":           header + fact("author: x\n", "Works"),
		"duplicate key":         header + fact("attribute: a\nattribute: b\n", "Works"),
		"missing bytes":         header + "\n## fact\n---\nkind: fact\ntier: user\n---\nWorks\n",
		"bytes too long":        header + "\n## fact\n---\nkind: fact\ntier: user\nbytes: 99\n---\nWorks\n",
		"bytes not a number":    header + "\n## fact\n---\nkind: fact\ntier: user\nbytes: 5x\n---\nWorks\n",
		"text after body":       header + fact("", "Works") + "junk\n",
		"bad kind":              header + "\n## fact\n---\nkind: rumour\ntier: user\nbytes: 2\n---\nNo\n",
		"bad tier":              header + "\n## fact\n---\nkind: fact\ntier: root\nbytes: 2\n---\nNo\n",
		"entities not list":     header + "\n## fact\n---\nkind: fact\ntier: user\nentities: api\nbytes: 2\n---\nNo\n",
		"bad observed":          header + "\n## fact\n---\nkind: fact\ntier: user\nobserved: soon\nbytes: 2\n---\nNo\n",
		"context without words": header + "\n## fact\n---\nkind: fact\ntier: user\ncontext: the\nbytes: 2\n---\nNo\n",
		"page slug":             header + "\n## page ../etc/passwd\n---\ntitle: x\nkind: topic\ncore: false\ntier: user\nbytes: 1\n---\nx\n",
		"page core":             header + "\n## page topic/x\n---\ntitle: x\nkind: topic\ncore: yes\ntier: user\nbytes: 1\n---\nx\n",
		"page kind":             header + "\n## page topic/x\n---\ntitle: x\nkind: poem\ncore: false\ntier: user\nbytes: 1\n---\nx\n",
		"invalid utf-8":         header + "\n## fact\n---\nkind: fact\ntier: user\nbytes: 1\n---\n\xff\n",
		"credential":            header + fact("", "Key sk-abcdefghijklmnopqrstuvwxyz0123 is in use"),
		"too large":             header + strings.Repeat("x", exchangeMaxBytes),
	}
	for name, md := range bad {
		if _, err := f.svc.Import(ctx, f.env, SharedScope, md); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: accepted (%v)", name, err)
		}
	}
	if facts, _ := f.svc.Facts(ctx, f.env, FactFilter{Scope: SharedScope}); len(facts) != 1 {
		t.Fatalf("a refused file wrote facts: %d", len(facts))
	}
}

// The body is counted in bytes, so text that looks like sections, markers or front matter is
// just text.
func TestImportBodiesAreOpaque(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	body := "## page evil\n---\nbytes: 0\n---\n<!-- studio:compiled sha=00000000 -->"
	md := "---\nstudio-memory: 1\nscope: shared\nexported: 2026-10-13T09:00:00Z\n---\n" +
		"\n## fact\n---\nkind: fact\ntier: user\nbytes: " + strconv.Itoa(len(body)) + "\n---\n" + body + "\n"
	res, err := f.svc.Import(ctx, f.env, SharedScope, md)
	if err != nil || res.Facts != 1 || res.Pages != 0 {
		t.Fatalf("import %+v %v", res, err)
	}
	facts, _ := f.svc.Facts(ctx, f.env, FactFilter{Scope: SharedScope})
	if len(facts) != 1 || facts[0].Text != body {
		t.Fatalf("facts %+v", facts)
	}
}
