//go:build linux

package guest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBrowserPoliciesAreWrittenOnceAsJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policies", "managed", "sandbox-studio.json")
	if err := writeBrowserPolicies(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if got["ComponentUpdatesEnabled"] != false || got["NewTabPageLocation"] != "about:blank" {
		t.Fatalf("policies = %v", got)
	}

	// The same policies leave the file alone; another content is replaced.
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if err := writeBrowserPolicies(path); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(path); !st.ModTime().Equal(old) {
		t.Fatal("unchanged policies were rewritten")
	}
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeBrowserPolicies(path); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(path); string(again) != string(data) {
		t.Fatalf("stale policies kept: %s", again)
	}
}
