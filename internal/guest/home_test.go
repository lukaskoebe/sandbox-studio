//go:build linux

package guest

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestSeedHomeCopiesSkeletonOnceAndMarks(t *testing.T) {
	skel := t.TempDir()
	must(t, os.Chmod(skel, 0o750))
	must(t, os.WriteFile(filepath.Join(skel, ".bashrc"), []byte("alias ll='ls -l'\n"), 0o644))
	must(t, os.MkdirAll(filepath.Join(skel, ".config", "tool"), 0o700))
	must(t, os.WriteFile(filepath.Join(skel, ".config", "tool", "conf"), []byte("x"), 0o600))
	must(t, os.Symlink("/etc/passwd", filepath.Join(skel, "link")))

	home := t.TempDir()
	must(t, os.Mkdir(filepath.Join(home, "lost+found"), 0o700))
	must(t, SeedHome(skel, home, &bytes.Buffer{}))

	if data, err := os.ReadFile(filepath.Join(home, ".bashrc")); err != nil || string(data) != "alias ll='ls -l'\n" {
		t.Fatalf(".bashrc = %q, %v", data, err)
	}
	if st, err := os.Stat(filepath.Join(home, ".config", "tool", "conf")); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("conf = %v, %v", st, err)
	}
	if target, err := os.Readlink(filepath.Join(home, "link")); err != nil || target != "/etc/passwd" {
		t.Fatalf("link = %q, %v", target, err)
	}
	if st, err := os.Stat(home); err != nil || st.Mode().Perm() != 0o750 {
		t.Fatalf("home mode = %v, %v", st.Mode(), err)
	}
	if _, err := os.Stat(filepath.Join(home, homeSeeded)); err != nil {
		t.Fatalf("no seed mark: %v", err)
	}

	// A seeded home is never seeded again: a deleted file stays deleted and an edit stays.
	must(t, os.Remove(filepath.Join(home, ".config", "tool", "conf")))
	must(t, os.WriteFile(filepath.Join(home, ".bashrc"), []byte("mine\n"), 0o644))
	must(t, SeedHome(skel, home, &bytes.Buffer{}))
	if _, err := os.Lstat(filepath.Join(home, ".config", "tool", "conf")); !os.IsNotExist(err) {
		t.Fatalf("deleted file came back: %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(home, ".bashrc")); string(data) != "mine\n" {
		t.Fatalf(".bashrc = %q", data)
	}
}

func TestSeedHomeNeverOverwritesAnUnmarkedHome(t *testing.T) {
	skel := t.TempDir()
	must(t, os.WriteFile(filepath.Join(skel, ".bashrc"), []byte("image\n"), 0o644))
	home := t.TempDir()
	must(t, os.WriteFile(filepath.Join(home, ".bashrc"), []byte("partial\n"), 0o644))

	must(t, SeedHome(skel, home, &bytes.Buffer{}))
	if data, _ := os.ReadFile(filepath.Join(home, ".bashrc")); string(data) != "partial\n" {
		t.Fatalf(".bashrc = %q", data)
	}
	if _, err := os.Stat(filepath.Join(home, homeSeeded)); err != nil {
		t.Fatalf("no seed mark: %v", err)
	}
}

func TestSeedHomeWithoutSkeletonMarks(t *testing.T) {
	home := t.TempDir()
	var warn bytes.Buffer
	must(t, SeedHome(filepath.Join(t.TempDir(), "missing"), home, &warn))
	if _, err := os.Stat(filepath.Join(home, homeSeeded)); err != nil || warn.Len() == 0 {
		t.Fatalf("mark %v, warning %q", err, warn.String())
	}
}

func TestHomeExportImportCarriesHomeAndDropsLegacyMise(t *testing.T) {
	src := t.TempDir() // not a mount point: a home on the root disk
	must(t, os.MkdirAll(filepath.Join(src, ".local", "share", "mise", "installs"), 0o755))
	must(t, os.WriteFile(filepath.Join(src, ".local", "share", "mise", "installs", "node"), []byte("bin"), 0o755))
	must(t, os.MkdirAll(filepath.Join(src, ".local", "share", "opencode"), 0o755))
	must(t, os.WriteFile(filepath.Join(src, ".local", "share", "opencode", "session.json"), []byte("{}"), 0o644))
	profile := "# ~/.profile\nexport EDITOR=vi\n\n" + legacyProfileStart + "\nexport MISE_DATA_DIR='/home/agent/.local/share/mise'\n\n" +
		legacyProfileEnd + "\nexport AFTER=1\n"
	must(t, os.WriteFile(filepath.Join(src, ".profile"), []byte(profile), 0o644))
	must(t, os.Symlink("/etc/shadow", filepath.Join(src, ".bash_profile")))

	var archive bytes.Buffer
	must(t, ExportHome(src, &archive, &bytes.Buffer{}))
	dst := t.TempDir()
	must(t, os.Mkdir(filepath.Join(dst, "lost+found"), 0o700))
	must(t, ImportHome(dst, &archive))

	if data, err := os.ReadFile(filepath.Join(dst, ".local", "share", "opencode", "session.json")); err != nil || string(data) != "{}" {
		t.Fatalf("session = %q, %v", data, err)
	}
	if _, err := os.Lstat(filepath.Join(dst, ".local", "share", "mise")); !os.IsNotExist(err) {
		t.Fatalf("legacy mise toolchains were copied: %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(dst, ".profile")); string(data) != "# ~/.profile\nexport EDITOR=vi\n\nexport AFTER=1\n" {
		t.Fatalf(".profile = %q", data)
	}
	if target, err := os.Readlink(filepath.Join(dst, ".bash_profile")); err != nil || target != "/etc/shadow" {
		t.Fatalf(".bash_profile = %q, %v", target, err)
	}
	if _, err := os.Stat(filepath.Join(dst, homeSeeded)); err != nil {
		t.Fatalf("an imported home is not marked: %v", err)
	}
	// A seeded home is never seeded over an import.
	skel := t.TempDir()
	must(t, os.WriteFile(filepath.Join(skel, ".bashrc"), []byte("image\n"), 0o644))
	must(t, SeedHome(skel, dst, &bytes.Buffer{}))
	if _, err := os.Lstat(filepath.Join(dst, ".bashrc")); !os.IsNotExist(err) {
		t.Fatalf("seeded over an imported home: %v", err)
	}
}

func TestImportHomeRefusesANonEmptyHome(t *testing.T) {
	var archive bytes.Buffer
	must(t, ExportHome(t.TempDir(), &archive, &bytes.Buffer{}))
	dst := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dst, ".bashrc"), nil, 0o644))
	if err := ImportHome(dst, &archive); err == nil {
		t.Fatal("imported into a home that holds files")
	}
}

func TestWithoutLegacyBlockNeedsBothEnds(t *testing.T) {
	for _, in := range []string{
		"export A=1\n",
		legacyProfileStart + "\nexport A=1\n",
		legacyProfileEnd + "\n" + legacyProfileStart + "\n",
	} {
		if out, ok := withoutLegacyBlock([]byte(in)); ok || string(out) != in {
			t.Errorf("%q: stripped to %q", in, out)
		}
	}
}
