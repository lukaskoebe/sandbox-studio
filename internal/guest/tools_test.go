//go:build linux

package guest

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// A rebase that keeps tools moves the toolchains, aliases and plugins the new template lacks
// into its /opt/mise and leaves its own alone. The old global pins the new config.toml doesn't
// have go to their own file; the template's pins win. Caches and shims stay behind.
func TestToolsMergeIntoTheNewTemplate(t *testing.T) {
	miseBinary = filepath.Join(t.TempDir(), "no-mise")
	old, cur := t.TempDir(), t.TempDir()
	writeFiles(t, old, map[string]string{
		"installs/node/20.11.0/bin/node":       "old node 20",
		"installs/node/22.1.0/bin/node":        "old node 22",
		"installs/python/3.12.1/bin/py":        "python",
		"plugins/foo/bin/install":              "plugin",
		"config/config.toml":                   "[tools]\nnode = \"20\"\npython = \"3.12\"\n\n[settings]\nexperimental = true\n",
		"config/conf.d/studio-kept-tools.toml": "[tools]\n\"npm:prettier\" = \"3\"\nnode = \"18\"\n",
		"cache/huge":                           "cache",
		"shims/node":                           "shim",
	})
	if err := os.Symlink("20.11.0", filepath.Join(old, "installs/node/20")); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, cur, map[string]string{
		"installs/node/22.1.0/bin/node": "template node 22",
		"config/config.toml":            "[tools]\nnode = \"22\"\n",
	})

	var archive, warn bytes.Buffer
	if err := ExportTools(old, &archive, &warn, Zstd, nil); err != nil {
		t.Fatal(err)
	}
	if err := ImportTools(cur, &archive, &warn, nil); err != nil {
		t.Fatal(err)
	}
	if warn.Len() > 0 {
		t.Errorf("warned: %s", warn.String())
	}
	for name, want := range map[string]string{
		"installs/node/20.11.0/bin/node": "old node 20",
		"installs/node/22.1.0/bin/node":  "template node 22",
		"installs/python/3.12.1/bin/py":  "python",
		"plugins/foo/bin/install":        "plugin",
		"config/config.toml":             "[tools]\nnode = \"22\"\n",
	} {
		if got, err := os.ReadFile(filepath.Join(cur, name)); err != nil || string(got) != want {
			t.Errorf("%s: %q %v", name, got, err)
		}
	}
	if target, err := os.Readlink(filepath.Join(cur, "installs/node/20")); err != nil || target != "20.11.0" {
		t.Errorf("alias: %q %v", target, err)
	}
	for _, gone := range []string{"cache", "shims", toolsStaging} {
		if _, err := os.Lstat(filepath.Join(cur, gone)); !os.IsNotExist(err) {
			t.Errorf("%s came along: %v", gone, err)
		}
	}
	kept, err := os.ReadFile(filepath.Join(cur, keptToolsFile))
	if err != nil {
		t.Fatal(err)
	}
	pins, err := toolPins(filepath.Join(cur, "config"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pins) != 3 || pins["node"] != "22" || pins["python"] != "3.12" || pins["npm:prettier"] != "3" {
		t.Errorf("pins %v from\n%s", pins, kept)
	}
	if strings.Contains(string(kept), "node") {
		t.Errorf("the template's pin was overridden:\n%s", kept)
	}
}

// A sandbox from before /opt/mise exports an empty tree, and an unreadable old config keeps
// the toolchains with a warning.
func TestToolsWithoutConfig(t *testing.T) {
	miseBinary = filepath.Join(t.TempDir(), "no-mise")
	var archive, warn bytes.Buffer
	if err := ExportTools(filepath.Join(t.TempDir(), "missing"), &archive, &warn, Zstd, nil); err != nil {
		t.Fatal(err)
	}
	cur := t.TempDir()
	if err := ImportTools(cur, &archive, &warn, nil); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(cur); len(entries) != 0 {
		t.Errorf("the empty tree left %v", entries)
	}

	old := t.TempDir()
	writeFiles(t, old, map[string]string{
		"installs/go/1.23.0/bin/go": "go",
		"config/config.toml":        "[tools\nbroken",
	})
	archive.Reset()
	if err := ExportTools(old, &archive, &warn, Zstd, nil); err != nil {
		t.Fatal(err)
	}
	if err := ImportTools(cur, &archive, &warn, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cur, "installs/go/1.23.0/bin/go")); err != nil {
		t.Error(err)
	}
	if !strings.Contains(warn.String(), "not their pins") {
		t.Errorf("warn %q", warn.String())
	}
}

// What the new VM has already, it says, and the old VM's export leaves it out: the import
// would keep the new VM's anyway, so the merge comes out the same.
func TestToolsExportLeavesOutWhatTheTargetHas(t *testing.T) {
	miseBinary = filepath.Join(t.TempDir(), "no-mise")
	old, cur := t.TempDir(), t.TempDir()
	writeFiles(t, old, map[string]string{
		"installs/node/20.11.0/bin/node": "old node 20",
		"installs/node/22.1.0/bin/node":  strings.Repeat("old node 22", 1000),
		"plugins/foo/bin/install":        "old plugin",
		"plugins/bar/bin/install":        "bar plugin",
	})
	writeFiles(t, cur, map[string]string{
		"installs/node/22.1.0/bin/node": "template node 22",
		"plugins/foo/bin/install":       "template plugin",
		"installs/stray":                "not a tool's directory",
	})
	if err := os.Symlink("22.1.0", filepath.Join(cur, "installs/node/22")); err != nil {
		t.Fatal(err)
	}
	have, err := ToolsHave(cur)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(have)
	if want := []string{"installs/node/22", "installs/node/22.1.0", "plugins/foo"}; !slices.Equal(have, want) {
		t.Fatalf("have %q, want %q", have, want)
	}
	var archive, warn bytes.Buffer
	if err := ExportTools(old, &archive, &warn, Zstd, have); err != nil {
		t.Fatal(err)
	}
	var names []string
	zr, err := decompressor(bytes.NewReader(archive.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		names = append(names, h.Name)
	}
	for _, name := range names {
		if strings.HasPrefix(name, "installs/node/22.1.0") || strings.HasPrefix(name, "plugins/foo") {
			t.Errorf("the archive has %s, which the target has", name)
		}
	}
	if !slices.Contains(names, "installs/node/20.11.0/bin/node") || !slices.Contains(names, "plugins/bar/bin/install") {
		t.Errorf("the archive lacks what the target lacks: %q", names)
	}
	if err := ImportTools(cur, &archive, &warn, nil); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"installs/node/20.11.0/bin/node": "old node 20",
		"installs/node/22.1.0/bin/node":  "template node 22",
		"plugins/foo/bin/install":        "template plugin",
		"plugins/bar/bin/install":        "bar plugin",
	} {
		if got, err := os.ReadFile(filepath.Join(cur, name)); err != nil || string(got) != want {
			t.Errorf("%s: %q %v", name, got, err)
		}
	}
	// A target without /opt/mise's dirs has nothing.
	if have, err := ToolsHave(t.TempDir()); err != nil || len(have) != 0 {
		t.Errorf("an empty dir has %q, %v", have, err)
	}
}
