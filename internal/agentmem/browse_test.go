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
	writes    int
	files     map[string][]agentproto.HomeFile
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
	if g.writes != 1 || len(g.files[f.sbAda.ID]) != 2 || g.files[f.sbBob.ID] != nil || g.files[f.sbPlain.ID] != nil {
		t.Fatalf("first sync wrote %d, files %v", g.writes, g.files)
	}
	f.svc.syncBrowse(f.ctx, g, written)
	if g.writes != 1 {
		t.Fatalf("an unchanged memory was written again: %d writes", g.writes)
	}

	f.mk(memory.SharedScope, "Lint with make lint", memory.TierUser, "", "", "", day(1))
	f.svc.syncBrowse(f.ctx, g, written)
	if g.writes != 2 || !strings.Contains(g.files[f.sbAda.ID][0].Content, "Lint with make lint") {
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
	if g.writes != 3 || !strings.Contains(g.files[f.sbAda.ID][0].Content, "Test with make test") {
		t.Fatalf("reconnected sandbox not brought up to date: %d writes", g.writes)
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
