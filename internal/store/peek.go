package store

import (
	"context"
	"database/sql"
	"errors"
)

// PeekSetting reads one setting from the catalog at path without migrating it or writing to
// it. Clients of a running Studio (studio tui, login-url) read its key this way: a client of
// another version must not change the server's catalog under it.
func PeekSetting(ctx context.Context, path, key string) ([]byte, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var v []byte
	err = db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return v, err
}
