package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Kinds of sandbox event. A sandbox's history is its events in order.
const (
	EventCreate     = "create"     // made from a template or not, or imported
	EventFork       = "fork"       // forked; OtherSandboxID is the other side of the fork
	EventRebase     = "rebase"     // moved to another template: a new generation
	EventRestore    = "restore"    // restored from a checkpoint: a new generation
	EventCheckpoint = "checkpoint" // a checkpoint was saved
	EventDelete     = "delete"     // deleted; the event is the sandbox's tombstone
)

// SandboxEvent is one successful step in a sandbox's history. Events outlive their sandbox:
// a delete event keeps the name, template and fork source it had.
type SandboxEvent struct {
	ID             int64     `json:"id"`
	EnvironmentID  string    `json:"environmentId"`
	SandboxID      string    `json:"sandboxId"`
	Kind           string    `json:"kind" enum:"create,fork,rebase,restore,checkpoint,delete,merge"`
	Generation     int       `json:"generation" doc:"The sandbox's generation after the event"`
	Name           string    `json:"name" doc:"The sandbox's name at the time"`
	TemplateID     string    `json:"templateId,omitempty" doc:"The template of the generation; empty for none"`
	FromTemplateID string    `json:"fromTemplateId,omitempty" doc:"For a rebase, the template it moved from"`
	CheckpointID   string    `json:"checkpointId,omitempty"`
	CheckpointName string    `json:"checkpointName,omitempty"`
	OtherSandboxID string    `json:"otherSandboxId,omitempty" doc:"For a fork, the other sandbox. On a delete event, the source the sandbox was forked from. For a merge, the fork merged"`
	CreatedAt      time.Time `json:"createdAt"`
}

const sandboxEventCols = "id, environment_id, sandbox_id, kind, generation, name, template_id, from_template_id, checkpoint_id, checkpoint_name, other_sandbox_id, created_at"

func scanSandboxEvent(row interface{ Scan(...any) error }) (SandboxEvent, error) {
	var ev SandboxEvent
	var created int64
	err := row.Scan(&ev.ID, &ev.EnvironmentID, &ev.SandboxID, &ev.Kind, &ev.Generation, &ev.Name, &ev.TemplateID, &ev.FromTemplateID,
		&ev.CheckpointID, &ev.CheckpointName, &ev.OtherSandboxID, &created)
	ev.CreatedAt = time.Unix(created, 0)
	return ev, err
}

// RecordSandboxEvent appends an event to a sandbox's history. Callers record only operations
// that succeeded.
func (s *Store) RecordSandboxEvent(ctx context.Context, ev SandboxEvent) error {
	return insertSandboxEvent(ctx, s.db, ev)
}

type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func insertSandboxEvent(ctx context.Context, x execer, ev SandboxEvent) error {
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = time.Unix(now(), 0)
	}
	_, err := x.ExecContext(ctx, `INSERT INTO sandbox_events (environment_id, sandbox_id, kind, generation, name, template_id, from_template_id, checkpoint_id, checkpoint_name, other_sandbox_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ev.EnvironmentID, ev.SandboxID, ev.Kind, ev.Generation, ev.Name, ev.TemplateID, ev.FromTemplateID,
		ev.CheckpointID, ev.CheckpointName, ev.OtherSandboxID, ev.CreatedAt.Unix())
	return err
}

// SandboxEvents lists the events of a sandbox in order. A deleted sandbox keeps its history.
func (s *Store) SandboxEvents(ctx context.Context, envID, sandboxID string) ([]SandboxEvent, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+sandboxEventCols+" FROM sandbox_events WHERE environment_id = ? AND sandbox_id = ? ORDER BY created_at, id", envID, sandboxID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SandboxEvent{}
	for rows.Next() {
		ev, err := scanSandboxEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// DeleteEvent returns the tombstone of a deleted sandbox, or ErrNotFound.
func (s *Store) DeleteEvent(ctx context.Context, envID, sandboxID string) (SandboxEvent, error) {
	ev, err := scanSandboxEvent(s.db.QueryRowContext(ctx, "SELECT "+sandboxEventCols+" FROM sandbox_events WHERE environment_id = ? AND sandbox_id = ? AND kind = ?", envID, sandboxID, EventDelete))
	if errors.Is(err, sql.ErrNoRows) {
		return ev, ErrNotFound
	}
	return ev, err
}

// DeleteEvents lists the tombstones of an environment's deleted sandboxes, in deletion order.
func (s *Store) DeleteEvents(ctx context.Context, envID string) ([]SandboxEvent, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+sandboxEventCols+" FROM sandbox_events WHERE environment_id = ? AND kind = ? ORDER BY created_at, id", envID, EventDelete)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SandboxEvent{}
	for rows.Next() {
		ev, err := scanSandboxEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// sandboxIdentity reads the name and template a sandbox has in the catalog, inside tx.
func sandboxIdentity(ctx context.Context, tx *sql.Tx, envID, sandboxID string) (name, templateID string, err error) {
	err = tx.QueryRowContext(ctx, "SELECT name, IFNULL(template_id, '') FROM sandboxes WHERE environment_id = ? AND id = ?", envID, sandboxID).Scan(&name, &templateID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	return name, templateID, err
}

// checkpointName reads a checkpoint's name inside tx; it is empty if the checkpoint is gone.
func checkpointName(ctx context.Context, tx *sql.Tx, envID, sandboxID, checkpointID string) (string, error) {
	var name string
	err := tx.QueryRowContext(ctx, "SELECT name FROM checkpoints WHERE environment_id = ? AND sandbox_id = ? AND id = ?", envID, sandboxID, checkpointID).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return name, err
}
