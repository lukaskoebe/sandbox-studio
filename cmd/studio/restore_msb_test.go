package main

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/msbdb"
	"github.com/lukaskoebe/sandbox-studio/internal/paths"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// writeCatalog writes a small catalog shaped like msb's with the named sandboxes.
func writeCatalog(t *testing.T, path string, sandboxes ...string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		"CREATE TABLE seaql_migrations (version TEXT PRIMARY KEY, applied_at INTEGER NOT NULL)",
		"INSERT INTO seaql_migrations VALUES ('m1', 1)",
		"CREATE TABLE sandbox (id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE)",
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	for _, name := range sandboxes {
		if _, err := db.Exec("INSERT INTO sandbox (name) VALUES (?)", name); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRestoreMSBDBRebuildsFromTheNewestAside(t *testing.T) {
	ctx := context.Background()
	t.Setenv("MSB_HOME", t.TempDir())
	t.Setenv("SANDBOX_STUDIO_HOME", t.TempDir())
	path, _ := msbdb.Path()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	writeCatalog(t, path+".broken-20261001T000000Z", "studio-old-g1")
	writeCatalog(t, path+".broken-20261022T072947Z", "studio-a-g1", "studio-b-g2")
	// Nothing listens on port 1, so no Studio runs.
	const addr = "127.0.0.1:1"
	vms := []msbdb.VM{{PID: 42, Name: "ss-a-g1"}}
	runningVMs = func(string) []msbdb.VM { return vms }
	t.Cleanup(func() { runningVMs = msbdb.RunningVMs })
	var out bytes.Buffer
	if status := runRestoreMSBDB(&out, addr, []string{"-replace"}); status != 1 {
		t.Fatalf("with a VM running: status %d", status)
	}
	vms = nil
	holders := []int{4242}
	msbHolders = func(string) []int { return holders }
	t.Cleanup(func() { msbHolders = msbdb.Holders })
	out.Reset()
	if status := runRestoreMSBDB(&out, addr, []string{"-replace"}); status != 1 {
		t.Fatalf("with the catalog open in another process: status %d", status)
	}
	holders = nil

	// Another Studio on this microsandbox home.
	release, _, err := lockMSB()
	if err != nil {
		t.Fatal(err)
	}
	if status := runRestoreMSBDB(&out, addr, []string{"-replace"}); status != 1 {
		t.Fatalf("with another Studio's lock held: status %d", status)
	}
	_ = release()

	writeCatalog(t, path, "studio-new-g1")
	if status := runRestoreMSBDB(&out, addr, nil); status != 1 {
		t.Fatalf("over a catalog with sandboxes: status %d", status)
	}
	if status := runRestoreMSBDB(&out, addr, []string{"-replace"}); status != 0 {
		t.Fatalf("-replace: status %d", status)
	}
	if names, err := msbdb.Sandboxes(ctx, path); err != nil || strings.Join(names, ",") != "studio-a-g1,studio-b-g2" {
		t.Fatalf("sandboxes = %v, %v", names, err)
	}
	if !strings.Contains(out.String(), "2 sandboxes") || !strings.Contains(out.String(), ".replaced-") {
		t.Fatalf("output:\n%s", out.String())
	}
	if status := runRestoreMSBDB(&out, addr, []string{"a", "b"}); status == 0 {
		t.Fatal("two files were taken")
	}
}

// The case from a Mac: a repair moved a damaged catalog aside before a rebase, the rebase made
// the sandbox's second generation, and the catalog was damaged again. Restoring from the newest
// damaged one gave msb back the first generation only. The restore picks the saved catalog that
// knows the VM Studio expects, here the one an earlier restore replaced.
func TestRestoreMSBDBPicksTheSavedCatalogThatKnowsStudiosVMs(t *testing.T) {
	ctx := context.Background()
	t.Setenv("MSB_HOME", t.TempDir())
	t.Setenv("SANDBOX_STUDIO_HOME", t.TempDir())
	p, err := paths.Default()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, p.DB())
	if err != nil {
		t.Fatal(err)
	}
	env, err := st.CreateEnvironment(ctx, "e")
	if err != nil {
		t.Fatal(err)
	}
	sb, err := st.CreateSandbox(ctx, store.Sandbox{EnvironmentID: env.ID, Name: "katara", CPUs: 1, MemoryMiB: 512, WorkspaceMiB: 1024, DockerMiB: 1024})
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	db, err := sql.Open("sqlite", "file:"+p.DB())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE sandboxes SET generation = 2 WHERE id = ?", sb.ID); err != nil {
		t.Fatal(err)
	}
	db.Close()
	g1, g2 := "ss-"+sb.ID+"-g1", "ss-"+sb.ID+"-g2"

	path, _ := msbdb.Path()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	writeCatalog(t, path+".broken-20261113T073800Z", g1, "ss-other-g1")
	writeCatalog(t, path+".replaced-20261113T111025Z", g2, "ss-other-g1")
	writeCatalog(t, path, g1, "ss-other-g1")
	const addr = "127.0.0.1:1"

	var out bytes.Buffer
	if status := runRestoreMSBDB(&out, addr, []string{"-replace"}); status != 0 {
		t.Fatalf("status %d: %s", status, out.String())
	}
	known, err := msbdb.Sandboxes(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(known, g2) {
		t.Fatalf("restored catalog knows %v, want %s; output:\n%s", known, g2, out.String())
	}
	if !strings.Contains(out.String(), "Restoring from "+path+".replaced-20261113T111025Z") {
		t.Errorf("output doesn't say which it restored from:\n%s", out.String())
	}

	// Now the one in place knows it; no saved one knows more, so nothing changes.
	out.Reset()
	if status := runRestoreMSBDB(&out, addr, []string{"-replace"}); status != 1 {
		t.Fatalf("with nothing better saved: status %d: %s", status, out.String())
	}
	if known, _ := msbdb.Sandboxes(ctx, path); !slices.Contains(known, g2) {
		t.Fatalf("the catalog in place changed: %v", known)
	}
}
