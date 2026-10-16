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

// fakeSites is a browser whose page is page, per teammate: each teammate's browser approves its own sites.
type fakeSites struct {
	mu       sync.Mutex
	page     string
	approved map[string]map[string]bool // teammate ID -> site
	hosts    map[string][]string        // teammate ID -> host patterns
}

func (f *fakeSites) TopLevel(context.Context, string, string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.page
}

func (f *fakeSites) Allows(_ context.Context, _, teammate, host string, r HTTPRequest) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.hosts[teammate] {
		if Covers(p, host) {
			return true
		}
	}
	if r.Navigation {
		return f.approved[teammate][r.Site]
	}
	return f.page != "" && f.approved[teammate][f.page]
}

func (f *fakeSites) Approve(_ context.Context, _, teammate, site string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.approved[teammate] == nil {
		f.approved[teammate] = map[string]bool{}
	}
	f.approved[teammate][site] = true
}

func (f *fakeSites) ApproveHost(_ context.Context, _, teammate, pattern string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.hosts == nil {
		f.hosts = map[string][]string{}
	}
	f.hosts[teammate] = append(f.hosts[teammate], pattern)
}

func (f *fakeSites) Navigating(_ context.Context, _, _, site string) {
	f.setPage(site)
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
	e.Browsers = &fakeSites{page: "docs.example.com", approved: map[string]map[string]bool{}}
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
// asking; a page on another site still asks, only a navigation can be allowed for its site, and
// another teammate's browser approves nothing of this one's.
func TestSiteApprovalCoversTheNavigationAndItsPage(t *testing.T) {
	ctx := context.Background()
	e, env, _, sb, bob := newTeammateEngine(t) // the browser's teammate owns sb; bob's teammate owns bob
	sites := &fakeSites{page: "docs.example.com", approved: map[string]map[string]bool{}}
	e.Browsers = sites
	held := make(chan heldResult, 8)
	hold := func(sandbox string, host string, http HTTPRequest) {
		r, err := e.Decide(ctx, Request{EnvironmentID: env.ID, SandboxID: sandbox, Host: host, Port: 443, Browser: true, Deferred: true, HTTP: &http})
		held <- heldResult{r, err}
	}

	go hold(sb.ID, "news.example", HTTPRequest{Method: "GET", Path: "/", Navigation: true, Site: "news.example"})
	sites.setPage("news.example")
	go hold(sb.ID, "img.news.example", HTTPRequest{Method: "GET", Path: "/a.png"})
	go hold(bob.ID, "img.news.example", HTTPRequest{Method: "GET", Path: "/d.png"})
	// Three approvals are pending: the navigation, the page's request and bob's, which shares
	// its subject with the page's.
	var all []store.Approval
	for deadline := time.Now().Add(5 * time.Second); len(all) < 3; {
		if time.Now().After(deadline) {
			t.Fatalf("pending approvals: %+v", all)
		}
		all, _ = e.Store.Approvals(ctx, env.ID, store.StatusPending, 10)
		time.Sleep(time.Millisecond)
	}
	pending := map[string]store.Approval{}
	for _, a := range all {
		if a.SandboxID == sb.ID {
			pending[a.Subject] = a
		}
	}
	nav, ok := pending["news.example:443 news.example"]
	if !ok {
		t.Fatalf("no navigation approval: %v", pending)
	}
	sub, ok := pending["img.news.example:443 news.example"]
	if !ok || sub.Kind != KindRequest {
		t.Fatalf("no subresource approval of the page: %v", pending)
	}

	if _, err := e.Resolve(ctx, env.ID, nav.ID, Decision{Action: ActionAllowSite}); err != nil {
		t.Fatal(err)
	}
	if a, _ := e.Store.Approval(ctx, env.ID, nav.ID); a.Status != store.StatusApproved || a.RuleID != "" {
		t.Errorf("site approval: %+v", a)
	}
	// The navigation and the page's own request are answered; bob's request still waits.
	answered := 0
	for answered < 2 {
		select {
		case r := <-held:
			if r.err != nil || r.rule.Action != store.ActionAllow {
				t.Errorf("held request after the site was approved: %+v %v", r.rule, r.err)
			}
			answered++
		case <-time.After(5 * time.Second):
			t.Fatal("held requests were not answered by the site approval")
		}
	}
	if rules, _ := e.Store.Rules(ctx, env.ID); len(rules) != 0 {
		t.Errorf("a site approval made rules: %+v", rules)
	}
	left, _ := e.Store.Approvals(ctx, env.ID, store.StatusPending, 10)
	if len(left) != 1 || left[0].SandboxID != bob.ID {
		t.Errorf("pending after the site approval: %+v", left)
	}

	// The page is on the approved site now: its requests pass without an approval.
	go hold(sb.ID, "cdn.news.example", HTTPRequest{Method: "GET", Path: "/b.js"})
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
	go hold(sb.ID, "cdn.example.net", HTTPRequest{Method: "GET", Path: "/c.js"})
	pending = waitPending(t, e, env.ID, 2)
	docs, ok := pending["cdn.example.net:443 docs.example.com"]
	if !ok {
		t.Fatalf("no approval for the other page: %v", pending)
	}
	if _, err := e.Resolve(ctx, env.ID, docs.ID, Decision{Action: ActionAllowSite}); !errors.Is(err, ErrNotSite) {
		t.Errorf("a subresource was allowed as a site: %v", err)
	}
}

// Allowing a browser's request for the browser session makes no rule: the browser's requests to
// the hosts the pattern covers pass, the ones it made while it waited too, and the sandboxes' own
// connections to them still ask, as another teammate's browser does. A browser-only rule likewise
// covers the browsers' requests and none of the sandboxes' connections.
func TestBrowserSessionAndBrowserOnlyAllows(t *testing.T) {
	ctx := context.Background()
	e, env, _, sb, bob := newTeammateEngine(t) // the browser's teammate owns sb; bob's teammate owns bob
	if err := e.Store.SetSecurityLevel(ctx, env.ID, store.LevelLocked); err != nil {
		t.Fatal(err)
	}
	e.Browsers = &fakeSites{page: "docs.example.com", approved: map[string]map[string]bool{}}
	held := make(chan heldResult, 4)
	browser := func(sandbox, host string) {
		r, err := e.Decide(ctx, Request{EnvironmentID: env.ID, SandboxID: sandbox, Host: host, Port: 443, Browser: true, Deferred: true, HTTP: &HTTPRequest{Method: "GET", Path: "/lib.js"}})
		held <- heldResult{r, err}
	}
	answered := func(what string) {
		t.Helper()
		select {
		case r := <-held:
			if r.err != nil || r.rule.Action != store.ActionAllow {
				t.Errorf("%s: %+v %v", what, r.rule, r.err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: not answered", what)
		}
	}
	go browser(sb.ID, "cdn.example.net")
	go browser(sb.ID, "img.example.net")
	pending := waitPending(t, e, env.ID, 2)
	cdn, ok := pending["cdn.example.net:443 docs.example.com"]
	if !ok {
		t.Fatalf("subjects: %v", pending)
	}
	if _, err := e.Resolve(ctx, env.ID, cdn.ID, Decision{Action: ActionAllowSession, Host: "*.example.org"}); !errors.Is(err, ErrDoesNotCover) {
		t.Errorf("a pattern that doesn't cover the host: %v", err)
	}
	if _, err := e.Resolve(ctx, env.ID, cdn.ID, Decision{Action: ActionAllowSession, Host: "*.example.net"}); err != nil {
		t.Fatal(err)
	}
	answered("the request allowed for the session")
	answered("the request the pattern covers")
	if rules, _ := e.Store.Rules(ctx, env.ID); len(rules) != 0 {
		t.Errorf("a session allow made rules: %+v", rules)
	}
	browser(sb.ID, "fonts.example.net")
	answered("a later request of the browser")

	e.Hold = 20 * time.Millisecond
	if _, err := e.Decide(ctx, Request{EnvironmentID: env.ID, SandboxID: bob.ID, Host: "cdn.example.net", Port: 443, Browser: true, Deferred: true, HTTP: &HTTPRequest{Method: "GET", Path: "/lib.js"}}); !errors.Is(err, ErrUndecided) {
		t.Errorf("another teammate's browser: %v", err)
	}
	if _, err := e.Decide(ctx, Request{EnvironmentID: env.ID, SandboxID: sb.ID, Host: "cdn.example.net", Port: 443}); !errors.Is(err, ErrUndecided) {
		t.Errorf("the sandbox's own connection: %v", err)
	}
	pending = waitPending(t, e, env.ID, 2)
	conn, ok := pending["cdn.example.net:443"]
	if !ok {
		t.Fatalf("subjects: %v", pending)
	}
	if _, err := e.Resolve(ctx, env.ID, conn.ID, Decision{Action: ActionAllowSession}); !errors.Is(err, ErrNotBrowser) {
		t.Errorf("a connection was allowed for the browser session: %v", err)
	}

	// A browser-only rule answers bob's browser, and the sandbox's connection still asks.
	if _, err := e.Resolve(ctx, env.ID, pending["cdn.example.net:443 docs.example.com"].ID, Decision{Action: store.ActionAllow, Host: "*.example.net", Scope: "environment", BrowserOnly: true}); err != nil {
		t.Fatal(err)
	}
	if rules, _ := e.Store.Rules(ctx, env.ID); len(rules) != 1 || !rules[0].BrowserOnly {
		t.Errorf("rules: %+v", rules)
	}
	if a, _ := e.Store.Approval(ctx, env.ID, conn.ID); a.Status != store.StatusPending {
		t.Errorf("a browser-only rule settled the sandbox's connection: %+v", a)
	}
	if r, err := e.Decide(ctx, Request{EnvironmentID: env.ID, SandboxID: bob.ID, Host: "img.example.net", Port: 443, Browser: true, Deferred: true, HTTP: &HTTPRequest{Method: "GET", Path: "/a.png"}}); err != nil || !r.BrowserOnly {
		t.Errorf("a browser's request under a browser-only rule: %+v %v", r, err)
	}
	if _, err := e.Decide(ctx, Request{EnvironmentID: env.ID, SandboxID: bob.ID, Host: "img.example.net", Port: 443}); !errors.Is(err, ErrUndecided) {
		t.Errorf("a sandbox's connection under a browser-only rule: %v", err)
	}
	if e.Terminates(ctx, Request{EnvironmentID: env.ID, SandboxID: bob.ID, Host: "img.example.net", Port: 443}) {
		t.Error("a browser-only rule terminates a sandbox's connections")
	}
}

// A navigation the user allowed with a rule approves its site for the browser session, and the
// page it loads is on that site at once: its requests to other hosts pass without asking. One
// allowed once, or by the AI reviewer's rule, moves the page but approves nothing.
func TestAllowedNavigationApprovesItsSite(t *testing.T) {
	ctx := context.Background()
	e, env, _, sb, _ := newTeammateEngine(t) // the browser's teammate owns sb
	if err := e.Store.SetSecurityLevel(ctx, env.ID, store.LevelLocked); err != nil {
		t.Fatal(err)
	}
	sites := &fakeSites{approved: map[string]map[string]bool{}}
	e.Browsers = sites
	navigate := func(site string) (store.Rule, error) {
		return e.Decide(ctx, Request{EnvironmentID: env.ID, SandboxID: sb.ID, Host: "www." + site, Port: 443, Browser: true, Deferred: true,
			HTTP: &HTTPRequest{Method: "GET", Path: "/", Navigation: true, Site: site}})
	}
	subresource := func() error {
		_, err := e.Decide(ctx, Request{EnvironmentID: env.ID, SandboxID: sb.ID, Host: "cdn.example.net", Port: 443, Browser: true, Deferred: true,
			HTTP: &HTTPRequest{Method: "GET", Path: "/app.js"}})
		return err
	}
	approved := func(site string) bool {
		sites.mu.Lock()
		defer sites.mu.Unlock()
		return sites.approved[sb.TeammateID][site]
	}

	held := make(chan heldResult, 1)
	go func() {
		r, err := navigate("news.example")
		held <- heldResult{r, err}
	}()
	nav := waitPending(t, e, env.ID, 1)["www.news.example:443 news.example"]
	if _, err := e.Resolve(ctx, env.ID, nav.ID, Decision{Action: store.ActionAllow, Scope: "environment"}); err != nil {
		t.Fatal(err)
	}
	if r := <-held; r.err != nil || r.rule.Action != store.ActionAllow {
		t.Fatalf("the navigation: %+v %v", r.rule, r.err)
	}
	if !approved("news.example") || sites.TopLevel(ctx, env.ID, sb.TeammateID) != "news.example" {
		t.Fatal("an allowed navigation didn't approve its site, or the page isn't on it")
	}
	if err := subresource(); err != nil {
		t.Errorf("a request of the approved page asked: %v", err)
	}

	e.Hold = 20 * time.Millisecond
	if _, err := e.Store.CreateRule(ctx, store.Rule{EnvironmentID: env.ID, Host: "www.reviewed.example", Action: store.ActionAllow, Origin: store.OriginReviewer}); err != nil {
		t.Fatal(err)
	}
	if err := e.Settle(ctx, env.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := navigate("reviewed.example"); err != nil {
		t.Fatal(err)
	}
	if approved("reviewed.example") || sites.TopLevel(ctx, env.ID, sb.TeammateID) != "reviewed.example" {
		t.Error("the reviewer's rule approved the site, or the page didn't move")
	}
	if err := subresource(); !errors.Is(err, ErrUndecided) {
		t.Errorf("a request of a page the reviewer allowed: %v", err)
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

// A request Chromium makes for its own services is denied without asking and recorded with its
// reason; the same host's other paths ask as usual, and an allow rule wins.
func TestBrowserChromiumServicesAreDenied(t *testing.T) {
	ctx := context.Background()
	e, env, sb := newEngine(t)
	e.Hold = 50 * time.Millisecond
	list := Request{EnvironmentID: env.ID, SandboxID: sb.ID, Host: "accounts.google.com", Port: 443, Browser: true, Deferred: true,
		HTTP: &HTTPRequest{Method: "POST", Path: "/ListAccounts"}}
	if _, err := e.Decide(ctx, Request{EnvironmentID: env.ID, SandboxID: sb.ID, Host: "accounts.google.com", Port: 443, Browser: true, Deferred: true}); !errors.Is(err, ErrPerRequest) {
		t.Fatalf("the connection: got %v, want its requests decided one at a time", err)
	}
	for range 2 {
		if _, err := e.Decide(ctx, list); !errors.Is(err, ErrChromium) {
			t.Fatalf("got %v, want ErrChromium", err)
		}
	}
	denied, _ := e.Store.Approvals(ctx, env.ID, store.StatusDenied, 10)
	if len(denied) != 1 || denied[0].Subject != "chromium accounts.google.com:443" || denied[0].Attempts != 2 || denied[0].Kind != KindRequest {
		t.Fatalf("activity: %+v", denied)
	}
	var req NetworkRequest
	if err := json.Unmarshal(denied[0].Payload, &req); err != nil || req.Decided != DecidedChromium || req.Reason == "" || req.Path != "/ListAccounts" {
		t.Errorf("payload %+v %v", req, err)
	}

	// A report Chromium uploads is denied whichever host the site names.
	report := Request{EnvironmentID: env.ID, SandboxID: sb.ID, Host: "a.nel.cloudflare.com", Port: 443, Browser: true, Deferred: true,
		HTTP: &HTTPRequest{Method: "OPTIONS", Path: "/report/v4", Report: true}}
	if _, err := e.Decide(ctx, report); !errors.Is(err, ErrChromium) {
		t.Fatalf("a report upload: got %v, want ErrChromium", err)
	}

	signin := list
	signin.HTTP = &HTTPRequest{Method: "GET", Path: "/signin", Navigation: true, Site: "google.com"}
	if _, err := e.Decide(ctx, signin); !errors.Is(err, ErrUndecided) {
		t.Fatalf("a sign-in page: got %v, want it asked", err)
	}

	if _, err := e.Store.CreateRule(ctx, store.Rule{EnvironmentID: env.ID, Host: "accounts.google.com", Ports: []int{443}, Action: store.ActionAllow}); err != nil {
		t.Fatal(err)
	}
	if err := e.Settle(ctx, env.ID); err != nil {
		t.Fatal(err)
	}
	if r, err := e.Decide(ctx, list); err != nil || r.Action != store.ActionAllow {
		t.Fatalf("an allow rule did not win: %+v %v", r, err)
	}
}
