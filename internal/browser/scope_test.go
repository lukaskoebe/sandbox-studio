package browser

import (
	"context"
	"testing"
	"time"
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
