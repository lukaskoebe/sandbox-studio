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
	"github.com/lukaskoebe/sandbox-studio/internal/updates"
)

func TestSystemSettings(t *testing.T) {
	h, s := newTestServer(t)
	home := t.TempDir()
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tag_name":"v9.0.0","html_url":"https://github.com/lukaskoebe/sandbox-studio/releases/tag/v9.0.0"}`))
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
	if json.Unmarshal(rec.Body.Bytes(), &up) != nil || up.Enabled || up.Available {
		t.Fatalf("updates default: %d %s", rec.Code, rec.Body)
	}
	rec = do(h, "PUT", "http://localhost:7878/api/system/updates", `{"enabled":true}`)
	if json.Unmarshal(rec.Body.Bytes(), &up) != nil || !up.Available || up.Latest != "v9.0.0" {
		t.Fatalf("enable updates: %d %s", rec.Code, rec.Body)
	}
}
