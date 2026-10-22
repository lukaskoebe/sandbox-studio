package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// EventMerge is a merge of a fork into a sandbox; OtherSandboxID is the fork.
const EventMerge = "merge"

// The states of a merge (docs/design/fork-merge.md).
const (
	MergePreparing = "preparing"
	MergeReady     = "ready"
	MergeConflicts = "conflicts"
	MergeFailed    = "failed"
	MergeAccepted  = "accepted"
	MergeAborted   = "aborted"
)

// Merge is a merge of a fork back into its source, done in a scratch sandbox.
type Merge struct {
	ID            string    `json:"id"`
	EnvironmentID string    `json:"environmentId"`
	SourceID      string    `json:"sourceId" doc:"The sandbox the fork is merged into"`
	ForkID        string    `json:"forkId"`
	ScratchID     string    `json:"scratchId,omitempty" doc:"The sandbox the merge is done in; empty until it exists"`
	State         string    `json:"state" enum:"preparing,ready,conflicts,failed,accepted,aborted"`
	Error         string    `json:"error,omitempty"`
	Report        string    `json:"-"`
	CheckpointID  string    `json:"checkpointId,omitempty" doc:"The source's checkpoint taken before Accept"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// Open reports whether the merge can still be accepted or aborted.
func (m Merge) Open() bool {
	return m.State == MergePreparing || m.State == MergeReady || m.State == MergeConflicts
}

const mergeCols = "id, environment_id, source_id, fork_id, scratch_id, state, error, report, checkpoint_id, created_at, updated_at"

func scanMerge(row interface{ Scan(...any) error }) (Merge, error) {
	var m Merge
	var created, updated int64
	err := row.Scan(&m.ID, &m.EnvironmentID, &m.SourceID, &m.ForkID, &m.ScratchID, &m.State, &m.Error, &m.Report,
		&m.CheckpointID, &created, &updated)
	m.CreatedAt, m.UpdatedAt = time.Unix(created, 0), time.Unix(updated, 0)
	return m, err
}

// CreateMerge records a merge in state preparing. It fails with ErrConflict while another
// merge into the same sandbox is open.
func (s *Store) CreateMerge(ctx context.Context, m Merge) (Merge, error) {
	if m.ID == "" {
		m.ID = NewID()
	}
	t := now()
	m.State, m.CreatedAt, m.UpdatedAt = MergePreparing, time.Unix(t, 0), time.Unix(t, 0)
	_, err := s.db.ExecContext(ctx, "INSERT INTO merges ("+mergeCols+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		m.ID, m.EnvironmentID, m.SourceID, m.ForkID, m.ScratchID, m.State, m.Error, m.Report, m.CheckpointID, t, t)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return Merge{}, ErrConflict
	}
	return m, err
}

// Merge returns one merge.
func (s *Store) Merge(ctx context.Context, envID, id string) (Merge, error) {
	m, err := scanMerge(s.db.QueryRowContext(ctx, "SELECT "+mergeCols+" FROM merges WHERE environment_id = ? AND id = ?", envID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Merge{}, ErrNotFound
	}
	return m, err
}

// MergesOf lists the merges a sandbox takes part in, as source, fork or scratch sandbox,
// newest first.
func (s *Store) MergesOf(ctx context.Context, envID, sandboxID string) ([]Merge, error) {
	return s.merges(ctx, "WHERE environment_id = ? AND ? IN (source_id, fork_id, scratch_id) ORDER BY created_at DESC, id", envID, sandboxID)
}

// OpenMerges lists the open merges of every environment, for startup recovery.
func (s *Store) OpenMerges(ctx context.Context) ([]Merge, error) {
	return s.merges(ctx, "WHERE state IN ('preparing', 'ready', 'conflicts') ORDER BY environment_id, created_at")
}

func (s *Store) merges(ctx context.Context, where string, args ...any) ([]Merge, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+mergeCols+" FROM merges "+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Merge{}
	for rows.Next() {
		m, err := scanMerge(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// UpdateMerge saves the scratch sandbox, state, error, report and checkpoint of an open
// merge. It fails with ErrConflict once the merge is closed, so an abort is never undone by
// a preparation that finishes after it. With ev, the update and the event commit together.
func (s *Store) UpdateMerge(ctx context.Context, m Merge, ev *SandboxEvent) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE merges SET scratch_id = ?, state = ?, error = ?, report = ?, checkpoint_id = ?, updated_at = ?
		WHERE environment_id = ? AND id = ? AND state IN ('preparing', 'ready', 'conflicts')`,
		m.ScratchID, m.State, m.Error, m.Report, m.CheckpointID, now(), m.EnvironmentID, m.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		var exists int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM merges WHERE environment_id = ? AND id = ?", m.EnvironmentID, m.ID).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return ErrNotFound
		}
		return ErrConflict
	}
	if ev != nil {
		if err := insertSandboxEvent(ctx, tx, *ev); err != nil {
			return err
		}
	}
	return tx.Commit()
}
