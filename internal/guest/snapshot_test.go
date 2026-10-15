//go:build linux

package guest

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

func newSnapshots(t *testing.T) (*snapshots, string) {
	t.Helper()
	dir := t.TempDir()
	ws := filepath.Join(dir, "workspace")
	must(t, os.Mkdir(ws, 0o755))
	return &snapshots{
		workspace: ws,
		store:     filepath.Join(dir, "home", snapshotDir),
		owner:     [2]int{os.Getuid(), os.Getgid()},
		list: func(d string) ([]string, error) {
			cmd := exec.Command("git", "-c", "core.fsmonitor=false", "ls-files", "-co", "--exclude-standard", "-z")
			cmd.Dir = d
			cmd.Env = []string{"HOME=" + dir, "GIT_CONFIG_NOSYSTEM=1", "PATH=" + os.Getenv("PATH")}
			return gitOutput(cmd)
		},
		cap:       agentproto.MaxSnapshotBytes,
		maxFile:   agentproto.MaxSnapshotFile,
		maxListed: agentproto.MaxChangesListed,
	}, ws
}

func put(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(root, name)
		must(t, os.MkdirAll(filepath.Dir(p), 0o755))
		must(t, os.WriteFile(p, []byte(content), 0o644))
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init", "-q", dir)
	cmd.Env = []string{"HOME=" + t.TempDir(), "GIT_CONFIG_NOSYSTEM=1", "PATH=" + os.Getenv("PATH")}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
}

func take(t *testing.T, s *snapshots, req agentproto.SnapshotRequest) agentproto.SnapshotReply {
	t.Helper()
	if req.Op == "" {
		req.Op = agentproto.SnapshotTake
	}
	r, err := s.serve(req)
	if err != nil {
		t.Fatalf("%s: %v", req.Op, err)
	}
	return r
}

func TestSnapshotRestore(t *testing.T) {
	s, ws := newSnapshots(t)
	gitInit(t, ws)
	put(t, ws, map[string]string{
		".gitignore":   "build/\n*.log\n",
		"a.txt":        "one\ntwo\nthree\n",
		"src/b.go":     "package b\n",
		"gone.txt":     "bye\n",
		"build/out":    "artifact\n",
		"debug.log":    "log\n",
		"keep/old.txt": "old\n",
	})
	must(t, os.Symlink("a.txt", filepath.Join(ws, "link")))
	before := take(t, s, agentproto.SnapshotRequest{ID: "t1"})
	if before.Files == 0 {
		t.Fatalf("empty snapshot %+v", before)
	}

	// The turn edits, deletes, creates, and touches ignored files and .git.
	put(t, ws, map[string]string{
		"a.txt":       "one\n2\nthree\nfour\n",
		"new/c.txt":   "new\n",
		"build/out":   "rebuilt\n",
		"build/more":  "more\n",
		"debug.log":   "more log\n",
		".git/marker": "x",
	})
	must(t, os.Remove(filepath.Join(ws, "gone.txt")))
	must(t, os.RemoveAll(filepath.Join(ws, "keep")))
	must(t, os.Remove(filepath.Join(ws, "link")))
	must(t, os.Symlink("/etc/passwd", filepath.Join(ws, "link")))
	must(t, os.Chmod(filepath.Join(ws, "src/b.go"), 0o755))

	after := take(t, s, agentproto.SnapshotRequest{ID: "t1-after", Base: "t1"})
	got := map[string]agentproto.FileChange{}
	for _, c := range after.Changes {
		got[c.Path] = c
	}
	if len(got) != 6 || got["a.txt"].Status != "modified" || got["a.txt"].Added != 2 || got["a.txt"].Removed != 1 ||
		got["new/c.txt"].Status != "added" || got["gone.txt"].Status != "deleted" || got["keep/old.txt"].Status != "deleted" ||
		got["link"].Status != "modified" || got["src/b.go"].Status != "modified" || got["a.txt"].Diff != "" {
		t.Fatalf("changes %+v", after.Changes)
	}
	ch := take(t, s, agentproto.SnapshotRequest{Op: agentproto.SnapshotChanges, ID: "t1-after", Base: "t1", Path: "a.txt"})
	if len(ch.Changes) != 1 || !strings.Contains(ch.Changes[0].Diff, "-two\n+2\n") || !strings.HasPrefix(ch.Changes[0].Diff, "--- a/a.txt\n") {
		t.Fatalf("diff %+v", ch.Changes)
	}

	r := take(t, s, agentproto.SnapshotRequest{Op: agentproto.SnapshotRestore, ID: "t1"})
	if len(r.Failed) != 0 {
		t.Fatalf("restore %+v", r)
	}
	if read(t, filepath.Join(ws, "a.txt")) != "one\ntwo\nthree\n" || read(t, filepath.Join(ws, "gone.txt")) != "bye\n" || read(t, filepath.Join(ws, "keep/old.txt")) != "old\n" {
		t.Fatal("files not restored")
	}
	if _, err := os.Lstat(filepath.Join(ws, "new")); !os.IsNotExist(err) {
		t.Fatalf("created directory kept: %v", err)
	}
	if target, _ := os.Readlink(filepath.Join(ws, "link")); target != "a.txt" {
		t.Fatalf("link %q", target)
	}
	if info, _ := os.Stat(filepath.Join(ws, "src/b.go")); info.Mode().Perm() != 0o644 {
		t.Fatalf("mode %v", info.Mode())
	}
	// Ignored files and .git are as the turn left them.
	if read(t, filepath.Join(ws, "build/out")) != "rebuilt\n" || read(t, filepath.Join(ws, "build/more")) != "more\n" ||
		read(t, filepath.Join(ws, "debug.log")) != "more log\n" || read(t, filepath.Join(ws, ".git/marker")) != "x" {
		t.Fatal("ignored files or .git touched")
	}
	// A second restore has nothing to do.
	if r := take(t, s, agentproto.SnapshotRequest{Op: agentproto.SnapshotRestore, ID: "t1"}); r.Restored != 0 || r.Removed != 0 {
		t.Fatalf("restore again %+v", r)
	}
}

