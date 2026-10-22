package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A database that a newer Studio migrated is refused, untouched, instead of being run with
// tables this binary doesn't know.
func TestOpenRefusesANewerSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "studio.db")
	st, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	var known int
	if err := st.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&known); err != nil {
		t.Fatal(err)
	}
	st.Close()

	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	newer := known + 3
	if _, err := db.ExecContext(ctx, "PRAGMA user_version = "+strconv.Itoa(newer)); err != nil {
		t.Fatal(err)
	}
	db.Close()

	_, err = Open(ctx, path)
	if !errors.Is(err, ErrNewerSchema) || !strings.Contains(err.Error(), "schema "+strconv.Itoa(newer)) {
		t.Fatalf("open: %v", err)
	}
	// Reopening the same version still works.
	db, _ = sql.Open("sqlite", "file:"+path)
	defer db.Close()
	if _, err := db.ExecContext(ctx, "PRAGMA user_version = "+strconv.Itoa(known)); err != nil {
		t.Fatal(err)
	}
	st, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
}
