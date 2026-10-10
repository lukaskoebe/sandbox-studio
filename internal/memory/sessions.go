package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Helpers for memory in agent sessions (package agentmem).

// PageBySlug returns the page of a scope with slug, without its timeline.
func (s *Service) PageBySlug(ctx context.Context, envID, scope, slug string) (Page, error) {
	if err := ValidateScope(scope); err != nil {
		return Page{}, err
	}
	p, err := scanPage(s.db.QueryRowContext(ctx, "SELECT "+pageCols+" FROM memory_pages WHERE environment_id = ? AND scope = ? AND slug = ?",
		envID, scope, slug))
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

// FactsChangedSince lists the facts of a scope created or edited after since, newest
// first, leaving out those written by exceptAuthor. Retracted and superseded facts are not
// listed.
func (s *Service) FactsChangedSince(ctx context.Context, envID, scope string, since time.Time, exceptAuthor string, limit int) ([]Fact, error) {
	if err := ValidateScope(scope); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+factCols+` FROM memory_facts
		WHERE environment_id = ? AND scope = ? AND updated_at > ? AND author_persona != ? AND status IN ('active', 'disputed')
		ORDER BY updated_at DESC, id LIMIT ?`, envID, scope, since.Unix(), exceptAuthor, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Fact{}
	for rows.Next() {
		f, err := scanFact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// Reinforce records another source for a fact that was learned again: support count
// up, the source linked, and the tier raised if the new origin is trusted more.
func (s *Service) Reinforce(ctx context.Context, envID, id string, o Origin) (Fact, error) {
	if err := o.validate(); err != nil {
		return Fact{}, err
	}
	old, err := s.Fact(ctx, envID, id)
	if err != nil {
		return Fact{}, err
	}
	tier := old.Tier
	if tierBoost[o.Tier] > tierBoost[tier] {
		tier = o.Tier
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Fact{}, err
	}
	defer tx.Rollback()
	if err := checkPersonas(ctx, tx, envID, "", o.AuthorPersona); err != nil {
		return Fact{}, err
	}
	sourceID, err := s.insertSource(ctx, tx, envID, old.Scope, o)
	if err != nil {
		return Fact{}, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO memory_fact_sources (fact_id, source_id) VALUES (?, ?)", id, sourceID); err != nil {
		return Fact{}, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE memory_facts SET support_count = support_count + 1, tier = ?, updated_at = ? WHERE environment_id = ? AND id = ?",
		tier, s.unix(), envID, id); err != nil {
		return Fact{}, fmt.Errorf("reinforce: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Fact{}, err
	}
	return s.Fact(ctx, envID, id)
}
