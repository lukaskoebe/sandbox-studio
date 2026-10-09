//go:build linux

package guest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestWorkspaceExportImportRoundTrip(t *testing.T) {
	src := t.TempDir()
	mtime := time.Date(2024, 5, 6, 7, 8, 9, 123456789, time.UTC)
	must(t, os.Mkdir(filepath.Join(src, "sub"), 0o750))
	must(t, os.Mkdir(filepath.Join(src, "empty"), 0o700))
	must(t, os.Mkdir(filepath.Join(src, "lost+found"), 0o700))
	must(t, os.WriteFile(filepath.Join(src, "lost+found", "x"), []byte("x"), 0o600))
	must(t, os.WriteFile(filepath.Join(src, "run.sh"), []byte("#!/bin/sh\necho hi\n"), 0o755))
	must(t, os.Chmod(filepath.Join(src, "run.sh"), 0o4755)) // the setuid bit is kept too
	must(t, os.WriteFile(filepath.Join(src, "sub", "data"), bytes.Repeat([]byte("data"), 50000), 0o640))
	must(t, os.Link(filepath.Join(src, "sub", "data"), filepath.Join(src, "hard")))
	must(t, os.Symlink("sub/data", filepath.Join(src, "rel")))
	must(t, os.Symlink("/etc/passwd", filepath.Join(src, "abs")))
	must(t, syscall.Mkfifo(filepath.Join(src, "fifo"), 0o600))
	must(t, os.Chtimes(filepath.Join(src, "sub", "data"), mtime, mtime))
	must(t, os.Chtimes(filepath.Join(src, "sub"), mtime, mtime))
	must(t, os.Chmod(filepath.Join(src, "sub"), 0o550)) // read-only directories still get their files

	var archive, warn bytes.Buffer
	must(t, ExportWorkspace(src, &archive, &warn))
	if !strings.Contains(warn.String(), "fifo") {
		t.Fatalf("missing FIFO warning: %q", warn.String())
	}

	dst := t.TempDir()
	must(t, os.Mkdir(filepath.Join(dst, "lost+found"), 0o700))
	must(t, ImportWorkspace(dst, &archive))
	t.Cleanup(func() { os.Chmod(filepath.Join(dst, "sub"), 0o700) })
	t.Cleanup(func() { os.Chmod(filepath.Join(src, "sub"), 0o700) })

	for _, name := range []string{"sub", "empty", "run.sh", "sub/data", "hard", "rel", "abs"} {
		a, err := os.Lstat(filepath.Join(src, name))
		must(t, err)
		b, err := os.Lstat(filepath.Join(dst, name))
		must(t, err)
		if a.Mode() != b.Mode() {
			t.Errorf("%s: mode %v, want %v", name, b.Mode(), a.Mode())
		}
		if a.Mode().Type() != os.ModeSymlink && !a.ModTime().Equal(b.ModTime()) {
			t.Errorf("%s: mtime %v, want %v", name, b.ModTime(), a.ModTime())
		}
	}
	data, err := os.ReadFile(filepath.Join(dst, "sub", "data"))
	must(t, err)
	if !bytes.Equal(data, bytes.Repeat([]byte("data"), 50000)) {
		t.Error("file content differs")
	}
	a, _ := os.Stat(filepath.Join(dst, "sub", "data"))
	b, _ := os.Stat(filepath.Join(dst, "hard"))
	if !os.SameFile(a, b) {
		t.Error("hardlink was not kept")
	}
	if target, _ := os.Readlink(filepath.Join(dst, "abs")); target != "/etc/passwd" {
		t.Errorf("symlink target %q", target)
	}
	for _, name := range []string{"fifo", "lost+found/x"} {
		if _, err := os.Lstat(filepath.Join(dst, name)); !os.IsNotExist(err) {
			t.Errorf("%s was copied", name)
		}
	}
}

func TestWorkspaceImportRefusesNonEmptyWorkspace(t *testing.T) {
	var archive bytes.Buffer
	must(t, ExportWorkspace(t.TempDir(), &archive, &bytes.Buffer{}))
	dst := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dst, "existing"), nil, 0o600))
	if err := ImportWorkspace(dst, &archive); err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("err = %v", err)
	}
}

