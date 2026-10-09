//go:build linux

package upperlayer

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/ocilayer"
	"golang.org/x/sys/unix"
)

func writeTestLayer(t *testing.T, root string, limits ocilayer.Limits) ([]byte, error) {
	t.Helper()
	upper, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer upper.Close()
	var output bytes.Buffer
	err = Write(context.Background(), &output, upper, limits)
	return output.Bytes(), err
}

func archiveNames(t *testing.T, data []byte) map[string]tar.Header {
	t.Helper()
	result := make(map[string]tar.Header)
	reader := tar.NewReader(bytes.NewReader(data))
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return result
		}
		if err != nil {
			t.Fatal(err)
		}
		name := strings.TrimSuffix(header.Name, "/")
		result[name] = *header
	}
}

func requireNormalized(t *testing.T, data []byte) {
	t.Helper()
	if _, err := ocilayer.Normalize(context.Background(), io.Discard, bytes.NewReader(data), ocilayer.Limits{}); err != nil {
		t.Fatalf("generated layer is rejected by ocilayer.Normalize: %v", err)
	}
}

func TestWriteStreamsRegularSymlinkAndHardlink(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "etc", "app")
	if err := os.WriteFile(file, []byte("payload"), 0o751); err != nil {
		t.Fatal(err)
	}
	mtime := time.Date(2024, 2, 3, 4, 5, 6, 123456789, time.UTC)
	if err := os.Chtimes(file, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	var fileStat unix.Stat_t
	if err := unix.Stat(file, &fileStat); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(file, filepath.Join(root, "etc", "app-copy")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("app", filepath.Join(root, "etc", "current")); err != nil {
		t.Fatal(err)
	}

	data, err := writeTestLayer(t, root, ocilayer.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	requireNormalized(t, data)
	entries := archiveNames(t, data)
	if entries["etc"].Typeflag != tar.TypeDir || entries["etc/app"].Typeflag != tar.TypeReg {
		t.Fatalf("missing directory or regular file entries: %+v", entries)
	}
	if entries["etc/current"].Typeflag != tar.TypeSymlink || entries["etc/current"].Linkname != "app" {
		t.Fatalf("symlink was not preserved: %+v", entries["etc/current"])
	}
	if entries["etc/app-copy"].Typeflag != tar.TypeLink || entries["etc/app-copy"].Linkname != "etc/app" {
		t.Fatalf("hardlink was not preserved: %+v", entries["etc/app-copy"])
	}
	if entries["etc/app"].Mode != 0o751 {
		t.Fatalf("file mode was not preserved: %#o", entries["etc/app"].Mode)
	}
	if entries["etc/app"].Uid != int(fileStat.Uid) || entries["etc/app"].Gid != int(fileStat.Gid) {
		t.Fatalf("file owner was not preserved: uid=%d gid=%d", entries["etc/app"].Uid, entries["etc/app"].Gid)
	}
	if !entries["etc/app"].ModTime.Equal(time.Unix(fileStat.Mtim.Sec, fileStat.Mtim.Nsec).UTC()) {
		t.Fatalf("file mtime was not preserved: got %v, want %v", entries["etc/app"].ModTime, mtime)
	}
}

func TestWriteExcludesStaticRuntimePaths(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"workspace", "var/log", "var/lib/docker", "etc", "bin", "opt/studio", "tmp"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, data := range map[string]string{
		"workspace/secret":        "excluded",
		"workspace/.wh.removed":   "excluded whiteout-style name",
		"var/log/system.log":      "excluded",
		"var/lib/docker/metadata": "excluded",
		"etc/hostname":            "excluded",
		"etc/passwd":              "included",
		"bin/tool":                "included",
		"opt/studio/launcher":     "excluded",
		"tmp/.wh.issue.net":       "excluded whiteout-style name",
	} {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := unix.Setxattr(filepath.Join(root, "workspace", "secret"), "user.note", []byte("ignored"), 0); err != nil && !errors.Is(err, unix.ENOTSUP) && !errors.Is(err, unix.EOPNOTSUPP) {
		t.Fatalf("set test xattr on excluded path: %v", err)
	}

	data, err := writeTestLayer(t, root, ocilayer.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	requireNormalized(t, data)
	entries := archiveNames(t, data)
	for _, name := range []string{"workspace", "workspace/secret", "workspace/.wh.removed", "var/log", "var/log/system.log", "var/lib/docker", "var/lib/docker/metadata", "etc/hostname", "opt/studio", "opt/studio/launcher", "tmp", "tmp/.wh.issue.net"} {
		if _, ok := entries[name]; ok {
			t.Errorf("excluded path %q appeared in layer", name)
		}
	}
	for _, name := range []string{"etc/passwd", "bin/tool"} {
		if _, ok := entries[name]; !ok {
			t.Errorf("included path %q is missing", name)
		}
	}
	if _, ok := entries["var"]; !ok {
		t.Fatal("included ancestor directory var is missing")
	}
}

func TestWriteEnforcesLimits(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a"), []byte("four"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := writeTestLayer(t, root, ocilayer.Limits{MaxFileBytes: 3}); !errors.Is(err, ocilayer.ErrFileLimit) {
		t.Fatalf("MaxFileBytes: got %v", err)
	}
	if _, err := writeTestLayer(t, root, ocilayer.Limits{MaxInputBytes: 3, MaxFileBytes: 10}); !errors.Is(err, ocilayer.ErrInputLimit) {
		t.Fatalf("MaxInputBytes: got %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "b"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := writeTestLayer(t, root, ocilayer.Limits{MaxEntries: 1}); !errors.Is(err, ocilayer.ErrEntryLimit) {
		t.Fatalf("MaxEntries: got %v", err)
	}
	if _, err := writeTestLayer(t, root, ocilayer.Limits{MaxOutputBytes: 512}); !errors.Is(err, ocilayer.ErrOutputLimit) {
		t.Fatalf("MaxOutputBytes: got %v", err)
	}
	if _, err := writeTestLayer(t, root, ocilayer.Limits{MaxPathBytes: -1}); err == nil {
		t.Fatal("accepted negative limit")
	}
}

func TestWriteRejectsUnsupportedTypesAndAttributes(t *testing.T) {
	root := t.TempDir()
	if err := unix.Mkfifo(filepath.Join(root, "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := writeTestLayer(t, root, ocilayer.Limits{}); err == nil || !strings.Contains(err.Error(), "FIFO") {
		t.Fatalf("FIFO was not rejected explicitly: %v", err)
	}

	if _, _, err := inspectXattrs(map[string][]byte{"user.note": []byte("value")}, false); err == nil {
		t.Fatal("accepted unknown xattr")
	}
	if _, _, err := inspectXattrs(map[string][]byte{"user.overlay.opaque": []byte("y")}, true); err == nil {
		t.Fatal("translated an ordinary user.overlay xattr as an OverlayFS feature")
	}
	if _, _, err := inspectXattrs(map[string][]byte{"trusted.overlay.metacopy": []byte("y")}, false); err == nil {
		t.Fatal("accepted unsupported overlay feature")
	}

	root = t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".wh.old"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := writeTestLayer(t, root, ocilayer.Limits{}); err == nil || !strings.Contains(err.Error(), "reserved .wh.*") {
		t.Fatalf("reserved input filename was not rejected: %v", err)
	}
}

func TestSecurityCapabilityIsPreservedAsPAX(t *testing.T) {
	capability := []byte{1, 0, 0, 2, 0, 0, 0, 0}
	_, value, err := inspectXattrs(map[string][]byte{"security.capability": capability}, false)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	writer := tar.NewWriter(&output)
	if err := writer.WriteHeader(headerFor(&unix.Stat_t{Mode: 0o755}, "bin/helper", tar.TypeReg, 1, "", value)); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	requireNormalized(t, output.Bytes())
	reader := tar.NewReader(bytes.NewReader(output.Bytes()))
	header, err := reader.Next()
	if err != nil {
		t.Fatal(err)
	}
	if got := header.PAXRecords["SCHILY.xattr.security.capability"]; got != string(capability) {
		t.Fatalf("security.capability changed: got %q, want %q", got, capability)
	}
}

func TestWriteOpaqueTranslationAndRootRejection(t *testing.T) {
	features, _, err := inspectXattrs(map[string][]byte{"trusted.overlay.opaque": []byte("y")}, true)
	if err != nil || !features.opaque {
		t.Fatalf("opaque y was not recognized: features=%+v err=%v", features, err)
	}
	features, _, err = inspectXattrs(map[string][]byte{"trusted.overlay.opaque": []byte("x")}, true)
	if err != nil || features.opaque {
		t.Fatalf("opaque x was not ignored: features=%+v err=%v", features, err)
	}

	limits, err := withDefaults(ocilayer.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	writer := tar.NewWriter(&output)
	walker := &walker{
		ctx:      context.Background(),
		tar:      writer,
		limits:   limits,
		entries:  make(map[string]entryKind),
		symlinks: make(map[string]string),
	}
	directoryStat := &unix.Stat_t{Mode: unix.S_IFDIR | 0o755}
	if err := walker.emit(headerFor(directoryStat, "usr", tar.TypeDir, 0, "", nil)); err != nil {
		t.Fatal(err)
	}
	if err := walker.writeOpaqueMarker("usr", directoryStat); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	requireNormalized(t, output.Bytes())
	if entries := archiveNames(t, output.Bytes()); entries["usr/.wh..wh..opq"].Typeflag != tar.TypeReg {
		t.Fatalf("missing generated opaque marker: %+v", entries)
	}

	if err := validateRootAttributes(map[string][]byte{"trusted.overlay.opaque": []byte("y")}); err == nil || !strings.Contains(err.Error(), "root opaque directory is unsupported") {
		t.Fatalf("root opaque directory was not rejected explicitly: %v", err)
	}
}

func TestOverlayUUIDIsAcceptedOnlyOnRoot(t *testing.T) {
	const name = "trusted.overlay.uuid"
	uuid := make([]byte, 16)
	if !knownXattr(name) {
		t.Fatal("trusted.overlay.uuid is not accepted by bounded xattr reading")
	}
	attrs, err := readNamedAttrs([]byte(name+"\x00"), 64, func(got string, _ int64) ([]byte, error) {
		if got != name {
			t.Fatalf("requested xattr %q, want %q", got, name)
		}
		return uuid, nil
	})
	if err != nil {
		t.Fatalf("read root UUID xattr: %v", err)
	}
	if err := validateRootAttributes(attrs); err != nil {
		t.Fatalf("valid 16-byte root UUID was rejected: %v", err)
	}
	if err := validateRootAttributes(map[string][]byte{name: uuid[:15]}); err == nil {
		t.Fatal("accepted malformed root UUID length")
	}
	if _, _, err := inspectXattrs(map[string][]byte{name: uuid}, false); err == nil {
		t.Fatal("accepted trusted.overlay.uuid on a non-root entry")
	}
}

func TestWriteRejectsOpaqueAncestorOfExcludedPath(t *testing.T) {
	if !opaqueDeletesExcluded("var") || opaqueDeletesExcluded("tmp") || opaqueDeletesExcluded("var/log") {
		t.Fatal("opaque ancestor detection did not distinguish strict ancestors from excluded paths")
	}

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "var"), []byte("replacement"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := writeTestLayer(t, root, ocilayer.Limits{}); err == nil || !strings.Contains(err.Error(), "non-directory ancestor") {
		t.Fatalf("non-directory ancestor of an excluded subtree was not rejected: %v", err)
	}
}

func TestWhiteoutTranslations(t *testing.T) {
	for input, want := range map[string]string{
		"old":                     ".wh.old",
		"etc/hosts":               "etc/.wh.hosts",
		"var/lib/dbus/machine-id": "var/lib/dbus/.wh.machine-id",
	} {
		if got := whiteoutTarName(input); got != want {
			t.Errorf("whiteoutTarName(%q) = %q, want %q", input, got, want)
		}
	}
	features, _, err := inspectXattrs(map[string][]byte{"trusted.overlay.whiteout": []byte{}}, false)
	if err != nil || !features.whiteout {
		t.Fatalf("regular-file overlay whiteout was not recognized: features=%+v err=%v", features, err)
	}
	if _, _, err := inspectXattrs(map[string][]byte{"trusted.overlay.whiteout": []byte("unexpected")}, false); err == nil {
		t.Fatal("accepted non-empty overlay whiteout attribute")
	}
	if !excludedWhiteout("var") || excludedWhiteout("tmp") || excludedWhiteout("var/lib/docker") || excludedWhiteout("usr/bin/tool") {
		t.Fatal("strict excluded ancestor whiteout detection is incorrect")
	}

	limits, err := withDefaults(ocilayer.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	writer := tar.NewWriter(&output)
	walker := &walker{
		ctx:      context.Background(),
		tar:      writer,
		limits:   limits,
		entries:  make(map[string]entryKind),
		symlinks: make(map[string]string),
	}
	whiteoutStat := &unix.Stat_t{Mode: unix.S_IFCHR | 0o600}
	if err := walker.emit(headerFor(&unix.Stat_t{Mode: unix.S_IFDIR | 0o755}, "etc", tar.TypeDir, 0, "", nil)); err != nil {
		t.Fatal(err)
	}
	if err := walker.writeWhiteoutHeader("etc/issue.net", whiteoutStat, nil); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	requireNormalized(t, output.Bytes())
	entries := archiveNames(t, output.Bytes())
	if got := entries["etc/.wh.issue.net"]; got.Typeflag != tar.TypeReg || got.Size != 0 {
		t.Fatalf("nested character whiteout did not become a regular marker: %+v", got)
	}
}
