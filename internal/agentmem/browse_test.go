package agentmem

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/memory"
)

// fakeGuest is a sandbox runtime that has some sandboxes connected and records the writes.
type fakeGuest struct {
	mu        sync.Mutex
	connected map[string]bool
	outdated  bool // a guest of another version, which knows no HomeDir
	writes    int
	files     map[string][]agentproto.HomeFile
}

func (g *fakeGuest) WriteHomeDir(_ context.Context, id string, dir agentproto.HomeDir) error {
	if g.outdated {
		return agentproto.ErrGuestOutdated
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.writes++
	g.files[id] = nil
	for _, f := range dir.Files {
		f.Path = dir.Dir + "/" + f.Path
		g.files[id] = append(g.files[id], f)
	}
	return nil
}

// file is the content of the file at p that sandbox id got, "" if none.
func (g *fakeGuest) file(id, p string) string {
	for _, f := range g.files[id] {
		if f.Path == p {
			return f.Content
		}
	}
	return ""
}

func (g *fakeGuest) Connected(id string) (agentproto.Hello, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return agentproto.Hello{}, g.connected[id]
}

func (g *fakeGuest) WriteHomeFiles(_ context.Context, id string, files []agentproto.HomeFile) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.writes++
	g.files[id] = files
	return nil
}

// The copies hold what memory would surface to a session without a query, and nothing more:
// no superseded or retracted text, no context-bound facts, nothing secret-like.
func TestMemoryCopiesHoldOnlyWhatSessionsMayRead(t *testing.T) {
	f := newFixture(t)
	ctx := f.ctx
	if _, err := f.mem.CreatePage(ctx, f.env, memory.PageInput{Scope: memory.SharedScope, Slug: "project/api", Title: "API", Kind: "project",
		Compiled: "Deployed with make ship.", AlwaysLoad: true}, memory.UserOrigin); err != nil {
		t.Fatal(err)
	}
	f.mk(memory.SharedScope, "Deploy the api with make ship", memory.TierUser, "", "project/api", "", day(1))
	f.mk(memory.SharedScope, "The api logs to stdout", memory.TierUser, "", "", "", day(2))
	old := f.mk(memory.SharedScope, "The api runs on Go 1.25", memory.TierUser, "", "", "", day(3))
	if _, err := f.mem.CreateFact(ctx, f.env, memory.FactInput{Scope: memory.SharedScope, Kind: memory.KindFact, Text: "The api runs on Go 1.26", Supersedes: old.ID}, memory.UserOrigin); err != nil {
		t.Fatal(err)
	}
	if _, err := f.mem.CreateFact(ctx, f.env, memory.FactInput{Scope: memory.SharedScope, Kind: memory.KindFact, Text: "Staging uses port 5433", Context: "staging"}, memory.UserOrigin); err != nil {
		t.Fatal(err)
	}
	f.mk(memory.SharedScope, "Old key sk-abcdefghijklmnopqrstuvwxyz0123 is in use", memory.TierUser, "", "", "", day(4))
	f.mk(memory.TeammateScope(f.ada.ID), "I prefer tabs", memory.TierUser, f.ada.ID, "", "", day(5))

	files, err := f.svc.BrowseFiles(ctx, f.env, f.ada.ID)
	if err != nil || len(files) != 2 || files[0].Path != "memory/shared.md" || files[1].Path != "memory/yours.md" || files[0].Mode != 0o444 {
		t.Fatalf("files %+v %v", files, err)
	}
	shared, own := files[0].Content, files[1].Content
	for _, want := range []string{"## Pages", "project/api, core", "Deployed with make ship.", "The api logs to stdout", "[redacted]"} {
		if !strings.Contains(shared, want) {
			t.Errorf("shared copy lacks %q:\n%s", want, shared)
		}
	}
	for _, not := range []string{"Go 1.25", "Staging uses port 5433", "sk-abcdefghijklmnopqrstuvwxyz0123", "Deploy the api with make ship"} {
		if strings.Contains(shared, not) {
			t.Errorf("shared copy has %q:\n%s", not, shared)
		}
	}
	if !strings.Contains(shared, "Go 1.26") {
		t.Errorf("the current fact is missing:\n%s", shared)
	}
	if !strings.Contains(own, "I prefer tabs") || strings.Contains(own, "logs to stdout") {
		t.Errorf("own copy:\n%s", own)
	}
}

