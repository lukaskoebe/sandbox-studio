package policy

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// fakeSites is a browser whose page is page and whose approved sites are approved.
type fakeSites struct {
	mu       sync.Mutex
	page     string
	approved map[string]bool
}

func (f *fakeSites) TopLevel(context.Context, string, string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.page
}

func (f *fakeSites) Allows(_ context.Context, _, _ string, r HTTPRequest) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Navigation {
		return f.approved[r.Site]
	}
	return f.page != "" && f.approved[f.page]
}

func (f *fakeSites) Approve(_ context.Context, _, _, site string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.approved[site] = true
}

func (f *fakeSites) setPage(page string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.page = page
}

// heldResult is what a held request is answered with.
type heldResult struct {
	rule store.Rule
	err  error
}

// waitPending waits until n approvals are pending and returns them by subject.
func waitPending(t *testing.T, e *Engine, envID string, n int) map[string]store.Approval {
	t.Helper()
	bySubject := map[string]store.Approval{}
	for deadline := time.Now().Add(5 * time.Second); len(bySubject) < n; {
		if time.Now().After(deadline) {
			t.Fatalf("pending approvals: %v", bySubject)
		}
		pending, _ := e.Store.Approvals(context.Background(), envID, store.StatusPending, 10)
		for _, a := range pending {
			bySubject[a.Subject] = a
		}
		time.Sleep(time.Millisecond)
	}
	return bySubject
}

func TestSite(t *testing.T) {
	for host, want := range map[string]string{
		"docs.example.com": "example.com",
		"Example.COM.":     "example.com",
		"news.co.uk":       "news.co.uk",
		"a.b.news.co.uk":   "news.co.uk",
		"192.0.2.7":        "192.0.2.7",
		"localhost":        "localhost",
	} {
		if got := Site(host); got != want {
			t.Errorf("Site(%q) = %q, want %q", host, got, want)
		}
	}
}

// A browser's connection that no rule covers is not held: its requests are asked for one by one.
func TestDeferredConnectionIsNotHeld(t *testing.T) {
	e, env, sb := newEngine(t)
	_, err := e.Decide(context.Background(), Request{EnvironmentID: env.ID, SandboxID: sb.ID, Host: "cdn.example.net", Port: 443, Deferred: true})
	if !errors.Is(err, ErrPerRequest) {
		t.Fatalf("got %v, want ErrPerRequest", err)
	}
	if pending, _ := e.Store.Approvals(context.Background(), env.ID, store.StatusPending, 10); len(pending) != 0 {
		t.Fatalf("a deferred connection raised an approval: %+v", pending)
	}
}