func TestSnapshotIgnoreRuleChanged(t *testing.T) {
	s, ws := newSnapshots(t)
	gitInit(t, ws)
	put(t, ws, map[string]string{".gitignore": "secret.txt\n", "secret.txt": "keep me\n"})
	take(t, s, agentproto.SnapshotRequest{ID: "t1"})
	put(t, ws, map[string]string{".gitignore": ""})
	take(t, s, agentproto.SnapshotRequest{Op: agentproto.SnapshotRestore, ID: "t1"})
	if read(t, filepath.Join(ws, "secret.txt")) != "keep me\n" || read(t, filepath.Join(ws, ".gitignore")) != "secret.txt\n" {
		t.Fatal("an ignored file was removed")
	}
}

func TestSnapshotSymlinks(t *testing.T) {
	s, ws := newSnapshots(t)
	outside := t.TempDir()
	put(t, outside, map[string]string{"target.txt": "outside\n"})
	put(t, ws, map[string]string{"dir/f.txt": "inside\n"})
	must(t, os.Symlink(filepath.Join(outside, "target.txt"), filepath.Join(ws, "out.txt")))
	must(t, os.Symlink(outside, filepath.Join(ws, "outdir")))
	take(t, s, agentproto.SnapshotRequest{ID: "t1"})
	m := mustManifest(t, s, "t1")
	for _, e := range m.Entries {
		if strings.HasPrefix(e.Path, "outdir/") || (e.Path == "out.txt" && e.Type != 'l') {
			t.Fatalf("followed a symlink: %+v", e)
		}
	}

	// The turn replaces dir with a symlink to outside: restoring must not write through it.
	must(t, os.RemoveAll(filepath.Join(ws, "dir")))
	must(t, os.Symlink(outside, filepath.Join(ws, "dir")))
	// And a file with a symlink to an outside file.
	must(t, os.Remove(filepath.Join(ws, "out.txt")))
	put(t, ws, map[string]string{"out.txt": "replaced\n"})
	r := take(t, s, agentproto.SnapshotRequest{Op: agentproto.SnapshotRestore, ID: "t1"})
	if len(r.Failed) != 0 {
		t.Fatalf("restore %+v", r)
	}
	if _, err := os.Stat(filepath.Join(outside, "f.txt")); !os.IsNotExist(err) {
		t.Fatalf("wrote through a symlink: %v", err)
	}
	if read(t, filepath.Join(outside, "target.txt")) != "outside\n" {
		t.Fatal("outside file changed")
	}
	if info, err := os.Lstat(filepath.Join(ws, "dir")); err != nil || !info.IsDir() || read(t, filepath.Join(ws, "dir/f.txt")) != "inside\n" {
		t.Fatalf("dir not restored: %v", err)
	}
	if target, _ := os.Readlink(filepath.Join(ws, "out.txt")); target != filepath.Join(outside, "target.txt") {
		t.Fatalf("link %q", target)
	}

	// A symlinked workspace root is refused.
	link := filepath.Join(t.TempDir(), "ws")
	must(t, os.Symlink(outside, link))
	s.workspace = link
	if _, err := s.serve(agentproto.SnapshotRequest{Op: agentproto.SnapshotTake, ID: "t2"}); err == nil {
		t.Fatal("snapshotted through a symlinked workspace")
	}
}