func TestMemoryCopiesAreSyncedToConnectedSandboxes(t *testing.T) {
	f := newFixture(t)
	g := &fakeGuest{connected: map[string]bool{f.sbAda.ID: true, f.sbBob.ID: false}, files: map[string][]agentproto.HomeFile{}}
	written := map[string]string{}

	f.svc.syncBrowse(f.ctx, g, written)
	if g.writes != 1 || g.file(f.sbAda.ID, "memory/index.md") == "" || g.files[f.sbBob.ID] != nil || g.files[f.sbPlain.ID] != nil {
		t.Fatalf("first sync wrote %d, files %v", g.writes, g.files)
	}
	f.svc.syncBrowse(f.ctx, g, written)
	if g.writes != 1 {
		t.Fatalf("an unchanged memory was written again: %d writes", g.writes)
	}

	f.mk(memory.SharedScope, "Lint with make lint", memory.TierUser, "", "", "", day(1))
	f.svc.syncBrowse(f.ctx, g, written)
	if g.writes != 2 || !strings.Contains(g.file(f.sbAda.ID, "memory/shared/facts.md"), "Lint with make lint") {
		t.Fatalf("a change was not written: %d writes", g.writes)
	}

	// A sandbox that is not connected gets nothing; once it is, it gets the current copies.
	g.connected[f.sbAda.ID] = false
	f.mk(memory.SharedScope, "Test with make test", memory.TierUser, "", "", "", day(2))
	f.svc.syncBrowse(f.ctx, g, written)
	if g.writes != 2 {
		t.Fatalf("a disconnected sandbox was written: %d writes", g.writes)
	}
	g.connected[f.sbAda.ID] = true
	f.svc.syncBrowse(f.ctx, g, written)
	if g.writes != 3 || !strings.Contains(g.file(f.sbAda.ID, "memory/shared/facts.md"), "Test with make test") {
		t.Fatalf("reconnected sandbox not brought up to date: %d writes", g.writes)
	}

	// A guest of another version gets the two files of before.
	g.outdated = true
	f.mk(memory.SharedScope, "Format with make fmt", memory.TierUser, "", "", "", day(3))
	f.svc.syncBrowse(f.ctx, g, written)
	if g.writes != 4 || !strings.Contains(g.file(f.sbAda.ID, "memory/shared.md"), "Format with make fmt") {
		t.Fatalf("an outdated guest got %v", g.files[f.sbAda.ID])
	}
}

// The folder has a page per file, with [[links]] as relative links, the page's facts, its
// timeline and what links to it, from this teammate's memory and shared memory only.
func TestMemoryFolder(t *testing.T) {
	f := newFixture(t)
	ctx := f.ctx
	page := func(scope, slug, compiled string) memory.Page {
		t.Helper()
		p, err := f.mem.CreatePage(ctx, f.env, memory.PageInput{Scope: scope, Slug: slug, Title: slug, Kind: "project", Compiled: compiled}, memory.UserOrigin)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	ada, bob := memory.TeammateScope(f.ada.ID), memory.TeammateScope(f.bob.ID)
	shop := page(ada, "project/webshop", "The shop. Deploys follow [[project/ci|the CI page]]; see [[project/missing]].")
	page(memory.SharedScope, "project/ci", "CI runs on every push.")
	page(bob, "project/bobs", "Bob's notes on [[project/ci]].")
	f.mk(ada, "The shop uses Stripe", memory.TierUser, f.ada.ID, "project/webshop", "", day(1))
	f.mk(ada, "Tabs, not spaces", memory.TierUser, f.ada.ID, "", "", day(2))
	if _, err := f.mem.AppendTimeline(ctx, f.env, shop.ID, memory.TimelineInput{Text: "Moved checkout to [[project/ci]]"}, memory.UserOrigin); err != nil {
		t.Fatal(err)
	}
	dir, err := newBrowser(f.svc, f.env).folder(ctx, f.ada.ID)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, file := range dir.Files {
		files[file.Path] = file.Content
		if file.Mode != 0o444 {
			t.Errorf("%s has mode %o", file.Path, file.Mode)
		}
	}
	shopFile := files["pages/project/webshop.md"]
	for _, want := range []string{"[the CI page](../../shared/pages/project/ci.md)", "project/missing (no page yet)", "## Facts\n\n- The shop uses Stripe",
		"## Timeline", "Moved checkout to [project/ci](../../shared/pages/project/ci.md)"} {
		if !strings.Contains(shopFile, want) {
			t.Errorf("the shop page lacks %q:\n%s", want, shopFile)
		}
	}
	ci := files["shared/pages/project/ci.md"]
	if !strings.Contains(ci, "## Linked from\n\n- [project/webshop](../../../pages/project/webshop.md)") || strings.Contains(ci, "bobs") {
		t.Errorf("the CI page's backlinks:\n%s", ci)
	}
	if !strings.Contains(files["index.md"], "[project/webshop](pages/project/webshop.md)") || !strings.Contains(files["index.md"], "[project/ci](shared/pages/project/ci.md)") {
		t.Errorf("index:\n%s", files["index.md"])
	}
	if !strings.Contains(files["facts.md"], "Tabs, not spaces") || strings.Contains(files["facts.md"], "Stripe") {
		t.Errorf("facts:\n%s", files["facts.md"])
	}
	if _, ok := files["pages/project/bobs.md"]; ok {
		t.Error("bob's page is in ada's copy")
	}
}

func TestTruncateBytesKeepsRunes(t *testing.T) {
	if got := truncateBytes("ééé", 3); got != "é" {
		t.Fatalf("got %q", got)
	}
	if got := truncateBytes("abc", 10); got != "abc" {
		t.Fatalf("got %q", got)
	}
}
