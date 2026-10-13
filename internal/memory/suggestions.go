package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Suggestions are what consolidation proposes and the user accepts or dismisses. Nothing is
// applied by itself (docs/memory.md, Suggestions).

// Suggestion kinds.
const (
	SuggestPromote = "promote" // a teammate's fact, proposed for shared memory
	SuggestCore    = "core"    // a fact whose page should be a core page
)

// Suggestion statuses.
const (
	SuggestOpen      = "open"
	SuggestAccepted  = "accepted"
	SuggestDismissed = "dismissed"
)

// A fact is a core candidate once CoreRecalls different sessions had it injected within
// CoreWindow.
const (
	CoreRecalls = 5
	CoreWindow  = 30 * 24 * time.Hour
)

// Suggestion is a proposal about one fact, open until the user decides.
type Suggestion struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind" enum:"promote,core"`
	Status    string    `json:"status" enum:"open,accepted,dismissed"`
	Reason    string    `json:"reason" doc:"Why it is suggested"`
	Fact      Fact      `json:"fact"`
	CreatedAt time.Time `json:"createdAt"`
}

// AddSuggestion opens a suggestion for a fact. A fact has at most one suggestion of each
// kind, so one the user decided is never made again; added reports whether one was opened.
func (s *Service) AddSuggestion(ctx context.Context, envID, factID, kind, reason string) (added bool, err error) {
	if err := oneOf("kind", kind, []string{SuggestPromote, SuggestCore}); err != nil {
		return false, err
	}
	res, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO memory_suggestions (id, environment_id, fact_id, kind, reason, status, created_at)
		SELECT ?, environment_id, id, ?, ?, 'open', ? FROM memory_facts WHERE environment_id = ? AND id = ?`,
		newID(), kind, truncate(reason, 300), s.unix(), envID, factID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// Suggestion returns one suggestion with its fact.
func (s *Service) Suggestion(ctx context.Context, envID, id string) (Suggestion, error) {
	var sg Suggestion
	var factID string
	var created int64
	err := s.db.QueryRowContext(ctx, `SELECT id, kind, status, reason, fact_id, created_at FROM memory_suggestions
		WHERE environment_id = ? AND id = ?`, envID, id).Scan(&sg.ID, &sg.Kind, &sg.Status, &sg.Reason, &factID, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return sg, ErrNotFound
	}
	if err != nil {
		return sg, err
	}
	sg.CreatedAt = time.Unix(created, 0).UTC()
	sg.Fact, err = s.Fact(ctx, envID, factID)
	return sg, err
}

// Suggestions lists the open suggestions about the facts of a scope, oldest first.
func (s *Service) Suggestions(ctx context.Context, envID, scope string) ([]Suggestion, error) {
	if err := ValidateScope(scope); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT g.id FROM memory_suggestions g JOIN memory_facts f ON f.id = g.fact_id
		WHERE g.environment_id = ? AND f.scope = ? AND g.status = 'open' ORDER BY g.created_at, g.id`, envID, scope)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	out := []Suggestion{}
	for _, id := range ids {
		sg, err := s.Suggestion(ctx, envID, id)
		if err != nil {
			return nil, err
		}
		out = append(out, sg)
	}
	return out, nil
}

// DecideSuggestion records that the user dismissed a suggestion, or accepted a promotion
// (the caller has proposed the fact by then). A decided suggestion stays decided.
func (s *Service) DecideSuggestion(ctx context.Context, envID, id, status string) (Suggestion, error) {
	if status != SuggestAccepted && status != SuggestDismissed {
		return Suggestion{}, fmt.Errorf("%w: status %q", ErrInvalid, status)
	}
	if err := oneRow(s.db.ExecContext(ctx, `UPDATE memory_suggestions SET status = ?, decided_at = ?
		WHERE environment_id = ? AND id = ? AND status = 'open'`, status, s.unix(), envID, id)); err != nil {
		return Suggestion{}, s.openSuggestionError(ctx, envID, id, err)
	}
	return s.Suggestion(ctx, envID, id)
}

