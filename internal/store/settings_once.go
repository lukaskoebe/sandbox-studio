package store

import (
	"context"
	"database/sql"
	"errors"
)

// CreateSetting stores value under key only if the key is absent, then returns
// the value that won. This is useful for one-time install-wide settings whose
// bytes have already been sealed by the caller.
func (s *Store) CreateSetting(ctx context.Context, key string, value []byte) ([]byte, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, "INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO NOTHING", key, value); err != nil {
		return nil, err
	}
	var stored []byte
	err = tx.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", key).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return stored, nil
}
