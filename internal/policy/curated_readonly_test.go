package policy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func TestCuratedReadOnlyHostsAllowReads(t *testing.T) {
	entry := func(host string) CuratedEntry {
		t.Helper()
		e, ok := curatedMatch(host, 443, nil)
		if !ok {
			t.Fatalf("%s is not on the list", host)
		}
		return e
	}
	for _, c := range []struct {
		host, method, path string
		want               bool
	}{
		{"github.com", http.MethodGet, "/o/r.git/info/refs", true},
		{"github.com", http.MethodHead, "/o/r", true},
		{"github.com", http.MethodPost, "/o/r.git/git-upload-pack", true},
		{"github.com", http.MethodPost, "/o/r.git/info/lfs/objects/batch", true},
		{"github.com", http.MethodPost, "/o/r.git/git-receive-pack", false},
		{"github.com", http.MethodPost, "/o/r.git/git-receive-pack/../git-upload-pack", false},
		{"github.com", http.MethodPost, "/o//r.git/git-upload-pack", false},
		{"github.com", http.MethodPut, "/o/r.git/git-upload-pack", false},
		{"github.com", "get", "/o/r", false},
		{"gitlab.com", http.MethodPost, "/o/r.git/git-upload-pack", true},
		{"gitlab.com", http.MethodPost, "/api/v4/projects", false},
		{"registry.npmjs.org", http.MethodGet, "/left-pad", true},
		{"registry.npmjs.org", http.MethodPost, "/-/npm/v1/security/advisories/bulk", true},
		{"registry.npmjs.org", http.MethodPut, "/left-pad", false},
		{"crates.io", http.MethodPut, "/api/v1/crates/new", false},
		{"rubygems.org", http.MethodPost, "/api/v1/gems", false},
		{"registry-1.docker.io", http.MethodGet, "/v2/library/alpine/manifests/3", true},
		{"registry-1.docker.io", http.MethodPost, "/v2/x/y/blobs/uploads/", false},
		{"registry-1.docker.io", http.MethodPatch, "/v2/x/y/blobs/uploads/1", false},
		{"ghcr.io", http.MethodPut, "/v2/x/y/manifests/1", false},
		{"quay.io", http.MethodPut, "/v2/x/y/manifests/1", false},
		// The entry's other hosts only serve downloads, and nothing reads their requests.
		{"codeload.github.com", http.MethodPost, "/anything", true},
		{"auth.docker.io", http.MethodPost, "/token", true},
	} {
		r := &HTTPRequest{Method: c.method, Path: c.path, FullPath: c.path}
		if got := entry(c.host).allowsRequest(c.host, r); got != c.want {
			t.Errorf("%s %s on %s: %v, want %v", c.method, c.path, c.host, got, c.want)
		}
	}
}

