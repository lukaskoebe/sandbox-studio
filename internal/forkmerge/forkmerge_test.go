//go:build linux

package forkmerge

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// sandbox is a pair of trees standing in for a sandbox's /workspace and /home/agent.
type sandbox struct {
	t   *testing.T
	cfg Config
}

func newSandbox(t *testing.T, dir string) *sandbox {
	t.Helper()
	cfg := Config{Workspace: filepath.Join(dir, "workspace"), Home: filepath.Join(dir, "home")}
	for _, d := range []string{cfg.Workspace, cfg.Home} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return &sandbox{t: t, cfg: cfg}
}

// fork copies s, as Studio's fork does.
func (s *sandbox) fork(dir string) *sandbox {
	s.t.Helper()
	f := newSandbox(s.t, dir)
	for _, pair := range [][2]string{{s.cfg.Workspace, f.cfg.Workspace}, {s.cfg.Home, f.cfg.Home}} {
		if out, err := exec.Command("cp", "-a", pair[0]+"/.", pair[1]).CombinedOutput(); err != nil {
			s.t.Fatalf("cp: %v: %s", err, out)
		}
	}
	return f
}

func (s *sandbox) path(p string) string {
	s.t.Helper()
	abs, err := s.cfg.abs(p)
	if err != nil {
		s.t.Fatal(err)
	}
	return abs
}

func (s *sandbox) write(p, content string) {
	s.t.Helper()
	abs := s.path(p)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		s.t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		s.t.Fatal(err)
	}
}

func (s *sandbox) read(p string) string {
	s.t.Helper()
	b, err := os.ReadFile(s.path(p))
	if err != nil {
		s.t.Fatal(err)
	}
	return string(b)
}

func (s *sandbox) exists(p string) bool {
	_, err := os.Lstat(s.path(p))
	return err == nil
}

