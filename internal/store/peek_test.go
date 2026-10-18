package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func TestPeekSettingNeitherMigratesNorWrites(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "studio.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "CREATE TABLE settings (key TEXT PRIMARY KEY, value BLOB); INSERT INTO settings VALUES ('k', x'0102')"); err != nil {
		t.Fatal(err)
	}
	db.Close()

	v, err := PeekSetting(ctx, path, "k")
	if err != nil || string(v) != "\x01\x02" {
		t.Fatalf("peek: %q, %v", v, err)
	}
	if _, err := PeekSetting(ctx, path, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a missing setting: %v", err)
	}
	db, err = sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version, tables int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type = 'table'").Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if version != 0 || tables != 1 {
		t.Fatalf("the catalog changed: version %d, %d tables", version, tables)
	}
	if _, err := PeekSetting(ctx, filepath.Join(t.TempDir(), "none.db"), "k"); err == nil {
		t.Fatal("a missing catalog was read")
	}
}