// AcceptCore accepts a core suggestion: the page of the fact becomes a core page, loaded into
// every session of its scope. Both happen in one transaction, so a page the core budget
// refuses leaves the suggestion open. A page that is core already is accepted as it is.
func (s *Service) AcceptCore(ctx context.Context, envID, id string) (Suggestion, error) {
	sg, err := s.Suggestion(ctx, envID, id)
	if err != nil {
		return sg, err
	}
	if sg.Kind != SuggestCore || sg.Status != SuggestOpen {
		return sg, fmt.Errorf("%w: suggestion %s is not an open core page suggestion", ErrInvalid, id)
	}
	slug := EntitySlug(pageEntity(sg.Fact))
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return sg, err
	}
	defer tx.Rollback()
	p, err := scanPage(tx.QueryRowContext(ctx, "SELECT "+pageCols+" FROM memory_pages WHERE environment_id = ? AND scope = ? AND slug = ?",
		envID, sg.Fact.Scope, slug))
	if err != nil {
		return sg, err
	}
	if !p.AlwaysLoad {
		if err := checkCoreBudget(ctx, tx, envID, p.Scope, p.ID, p.Compiled); err != nil {
			return sg, err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE memory_pages SET always_load = 1, updated_at = ? WHERE id = ?", s.unix(), p.ID); err != nil {
			return sg, err
		}
	}
	if err := oneRow(tx.ExecContext(ctx, `UPDATE memory_suggestions SET status = 'accepted', decided_at = ?
		WHERE environment_id = ? AND id = ? AND status = 'open'`, s.unix(), envID, id)); err != nil {
		return sg, s.openSuggestionError(ctx, envID, id, err)
	}
	if err := tx.Commit(); err != nil {
		return sg, err
	}
	return s.Suggestion(ctx, envID, id)
}

// openSuggestionError says why an update to an open suggestion touched nothing.
func (s *Service) openSuggestionError(ctx context.Context, envID, id string, err error) error {
	if !errors.Is(err, ErrNotFound) {
		return err
	}
	if _, serr := s.Suggestion(ctx, envID, id); serr != nil {
		return serr
	}
	return fmt.Errorf("suggestion %s is decided already: %w", id, ErrExists)
}

// CoreCandidate is a fact that sessions keep recalling, with the page that could become core.
type CoreCandidate struct {
	Fact     Fact
	Sessions int
	Page     Page
}

// CoreCandidates lists the active facts of a scope that sessions had injected in at least
// CoreRecalls different sessions within CoreWindow before now, and whose page (the page of
// their entity in the scope) exists and is not a core page yet.
func (s *Service) CoreCandidates(ctx context.Context, envID, scope string, now time.Time) ([]CoreCandidate, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT item_id, COUNT(DISTINCT session_id) FROM memory_session_log
		WHERE environment_id = ? AND kind = 'recall' AND item_type = 'fact' AND at >= ? AND session_id IS NOT NULL
		GROUP BY item_id HAVING COUNT(DISTINCT session_id) >= ? ORDER BY COUNT(DISTINCT session_id) DESC, item_id`,
		envID, now.Add(-CoreWindow).Unix(), CoreRecalls)
	if err != nil {
		return nil, err
	}
	type recalled struct {
		id       string
		sessions int
	}
	var list []recalled
	for rows.Next() {
		var r recalled
		if err := rows.Scan(&r.id, &r.sessions); err != nil {
			rows.Close()
			return nil, err
		}
		list = append(list, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	var out []CoreCandidate
	for _, r := range list {
		f, err := s.Fact(ctx, envID, r.id)
		if errors.Is(err, ErrNotFound) || (err == nil && (f.Scope != scope || f.Status != StatusActive)) {
			continue
		}
		if err != nil {
			return nil, err
		}
		slug := EntitySlug(pageEntity(f))
		if slug == "" {
			continue
		}
		p, err := s.PageBySlug(ctx, envID, scope, slug)
		if errors.Is(err, ErrNotFound) || (err == nil && p.AlwaysLoad) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, CoreCandidate{Fact: f, Sessions: r.sessions, Page: p})
	}
	return out, nil
}

// CoreReason is the words a core suggestion carries.
func CoreReason(c CoreCandidate) string {
	return fmt.Sprintf("Recalled in %d sessions in the last 30 days; the page %s could be a core page.", c.Sessions, c.Page.Slug)
}