func (s *sandbox) git(repo string, args ...string) string {
	s.t.Helper()
	dir, err := s.cfg.repoDir(repo)
	if err != nil {
		s.t.Fatal(err)
	}
	out, err := s.cfg.git(dir, args...)
	if err != nil {
		s.t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func gitEnv(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
}

func lines(n ...string) string { return strings.Join(n, "\n") + "\n" }

// merge runs a whole merge: fork exports, scratch (a fresh fork of source) applies.
func merge(t *testing.T, source, fork *sandbox, dir string) (*sandbox, *Report) {
	t.Helper()
	scratch := source.fork(dir)
	if _, err := RecordBase(scratch.cfg, StateStart, "fork-1"); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err := ExportChanges(fork.cfg, "fork-1", &archive); err != nil {
		t.Fatal(err)
	}
	rep, err := Apply(scratch.cfg, ApplyOptions{Fork: "Fork One", Ours: "main-box"}, &archive)
	if err != nil {
		t.Fatal(err)
	}
	return scratch, rep
}

func outcomes(rep *Report) map[string]string {
	out := map[string]string{}
	for _, f := range rep.Files {
		out[f.Path] = strings.TrimSuffix(f.Outcome+" "+f.Conflict, " ")
	}
	return out
}

func TestMergeAndReplay(t *testing.T) {
	gitEnv(t)
	root := t.TempDir()
	source := newSandbox(t, filepath.Join(root, "source"))

	source.write("workspace/app/a.txt", lines("1", "2", "3", "4", "5"))
	source.write("workspace/app/b.txt", lines("b"))
	source.write("workspace/app/c.txt", lines("c"))
	source.write("workspace/app/.gitignore", "node_modules/\n")
	source.write("workspace/app/node_modules/x.js", "ignored\n")
	source.git("workspace/app", "init", "-q", "-b", "main")
	source.git("workspace/app", "add", "-A")
	source.git("workspace/app", "commit", "-q", "-m", "c0")
	source.write("workspace/notes.md", lines("one", "two", "three", "four", "five"))
	source.write("workspace/image.bin", "\x00\x01base")
	source.write("home/.bashrc", "alias a=b\n")
	source.write("home/old.txt", "old\n")
	source.write("home/.claude/projects/p/s1.jsonl", "{}\n")
	source.write("home/.cache/big", "cache\n")

	fork := source.fork(filepath.Join(root, "fork"))
	if _, err := RecordBase(fork.cfg, StateBase, "fork-1"); err != nil {
		t.Fatal(err)
	}

	// The fork commits, edits and adds.
	fork.write("workspace/app/b.txt", lines("b", "committed in the fork"))
	fork.git("workspace/app", "commit", "-q", "-am", "c1")
	fork.git("workspace/app", "branch", "feature")
	fork.write("workspace/app/a.txt", lines("1 fork", "2", "3", "4", "5"))
	fork.write("workspace/app/c.txt", lines("c fork"))
	fork.write("workspace/app/new.txt", "new\n")
	fork.write("workspace/app/node_modules/y.js", "ignored too\n")
	fork.write("workspace/notes.md", lines("one fork", "two", "three", "four", "five"))
	fork.write("workspace/image.bin", "\x00\x01fork")
	fork.write("home/.bashrc", "alias a=c\n")
	if err := os.Remove(fork.path("home/old.txt")); err != nil {
		t.Fatal(err)
	}
	fork.write("home/.claude/projects/p/s1.jsonl", "{}\n{\"fork\":1}\n")
	fork.write("home/.claude/projects/p/s2.jsonl", "{}\n")
	fork.write("home/.cache/big", "fork cache\n")
	fork.write("home/.local/share/mise/installs/node/22.1.0/bin/node", "#!/bin/sh\n")
	if err := os.Symlink("node", fork.path("home/.local/share/mise/installs/node/22.1.0/bin/nodejs")); err != nil {
		t.Fatal(err)
	}

	// Meanwhile the source changes too.
	source.write("workspace/app/a.txt", lines("1", "2", "3", "4", "5 source"))
	source.write("workspace/app/c.txt", lines("c source"))
	source.write("workspace/notes.md", lines("one", "two", "three", "four", "five source"))
	source.write("workspace/image.bin", "\x00\x01source")
	source.write("home/.claude/projects/p/s1.jsonl", "{}\n{\"source\":1}\n")

	scratch, rep := merge(t, source, fork, filepath.Join(root, "scratch"))
	want := map[string]string{
		"workspace/app/a.txt":              "merged",
		"workspace/app/c.txt":              "conflict text",
		"workspace/app/new.txt":            "theirs",
		"workspace/notes.md":               "merged",
		"workspace/image.bin":              "conflict binary",
		"home/.bashrc":                     "theirs",
		"home/old.txt":                     "deleted",
		"home/.claude/projects/p/s1.jsonl": "kept",
		"home/.claude/projects/p/s2.jsonl": "theirs",
	}
	got := outcomes(rep)
	for p, o := range want {
		if got[p] != o {
			t.Errorf("%s: %q, want %q", p, got[p], o)
		}
	}
	for p := range got {
		if _, ok := want[p]; !ok {
			t.Errorf("unexpected %s: %s", p, got[p])
		}
	}
	if rep.Conflicts != 2 {
		t.Errorf("conflicts = %d, want 2", rep.Conflicts)
	}
	if a := scratch.read("workspace/app/a.txt"); a != lines("1 fork", "2", "3", "4", "5 source") {
		t.Errorf("a.txt = %q", a)
	}
	if c := scratch.read("workspace/app/c.txt"); !strings.Contains(c, "<<<<<<< main-box") || !strings.Contains(c, ">>>>>>> Fork One") {
		t.Errorf("c.txt has no markers: %q", c)
	}
	if scratch.exists("workspace/app/node_modules/y.js") || scratch.read("home/.cache/big") != "cache\n" {
		t.Error("ignored files or caches were merged")
	}
	if !scratch.exists("home/.local/share/mise/installs/node/22.1.0/bin/nodejs") || !slices.Equal(rep.Installs, []string{"home/.local/share/mise/installs/node/22.1.0"}) {
		t.Errorf("install not copied: %v", rep.Installs)
	}
	// The fork's commits are branches next to the sandbox's own.
	forkMain := fork.git("workspace/app", "rev-parse", "main")
	if got := scratch.git("workspace/app", "rev-parse", "fork/fork-one/main"); got != forkMain {
		t.Errorf("fork/fork-one/main = %s, want %s", got, forkMain)
	}
	if got := scratch.git("workspace/app", "rev-parse", "fork/fork-one/feature"); got != forkMain {
		t.Errorf("fork/fork-one/feature = %s", got)
	}
	if scratch.git("workspace/app", "rev-parse", "main") != source.git("workspace/app", "rev-parse", "main") {
		t.Error("main moved")
	}
	// Resolve: fix the text conflict, take the fork's binary.
	st, err := Status(scratch.cfg)
	if err != nil || st.Conflicts != 2 {
		t.Fatalf("status = %+v, %v", st, err)
	}
	scratch.write("workspace/app/c.txt", lines("c both"))
	var art string
	for _, f := range st.Files {
		if f.Conflict == ConflictBinary {
			art = filepath.Join(scratch.cfg.staging(), filepath.FromSlash(f.Artifact))
		}
	}
	if err := os.Rename(art, scratch.path("workspace/image.bin")); err != nil {
		t.Fatal(err)
	}
	if st, err = Status(scratch.cfg); err != nil || st.Conflicts != 0 {
		t.Fatalf("status after resolving = %d, %v", st.Conflicts, err)
	}

	// Accept: replay the scratch sandbox into the source.
	var archive bytes.Buffer
	if err := ExportReplay(scratch.cfg, &archive); err != nil {
		t.Fatal(err)
	}
	if err := Replay(source.cfg, &archive); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"workspace/app/a.txt", "workspace/app/c.txt", "workspace/app/new.txt", "workspace/notes.md", "workspace/image.bin", "home/.bashrc", "home/.claude/projects/p/s1.jsonl", "home/.claude/projects/p/s2.jsonl"} {
		if source.read(p) != scratch.read(p) {
			t.Errorf("%s differs after the replay", p)
		}
	}
	if source.exists("home/old.txt") || !source.exists("home/.local/share/mise/installs/node/22.1.0/bin/node") {
		t.Error("deletion or install not replayed")
	}
	if got := source.git("workspace/app", "rev-parse", "fork/fork-one/main"); got != forkMain {
		t.Errorf("source fork/fork-one/main = %s", got)
	}
	if source.exists("workspace/" + stagingDir) {
		t.Error("staging left behind")
	}
	if st := source.git("workspace/app", "status", "--porcelain"); !strings.Contains(st, "M a.txt") || !strings.Contains(st, "?? new.txt") {
		t.Errorf("source status = %q", st)
	}
}

func TestReplayRefusesWhatChangedSince(t *testing.T) {
	gitEnv(t)
	root := t.TempDir()
	source := newSandbox(t, filepath.Join(root, "source"))
	source.write("workspace/a.txt", "a\n")
	source.write("workspace/b.txt", "b\n")
	fork := source.fork(filepath.Join(root, "fork"))
	if _, err := RecordBase(fork.cfg, StateBase, "fork-1"); err != nil {
		t.Fatal(err)
	}
	fork.write("workspace/a.txt", "a fork\n")
	fork.write("workspace/b.txt", "b fork\n")
	scratch, rep := merge(t, source, fork, filepath.Join(root, "scratch"))
	if rep.Conflicts != 0 || scratch.read("workspace/a.txt") != "a fork\n" {
		t.Fatalf("merge: %+v", rep)
	}
	source.write("workspace/b.txt", "b changed meanwhile\n")
	var archive bytes.Buffer
	if err := ExportReplay(scratch.cfg, &archive); err != nil {
		t.Fatal(err)
	}
	err := Replay(source.cfg, &archive)
	var conflict *ReplayConflictError
	if !errors.As(err, &conflict) || !slices.Equal(conflict.Paths, []string{"workspace/b.txt"}) {
		t.Fatalf("replay = %v", err)
	}
	if source.read("workspace/a.txt") != "a\n" {
		t.Error("the replay changed a file although it refused")
	}
}

// A replay that stopped halfway, on a file it couldn't write, is finished by running it again:
// what it already did counts as unchanged. A disk too full for it fails it before it starts.
func TestReplayRunsAgainAfterStoppingHalfway(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into read-only directories")
	}
	gitEnv(t)
	root := t.TempDir()
	source := newSandbox(t, filepath.Join(root, "source"))
	source.write("workspace/app/a.txt", "a\n")
	source.git("workspace/app", "init", "-q", "-b", "main")
	source.git("workspace/app", "add", "-A")
	source.git("workspace/app", "commit", "-q", "-m", "c0")
	source.write("workspace/b.txt", "b\n")
	source.write("workspace/z/late.txt", "late\n")
	fork := source.fork(filepath.Join(root, "fork"))
	if _, err := RecordBase(fork.cfg, StateBase, "fork-1"); err != nil {
		t.Fatal(err)
	}
	fork.write("workspace/app/a.txt", "a fork\n")
	fork.git("workspace/app", "commit", "-q", "-am", "c1")
	fork.write("workspace/b.txt", "b fork\n")
	fork.write("workspace/z/late.txt", "late fork\n")
	scratch, rep := merge(t, source, fork, filepath.Join(root, "scratch"))
	if rep.Conflicts != 0 {
		t.Fatalf("merge: %+v", rep)
	}
	// The scratch sandbox's own commit moves main in the replay.
	scratch.git("workspace/app", "reset", "-q", "--hard", "fork/fork-one/main")
	var archive bytes.Buffer
	if err := ExportReplay(scratch.cfg, &archive); err != nil {
		t.Fatal(err)
	}

	real := diskFree
	t.Cleanup(func() { diskFree = real })
	diskFree = func(string) (uint64, [2]int32, error) { return 1 << 20, [2]int32{1}, nil }
	if err := Replay(source.cfg, bytes.NewReader(archive.Bytes())); !errors.Is(err, ErrNoRoomToReplay) {
		t.Fatalf("replay on a full disk = %v", err)
	}
	if source.read("workspace/b.txt") != "b\n" || source.exists("workspace/"+stagingDir) {
		t.Fatal("a replay refused for room changed something")
	}
	diskFree = real

	z := source.path("workspace/z")
	if err := os.Chmod(z, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(z, 0o755) })
	if err := Replay(source.cfg, bytes.NewReader(archive.Bytes())); err == nil || !strings.Contains(err.Error(), "z/late.txt") {
		t.Fatalf("replay into a read-only directory = %v", err)
	}
	if source.read("workspace/b.txt") != "b fork\n" || source.read("workspace/z/late.txt") != "late\n" {
		t.Fatal("the replay didn't stop halfway")
	}
	if err := os.Chmod(z, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Replay(source.cfg, bytes.NewReader(archive.Bytes())); err != nil {
		t.Fatalf("replay run again = %v", err)
	}
	for _, p := range []string{"workspace/app/a.txt", "workspace/b.txt", "workspace/z/late.txt"} {
		if source.read(p) != scratch.read(p) {
			t.Errorf("%s differs after the replay ran again", p)
		}
	}
	if got, want := source.git("workspace/app", "rev-parse", "main"), scratch.git("workspace/app", "rev-parse", "main"); got != want {
		t.Errorf("main = %s, want %s", got, want)
	}
	if st := source.git("workspace/app", "status", "--porcelain"); st != "" {
		t.Errorf("source status = %q", st)
	}
	// Run a third time, everything counts as taken and stays.
	if err := Replay(source.cfg, bytes.NewReader(archive.Bytes())); err != nil {
		t.Fatalf("replay run a third time = %v", err)
	}
}

