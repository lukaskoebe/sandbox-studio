//go:build linux

package guest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestCreatedBundle(t *testing.T) {
	for _, tc := range []struct {
		args   []string
		bundle string
		ok     bool
	}{
		{[]string{"--root", "/run/runc", "--log-format", "json", "create", "--bundle", "/b", "--pid-file", "p", "id"}, "/b", true},
		{[]string{"create", "--bundle=/b", "id"}, "/b", true},
		{[]string{"run", "-b", "/b", "id"}, "/b", true},
		{[]string{"--root", "/run/runc", "start", "id"}, "", false},
		{[]string{"features"}, "", false},
	} {
		bundle, ok := createdBundle(tc.args)
		if ok != tc.ok || (tc.ok && bundle != tc.bundle) {
			t.Errorf("%v: got %q %v", tc.args, bundle, ok)
		}
	}
}

func TestAddCA(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	spec := `{"ociVersion":"1.2.0","process":{"args":["sh"],"env":["PATH=/bin","NODE_EXTRA_CA_CERTS=/mine.pem"],"cwd":"/"},` +
		`"mounts":[{"destination":"/proc","type":"proc","source":"proc"}],"linux":{"namespaces":[{"type":"pid"}]}}`
	if err := os.WriteFile(path, []byte(spec), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := addCA(path); err != nil {
		t.Fatal(err)
	}
	// A second pass (runc run after a failed create, say) changes nothing.
	if err := addCA(path); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	var got struct {
		OCIVersion string `json:"ociVersion"`
		Process    struct {
			Args []string `json:"args"`
			Env  []string `json:"env"`
			Cwd  string   `json:"cwd"`
		} `json:"process"`
		Mounts []struct {
			Destination string   `json:"destination"`
			Source      string   `json:"source"`
			Options     []string `json:"options"`
		} `json:"mounts"`
		Linux map[string]any `json:"linux"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.OCIVersion != "1.2.0" || got.Process.Cwd != "/" || len(got.Process.Args) != 1 || got.Linux["namespaces"] == nil {
		t.Errorf("spec fields lost: %s", raw)
	}
	if len(got.Mounts) != 2 || got.Mounts[1].Destination != ContainerCADir || got.Mounts[1].Source != ConfigDir ||
		!slices.Contains(got.Mounts[1].Options, "ro") {
		t.Errorf("mounts: %+v", got.Mounts)
	}
	want := []string{"PATH=/bin", "NODE_EXTRA_CA_CERTS=/mine.pem",
		"SSL_CERT_FILE=/dev/sandbox-studio/ca-bundle.crt", "CURL_CA_BUNDLE=/dev/sandbox-studio/ca-bundle.crt",
		"REQUESTS_CA_BUNDLE=/dev/sandbox-studio/ca-bundle.crt", "GIT_SSL_CAINFO=/dev/sandbox-studio/ca-bundle.crt"}
	if !slices.Equal(got.Process.Env, want) {
		t.Errorf("env = %q", got.Process.Env)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", info.Mode())
	}
}
