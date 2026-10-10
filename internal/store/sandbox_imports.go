package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Sandbox import states. Ready and failed are terminal.
const (
	ImportUploading = "uploading"
	ImportBuilding  = "building"
	ImportCreating  = "creating"
	ImportImporting = "importing"
	ImportReady     = "ready"
	ImportFailed    = "failed"

	maxImportErrorBytes = 1000
)

// SandboxImport is the durable status of one sandbox import.
type SandboxImport struct {
	ID            string    `json:"id"`
	EnvironmentID string    `json:"environmentId"`
	Name          string    `json:"name" doc:"Name of the new sandbox; it gets a suffix if the exported name is taken"`
	State         string    `json:"state" enum:"uploading,building,creating,importing,ready,failed"`
	Error         string    `json:"error,omitempty"`
	BuildJobID    string    `json:"buildJobId,omitempty"`
	SandboxID     string    `json:"sandboxId,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

const sandboxImportCols = "id, environment_id, name, state, error, build_job_id, IFNULL(sandbox_id, ''), created_at, updated_at"

func scanSandboxImport(row interface{ Scan(...any) error }) (SandboxImport, error) {
	var imp SandboxImport
	var created, updated int64
	err := row.Scan(&imp.ID, &imp.EnvironmentID, &imp.Name, &imp.State, &imp.Error, &imp.BuildJobID, &imp.SandboxID, &created, &updated)
	imp.CreatedAt, imp.UpdatedAt = time.Unix(created, 0), time.Unix(updated, 0)
	return imp, err
}

// CreateSandboxImport records a new import in the uploading state.
func (s *Store) CreateSandboxImport(ctx context.Context, envID, name string) (SandboxImport, error) {
	t := now()
	imp := SandboxImport{ID: NewID(), EnvironmentID: envID, Name: name, State: ImportUploading,
		CreatedAt: time.Unix(t, 0), UpdatedAt: time.Unix(t, 0)}
	_, err := s.db.ExecContext(ctx, "INSERT INTO sandbox_imports (id, environment_id, name, state, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)",
		imp.ID, envID, name, imp.State, t, t)
	if isForeignKeyError(err) {
		return SandboxImport{}, ErrNotFound
	}
	return imp, err
}

// SandboxImport returns one import of an environment.
func (s *Store) SandboxImport(ctx context.Context, envID, id string) (SandboxImport, error) {
	imp, err := scanSandboxImport(s.db.QueryRowContext(ctx, "SELECT "+sandboxImportCols+" FROM sandbox_imports WHERE environment_id = ? AND id = ?", envID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return SandboxImport{}, ErrNotFound
	}
	return imp, err
}

// ActiveSandboxImports lists imports in a non-terminal state, across environments, for
// startup recovery.
func (s *Store) ActiveSandboxImports(ctx context.Context) ([]SandboxImport, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+sandboxImportCols+" FROM sandbox_imports WHERE state NOT IN (?, ?) ORDER BY created_at, id", ImportReady, ImportFailed)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SandboxImport{}
	for rows.Next() {
		imp, err := scanSandboxImport(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, imp)
	}
	return out, rows.Err()
}

// AdvanceSandboxImport moves an import from one non-terminal state to the next. A
// non-empty buildJobID is recorded too. It fails with ErrConflict if the state changed.
func (s *Store) AdvanceSandboxImport(ctx context.Context, envID, id, from, to, buildJobID string) error {
	if to == ImportReady || to == ImportFailed {
		return ErrConflict
	}
	res, err := s.db.ExecContext(ctx, "UPDATE sandbox_imports SET state = ?, build_job_id = CASE WHEN ? = '' THEN build_job_id ELSE ? END, updated_at = ? WHERE environment_id = ? AND id = ? AND state = ?",
		to, buildJobID, buildJobID, now(), envID, id, from)
	if err != nil {
		return err
	}
	return exactlyOneRow(res)
}

// CreateImportedSandbox inserts sb as CreateSandbox does and links it to an import in the
// creating state, in one transaction, so recovery always finds a half-made sandbox.
func (s *Store) CreateImportedSandbox(ctx context.Context, importID string, sb Sandbox) (Sandbox, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return sb, err
	}
	defer tx.Rollback()
	var state, linked string
	err = tx.QueryRowContext(ctx, "SELECT state, IFNULL(sandbox_id, '') FROM sandbox_imports WHERE environment_id = ? AND id = ?", sb.EnvironmentID, importID).Scan(&state, &linked)
	if errors.Is(err, sql.ErrNoRows) {
		return sb, ErrNotFound
	}
	if err != nil {
		return sb, err
	}
	if state != ImportCreating || linked != "" {
		return sb, ErrConflict
	}
	sb, err = insertSandbox(ctx, tx, sb)
	if err != nil {
		return sb, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE sandbox_imports SET sandbox_id = ?, name = ?, updated_at = ? WHERE id = ?", sb.ID, sb.Name, now(), importID); err != nil {
		return sb, err
	}
	return sb, tx.Commit()
}

// FinishSandboxImport ends an import as ready or failed. Ready is only reachable from
// importing; failed from any non-terminal state. A finished import is never changed.
func (s *Store) FinishSandboxImport(ctx context.Context, envID, id, state, message string) error {
	var res sql.Result
	var err error
	switch state {
	case ImportReady:
		res, err = s.db.ExecContext(ctx, "UPDATE sandbox_imports SET state = ?, error = '', updated_at = ? WHERE environment_id = ? AND id = ? AND state = ? AND sandbox_id IS NOT NULL",
			ImportReady, now(), envID, id, ImportImporting)
	case ImportFailed:
		if len(message) > maxImportErrorBytes {
			message = message[:maxImportErrorBytes]
		}
		res, err = s.db.ExecContext(ctx, "UPDATE sandbox_imports SET state = ?, error = ?, updated_at = ? WHERE environment_id = ? AND id = ? AND state NOT IN (?, ?)",
			ImportFailed, message, now(), envID, id, ImportReady, ImportFailed)
	default:
		return ErrConflict
	}
	if err != nil {
		return err
	}
	return exactlyOneRow(res)
}

// PruneSandboxImports deletes finished imports last updated before cutoff.
func (s *Store) PruneSandboxImports(ctx context.Context, cutoff time.Time) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM sandbox_imports WHERE state IN (?, ?) AND updated_at < ?", ImportReady, ImportFailed, cutoff.Unix())
	return err
}

func isForeignKeyError(err error) bool {
	return err != nil && strings.Contains(strings.ToUpper(err.Error()), "FOREIGN KEY")
}
