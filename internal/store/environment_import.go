package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ImportEnvironment creates an environment with its providers, teammates and rules in one
// transaction, so a failed import leaves nothing behind. The caller sets every ID; the
// providers and teammates are written in the order given, so a teammate's provider and a
// rule's teammate must come before it. Keys, secrets and sandboxes are not imported.
func (s *Store) ImportEnvironment(ctx context.Context, env Environment, providers []Provider, teammates []Teammate, rules []Rule) (Environment, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return env, err
	}
	defer tx.Rollback()
	t := now()
	env.CreatedAt = time.Unix(t, 0)
	if _, err := tx.ExecContext(ctx, "INSERT INTO environments (id, name, created_at) VALUES (?, ?, ?)", env.ID, env.Name, t); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return env, fmt.Errorf("an environment named %q: %w", env.Name, ErrExists)
		}
		return env, err
	}
	for _, p := range providers {
		p.EnvironmentID = env.ID
		if err := insertProvider(ctx, tx, p, t); err != nil {
			return env, err
		}
	}
	for _, p := range teammates {
		p.EnvironmentID = env.ID
		if p.ApprovalScope == "" {
			p.ApprovalScope = "environment"
		}
		if err := teammateExists(insertTeammate(ctx, tx, p, t), p.Name); err != nil {
			return env, err
		}
	}
	for _, r := range rules {
		r.EnvironmentID, r.CreatedAt = env.ID, env.CreatedAt
		if r.Ports == nil {
			r.Ports = []int{}
		}
		if err := insertRule(ctx, tx, r); err != nil {
			return env, err
		}
	}
	// The file doesn't carry the level or the curated list: an import starts as a new environment does.
	env.SecurityLevel, env.CuratedOff = LevelGuarded, []string{}
	return env, tx.Commit()
}
