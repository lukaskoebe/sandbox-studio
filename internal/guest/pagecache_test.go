//go:build linux

package guest

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// cachedPages is how many pages of the file at p are in the page cache.
func cachedPages(t *testing.T, p string) uint64 {
	t.Helper()
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var st unix.Cachestat_t
	if err := unix.Cachestat(uint(f.Fd()), &unix.CachestatRange{}, &st, 0); err != nil {
		t.Skipf("cachestat: %v", err)
	}
	return st.Cache
}

// evict writes the file at p to the disk and drops it from the cache.
func evict(t *testing.T, p string) {
	t.Helper()
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	must(t, f.Sync())
	must(t, unix.Fadvise(int(f.Fd()), 0, 0, unix.FADV_DONTNEED))
}

func TestCopiesLeaveTheirFilesOutOfTheCache(t *testing.T) {
	src := t.TempDir()
	var fs unix.Statfs_t
	must(t, unix.Statfs(src, &fs))
	if fs.Type == unix.TMPFS_MAGIC {
		t.Skip("a tmpfs keeps its files in the cache; set TMPDIR to a disk's directory")
	}
	// A file of a few chunks, and more small files than are dropped at once.
	files := map[string]int{"big": 3*cacheChunk + 12345, "hot": 1 << 20}
	for i := range maxPending + 20 {
		files[fmt.Sprintf("small/%03d", i)] = 8 << 10
	}
	must(t, os.Mkdir(filepath.Join(src, "small"), 0o755))
	for name, size := range files {
		p := filepath.Join(src, name)
		must(t, os.WriteFile(p, bytes.Repeat([]byte{byte(len(name))}, size), 0o644))
		evict(t, p)
		if cachedPages(t, p) != 0 {
			t.Skip("this file system keeps files in the cache")
		}
	}
	// A file the sandbox has read keeps its pages.
	if _, err := os.ReadFile(filepath.Join(src, "hot")); err != nil {
		t.Fatal(err)
	}

	var archive bytes.Buffer
	must(t, ExportWorkspaceAs(src, &archive, &bytes.Buffer{}, Zstd))
	for name := range files {
		if n := cachedPages(t, filepath.Join(src, name)); (n > 0) != (name == "hot") {
			t.Errorf("export: %s has %d pages in the cache", name, n)
		}
	}

	dst := t.TempDir()
	must(t, ImportWorkspace(dst, &archive, nil))
	for name, size := range files {
		p := filepath.Join(dst, name)
		if st, err := os.Stat(p); err != nil || st.Size() != int64(size) {
			t.Fatalf("import: %s: %v, %v", name, st, err)
		}
		if n := cachedPages(t, p); n != 0 {
			t.Errorf("import: %s has %d pages in the cache", name, n)
		}
	}
}
