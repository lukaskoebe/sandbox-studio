package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Conflict pairs two facts the consolidation judge found at odds. FactA is the older one.
type Conflict struct {
	ID         string     `json:"id"`
	FactA      Fact       `json:"factA"`
	FactB      Fact       `json:"factB"`
	Verdict    string     `json:"verdict" enum:"contradiction,temporal_supersession,context_dependent,duplicate" doc:"temporal_supersession: B looks like a newer value of A but has a lower tier or another scope, so it is not applied automatically"`
	Reason     string     `json:"reason,omitempty" doc:"The judge's reasons"`
	Status     string     `json:"status" enum:"open,resolved"`
	Resolution string     `json:"resolution,omitempty" enum:"keep_a,keep_b,keep_both,edit"`
	Note       string     `json:"note,omitempty"`
	ApprovalID string     `json:"approvalId,omitempty" doc:"The memory.conflict approval in the inbox"`
	CreatedAt  time.Time  `json:"createdAt"`
	ResolvedAt *time.Time `json:"resolvedAt,omitempty"`
}

// ConflictResolution is the user's decision on a conflict.
type ConflictResolution struct {
	Resolution string `json:"resolution" enum:"keep_a,keep_b,keep_both,edit" doc:"keep_a or keep_b retracts the other fact; keep_both keeps both, optionally qualified (\"in project X, …\"); edit replaces both with text"`
	Note       string `json:"note,omitempty" maxLength:"500"`
	Text       string `json:"text,omitempty" maxLength:"2000" doc:"edit: the fact that replaces both"`
	TextA      string `json:"textA,omitempty" maxLength:"2000" doc:"keep_both: a qualified rewrite of A"`
	TextB      string `json:"textB,omitempty" maxLength:"2000" doc:"keep_both: a qualified rewrite of B"`
}

func (r *ConflictResolution) validate() error {
	if err := oneOf("resolution", r.Resolution, resolutions); err != nil {
		return err
	}
	r.Text, r.TextA, r.TextB = strings.TrimSpace(r.Text), strings.TrimSpace(r.TextA), strings.TrimSpace(r.TextB)
	if r.Resolution == "edit" {
		if err := notEmpty("text", r.Text, 2000); err != nil {
			return err
		}
	} else if r.Text != "" {
		return fmt.Errorf("%w: text is only for edit", ErrInvalid)
	}
	if r.Resolution != "keep_both" && (r.TextA != "" || r.TextB != "") {
		return fmt.Errorf("%w: textA and textB are only for keep_both", ErrInvalid)
	}
	for _, t := range []string{r.TextA, r.TextB} {
		if len([]rune(t)) > 2000 {
			return fmt.Errorf("%w: text is longer than 2000 characters", ErrInvalid)
		}
	}
	return nil
}

// disputes reports which facts of a conflict get the disputed mark. Within one scope both
// do; across scopes only the persona fact does, because the shared fact wins at recall
// until the conflict is resolved.
func disputes(a, b Fact) (bool, bool) {
	if a.Scope == b.Scope {
		return true, true
	}
	return a.Scope != SharedScope, b.Scope != SharedScope
}

// CreateConflict records a conflict between two facts; see OpenConflict.
func (s *Service) CreateConflict(ctx context.Context, envID, factA, factB, verdict, reason string) (Conflict, error) {
	c, _, err := s.OpenConflict(ctx, envID, factA, factB, verdict, reason)
	return c, err
}

