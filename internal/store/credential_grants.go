package store

import "context"

// CredentialGrant is a user's "always" for the credentials of a secret (a secret with a Kind):
// they are issued to SandboxID, or to every sandbox TeammateID owns, without asking. Exactly one
// of the two is set.
type CredentialGrant struct {
	EnvironmentID string
	SecretID      string
	SandboxID     string
	TeammateID    string
}

// GrantCredential records g; granting again is no change.
func (s *Store) GrantCredential(ctx context.Context, g CredentialGrant) error {
	_, err := s.db.ExecContext(ctx, "INSERT OR IGNORE INTO credential_grants (environment_id, secret_id, sandbox_id, teammate_id, created_at) VALUES (?, ?, ?, ?, ?)",
		g.EnvironmentID, g.SecretID, g.SandboxID, g.TeammateID, now())
	return err
}

// CredentialGranted reports whether a grant covers the secret's credentials for the sandbox
// sandboxID owned by teammateID.
func (s *Store) CredentialGranted(ctx context.Context, envID, secretID, sandboxID, teammateID string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM credential_grants WHERE environment_id = ? AND secret_id = ?
		AND (sandbox_id = ? OR (teammate_id <> '' AND teammate_id = ?))`, envID, secretID, sandboxID, teammateID).Scan(&n)
	return n > 0, err
}

// RevokeCredentialGrants removes every grant of a secret, so its credentials are asked for
// again.
func (s *Store) RevokeCredentialGrants(ctx context.Context, envID, secretID string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM credential_grants WHERE environment_id = ? AND secret_id = ?", envID, secretID)
	return err
}
