package updates

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

type memSettings map[string][]byte

func (m memSettings) Setting(_ context.Context, key string) ([]byte, error) {
	v, ok := m[key]
	if !ok {
		return nil, store.ErrNotFound
	}
	return v, nil
}

func (m memSettings) SetSetting(_ context.Context, key string, value []byte) error {
	m[key] = value
	return nil
}

// fakeGitHub serves a latest release and counts requests. Tests never reach real GitHub.
func fakeGitHub(t *testing.T, tag string) (*httptest.Server, *atomic.Int32) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("Accept") != "application/vnd.github+json" {
			t.Errorf("accept header %q", r.Header.Get("Accept"))
		}
		w.Write([]byte(`{"tag_name":"` + tag + `","html_url":"https://github.com/lukaskoebe/sandbox-studio/releases/tag/` + tag + `"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestOffByDefault(t *testing.T) {
	srv, hits := fakeGitHub(t, "v1.1.0")
	c := &Checker{Settings: memSettings{}, Client: srv.Client(), URL: srv.URL, Current: "v1.0.0"}
	st, err := c.CheckIfDue(context.Background())
	if err != nil || st.Enabled || st.Available || hits.Load() != 0 {
		t.Fatalf("%+v %v hits=%d", st, err, hits.Load())
	}
}

func TestChecksAtMostDaily(t *testing.T) {
	srv, hits := fakeGitHub(t, "v1.1.0")
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	c := &Checker{Settings: memSettings{}, Client: srv.Client(), URL: srv.URL, Current: "v1.0.0", Now: func() time.Time { return now }}
	ctx := context.Background()

	st, err := c.SetEnabled(ctx, true)
	if err != nil || !st.Available || st.Latest != "v1.1.0" || st.URL == "" || hits.Load() != 1 {
		t.Fatalf("enable: %+v %v hits=%d", st, err, hits.Load())
	}
	now = now.Add(23 * time.Hour)
	c.CheckIfDue(ctx)
	c.SetEnabled(ctx, false)
	c.SetEnabled(ctx, true)
	if hits.Load() != 1 {
		t.Fatalf("checked again within a day: %d", hits.Load())
	}
	now = now.Add(2 * time.Hour)
	if st, _ = c.CheckIfDue(ctx); hits.Load() != 2 || !st.Available {
		t.Fatalf("after a day: %+v hits=%d", st, hits.Load())
	}
	// Disabled checkers keep the last result but don't show it.
	if st, _ = c.SetEnabled(ctx, false); st.Available {
		t.Fatalf("disabled but available: %+v", st)
	}
}

func TestFailedCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusForbidden)
	}))
	defer srv.Close()
	c := &Checker{Settings: memSettings{}, Client: srv.Client(), URL: srv.URL, Current: "v1.0.0"}
	st, err := c.SetEnabled(context.Background(), true)
	if err != nil || st.Error == "" || st.Available || st.CheckedAt == nil {
		t.Fatalf("%+v %v", st, err)
	}
}

func TestNewer(t *testing.T) {
	for _, tc := range []struct {
		latest, current string
		want            bool
	}{
		{"v1.0.1", "v1.0.0", true},
		{"v1.10.0", "v1.9.9", true},
		{"v1.0.0", "v1.0.0", false},
		{"v0.9.0", "v1.0.0", false},
		{"v1.0.0", "v1.0.0-rc.1", true},
		{"v1.0.0-rc.2", "v1.0.0-rc.1", true},
		{"v1.0.0-rc.1", "v1.0.0", false},
		{"v2.0.0", "dev", false},
		{"v1.0.1", "v1.0.0-3-gabc123", false},
		{"v1.0.1", "v1.0.0-dirty", false},
		{"garbage", "v1.0.0", false},
	} {
		if got := Newer(tc.latest, tc.current); got != tc.want {
			t.Errorf("Newer(%q, %q) = %v", tc.latest, tc.current, got)
		}
	}
}