// A request on a terminated connection is held as a network.request approval that names it and
// the top-level site it belongs to; a navigation's site is its own host, the page's is the browser's.
func TestRequestApprovalNamesTheRequest(t *testing.T) {
	ctx := context.Background()
	e, env, sb := newEngine(t)
	e.Browsers = &fakeSites{page: "docs.example.com", approved: map[string]bool{}}
	held := make(chan heldResult, 2)
	hold := func(req Request) {
		r, err := e.Decide(ctx, req)
		held <- heldResult{r, err}
	}
	go hold(Request{EnvironmentID: env.ID, SandboxID: sb.ID, Host: "cdn.example.net", Port: 443, Deferred: true,
		HTTP: &HTTPRequest{Method: "GET", Path: "/lib.js", Initiator: "https://docs.example.com"}})
	go hold(Request{EnvironmentID: env.ID, SandboxID: sb.ID, Host: "news.example", Port: 443, Deferred: true,
		HTTP: &HTTPRequest{Method: "GET", Path: "/", Navigation: true, Site: "news.example"}})

	// Both requests are pending, each under its own subject.
	bySubject := map[string]store.Approval{}
	for deadline := time.Now().Add(5 * time.Second); len(bySubject) < 2; {
		if time.Now().After(deadline) {
			t.Fatalf("pending requests: %v", bySubject)
		}
		pending, _ := e.Store.Approvals(ctx, env.ID, store.StatusPending, 10)
		for _, a := range pending {
			if a.Kind != KindRequest {
				t.Fatalf("kind %q, want %q", a.Kind, KindRequest)
			}
			bySubject[a.Subject] = a
		}
		time.Sleep(time.Millisecond)
	}
	sub, ok := bySubject["cdn.example.net:443 docs.example.com"]
	if !ok {
		t.Fatalf("subjects: %v", bySubject)
	}
	nav, ok := bySubject["news.example:443 news.example"]
	if !ok {
		t.Fatalf("no navigation subject: %v", bySubject)
	}
	var req NetworkRequest
	if err := json.Unmarshal(sub.Payload, &req); err != nil {
		t.Fatal(err)
	}
	if req.Method != "GET" || req.Path != "/lib.js" || req.Site != "docs.example.com" || req.Navigation || req.Initiator != "https://docs.example.com" {
		t.Errorf("payload %+v", req)
	}
	if err := json.Unmarshal(nav.Payload, &req); err != nil || !req.Navigation || req.Site != "news.example" {
		t.Errorf("navigation payload %+v %v", req, err)
	}

	// Each decision answers the request held for it.
	if _, err := e.Resolve(ctx, env.ID, sub.ID, Decision{Action: "allow", Host: "*.example.net", Scope: "environment"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Resolve(ctx, env.ID, nav.ID, Decision{Action: "deny", Scope: "environment"}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case r := <-held:
			if r.err != nil {
				t.Fatalf("held request: %v", r.err)
			}
			if r.rule.Action == store.ActionAllow && r.rule.Host != "*.example.net" || r.rule.Action == store.ActionDeny && r.rule.Host != "news.example" {
				t.Errorf("held request answered by %+v", r.rule)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("a held request was not answered")
		}
	}
}

// Allowing a navigation's site for the browser session answers the navigation, and the requests
// its page made to that site while it waited. Later requests of the page are allowed without
// asking; a page on another site still asks, and only a navigation can be allowed for its site.
func TestSiteApprovalCoversTheNavigationAndItsPage(t *testing.T) {
	ctx := context.Background()
	e, env, _, sb, _ := newTeammateEngine(t) // the browser's teammate owns the sandbox
	sites := &fakeSites{page: "docs.example.com", approved: map[string]bool{}}
	e.Browsers = sites
	held := make(chan heldResult, 4)
	hold := func(req Request) {
		r, err := e.Decide(ctx, req)
		held <- heldResult{r, err}
	}
	request := func(host string, http HTTPRequest) Request {
		return Request{EnvironmentID: env.ID, SandboxID: sb.ID, Host: host, Port: 443, Browser: true, Deferred: true, HTTP: &http}
	}

	go hold(request("news.example", HTTPRequest{Method: "GET", Path: "/", Navigation: true, Site: "news.example"}))
	sites.setPage("news.example")
	go hold(request("img.news.example", HTTPRequest{Method: "GET", Path: "/a.png"}))
	pending := waitPending(t, e, env.ID, 2)
	nav, ok := pending["news.example:443 news.example"]
	if !ok {
		t.Fatalf("no navigation approval: %v", pending)
	}
	sub, ok := pending["img.news.example:443 news.example"]
	if !ok {
		t.Fatalf("no subresource approval of the page: %v", pending)
	}

	if _, err := e.Resolve(ctx, env.ID, nav.ID, Decision{Action: ActionAllowSite}); err != nil {
		t.Fatal(err)
	}
	if a, _ := e.Store.Approval(ctx, env.ID, nav.ID); a.Status != store.StatusApproved || a.RuleID != "" {
		t.Errorf("site approval: %+v", a)
	}
	for range 2 {
		select {
		case r := <-held:
			if r.err != nil || r.rule.Action != store.ActionAllow {
				t.Errorf("held request after the site was approved: %+v %v", r.rule, r.err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("held requests were not answered by the site approval")
		}
	}
	if rules, _ := e.Store.Rules(ctx, env.ID); len(rules) != 0 {
		t.Errorf("a site approval made rules: %+v", rules)
	}
	if left, _ := e.Store.Approvals(ctx, env.ID, store.StatusPending, 10); len(left) != 0 {
		t.Errorf("still pending: %+v", left)
	}
	if sub.Kind != KindRequest {
		t.Errorf("subresource approval %+v", sub)
	}

	// The page is on the approved site now: its requests pass without an approval.
	go hold(request("cdn.news.example", HTTPRequest{Method: "GET", Path: "/b.js"}))
	select {
	case r := <-held:
		if r.err != nil || r.rule.Action != store.ActionAllow {
			t.Errorf("request of an approved page: %+v %v", r.rule, r.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a request of an approved page asked")
	}

	// A page on another site asks for its subresources, and a subresource can't be a site.
	sites.setPage("docs.example.com")
	go hold(request("cdn.example.net", HTTPRequest{Method: "GET", Path: "/c.js"}))
	pending = waitPending(t, e, env.ID, 1)
	docs := pending["cdn.example.net:443 docs.example.com"]
	if docs.ID == "" {
		t.Fatalf("no approval for the other page: %v", pending)
	}
	if _, err := e.Resolve(ctx, env.ID, docs.ID, Decision{Action: ActionAllowSite}); !errors.Is(err, ErrNotSite) {
		t.Errorf("a subresource was allowed as a site: %v", err)
	}
}

// The tracker list denies a browser's requests to tracker hosts without asking, and an allow rule
// for the host wins. Each denial is recorded for Activity; the environment can turn the list off.
func TestBrowserTrackersAreDenied(t *testing.T) {
	ctx := context.Background()
	e, env, sb := newEngine(t)
	tracker := Request{EnvironmentID: env.ID, SandboxID: sb.ID, Host: "www.google-analytics.com", Port: 443, Browser: true, Deferred: true}
	for range 2 {
		if _, err := e.Decide(ctx, tracker); !errors.Is(err, ErrTracker) {
			t.Fatalf("got %v, want ErrTracker", err)
		}
	}
	if pending, _ := e.Store.Approvals(ctx, env.ID, store.StatusPending, 10); len(pending) != 0 {
		t.Fatalf("a tracker asked: %+v", pending)
	}
	denied, _ := e.Store.Approvals(ctx, env.ID, store.StatusDenied, 10)
	if len(denied) != 1 || denied[0].Subject != "tracker www.google-analytics.com:443" || denied[0].Attempts != 2 || denied[0].Kind != KindNetwork {
		t.Fatalf("activity: %+v", denied)
	}
	var req NetworkRequest
	if err := json.Unmarshal(denied[0].Payload, &req); err != nil || !req.Tracker {
		t.Errorf("payload %+v %v", req, err)
	}

	if _, err := e.Store.CreateRule(ctx, store.Rule{EnvironmentID: env.ID, Host: "www.google-analytics.com", Ports: []int{443}, Action: store.ActionAllow}); err != nil {
		t.Fatal(err)
	}
	if err := e.Settle(ctx, env.ID); err != nil {
		t.Fatal(err)
	}
	if r, err := e.Decide(ctx, tracker); err != nil || r.Action != store.ActionAllow {
		t.Fatalf("an allow rule did not win over the list: %+v %v", r, err)
	}

	if err := e.Store.SetBlockTrackers(ctx, env.ID, false); err != nil {
		t.Fatal(err)
	}
	other := tracker
	other.Host = "stats.g.doubleclick.net"
	if _, err := e.Decide(ctx, other); !errors.Is(err, ErrPerRequest) {
		t.Fatalf("with trackers allowed, got %v, want the browser's requests decided one at a time", err)
	}
}
