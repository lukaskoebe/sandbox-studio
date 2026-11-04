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

// PeekSandboxNames lists the names of the user's sandboxes from the database at path, without a
// Store: `studio uninstall` asks while Studio may be running, and whatever its version.
func PeekSandboxNames(ctx context.Context, path string) ([]string, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, "SELECT name FROM sandboxes WHERE kind = '' ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}
