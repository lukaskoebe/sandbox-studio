package store

import (
	"context"
	"database/sql"
	"errors"
)

// EnvironmentCA returns an environment's CA certificate (DER) and sealed private key, or
// ErrNotFound if it has none yet.
func (s *Store) EnvironmentCA(ctx context.Context, envID string) (cert, sealedKey []byte, err error) {
	err = s.db.QueryRowContext(ctx, "SELECT cert, sealed_key FROM environment_cas WHERE environment_id = ?", envID).Scan(&cert, &sealedKey)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	return cert, sealedKey, err
}

// AddEnvironmentCA stores an environment's CA unless it already has one, and returns the
// one that is stored, so concurrent first uses agree on a single CA.
func (s *Store) AddEnvironmentCA(ctx context.Context, envID string, cert, sealedKey []byte) ([]byte, []byte, error) {
	if _, err := s.db.ExecContext(ctx, "INSERT INTO environment_cas (environment_id, cert, sealed_key, created_at) VALUES (?, ?, ?, ?) ON CONFLICT DO NOTHING",
		envID, cert, sealedKey, now()); err != nil {
		return nil, nil, err
	}
	return s.EnvironmentCA(ctx, envID)
}
