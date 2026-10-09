package store

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"testing"
)

func TestMaxMemoryMigrationFromVersionSixAndPersistence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.sqlite")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	if len(names) < 6 {
		t.Fatalf("found %d migrations, want at least six", len(names))
	}
	for i, name := range names[:6] {
		body, err := migrations.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			tx.Rollback()
			t.Fatalf("apply %s: %v", name, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO environments (id, name, created_at) VALUES (?, ?, ?)", "legacy-env", "legacy", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO sandboxes (id, environment_id, name, generation, cpus, memory_mib, workspace_mib, docker_mib, created_at, dns_port) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		"legacy-box", "legacy-env", "legacy-box", 1, 2, 2048, 4096, 4096, 1, 17100); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := st.Sandbox(ctx, "legacy-env", "legacy-box")
	if err != nil || legacy.MaxMemoryMiB != legacy.MemoryMiB {
		t.Fatalf("migrated sandbox = %+v, %v; max memory should match its existing memory", legacy, err)
	}
	listed, err := st.Sandboxes(ctx, "legacy-env")
	if err != nil || len(listed) != 1 || listed[0].MaxMemoryMiB != 2048 {
		t.Fatalf("environment list = %+v, %v", listed, err)
	}
	all, err := st.AllSandboxes(ctx)
	if err != nil || len(all) != 1 || all[0].MaxMemoryMiB != 2048 {
		t.Fatalf("reconciliation list = %+v, %v", all, err)
	}

	created, err := st.CreateSandbox(ctx, Sandbox{
		EnvironmentID: "legacy-env", Name: "new-box", CPUs: 1, MemoryMiB: 1024,
		WorkspaceMiB: 1024, DockerMiB: 1024,
	})
	if err != nil || created.MaxMemoryMiB != created.MemoryMiB {
		t.Fatalf("zero max-memory default = %+v, %v", created, err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	reopened, err := st.Sandbox(ctx, "legacy-env", created.ID)
	if err != nil || reopened.MaxMemoryMiB != 1024 {
		t.Fatalf("reopened sandbox = %+v, %v", reopened, err)
	}
}
