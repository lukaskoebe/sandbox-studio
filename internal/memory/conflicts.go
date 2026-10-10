package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Conflict pairs two facts the consolidation judge found at odds.
type Conflict struct {
	ID         string     `json:"id"`
	FactA      Fact       `json:"factA"`
	FactB      Fact       `json:"factB"`
	Verdict    string     `json:"verdict" enum:"contradiction,temporal_supersession,context_dependent,duplicate"`
	Status     string     `json:"status" enum:"open,resolved"`
	Resolution string     `json:"resolution,omitempty" enum:"keep_a,keep_b,keep_both,dismiss"`
	Note       string     `json:"note,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	ResolvedAt *time.Time `json:"resolvedAt,omitempty"`
}

// CreateConflict records a conflict between two facts of an environment and marks the
// active ones disputed, so recall shows them with a marker until it is resolved.
func (s *Service) CreateConflict(ctx context.Context, envID, factA, factB, verdict, note string) (Conflict, error) {
	if err := oneOf("verdict", verdict, verdicts); err != nil {
		return Conflict{}, err
	}
	if factA == factB {
		return Conflict{}, fmt.Errorf("%w: a fact cannot conflict with itself", ErrInvalid)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Conflict{}, err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM memory_facts WHERE environment_id = ? AND id IN (?, ?)", envID, factA, factB).Scan(&n); err != nil {
		return Conflict{}, err
	}
	if n != 2 {
		return Conflict{}, fmt.Errorf("conflicting facts: %w", ErrNotFound)
	}
	id, now := newID(), s.unix()
	if _, err := tx.ExecContext(ctx, `INSERT INTO memory_conflicts (id, environment_id, fact_a, fact_b, verdict, note, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, id, envID, factA, factB, verdict, note, now); err != nil {
		return Conflict{}, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE memory_facts SET status = 'disputed', updated_at = ? WHERE id IN (?, ?) AND status = 'active'", now, factA, factB); err != nil {
		return Conflict{}, err
	}
	if err := tx.Commit(); err != nil {
		return Conflict{}, err
	}
	return s.Conflict(ctx, envID, id)
}

// ResolveConflict records the user's resolution. It does not change the facts yet; that
// is the consolidation job's part (M6).
func (s *Service) ResolveConflict(ctx context.Context, envID, id, resolution, note string) (Conflict, error) {
	if err := oneOf("resolution", resolution, resolutions); err != nil {
		return Conflict{}, err
	}
	var status string
	err := s.db.QueryRowContext(ctx, "SELECT status FROM memory_conflicts WHERE environment_id = ? AND id = ?", envID, id).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return Conflict{}, ErrNotFound
	}
	if err != nil {
		return Conflict{}, err
	}
	if status == "resolved" {
		return Conflict{}, fmt.Errorf("conflict %s is already resolved: %w", id, ErrExists)
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE memory_conflicts SET status = 'resolved', resolution = ?, note = CASE WHEN ? = '' THEN note ELSE ? END, resolved_at = ? WHERE id = ?",
		resolution, note, note, s.unix(), id); err != nil {
		return Conflict{}, err
	}
	return s.Conflict(ctx, envID, id)
}

type conflictRow struct {
	c            Conflict
	factA, factB string
}

func (s *Service) conflictRows(ctx context.Context, where string, args ...any) ([]conflictRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, fact_a, fact_b, verdict, status, resolution, note, created_at, resolved_at
		FROM memory_conflicts WHERE `+where+` ORDER BY status = 'resolved', created_at DESC, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []conflictRow
	for rows.Next() {
		var r conflictRow
		var created int64
		var resolved sql.NullInt64
		if err := rows.Scan(&r.c.ID, &r.factA, &r.factB, &r.c.Verdict, &r.c.Status, &r.c.Resolution, &r.c.Note, &created, &resolved); err != nil {
			return nil, err
		}
		r.c.CreatedAt, r.c.ResolvedAt = time.Unix(created, 0).UTC(), timePtr(resolved)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Service) fillConflicts(ctx context.Context, envID string, rows []conflictRow) ([]Conflict, error) {
	out := make([]Conflict, 0, len(rows))
	for _, r := range rows {
		var err error
		if r.c.FactA, err = s.Fact(ctx, envID, r.factA); err != nil {
			return nil, err
		}
		if r.c.FactB, err = s.Fact(ctx, envID, r.factB); err != nil {
			return nil, err
		}
		out = append(out, r.c)
	}
	return out, nil
}

// Conflict returns one conflict with both facts.
func (s *Service) Conflict(ctx context.Context, envID, id string) (Conflict, error) {
	rows, err := s.conflictRows(ctx, "environment_id = ? AND id = ?", envID, id)
	if err != nil {
		return Conflict{}, err
	}
	if len(rows) == 0 {
		return Conflict{}, ErrNotFound
	}
	out, err := s.fillConflicts(ctx, envID, rows)
	if err != nil {
		return Conflict{}, err
	}
	return out[0], nil
}

// Conflicts lists an environment's conflicts, open ones first. With scope set, only
// conflicts touching a fact of that scope are listed.
func (s *Service) Conflicts(ctx context.Context, envID, scope string) ([]Conflict, error) {
	where, args := "environment_id = ?", []any{envID}
	if scope != "" {
		if err := ValidateScope(scope); err != nil {
			return nil, err
		}
		where += " AND (fact_a IN (SELECT id FROM memory_facts WHERE scope = ?) OR fact_b IN (SELECT id FROM memory_facts WHERE scope = ?))"
		args = append(args, scope, scope)
	}
	rows, err := s.conflictRows(ctx, where, args...)
	if err != nil {
		return nil, err
	}
	return s.fillConflicts(ctx, envID, rows)
}
