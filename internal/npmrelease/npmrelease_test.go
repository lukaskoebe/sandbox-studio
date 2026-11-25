package npmrelease

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStablePrefersTheStableTagAndRemembersIt(t *testing.T) {
	asked := 0
	var path string
	tags := `{"latest":"2.1.345","stable":"2.1.340","next":"2.2.0-beta.1"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked++
		path = r.URL.EscapedPath()
		_, _ = io.WriteString(w, tags)
	}))
	defer srv.Close()
	now := time.Unix(1_800_000_000, 0)
	f := &Finder{Registry: srv.URL, Now: func() time.Time { return now }}

	v, err := f.Stable(context.Background(), "@anthropic-ai/claude-code")
	if err != nil || v != "2.1.340" {
		t.Fatalf("got %q, %v", v, err)
	}
	if path != "/-/package/@anthropic-ai%2fclaude-code/dist-tags" {
		t.Fatalf("asked %s", path)
	}
	if v, _ := f.Stable(context.Background(), "@anthropic-ai/claude-code"); v != "2.1.340" || asked != 1 {
		t.Fatalf("a second ask within the TTL asked npm again (%d) or got %q", asked, v)
	}
	now = now.Add(ttl + time.Second)
	tags = `{"latest":"2.1.350"}`
	if v, _ := f.Stable(context.Background(), "@anthropic-ai/claude-code"); v != "2.1.350" || asked != 2 {
		t.Fatalf("after the TTL: %q, asked %d; a package without a stable tag takes latest", v, asked)
	}
}

func TestStableRefusesWhatIsNotAVersion(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		want   string
	}{
		{404, `{"error":"Not found"}`, "404"},
		{200, `{"latest":"^2 || file:/x"}`, "not a version"},
		{200, `{"beta":"1.0.0"}`, "no stable release"},
		{200, `not json`, "reading"},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = io.WriteString(w, tc.body)
		}))
		_, err := (&Finder{Registry: srv.URL}).Stable(context.Background(), "x")
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%d %s: %v, want %q", tc.status, tc.body, err, tc.want)
		}
	}
}