func TestCuratedWriteAsks(t *testing.T) {
	ctx := context.Background()
	e, env, sb := newEngine(t)
	e.Hold = 50 * time.Millisecond
	if err := e.Store.SetSecurityLevel(ctx, env.ID, store.LevelGuarded); err != nil {
		t.Fatal(err)
	}
	conn := Request{EnvironmentID: env.ID, SandboxID: sb.ID, SandboxName: sb.Name, Host: "github.com", Port: 443}
	on := func(method, path string) Request {
		req := conn
		req.Curated, req.RulesOnly = true, true
		req.HTTP = &HTTPRequest{Method: method, Path: path, FullPath: path}
		return req
	}
	pending := func() []store.Approval {
		t.Helper()
		list, err := e.Store.Approvals(ctx, env.ID, store.StatusPending, 10)
		if err != nil {
			t.Fatal(err)
		}
		return list
	}

	// The connection is allowed by the list, and terminated because of what the list says.
	rule, err := e.Decide(ctx, conn)
	if err != nil || rule.Action != store.ActionAllow || rule.Origin != store.OriginCurated {
		t.Fatalf("connection: %+v, %v", rule, err)
	}
	asked := conn
	asked.Curated = true
	if !e.Terminates(ctx, asked) {
		t.Error("the list's connection to a read-only host isn't terminated")
	}
	asked.Host = "codeload.github.com"
	if e.Terminates(ctx, asked) {
		t.Error("a host that only serves downloads is terminated")
	}

	// Reads pass, without a card.
	for _, r := range []Request{on(http.MethodGet, "/o/r.git/info/refs"), on(http.MethodPost, "/o/r.git/git-upload-pack")} {
		if rule, err := e.Decide(ctx, r); err != nil || rule.Action != store.ActionAllow {
			t.Fatalf("%s: %+v, %v", r.HTTP.Path, rule, err)
		}
	}
	if n := len(pending()); n != 0 {
		t.Fatalf("%d cards for reads", n)
	}

	// A write is held and asked about, and the card names it and is a sandbox's.
	if _, err := e.Decide(ctx, on(http.MethodPost, "/o/r.git/git-receive-pack")); !errors.Is(err, ErrUndecided) {
		t.Fatalf("write: %v", err)
	}
	if _, err := e.Decide(ctx, on(http.MethodPost, "/other/r.git/git-receive-pack")); !errors.Is(err, ErrUndecided) {
		t.Fatalf("second write: %v", err)
	}
	cards := pending()
	if len(cards) != 2 {
		t.Fatalf("%d cards for two writes, want one each: %+v", len(cards), cards)
	}
	var card store.Approval
	var req NetworkRequest
	for _, c := range cards {
		if strings.Contains(c.Subject, "/o/r.git") {
			card = c
		}
	}
	if err := json.Unmarshal(card.Payload, &req); err != nil {
		t.Fatal(err)
	}
	if card.Kind != KindRequest || !req.Write || req.Method != http.MethodPost || req.Path != "/o/r.git/git-receive-pack" || req.Site != "" {
		t.Fatalf("card: %+v %+v", card, req)
	}

	// An allow narrowed to the request is a rule for the sandbox's own connections, and settles
	// the card; the other write still asks.
	if _, err := e.Resolve(ctx, env.ID, card.ID, Decision{Action: store.ActionAllow, Scope: "sandbox", Method: http.MethodPost, PathPrefix: "/o/*"}); err != nil {
		t.Fatal(err)
	}
	if rule, err := e.Decide(ctx, on(http.MethodPost, "/o/r.git/git-receive-pack")); err != nil || rule.ID == "" {
		t.Fatalf("approved write: %+v, %v", rule, err)
	}
	if _, err := e.Decide(ctx, on(http.MethodPost, "/other/r.git/git-receive-pack")); !errors.Is(err, ErrUndecided) {
		t.Fatalf("a write the rule doesn't cover: %v", err)
	}

	// A rule for the whole host is the user's word: the connection is no longer the list's.
	if _, err := e.Store.CreateRule(ctx, store.Rule{EnvironmentID: env.ID, Host: "gitlab.com", Action: store.ActionAllow}); err != nil {
		t.Fatal(err)
	}
	if err := e.Settle(ctx, env.ID); err != nil {
		t.Fatal(err)
	}
	conn.Host = "gitlab.com"
	if rule, err := e.Decide(ctx, conn); err != nil || rule.Origin == store.OriginCurated || rule.ID == "" {
		t.Fatalf("a host rule: %+v, %v", rule, err)
	}

	// At open nothing is read-only, as the level allows the host itself.
	if err := e.Store.SetSecurityLevel(ctx, env.ID, store.LevelOpen); err != nil {
		t.Fatal(err)
	}
	conn.Host = "quay.io"
	if rule, err := e.Decide(ctx, conn); err != nil || rule.Origin == store.OriginCurated {
		t.Fatalf("open: %+v, %v", rule, err)
	}
}

// A browser's write to a read-only host asks too, as the browser's own request.
func TestCuratedWriteAsksInABrowser(t *testing.T) {
	ctx := context.Background()
	e, env, sb := newEngine(t)
	e.Hold = 50 * time.Millisecond
	if err := e.Store.SetSecurityLevel(ctx, env.ID, store.LevelGuarded); err != nil {
		t.Fatal(err)
	}
	req := Request{EnvironmentID: env.ID, SandboxID: sb.ID, Host: "github.com", Port: 443, Browser: true, Deferred: true,
		HTTP: &HTTPRequest{Method: http.MethodGet, Path: "/o/r"}}
	if rule, err := e.Decide(ctx, req); err != nil || rule.Action != store.ActionAllow {
		t.Fatalf("read: %+v, %v", rule, err)
	}
	req.HTTP = &HTTPRequest{Method: http.MethodPost, Path: "/o/r/issues"}
	if _, err := e.Decide(ctx, req); !errors.Is(err, ErrUndecided) {
		t.Fatalf("write: %v", err)
	}
	list, err := e.Store.Approvals(ctx, env.ID, store.StatusPending, 10)
	if err != nil {
		t.Fatal(err)
	}
	var n NetworkRequest
	if len(list) != 1 || json.Unmarshal(list[0].Payload, &n) != nil || n.Write {
		t.Fatalf("cards: %+v %+v", list, n)
	}
}