// OpenConflict records a conflict between two facts of an environment (A the older), marks
// them disputed (see disputes) and adds a timeline entry to the page of their entity in
// A's scope. It is idempotent: a pair that already has a conflict, in either order,
// returns that one with created false.
func (s *Service) OpenConflict(ctx context.Context, envID, factA, factB, verdict, reason string) (Conflict, bool, error) {
	if err := oneOf("verdict", verdict, verdicts); err != nil {
		return Conflict{}, false, err
	}
	if factA == factB {
		return Conflict{}, false, fmt.Errorf("%w: a fact cannot conflict with itself", ErrInvalid)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Conflict{}, false, err
	}
	defer tx.Rollback()
	a, errA := factTx(ctx, tx, envID, factA)
	b, errB := factTx(ctx, tx, envID, factB)
	if errors.Is(errA, ErrNotFound) || errors.Is(errB, ErrNotFound) {
		return Conflict{}, false, fmt.Errorf("conflicting facts: %w", ErrNotFound)
	}
	if err := errors.Join(errA, errB); err != nil {
		return Conflict{}, false, err
	}
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT id FROM memory_conflicts WHERE environment_id = ? AND
		((fact_a = ? AND fact_b = ?) OR (fact_a = ? AND fact_b = ?))`, envID, factA, factB, factB, factA).Scan(&existing)
	if err == nil {
		tx.Rollback()
		c, err := s.Conflict(ctx, envID, existing)
		return c, false, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Conflict{}, false, err
	}
	id, now := newID(), s.unix()
	if _, err := tx.ExecContext(ctx, `INSERT INTO memory_conflicts (id, environment_id, fact_a, fact_b, verdict, reason, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, id, envID, factA, factB, verdict, truncate(reason, 1000), now); err != nil {
		return Conflict{}, false, err
	}
	da, db := disputes(a, b)
	for _, d := range []struct {
		on bool
		id string
	}{{da, a.ID}, {db, b.ID}} {
		if !d.on {
			continue
		}
		if _, err := tx.ExecContext(ctx, "UPDATE memory_facts SET status = 'disputed', updated_at = ? WHERE id = ? AND status = 'active'", now, d.id); err != nil {
			return Conflict{}, false, err
		}
	}
	// The entry goes to the page in the scope of the fact that is not shared, if any.
	home, other := a, b
	if a.Scope == SharedScope && b.Scope != SharedScope {
		home, other = b, a
	}
	chunks, err := s.noteOnPage(ctx, tx, envID, home, other, time.Unix(now, 0),
		fmt.Sprintf("Conflict opened (%s): %q vs %q. %s", strings.ReplaceAll(verdict, "_", " "), truncate(a.Text, 300), truncate(b.Text, 300), truncate(reason, 300)))
	if err != nil {
		return Conflict{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Conflict{}, false, err
	}
	s.enqueue(chunks...)
	c, err := s.Conflict(ctx, envID, id)
	return c, true, err
}

// SetConflictApproval links a conflict to the approval that asks the user about it.
func (s *Service) SetConflictApproval(ctx context.Context, envID, id, approvalID string) error {
	return oneRow(s.db.ExecContext(ctx, "UPDATE memory_conflicts SET approval_id = ? WHERE environment_id = ? AND id = ?", approvalID, envID, id))
}

// ResolveConflict applies the user's decision, which changes what is recalled:
//   - keep_a / keep_b: the other fact is retracted; the kept one is active again.
//   - keep_both: both are active again, optionally rewritten with qualifiers (tier user).
//   - edit: a new user fact with r.Text replaces both, which become superseded. It goes to
//     shared memory if either fact was shared, else to their scope.
//
// A fact stays disputed while another open conflict disputes it.
func (s *Service) ResolveConflict(ctx context.Context, envID, id string, r ConflictResolution) (Conflict, error) {
	if err := r.validate(); err != nil {
		return Conflict{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Conflict{}, err
	}
	defer tx.Rollback()
	var status, fa, fb string
	err = tx.QueryRowContext(ctx, "SELECT status, fact_a, fact_b FROM memory_conflicts WHERE environment_id = ? AND id = ?", envID, id).Scan(&status, &fa, &fb)
	if errors.Is(err, sql.ErrNoRows) {
		return Conflict{}, ErrNotFound
	}
	if err != nil {
		return Conflict{}, err
	}
	if status == "resolved" {
		return Conflict{}, fmt.Errorf("conflict %s is already resolved: %w", id, ErrExists)
	}
	a, err := factTx(ctx, tx, envID, fa)
	if err != nil {
		return Conflict{}, err
	}
	b, err := factTx(ctx, tx, envID, fb)
	if err != nil {
		return Conflict{}, err
	}
	now := s.unix()
	setStatus := func(f Fact, st string) error {
		_, err := tx.ExecContext(ctx, "UPDATE memory_facts SET status = ?, updated_at = ? WHERE id = ?", st, now, f.ID)
		return err
	}
	var chunks []int64
	rewrite := func(f Fact, text string) error {
		if text == "" || text == f.Text {
			return nil
		}
		if _, err := tx.ExecContext(ctx, "UPDATE memory_facts SET text = ?, tier = 'user', updated_at = ? WHERE id = ?", text, now, f.ID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM memory_chunks WHERE fact_id = ?", f.ID); err != nil {
			return err
		}
		c, err := s.chunkFact(ctx, tx, envID, f.ID, f.Scope, f.Attribute, text)
		chunks = append(chunks, c)
		return err
	}
	summary := ""
	switch r.Resolution {
	case "keep_a":
		err, summary = setStatus(b, StatusRetracted), fmt.Sprintf("kept %q, retracted %q", truncate(a.Text, 200), truncate(b.Text, 200))
	case "keep_b":
		err, summary = setStatus(a, StatusRetracted), fmt.Sprintf("kept %q, retracted %q", truncate(b.Text, 200), truncate(a.Text, 200))
	case "keep_both":
		err, summary = errors.Join(rewrite(a, r.TextA), rewrite(b, r.TextB)), "kept both in their contexts"
	case "edit":
		var c int64
		c, err = s.replaceBoth(ctx, tx, envID, a, b, r.Text)
		chunks, summary = append(chunks, c), fmt.Sprintf("replaced both with %q", truncate(r.Text, 200))
	}
	if err != nil {
		return Conflict{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE memory_conflicts SET status = 'resolved', resolution = ?, note = CASE WHEN ? = '' THEN note ELSE ? END,
		resolved_at = ? WHERE id = ?`, r.Resolution, r.Note, r.Note, now, id); err != nil {
		return Conflict{}, err
	}
	for _, f := range []Fact{a, b} {
		if err := refreshDisputed(ctx, tx, f.ID, now); err != nil {
			return Conflict{}, err
		}
	}
	home, other := a, b
	if a.Scope == SharedScope && b.Scope != SharedScope {
		home, other = b, a
	}
	c, err := s.noteOnPage(ctx, tx, envID, home, other, time.Unix(now, 0), "Conflict resolved by the user: "+summary+".")
	if err != nil {
		return Conflict{}, err
	}
	if err := tx.Commit(); err != nil {
		return Conflict{}, err
	}
	s.enqueue(append(chunks, c...)...)
	return s.Conflict(ctx, envID, id)
}

// replaceBoth writes the user's replacement for a conflicting pair and supersedes both.
func (s *Service) replaceBoth(ctx context.Context, tx execer, envID string, a, b Fact, text string) (int64, error) {
	scope := a.Scope
	if b.Scope == SharedScope {
		scope = SharedScope
	}
	supersedes := a.ID
	if a.Scope != scope {
		supersedes = b.ID
	}
	entities := append([]string{}, a.EntityIDs...)
	for _, e := range b.EntityIDs {
		if !containsString(entities, e) {
			entities = append(entities, e)
		}
	}
	attr := a.Attribute
	if attr == "" {
		attr = b.Attribute
	}
	sourceID, err := s.insertSource(ctx, tx, envID, scope, Origin{Tier: TierUser, Evidence: "resolution of a memory conflict"})
	if err != nil {
		return 0, err
	}
	now := s.unix()
	id := newID()
	ents, _ := jsonMarshal(entities)
	if _, err := tx.ExecContext(ctx, `INSERT INTO memory_facts (id, environment_id, scope, kind, entity_ids, attribute, text, observed_at,
		confidence, tier, status, supersedes, author_persona, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, 'user', 'active', ?, '', ?, ?)`,
		id, envID, scope, a.Kind, ents, attr, text, now, supersedes, now, now); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO memory_fact_sources (fact_id, source_id) VALUES (?, ?)", id, sourceID); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE memory_facts SET status = 'superseded', updated_at = ? WHERE id IN (?, ?)", now, a.ID, b.ID); err != nil {
		return 0, err
	}
	return s.chunkFact(ctx, tx, envID, id, scope, attr, text)
}

// refreshDisputed makes a disputed fact active again unless an open conflict still
// disputes it.
func refreshDisputed(ctx context.Context, tx execer, factID string, now int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT fa.scope, fb.scope, fa.id FROM memory_conflicts c
		JOIN memory_facts fa ON fa.id = c.fact_a JOIN memory_facts fb ON fb.id = c.fact_b
		WHERE c.status = 'open' AND (c.fact_a = ? OR c.fact_b = ?)`, factID, factID)
	if err != nil {
		return err
	}
	still := false
	for rows.Next() {
		var sa, sb, idA string
		if err := rows.Scan(&sa, &sb, &idA); err != nil {
			rows.Close()
			return err
		}
		da, db := disputes(Fact{Scope: sa}, Fact{Scope: sb})
		if (idA == factID && da) || (idA != factID && db) {
			still = true
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if still {
		return nil
	}
	_, err = tx.ExecContext(ctx, "UPDATE memory_facts SET status = 'active', updated_at = ? WHERE id = ? AND status = 'disputed'", now, factID)
	return err
}

type conflictRow struct {
	c            Conflict
	factA, factB string
}

func (s *Service) conflictRows(ctx context.Context, where string, args ...any) ([]conflictRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, fact_a, fact_b, verdict, reason, status, resolution, note, IFNULL(approval_id, ''), created_at, resolved_at
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
		if err := rows.Scan(&r.c.ID, &r.factA, &r.factB, &r.c.Verdict, &r.c.Reason, &r.c.Status, &r.c.Resolution, &r.c.Note, &r.c.ApprovalID,
			&created, &resolved); err != nil {
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

// ConflictBetween returns the conflict of a fact pair in either order.
func (s *Service) ConflictBetween(ctx context.Context, envID, a, b string) (Conflict, error) {
	rows, err := s.conflictRows(ctx, "environment_id = ? AND ((fact_a = ? AND fact_b = ?) OR (fact_a = ? AND fact_b = ?))", envID, a, b, b, a)
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

// FactSources returns the sources of a fact, oldest first.
func (s *Service) FactSources(ctx context.Context, envID, factID string) ([]Source, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT s.id, s.scope, s.kind, s.session_ref, s.author_persona, s.evidence, s.created_at
		FROM memory_sources s JOIN memory_fact_sources fs ON fs.source_id = s.id
		WHERE s.environment_id = ? AND fs.fact_id = ? ORDER BY s.created_at, fs.rowid`, envID, factID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Source{}
	for rows.Next() {
		var src Source
		var created int64
		if err := rows.Scan(&src.ID, &src.Scope, &src.Kind, &src.SessionRef, &src.AuthorPersona, &src.Evidence, &created); err != nil {
			return nil, err
		}
		src.CreatedAt = time.Unix(created, 0).UTC()
		out = append(out, src)
	}
	return out, rows.Err()
}

// FactTimeline returns the timeline entries linked to any of the facts, oldest first.
func (s *Service) FactTimeline(ctx context.Context, envID string, factIDs ...string) ([]TimelineEntry, error) {
	out := []TimelineEntry{}
	if len(factIDs) == 0 {
		return out, nil
	}
	args := []any{envID}
	for _, id := range factIDs {
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, page_id, at, text, IFNULL(fact_id, ''), IFNULL(source_id, ''), author_persona, created_at
		FROM memory_timeline WHERE environment_id = ? AND fact_id IN (?`+strings.Repeat(", ?", len(factIDs)-1)+`) ORDER BY at, created_at, rowid`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var e TimelineEntry
		var at, created int64
		if err := rows.Scan(&e.ID, &e.PageID, &at, &e.Text, &e.FactID, &e.SourceID, &e.AuthorPersona, &created); err != nil {
			return nil, err
		}
		e.At, e.CreatedAt = time.Unix(at, 0).UTC(), time.Unix(created, 0).UTC()
		out = append(out, e)
	}
	return out, rows.Err()
}