func mustManifest(t *testing.T, s *snapshots, id string) *manifest {
	t.Helper()
	st, err := s.openStore()
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	m, err := st.readManifest(id)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func blobCount(t *testing.T, s *snapshots) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(filepath.Join(s.store, "blobs"), func(_ string, d os.DirEntry, _ error) error {
		if d != nil && !d.IsDir() {
			n++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSnapshotCapsAndGC(t *testing.T) {
	s, ws := newSnapshots(t)
	s.maxFile = 10
	s.maxListed = 2
	put(t, ws, map[string]string{"small": "1", "big.bin": strings.Repeat("x", 20)})
	take(t, s, agentproto.SnapshotRequest{ID: "a"})
	for _, e := range mustManifest(t, s, "a").Entries {
		if e.Path == "big.bin" && e.Hash != "" {
			t.Fatal("kept a file over the size limit")
		}
	}
	// A big file the turn changes is neither restored nor removed.
	put(t, ws, map[string]string{"small": "2", "big.bin": "y", "huge.bin": strings.Repeat("z", 20), "c": "3", "d": "4"})
	r := take(t, s, agentproto.SnapshotRequest{ID: "b", Base: "a"})
	if len(r.Changes) != 2 || r.More != 3 || !r.Changes[0].Binary {
		t.Fatalf("listed %+v more %d", r.Changes, r.More)
	}
	take(t, s, agentproto.SnapshotRequest{Op: agentproto.SnapshotRestore, ID: "a"})
	if read(t, filepath.Join(ws, "small")) != "1" || read(t, filepath.Join(ws, "huge.bin")) != strings.Repeat("z", 20) {
		t.Fatal("restore")
	}
	if _, err := os.Stat(filepath.Join(ws, "c")); !os.IsNotExist(err) {
		t.Fatal("new file kept")
	}

	// GC keeps what is asked for, and the blobs they use.
	if n := blobCount(t, s); n != 5 {
		t.Fatalf("blobs %d", n)
	}
	take(t, s, agentproto.SnapshotRequest{Op: agentproto.SnapshotGC, Keep: []string{"a"}})
	if n := blobCount(t, s); n != 1 {
		t.Fatalf("blobs after gc %d", n)
	}
	if _, err := s.serve(agentproto.SnapshotRequest{Op: agentproto.SnapshotRestore, ID: "b"}); err == nil {
		t.Fatal("restored a collected snapshot")
	}

	// Over the store's cap the oldest snapshots go.
	s.cap = 3
	put(t, ws, map[string]string{"small": "22", "c": "33"})
	r = take(t, s, agentproto.SnapshotRequest{ID: "c"})
	if !slices.Equal(r.Dropped, []string{"a"}) {
		t.Fatalf("dropped %v", r.Dropped)
	}
	if _, err := s.serve(agentproto.SnapshotRequest{Op: agentproto.SnapshotRestore, ID: "c"}); err != nil {
		t.Fatalf("the new snapshot went: %v", err)
	}

	for _, bad := range []agentproto.SnapshotRequest{
		{Op: agentproto.SnapshotTake, ID: "../x"},
		{Op: agentproto.SnapshotChanges, ID: "c", Base: "a", Path: "../etc"},
		{Op: "nope", ID: "c"},
	} {
		if _, err := s.serve(bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}

func TestSnapshotStoreNotOurs(t *testing.T) {
	s, ws := newSnapshots(t)
	put(t, ws, map[string]string{"a": "1"})
	must(t, os.MkdirAll(s.store, 0o755))
	if _, err := s.serve(agentproto.SnapshotRequest{Op: agentproto.SnapshotTake, ID: "a"}); err == nil || !strings.Contains(err.Error(), "not Studio's") {
		t.Fatalf("used an open store: %v", err)
	}
	// A symlinked store is refused too.
	must(t, os.Remove(s.store))
	must(t, os.Symlink(t.TempDir(), s.store))
	if _, err := s.serve(agentproto.SnapshotRequest{Op: agentproto.SnapshotTake, ID: "a"}); err == nil {
		t.Fatal("used a symlinked store")
	}
}

func TestDiff(t *testing.T) {
	edits := diffLines(splitLines([]byte("a\nb\nc\nd\ne\nf\ng\nh\ni\nj\n")), splitLines([]byte("a\nB\nc\nd\ne\nf\ng\nh\ni\nj\nk\n")))
	if a, r := countEdits(edits); a != 2 || r != 1 {
		t.Fatalf("counted +%d -%d", a, r)
	}
	d, cut := unified("f", edits, 1<<20)
	want := "--- a/f\n+++ b/f\n@@ -1,5 +1,5 @@\n a\n-b\n+B\n c\n d\n e\n@@ -8,3 +8,4 @@\n h\n i\n j\n+k\n"
	if d != want || cut {
		t.Fatalf("diff\n%s", d)
	}
	if d, cut := unified("f", edits, 20); len(d) != 20 || !cut {
		t.Fatalf("cut %q", d)
	}
	if isText([]byte{0, 1}) || !isText([]byte("ü\n")) {
		t.Fatal("isText")
	}
}

func TestFiles(t *testing.T) {
	ws := t.TempDir()
	outside := t.TempDir()
	put(t, outside, map[string]string{"secret": "s"})
	put(t, ws, map[string]string{"src/a.go": "package a\n", "b.txt": "b", "bin": "\x00\x01", "img.png": "\x89PNG\r\n\x1a\n0000"})
	must(t, os.Symlink(outside, filepath.Join(ws, "out")))
	must(t, os.Symlink(filepath.Join(outside, "secret"), filepath.Join(ws, "s")))
	must(t, os.WriteFile(filepath.Join(ws, "big"), make([]byte, agentproto.MaxReadBytes+1), 0o644))
	me := [2]int{os.Getuid(), os.Getgid()}

	r, err := browse(ws, me, agentproto.FilesRequest{Op: agentproto.FilesList})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range r.Entries {
		names = append(names, e.Name+":"+e.Type)
	}
	if strings.Join(names, ",") != "src:dir,b.txt:file,big:file,bin:file,img.png:file,out:symlink,s:symlink" {
		t.Fatalf("list %v", names)
	}
	if r, err := browse(ws, me, agentproto.FilesRequest{Op: agentproto.FilesRead, Path: "src/a.go"}); err != nil || string(r.Data) != "package a\n" || !strings.HasPrefix(r.MimeType, "text/plain") {
		t.Fatalf("read %+v %v", r, err)
	}
	if r, _ := browse(ws, me, agentproto.FilesRequest{Op: agentproto.FilesRead, Path: "bin"}); !r.Binary || r.Data != nil {
		t.Fatalf("binary %+v", r)
	}
	if r, _ := browse(ws, me, agentproto.FilesRequest{Op: agentproto.FilesRead, Path: "img.png"}); r.MimeType != "image/png" || r.Data == nil {
		t.Fatalf("image %+v", r)
	}
	if r, _ := browse(ws, me, agentproto.FilesRequest{Op: agentproto.FilesRead, Path: "big"}); !r.TooLarge || r.Data != nil {
		t.Fatalf("big %+v", r)
	}
	if r, _ := browse(ws, [2]int{-2, -2}, agentproto.FilesRequest{Op: agentproto.FilesRead, Path: "b.txt"}); r.Unreadable {
		t.Fatalf("world-readable file %+v", r)
	}
	must(t, os.Chmod(filepath.Join(ws, "b.txt"), 0o600))
	if r, _ := browse(ws, [2]int{-2, -2}, agentproto.FilesRequest{Op: agentproto.FilesRead, Path: "b.txt"}); !r.Unreadable || r.Data != nil {
		t.Fatalf("private file %+v", r)
	}
	for _, p := range []string{"s", "out/secret", "out", "../x", "/etc/passwd", "missing"} {
		op := agentproto.FilesRead
		if p == "out" {
			op = agentproto.FilesList
		}
		if r, err := browse(ws, me, agentproto.FilesRequest{Op: op, Path: p}); err == nil {
			t.Fatalf("%s: %+v", p, r)
		}
	}
}
