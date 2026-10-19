package msbdb

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 10, 19, 16, 0, 0, 0, time.UTC)

// catalog writes a database shaped like msb's: a migration table, a small table with an
// AUTOINCREMENT key and an index, and a table of many rows that fills the end of the file.
func catalog(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "msb.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		"PRAGMA user_version = 7",
		"CREATE TABLE seaql_migrations (version TEXT PRIMARY KEY, applied_at INTEGER NOT NULL)",
		"INSERT INTO seaql_migrations VALUES ('m1', 1), ('m2', 2)",
		"CREATE TABLE sandbox (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL UNIQUE)",
		"CREATE INDEX sandbox_name ON sandbox (name)",
		"INSERT INTO sandbox (name) VALUES ('ss-a'), ('ss-b')",
		"DELETE FROM sandbox WHERE name = 'ss-b'",
		"CREATE TABLE layer (id INTEGER PRIMARY KEY, digest TEXT NOT NULL)",
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	for i := range 3000 {
		if _, err := db.Exec("INSERT INTO layer (digest) VALUES (?)", strings.Repeat("d", 200)+string(rune('a'+i%26))); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// damage overwrites the last n bytes of the file.
func damageTail(t *testing.T, path string, n int) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	st, _ := f.Stat()
	if _, err := f.WriteAt(bytes.Repeat([]byte{0xA5}, n), st.Size()-int64(n)); err != nil {
		t.Fatal(err)
	}
}

func TestCheckPassesASoundOrMissingCatalog(t *testing.T) {
	ctx := context.Background()
	if err := Check(ctx, filepath.Join(t.TempDir(), "msb.db")); err != nil {
		t.Fatalf("missing: %v", err)
	}
	if err := Check(ctx, catalog(t)); err != nil {
		t.Fatalf("sound: %v", err)
	}
}

func TestFixSalvagesTheReadableRows(t *testing.T) {
	ctx := context.Background()
	path := catalog(t)
	damageTail(t, path, 64<<10)
	var d *Damaged
	if err := Check(ctx, path); !errors.As(err, &d) {
		t.Fatalf("Check = %v, want *Damaged", err)
	}
	before, _ := os.ReadFile(path)

	r, err := Fix(ctx, path, now)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Salvaged || r.Why != "" {
		t.Fatalf("repair = %+v, want salvaged", r)
	}
	if r.Aside != path+".broken-20261019T160000Z" {
		t.Fatalf("aside = %s", r.Aside)
	}
	if kept, _ := os.ReadFile(r.Aside); !bytes.Equal(kept, before) {
		t.Fatal("the damaged file was not kept as it was")
	}
	if err := Check(ctx, path); err != nil {
		t.Fatalf("Check after Fix = %v", err)
	}
	if strings.Join(r.Partial, ",") != "layer" {
		t.Fatalf("partial = %v, want layer", r.Partial)
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var migrations, layers, version int
	var sandbox string
	for q, dest := range map[string]any{
		"SELECT count(*) FROM seaql_migrations":  &migrations,
		"SELECT group_concat(name) FROM sandbox": &sandbox,
		"SELECT count(*) FROM layer":             &layers,
		"PRAGMA user_version":                    &version,
	} {
		if err := db.QueryRow(q).Scan(dest); err != nil {
			t.Fatal(q, err)
		}
	}
	if migrations != 2 || sandbox != "ss-a" || layers == 0 || layers >= 3000 || version != 7 {
		t.Fatalf("migrations %d, sandbox %q, layers %d, version %d", migrations, sandbox, layers, version)
	}
	// The counter came along, so a new sandbox doesn't reuse the deleted one's id.
	var id int
	if err := db.QueryRow("INSERT INTO sandbox (name) VALUES ('ss-c') RETURNING id").Scan(&id); err != nil || id != 3 {
		t.Fatalf("new id = %d, %v; want 3", id, err)
	}
	if _, err := db.Exec("INSERT INTO sandbox (name) VALUES ('ss-a')"); err == nil {
		t.Fatal("the unique constraint did not come along")
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), "msb.db.salvage-*")); len(left) > 0 {
		t.Fatalf("left behind: %v", left)
	}
}

func TestFixLeavesAnUnreadableCatalogToMSB(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "msb.db")
	if err := os.WriteFile(path, bytes.Repeat([]byte("not sqlite"), 1000), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+"-wal", []byte("wal"), 0o644); err != nil {
		t.Fatal(err)
	}
	var d *Damaged
	if err := Check(ctx, path); !errors.As(err, &d) || !strings.Contains(d.Error(), "not a database") {
		t.Fatalf("Check = %v", err)
	}
	r, err := Fix(ctx, path, now)
	if err != nil {
		t.Fatal(err)
	}
	if r.Salvaged || r.Why == "" {
		t.Fatalf("repair = %+v, want nothing salvaged and a reason", r)
	}
	for _, s := range []string{"", "-wal"} {
		if _, err := os.Stat(path + s); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s is still there", path+s)
		}
		if _, err := os.Stat(r.Aside + s); err != nil {
			t.Fatalf("%s was not kept: %v", r.Aside+s, err)
		}
	}
	if !strings.Contains(r.Summary(), "lost its list of sandboxes") {
		t.Fatalf("summary = %s", r.Summary())
	}
}

func TestFixWithoutTheMigrationsStartsOver(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "msb.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE sandbox (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	r, err := Fix(ctx, path, now)
	if err != nil {
		t.Fatal(err)
	}
	if r.Salvaged || !strings.Contains(r.Why, "migrations") {
		t.Fatalf("repair = %+v", r)
	}
}

// msb's own processes write the catalog while Studio checks it.
func TestCheckPassesACatalogInUse(t *testing.T) {
	ctx := context.Background()
	path := catalog(t)
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(wal)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("INSERT INTO sandbox (name) VALUES ('ss-live')"); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec("INSERT INTO layer (digest) VALUES ('open')"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + "-wal"); err != nil {
		t.Fatalf("no WAL: %v", err)
	}
	if err := Check(ctx, path); err != nil {
		t.Fatalf("Check = %v", err)
	}
}
