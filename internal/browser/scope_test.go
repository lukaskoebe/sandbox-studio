package browser

import (
	"context"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/policy"
)

func TestTopLevelNamesThePageUsedLast(t *testing.T) {
	ctx := context.Background()
	s := &Service{}
	if got := s.TopLevel(ctx, "env", "tm"); got != "" {
		t.Fatalf("a browser that never ran has a page: %q", got)
	}
	st := s.state("env", "tm")
	s.mu.Lock()
	st.page.url, st.page.used = "https://docs.example.com/guides", time.Now().Add(-time.Minute)
	st.eph = map[string]*page{"eph-1": {id: "eph-1", url: "https://news.example.org/a", used: time.Now()}}
	s.mu.Unlock()
	if got := s.TopLevel(ctx, "env", "tm"); got != "example.org" {
		t.Errorf("top level %q, want the ephemeral page used last", got)
	}
	s.mu.Lock()
	st.eph["eph-1"].url = "about:blank"
	s.mu.Unlock()
	if got := s.TopLevel(ctx, "env", "tm"); got != "example.com" {
		t.Errorf("top level %q, want the profile's page when the other has no web page", got)
	}
	if got := s.TopLevel(ctx, "other-env", "tm"); got != "" {
		t.Errorf("another environment sees the page: %q", got)
	}
}

func TestSiteApprovalsLastTheBrowserSession(t *testing.T) {
	ctx := context.Background()
	s := &Service{}
	const env, tm = "env", "tm"
	navigation := func(site string) policy.HTTPRequest {
		return policy.HTTPRequest{Method: "GET", Path: "/", Navigation: true, Site: site}
	}
	subresource := policy.HTTPRequest{Method: "GET", Path: "/app.js"}
	if s.Allows(ctx, env, tm, navigation("example.com")) {
		t.Fatal("a browser with no approved site allowed a navigation")
	}

	s.Approve(ctx, env, tm, "example.com")
	st := s.state(env, tm)
	s.mu.Lock()
	st.page.url = "https://docs.example.com/guides"
	st.eph = map[string]*page{"eph-1": {id: "eph-1", url: "about:blank"}}
	s.mu.Unlock()
	if !s.Allows(ctx, env, tm, navigation("example.com")) || s.Allows(ctx, env, tm, navigation("other.example")) {
		t.Error("a navigation is covered by its own approved site only")
	}
	if !s.Allows(ctx, env, tm, subresource) {
		t.Error("a subresource of a page on an approved site asked")
	}

	// With another web page open, the subresource's page is unknown: it must be approved too.
	s.mu.Lock()
	st.eph["eph-1"].url = "https://evil.example/"
	s.mu.Unlock()
	if s.Allows(ctx, env, tm, subresource) {
		t.Error("a subresource was allowed while a page on an unapproved site was open")
	}
	s.Approve(ctx, env, tm, "evil.example")
	if !s.Allows(ctx, env, tm, subresource) {
		t.Error("a subresource asked after every open page was approved")
	}
	if s.Allows(ctx, "other-env", tm, navigation("example.com")) {
		t.Error("another environment is covered by the approvals")
	}

	// The session ends with the browser's run.
	s.mu.Lock()
	st.reset()
	s.mu.Unlock()
	if s.Allows(ctx, env, tm, navigation("example.com")) {
		t.Error("an approval outlived the browser session")
	}
}
