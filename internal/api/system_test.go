package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/autostart"
	"github.com/lukaskoebe/sandbox-studio/internal/doctor"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/updates"
)

func TestSystemSettings(t *testing.T) {
	h, s := newTestServer(t)
	home := t.TempDir()
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v9.0.0","html_url":"https://github.com/lukaskoebe/sandbox-studio/releases/tag/v9.0.0"}`))
	}))
	defer gh.Close()
	s.System = System{
		Doctor: func(context.Context) doctor.Report {
			return doctor.Report{Status: doctor.Warn, Checks: []doctor.Check{{ID: "kvm", Status: doctor.Warn}}}
		},
		Autostart: &autostart.Service{GOOS: "linux", Home: home, ConfigHome: filepath.Join(home, ".config"), Exe: "/usr/bin/studio"},
		Updates:   &updates.Checker{Settings: s.Store, Client: gh.Client(), URL: gh.URL, Current: "v1.0.0"},
	}

	rec := do(h, "GET", "http://localhost:7878/api/system/doctor", "")
	var report doctor.Report
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &report) != nil || report.Status != doctor.Warn {
		t.Fatalf("doctor: %d %s", rec.Code, rec.Body)
	}

	var st autostart.Status
	rec = do(h, "PUT", "http://localhost:7878/api/system/autostart", `{"enabled":true}`)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &st) != nil || !st.Enabled {
		t.Fatalf("enable autostart: %d %s", rec.Code, rec.Body)
	}
	rec = do(h, "PUT", "http://localhost:7878/api/system/autostart", `{"enabled":false}`)
	if json.Unmarshal(rec.Body.Bytes(), &st) != nil || st.Enabled {
		t.Fatalf("disable autostart: %d %s", rec.Code, rec.Body)
	}

	var up updates.State
	rec = do(h, "GET", "http://localhost:7878/api/system/updates", "")
	if json.Unmarshal(rec.Body.Bytes(), &up) != nil || up.Mode != updates.ModeTell || up.Available || up.CanInstall {
		t.Fatalf("updates default: %d %s", rec.Code, rec.Body)
	}
	rec = do(h, "PUT", "http://localhost:7878/api/system/updates", `{"mode":"tell"}`)
	if json.Unmarshal(rec.Body.Bytes(), &up) != nil || !up.Available || up.Latest != "v9.0.0" {
		t.Fatalf("enable updates: %d %s", rec.Code, rec.Body)
	}
	// This Studio has no installer.
	if rec = do(h, "PUT", "http://localhost:7878/api/system/updates", `{"mode":"install"}`); rec.Code != http.StatusConflict {
		t.Fatalf("install mode without an installer: %d %s", rec.Code, rec.Body)
	}
}

// Previews are off until someone turns them on. Studio asks for the one-time notice only while
// nobody has chosen and there are sandboxes to preview; any choice ends it.
func TestPreviewSettingIsOffWithANoticeOnce(t *testing.T) {
	h, s := newTestServer(t)
	get := func() PreviewSetting {
		t.Helper()
		var st PreviewSetting
		rec := do(h, "GET", "http://localhost:7878/api/system/previews", "")
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &st) != nil {
			t.Fatalf("get: %d %s", rec.Code, rec.Body)
		}
		return st
	}
	if st := get(); st.Enabled || st.Notice {
		t.Fatalf("no sandboxes: %+v", st)
	}
	ctx := context.Background()
	env, err := s.Store.CreateEnvironment(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.CreateSandbox(ctx, store.Sandbox{EnvironmentID: env.ID, Name: "api"}); err != nil {
		t.Fatal(err)
	}
	if st := get(); st.Enabled || !st.Notice {
		t.Fatalf("a sandbox, no choice: %+v", st)
	}
	if rec := do(h, "PUT", "http://localhost:7878/api/system/previews", `{"enabled":true}`); rec.Code != 200 {
		t.Fatalf("enable: %d %s", rec.Code, rec.Body)
	}
	if st := get(); !st.Enabled || st.Notice {
		t.Fatalf("enabled: %+v", st)
	}
	do(h, "PUT", "http://localhost:7878/api/system/previews", `{"enabled":false}`)
	if st := get(); st.Enabled || st.Notice {
		t.Fatalf("disabled: %+v", st)
	}
}
