package main

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/msbdb"
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
