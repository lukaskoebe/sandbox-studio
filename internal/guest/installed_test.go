//go:build linux

package guest

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

func TestParseMiseList(t *testing.T) {
	// As mise 2026.10 prints it: a tool the global config pins, an older version nothing pins,
	// one a rebase kept, and one a config asks for that isn't installed.
	out := `{
  "node": [
    {"version": "24.11.0", "requested_version": "24", "install_path": "/opt/mise/installs/node/24.11.0",
     "source": {"type": "mise.toml", "path": "/opt/mise/config/config.toml"}, "installed": true, "active": true},
    {"version": "22.1.0", "install_path": "/opt/mise/installs/node/22.1.0", "installed": true, "active": false}
  ],
  "npm:prettier": [
    {"version": "3.6.2", "requested_version": "latest",
     "source": {"type": "mise.toml", "path": "/opt/mise/config/conf.d/studio-kept-tools.toml"}, "installed": true, "active": true}
  ],
  "go": [
    {"version": "1.25.4", "requested_version": "1.25", "source": {"type": "mise.toml", "path": "/opt/mise/config/config.toml"},
     "installed": false, "active": false}
  ],
  "python": [{"version": "3.12.1", "install_path": "/opt/mise/installs/python/3.12.1"}]
}`
	got, err := parseMiseList([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	want := []agentproto.MiseTool{
		{Name: "go", Version: "1.25.4", Requested: "1.25", Config: "/opt/mise/config/config.toml"},
		{Name: "node", Version: "22.1.0", Installed: true},
		{Name: "node", Version: "24.11.0", Requested: "24", Config: "/opt/mise/config/config.toml", Installed: true, Active: true},
		{Name: "npm:prettier", Version: "3.6.2", Requested: "latest", Config: "/opt/mise/config/conf.d/studio-kept-tools.toml", Installed: true, Active: true},
		// An older mise has no "installed": what it lists is installed.
		{Name: "python", Version: "3.12.1", Installed: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}

	if got, err := parseMiseList([]byte("{}")); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("no tools: %v, %v", got, err)
	}
	if _, err := parseMiseList([]byte("mise WARN something")); err == nil {
		t.Fatal("text that isn't JSON was read")
	}
}

func writePackage(t *testing.T, modules, pkg, version string) {
	t.Helper()
	dir := filepath.Join(modules, filepath.FromSlash(pkg))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"`+pkg+`","version":"`+version+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHarnessVersions(t *testing.T) {
	modules := t.TempDir()
	writePackage(t, modules, "@anthropic-ai/claude-code", "2.1.325")
	writePackage(t, modules, "opencode-ai", "1.18.35")
	got := harnessVersions(modules)
	want := []agentproto.HarnessVersion{{Harness: "claude", Version: "2.1.325"}, {Harness: "opencode", Version: "1.18.35"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v (codex isn't installed)", got, want)
	}
	if got := harnessVersions(filepath.Join(modules, "none")); got == nil || len(got) != 0 {
		t.Fatalf("no modules: %+v", got)
	}
}

func TestHarnessSpecTakesAnExactVersionOfClaude(t *testing.T) {
	if spec, err := harnessSpec(agentproto.HarnessUpdate{Harness: "claude", Version: "2.1.340"}); err != nil || spec != "@anthropic-ai/claude-code@2.1.340" {
		t.Fatalf("got %q, %v", spec, err)
	}
	for _, req := range []agentproto.HarnessUpdate{
		{Harness: "codex", Version: "0.170.0"}, // its adapter is pinned to it
		{Harness: "left-pad", Version: "1.0.0"},
		{Harness: "claude", Version: "latest"},
		{Harness: "claude", Version: "^2.1.0"},
		{Harness: "claude", Version: "2.1.340 --prefix=/tmp"},
		{Harness: "claude", Version: "file:/tmp/x"},
		{Harness: "claude", Version: ""},
	} {
		if spec, err := harnessSpec(req); err == nil {
			t.Errorf("%+v: got %q", req, spec)
		}
	}
}

func TestNPMInstallInstallsTheSpecAndSaysWhyItFailed(t *testing.T) {
	dir := t.TempDir()
	modules := filepath.Join(dir, "lib", "node_modules")
	old := npmBinary
	t.Cleanup(func() { npmBinary = old })
	npmBinary = filepath.Join(dir, "npm")
	// A fake npm: installs the version it is given, or fails as npm does without a network.
	script := `#!/bin/sh
spec=""
for a in "$@"; do spec="$a"; done
case "$spec" in
*@9.9.9) echo "npm error code ECONNRESET" >&2; echo "npm error network aborted" >&2; exit 1 ;;
esac
[ -n "$npm_config_cache" ] && [ -d "$npm_config_cache" ] || { echo "no cache dir" >&2; exit 2; }
mkdir -p "` + modules + `/@anthropic-ai/claude-code"
printf '{"version":"%s"}' "${spec##*@}" > "` + modules + `/@anthropic-ai/claude-code/package.json"
`
	if err := os.WriteFile(npmBinary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := npmInstall(context.Background(), "@anthropic-ai/claude-code@2.1.340"); err != nil {
		t.Fatal(err)
	}
	if v := packageVersion(modules, "@anthropic-ai/claude-code"); v != "2.1.340" {
		t.Fatalf("installed %q", v)
	}
	err := npmInstall(context.Background(), "@anthropic-ai/claude-code@9.9.9")
	if err == nil || !strings.Contains(err.Error(), "network aborted") {
		t.Fatalf("a failed install says %v", err)
	}
}
