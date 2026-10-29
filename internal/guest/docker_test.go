//go:build linux

package guest

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// A Docker archive keeps what overlay layers are made of: whiteouts, which are character
// devices 0/0, FIFOs and extended attributes. A workspace import refuses it.
func TestDockerExportImportKeepsSpecialFiles(t *testing.T) {
	src := t.TempDir()
	layer := filepath.Join(src, "overlay2", "abc", "diff")
	must(t, os.MkdirAll(layer, 0o755))
	must(t, os.WriteFile(filepath.Join(layer, "bin"), []byte("elf"), 0o755))
	must(t, syscall.Mkfifo(filepath.Join(layer, "fifo"), 0o640))
	if err := unix.Setxattr(filepath.Join(layer, "bin"), "user.studio", []byte("file\x00value"), 0); errors.Is(err, unix.ENOTSUP) {
		t.Skip("no user xattrs on the test filesystem")
	} else {
		must(t, err)
	}
	must(t, unix.Setxattr(layer, "user.opaque", []byte("y"), 0))
	whiteout := unix.Mknod(filepath.Join(layer, "deleted"), unix.S_IFCHR|0o600, 0) == nil

	var archive bytes.Buffer
	must(t, ExportDocker(src, &archive, &bytes.Buffer{}, Zstd))
	if err := ImportWorkspace(t.TempDir(), bytes.NewReader(archive.Bytes())); err == nil {
		t.Fatal("a workspace import took a FIFO")
	}
	dst := t.TempDir()
	must(t, ImportDocker(dst, &archive))

	got := filepath.Join(dst, "overlay2", "abc", "diff")
	for path, want := range map[string]string{filepath.Join(got, "bin") + "\x00user.studio": "file\x00value", got + "\x00user.opaque": "y"} {
		p, name, _ := bytes.Cut([]byte(path), []byte{0})
		value := make([]byte, 64)
		n, err := unix.Getxattr(string(p), string(name), value)
		must(t, err)
		if string(value[:n]) != want {
			t.Errorf("%s %s = %q, want %q", p, name, value[:n], want)
		}
	}
	if st, err := os.Lstat(filepath.Join(got, "fifo")); err != nil || st.Mode().Type() != os.ModeNamedPipe || st.Mode().Perm() != 0o640 {
		t.Errorf("fifo = %v, %v", st, err)
	}
	if whiteout {
		var st unix.Stat_t
		must(t, unix.Lstat(filepath.Join(got, "deleted"), &st))
		if st.Mode&unix.S_IFMT != unix.S_IFCHR || st.Rdev != 0 {
			t.Errorf("whiteout mode %o, rdev %d", st.Mode, st.Rdev)
		}
	}
	if data, err := os.ReadFile(filepath.Join(got, "bin")); err != nil || string(data) != "elf" {
		t.Errorf("bin = %q, %v", data, err)
	}
}