// A long file name still fits its temporary name.
func TestTakeWritesALongFileName(t *testing.T) {
	gitEnv(t)
	root := t.TempDir()
	source := newSandbox(t, filepath.Join(root, "source"))
	long := "workspace/" + strings.Repeat("n", 250)
	source.write(long, "a\n")
	fork := source.fork(filepath.Join(root, "fork"))
	if _, err := RecordBase(fork.cfg, StateBase, "fork-1"); err != nil {
		t.Fatal(err)
	}
	fork.write(long, "a fork\n")
	scratch, rep := merge(t, source, fork, filepath.Join(root, "scratch"))
	if rep.Conflicts != 0 || scratch.read(long) != "a fork\n" {
		t.Fatalf("merge: %+v", rep)
	}
	var archive bytes.Buffer
	if err := ExportReplay(scratch.cfg, &archive); err != nil {
		t.Fatal(err)
	}
	if err := Replay(source.cfg, &archive); err != nil || source.read(long) != "a fork\n" {
		t.Fatalf("replay = %v", err)
	}
}

func TestMergeWithoutBase(t *testing.T) {
	gitEnv(t)
	root := t.TempDir()
	source := newSandbox(t, filepath.Join(root, "source"))
	source.write("workspace/same.txt", "same\n")
	source.write("workspace/diff.txt", "source\n")
	fork := source.fork(filepath.Join(root, "fork"))
	fork.write("workspace/diff.txt", "fork\n")
	fork.write("workspace/added.txt", "added\n")
	if err := os.Remove(fork.path("workspace/same.txt")); err != nil {
		t.Fatal(err)
	}
	scratch, rep := merge(t, source, fork, filepath.Join(root, "scratch"))
	got := outcomes(rep)
	if !rep.NoBase || got["workspace/added.txt"] != "theirs" || got["workspace/diff.txt"] != "conflict text" || len(got) != 2 {
		t.Errorf("no base: %+v %v", rep, got)
	}
	if !scratch.exists("workspace/same.txt") {
		t.Error("a file was deleted without a base")
	}
}

func TestNewRepositoryInFork(t *testing.T) {
	gitEnv(t)
	root := t.TempDir()
	source := newSandbox(t, filepath.Join(root, "source"))
	source.write("workspace/readme", "hi\n")
	fork := source.fork(filepath.Join(root, "fork"))
	if _, err := RecordBase(fork.cfg, StateBase, "fork-1"); err != nil {
		t.Fatal(err)
	}
	fork.write("workspace/lib/x.go", "package x\n")
	fork.git("workspace/lib", "init", "-q", "-b", "trunk")
	fork.git("workspace/lib", "add", "-A")
	fork.git("workspace/lib", "commit", "-q", "-m", "x")
	fork.write("workspace/lib/y.go", "package x // wip\n")
	scratch, rep := merge(t, source, fork, filepath.Join(root, "scratch"))
	if rep.Conflicts != 0 {
		t.Fatalf("conflicts: %+v", rep)
	}
	if got := scratch.git("workspace/lib", "rev-parse", "trunk"); got != fork.git("workspace/lib", "rev-parse", "trunk") {
		t.Errorf("trunk = %s", got)
	}
	if st := scratch.git("workspace/lib", "status", "--porcelain"); st != "?? y.go" {
		t.Errorf("status = %q", st)
	}
}
