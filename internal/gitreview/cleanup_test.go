package gitreview

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

type cleanupFixture struct {
	ctx     context.Context
	st      *store.Store
	svc     *Service
	env     store.Environment
	outside string // a directory next to the staging root that must survive
}

func newCleanupFixture(t *testing.T) *cleanupFixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	env, err := st.CreateEnvironment(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	f := &cleanupFixture{ctx: ctx, st: st, env: env, svc: &Service{Store: st, Dir: filepath.Join(base, "staging")}, outside: filepath.Join(base, "outside")}
	f.write(t, filepath.Join(f.outside, "keep.txt"))
	return f
}

func (f *cleanupFixture) write(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// staging creates a staging repository stand-in for a sandbox and returns its directory.
func (f *cleanupFixture) staging(t *testing.T, envID, sandboxID string) string {
	t.Helper()
	dir := filepath.Join(f.svc.Dir, envID, sandboxID)
	f.write(t, filepath.Join(dir, "forge", "owner", "repo.git", "HEAD"))
	return dir
}

func (f *cleanupFixture) sandbox(t *testing.T, name string) store.Sandbox {
	t.Helper()
	sb, err := f.st.CreateSandbox(f.ctx, store.Sandbox{EnvironmentID: f.env.ID, Name: name})
	if err != nil {
		t.Fatal(err)
	}
	return sb
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func (f *cleanupFixture) assertOutsideIntact(t *testing.T) {
	t.Helper()
	if !exists(filepath.Join(f.outside, "keep.txt")) {
		t.Fatal("a file outside the staging root was removed")
	}
}

func TestForgetSandboxRemovesOnlyItsStaging(t *testing.T) {
	f := newCleanupFixture(t)
	gone, kept := store.NewID(), store.NewID()
	goneDir, keptDir := f.staging(t, f.env.ID, gone), f.staging(t, f.env.ID, kept)
	if err := f.svc.ForgetSandbox(f.env.ID, gone); err != nil {
		t.Fatal(err)
	}
	if exists(goneDir) || !exists(keptDir) {
		t.Fatalf("after forgetting %s: gone=%v kept=%v", gone, exists(goneDir), exists(keptDir))
	}
	if err := f.svc.ForgetSandbox(f.env.ID, kept); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(f.svc.Dir, f.env.ID)) {
		t.Fatal("the empty environment directory is left behind")
	}
	// A sandbox that never pushed has nothing to remove.
	if err := f.svc.ForgetSandbox(f.env.ID, store.NewID()); err != nil {
		t.Fatal(err)
	}
}

func TestForgetSandboxRefusesEscapes(t *testing.T) {
	f := newCleanupFixture(t)
	f.staging(t, f.env.ID, store.NewID())
	for _, id := range []string{"", ".", "..", "../outside", "a/b", "ABC", "/tmp", "x\x00"} {
		if err := f.svc.ForgetSandbox(f.env.ID, id); err == nil {
			t.Errorf("sandbox id %q was accepted", id)
		}
		if err := f.svc.ForgetSandbox(id, store.NewID()); err == nil {
			t.Errorf("environment id %q was accepted", id)
		}
	}
	// A sandbox directory that is a symlink out of the root.
	link := store.NewID()
	if err := os.Symlink(f.outside, filepath.Join(f.svc.Dir, f.env.ID, link)); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.ForgetSandbox(f.env.ID, link); err == nil {
		t.Fatal("a symlinked sandbox directory was accepted")
	}
	// An environment directory that is a symlink out of the root.
	envLink := store.NewID()
	f.write(t, filepath.Join(f.outside, "sb", "keep.txt"))
	if err := os.Symlink(f.outside, filepath.Join(f.svc.Dir, envLink)); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.ForgetSandbox(envLink, "sb"); err == nil {
		t.Fatal("an invalid id under a symlinked environment was accepted")
	}
	victim := store.NewID()
	f.write(t, filepath.Join(f.outside, victim, "keep.txt"))
	if err := f.svc.ForgetSandbox(envLink, victim); err == nil {
		t.Fatal("a symlinked environment directory was accepted")
	}
	f.assertOutsideIntact(t)
	if !exists(filepath.Join(f.outside, victim, "keep.txt")) || !exists(filepath.Join(f.svc.Dir, f.env.ID, link)) {
		t.Fatal("a refused removal removed something")
	}
}

func TestSweepRemovesStagingOfDeletedSandboxesOnly(t *testing.T) {
	f := newCleanupFixture(t)
	live := f.sandbox(t, "stopped") // exists in the store; its state doesn't matter
	liveDir := f.staging(t, f.env.ID, live.ID)
	deleted := f.sandbox(t, "deleted")
	deletedDir := f.staging(t, f.env.ID, deleted.ID)
	if err := f.st.DeleteSandbox(f.ctx, f.env.ID, deleted.ID); err != nil {
		t.Fatal(err)
	}
	orphanEnv := store.NewID()
	orphanDir := f.staging(t, orphanEnv, store.NewID())
	// Symlinks out of the root, at both levels, with names of sandboxes not in the store.
	envLink, sbLink := store.NewID(), store.NewID()
	f.write(t, filepath.Join(f.outside, store.NewID(), "keep.txt"))
	if err := os.Symlink(f.outside, filepath.Join(f.svc.Dir, envLink)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(f.outside, filepath.Join(f.svc.Dir, f.env.ID, sbLink)); err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(f.svc.Dir, "not-an-id")
	f.write(t, filepath.Join(stray, "file"))

	if err := f.svc.Sweep(f.ctx); err != nil {
		t.Fatal(err)
	}
	if !exists(liveDir) {
		t.Fatal("the staging of an existing sandbox was removed")
	}
	if exists(deletedDir) || exists(orphanDir) || exists(filepath.Join(f.svc.Dir, orphanEnv)) {
		t.Fatal("the staging of a deleted sandbox survived the sweep")
	}
	if !exists(filepath.Join(f.svc.Dir, envLink)) || !exists(filepath.Join(f.svc.Dir, f.env.ID, sbLink)) || !exists(stray) {
		t.Fatal("the sweep removed entries it doesn't own")
	}
	f.assertOutsideIntact(t)
}

func TestSweepWithoutStagingRoot(t *testing.T) {
	f := newCleanupFixture(t)
	if err := f.svc.Sweep(f.ctx); err != nil {
		t.Fatal(err)
	}
}
