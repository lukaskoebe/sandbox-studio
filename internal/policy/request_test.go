package policy

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// fakeBrowsers says which top-level site a browser's requests belong to.
type fakeBrowsers string

func (f fakeBrowsers) TopLevel(context.Context, string, string) string { return string(f) }

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
	e.Browsers = fakeBrowsers("docs.example.com")
	type result struct {
		rule store.Rule
		err  error
	}
	held := make(chan result, 2)
	hold := func(req Request) {
		r, err := e.Decide(ctx, req)
		held <- result{r, err}
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
