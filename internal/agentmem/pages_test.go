package agentmem

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/memory"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// A teammate writes its own pages at once; a shared page, or new truth for a page the user
// wrote, waits for the user's approval.
func TestMemoryPageTool(t *testing.T) {
	f := newFixture(t)
	ada := memory.TeammateScope(f.ada.ID)
	var res struct {
		Page       memory.Page
		ApprovalID string
	}
	call := func(params map[string]any) {
		t.Helper()
		res.Page, res.ApprovalID = memory.Page{}, ""
		if err := json.Unmarshal(f.mustCall(f.sbAda.ID, agentproto.MethodMemoryPage, params), &res); err != nil {
			t.Fatal(err)
		}
	}
	call(map[string]any{"slug": "project/webshop", "compiled": "The shop runs on Next.js. See [[topic/deploys]].", "timeline": "Set up the repo"})
	p := res.Page
	if p.Scope != ada || p.Title != "webshop" || p.Kind != "project" || p.Tier != memory.TierInferred || p.AuthorTeammate != f.ada.ID ||
		len(p.Timeline) != 1 || len(p.Links) != 1 || p.Links[0].Exists {
		t.Fatalf("new page %+v", p)
	}
	call(map[string]any{"slug": "project/webshop", "timeline": "Moved checkout"})
	if len(res.Page.Timeline) != 2 || res.Page.Compiled != p.Compiled {
		t.Fatalf("timeline only: %+v", res.Page)
	}

	// The user's page: a timeline entry is written, new compiled truth is proposed.
	user, err := f.mem.CreatePage(f.ctx, f.env, memory.PageInput{Scope: ada, Slug: "topic/style", Title: "Style", Kind: "topic", Compiled: "Tabs."}, memory.UserOrigin)
	if err != nil {
		t.Fatal(err)
	}
	call(map[string]any{"slug": "topic/style", "compiled": "Spaces."})
	if res.ApprovalID == "" {
		t.Fatalf("no approval for the user's page: %+v", res)
	}
	a, err := f.st.Approval(f.ctx, f.env, res.ApprovalID)
	if err != nil {
		t.Fatal(err)
	}
	var payload SharePayload
	if err := json.Unmarshal(a.Payload, &payload); err != nil || payload.Page == nil || payload.Page.Previous != "Tabs." || payload.Kind != "page" {
		t.Fatalf("payload %+v (%v)", payload, err)
	}
	if got, _ := f.mem.Page(f.ctx, f.env, user.ID); got.Compiled != "Tabs." {
		t.Fatalf("written before approval: %q", got.Compiled)
	}
	if _, err := f.svc.DecideShare(f.ctx, a, store.StatusApproved); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.mem.Page(f.ctx, f.env, user.ID); got.Compiled != "Spaces." || got.AuthorTeammate != f.ada.ID {
		t.Fatalf("after approval: %+v", got)
	}

	// A shared page waits too.
	call(map[string]any{"slug": "topic/deploys", "compiled": "Deploys run nightly.", "scope": "shared"})
	if res.ApprovalID == "" {
		t.Fatal("no approval for a shared page")
	}
	if _, err := f.mem.PageBySlug(f.ctx, f.env, memory.SharedScope, "topic/deploys"); err == nil {
		t.Fatal("the shared page was written before approval")
	}

	for _, bad := range []map[string]any{{"slug": "Bad Slug", "compiled": "x"}, {"slug": "topic/x"}, {"slug": "topic/x", "compiled": strings.Repeat("a", 8001)},
		{"slug": "topic/x", "compiled": "key sk-abcdefghijklmnopqrstuvwxyz0123"}, {"slug": "topic/x", "compiled": "x", "kind": "teammate-self"}} {
		if _, err := f.call(f.sbAda.ID, agentproto.MethodMemoryPage, bad); err == nil {
			t.Errorf("%v was written", bad)
		}
	}
	// At most 20 writes per session; 4 so far.
	for i := 0; i < maxPageWrites-4; i++ {
		f.mustCall(f.sbAda.ID, agentproto.MethodMemoryPage, map[string]any{"slug": "topic/n", "timeline": "entry"})
	}
	if _, err := f.call(f.sbAda.ID, agentproto.MethodMemoryPage, map[string]any{"slug": "topic/n", "timeline": "one more"}); err == nil {
		t.Fatal("the 21st page write went through")
	}
}