func TestWorkspaceImportRejectsMaliciousArchives(t *testing.T) {
	uid, gid := os.Getuid(), os.Getgid()
	dir := func(name string) *tar.Header {
		return &tar.Header{Name: name, Typeflag: tar.TypeDir, Mode: 0o755, Uid: uid, Gid: gid}
	}
	file := func(name string) *tar.Header {
		return &tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Uid: uid, Gid: gid, Size: 1}
	}
	link := func(name, target string, flag byte) *tar.Header {
		return &tar.Header{Name: name, Typeflag: flag, Linkname: target, Mode: 0o777, Uid: uid, Gid: gid}
	}
	cases := map[string][]*tar.Header{
		"absolute":             {file("/escape")},
		"dot-dot":              {file("../escape")},
		"inner dot-dot":        {dir("a/"), file("a/../../escape")},
		"through symlink":      {link("l", "..", tar.TypeSymlink), file("l/escape")},
		"through abs symlink":  {link("l", "/", tar.TypeSymlink), dir("l/tmp/")},
		"missing parent":       {file("a/b")},
		"duplicate":            {file("a"), file("a")},
		"file becomes dir":     {file("a"), dir("a/")},
		"symlink becomes file": {link("a", "x", tar.TypeSymlink), file("a")},
		"hardlink outside":     {link("h", "../escape", tar.TypeLink)},
		"hardlink absolute":    {link("h", "/etc/passwd", tar.TypeLink)},
		"hardlink to symlink":  {link("l", "/etc/passwd", tar.TypeSymlink), link("h", "l", tar.TypeLink)},
		"hardlink to later":    {link("h", "a", tar.TypeLink), file("a")},
		"device":               {{Name: "dev", Typeflag: tar.TypeChar, Mode: 0o600, Uid: uid, Gid: gid}},
		"fifo":                 {{Name: "p", Typeflag: tar.TypeFifo, Mode: 0o600, Uid: uid, Gid: gid}},
		"lost+found":           {file("lost+found/x")},
		"root twice":           {dir("./"), dir("./")},
		"root as file":         {{Name: ".", Typeflag: tar.TypeReg, Mode: 0o600, Uid: uid, Gid: gid}},
		"dot component":        {dir("a/"), file("a/./b")},
		"empty symlink target": {link("l", "", tar.TypeSymlink)},
	}
	for name, headers := range cases {
		t.Run(name, func(t *testing.T) {
			parent := t.TempDir()
			dst := filepath.Join(parent, "workspace")
			must(t, os.Mkdir(dst, 0o755))
			if err := ImportWorkspace(dst, bytes.NewReader(archive(t, headers))); err == nil {
				t.Fatal("import succeeded")
			}
			if _, err := os.Lstat(filepath.Join(parent, "escape")); !os.IsNotExist(err) {
				t.Fatal("import wrote outside the workspace")
			}
		})
	}
}

func TestWorkspaceImportRejectsTruncatedArchive(t *testing.T) {
	src := t.TempDir()
	must(t, os.WriteFile(filepath.Join(src, "f"), bytes.Repeat([]byte{1, 2, 3}, 100000), 0o644))
	var full bytes.Buffer
	must(t, ExportWorkspace(src, &full, &bytes.Buffer{}))
	for _, n := range []int{full.Len() / 2, full.Len() - 4} {
		if err := ImportWorkspace(t.TempDir(), bytes.NewReader(full.Bytes()[:n])); err == nil {
			t.Fatalf("import of %d of %d bytes succeeded", n, full.Len())
		}
	}
}

func archive(t *testing.T, headers []*tar.Header) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, h := range headers {
		h.Format = tar.FormatPAX
		must(t, tw.WriteHeader(h))
		if h.Size > 0 {
			_, err := tw.Write(bytes.Repeat([]byte{'x'}, int(h.Size)))
			must(t, err)
		}
	}
	must(t, tw.Close())
	must(t, gz.Close())
	return buf.Bytes()
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
