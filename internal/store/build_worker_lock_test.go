package store

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

const (
	buildWorkerLockHelperEnv = "STUDIO_BUILD_WORKER_LOCK_HELPER"
	buildWorkerLockDBEnv     = "STUDIO_BUILD_WORKER_LOCK_DB"
)

func requireFileBuildWorkerLockPlatform(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skipf("file-backed worker lock is unsupported on %s", runtime.GOOS)
	}
}

func TestBuildWorkerLockFileHandlesShareLeaseAndRelease(t *testing.T) {
	requireFileBuildWorkerLockPlatform(t)
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "studio.db")
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	independent, err := Open(ctx, filepath.Join(dir, "other.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	defer independent.Close()

	releaseFirst, err := first.AcquireBuildWorker(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseFirst()
	if _, err := first.AcquireBuildWorker(ctx); !errors.Is(err, ErrConflict) {
		t.Fatalf("same Store acquired a second worker lease: %v", err)
	}
	if _, err := second.AcquireBuildWorker(ctx); !errors.Is(err, ErrConflict) {
		t.Fatalf("second Store handle acquired a duplicate lease: %v", err)
	}
	releaseIndependent, err := independent.AcquireBuildWorker(ctx)
	if err != nil {
		t.Fatalf("independent database was blocked by another database's lease: %v", err)
	}
	defer releaseIndependent()
	if err := releaseIndependent(); err != nil {
		t.Fatal(err)
	}

	lockPath := path + ".build-lock"
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("lock file is missing: %v", err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(lockPath)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("lock file mode = %04o, want 0600", info.Mode().Perm())
		}
	}

	// Closing the database must not release the worker's independent lock fd.
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := second.AcquireBuildWorker(ctx); !errors.Is(err, ErrConflict) {
		t.Fatalf("Store.Close released an active worker lease: %v", err)
	}
	if err := releaseFirst(); err != nil {
		t.Fatal(err)
	}
	if err := releaseFirst(); err != nil {
		t.Fatalf("releasing worker lease twice: %v", err)
	}
	releaseSecond, err := second.AcquireBuildWorker(ctx)
	if err != nil {
		t.Fatalf("worker lease remained held after release: %v", err)
	}
	defer releaseSecond()
	if err := releaseSecond(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("release removed the persistent lock file: %v", err)
	}
}

func TestBuildWorkerLockResolvesDatabaseSymlink(t *testing.T) {
	requireFileBuildWorkerLockPlatform(t)
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "studio.db")
	initial, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := initial.Close(); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "alias.db")
	if err := os.Symlink(path, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	realStore, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer realStore.Close()
	aliasStore, err := Open(ctx, alias)
	if err != nil {
		t.Fatal(err)
	}
	defer aliasStore.Close()

	release, err := realStore.AcquireBuildWorker(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := aliasStore.AcquireBuildWorker(ctx); !errors.Is(err, ErrConflict) {
		t.Fatalf("symlink alias acquired a second worker lease: %v", err)
	}
}

func TestBuildWorkerLockRejectsSymlinkLockFile(t *testing.T) {
	requireFileBuildWorkerLockPlatform(t)
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "studio.db")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path+".build-lock"); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := store.AcquireBuildWorker(ctx); err == nil || errors.Is(err, ErrConflict) {
		t.Fatalf("symlink lock file was followed or misreported as an active lease: %v", err)
	}
	content, err := os.ReadFile(target)
	if err != nil || string(content) != "keep" {
		t.Fatalf("symlink target was changed: content=%q, err=%v", content, err)
	}
}

func TestBuildWorkerLockIsPerInMemoryStore(t *testing.T) {
	ctx := context.Background()
	first, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	second, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	defer second.Close()

	releaseFirst, err := first.AcquireBuildWorker(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseFirst()
	if _, err := first.AcquireBuildWorker(ctx); !errors.Is(err, ErrConflict) {
		t.Fatalf("same in-memory Store acquired a second worker lease: %v", err)
	}
	releaseSecond, err := second.AcquireBuildWorker(ctx)
	if err != nil {
		t.Fatalf("independent in-memory Store was blocked: %v", err)
	}
	defer releaseSecond()
	if err := releaseFirst(); err != nil {
		t.Fatal(err)
	}
	if err := releaseSecond(); err != nil {
		t.Fatal(err)
	}
}

func TestBuildWorkerLockHelperProcess(t *testing.T) {
	if os.Getenv(buildWorkerLockHelperEnv) != "1" {
		return
	}
	store, err := Open(context.Background(), os.Getenv(buildWorkerLockDBEnv))
	if err != nil {
		fmt.Fprintln(os.Stdout, "open error:", err)
		os.Exit(2)
	}
	release, err := store.AcquireBuildWorker(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stdout, "lock error:", err)
		os.Exit(3)
	}
	fmt.Fprintln(os.Stdout, "locked")
	_, _ = io.Copy(io.Discard, os.Stdin)
	runtime.KeepAlive(release)
	os.Exit(0)
}

func TestBuildWorkerLockReleasesAfterProcessExit(t *testing.T) {
	requireFileBuildWorkerLockPlatform(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "studio.db")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	cmd := exec.Command(os.Args[0], "-test.run=^TestBuildWorkerLockHelperProcess$")
	cmd.Env = append(os.Environ(), buildWorkerLockHelperEnv+"=1", buildWorkerLockDBEnv+"="+path)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	childExited := false
	defer func() {
		if !childExited && cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "locked\n" {
		t.Fatalf("helper did not acquire worker lock: line=%q, err=%v", line, err)
	}
	if _, err := store.AcquireBuildWorker(ctx); !errors.Is(err, ErrConflict) {
		t.Fatalf("parent acquired worker lock while helper held it: %v", err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = stdin.Close()
	_ = cmd.Wait()
	childExited = true
	release, err := store.AcquireBuildWorker(ctx)
	if err != nil {
		t.Fatalf("process exit left stale worker lock: %v", err)
	}
	defer release()
	if err := release(); err != nil {
		t.Fatal(err)
	}
}
