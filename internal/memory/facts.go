package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Source is where a piece of information came from.
type Source struct {
	ID            string    `json:"id"`
	Scope         string    `json:"scope"`
	Kind          string    `json:"kind" enum:"user,verified,document,inferred,consolidation"`
	SessionRef    string    `json:"sessionRef,omitempty"`
	AuthorPersona string    `json:"authorPersona,omitempty"`
	Evidence      string    `json:"evidence,omitempty" doc:"A short quote; full transcripts are never stored"`
	CreatedAt     time.Time `json:"createdAt"`
}

// Fact is an atomic claim.
type Fact struct {
	ID            string     `json:"id"`
	Scope         string     `json:"scope"`
	Kind          string     `json:"kind" enum:"preference,decision,fact,procedure,event"`
	EntityIDs     []string   `json:"entityIds"`
	Attribute     string     `json:"attribute,omitempty"`
	Text          string     `json:"text"`
	ObservedAt    time.Time  `json:"observedAt"`
	ValidFrom     *time.Time `json:"validFrom,omitempty"`
	ValidUntil    *time.Time `json:"validUntil,omitempty"`
	Confidence    float64    `json:"confidence"`
	Tier          string     `json:"tier" enum:"user,verified,document,inferred"`
	SupportCount  int        `json:"supportCount"`
	Status        string     `json:"status" enum:"active,superseded,disputed,retracted"`
	Supersedes    string     `json:"supersedes,omitempty"`
	AuthorPersona string     `json:"authorPersona,omitempty"`
	SourceIDs     []string   `json:"sourceIds"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
}

// FactInput creates or edits a fact. Zero values take defaults: observedAt now, confidence 1.
type FactInput struct {
	Scope      string     `json:"scope"`
	Kind       string     `json:"kind" enum:"preference,decision,fact,procedure,event"`
	EntityIDs  []string   `json:"entityIds,omitempty" maxItems:"32"`
	Attribute  string     `json:"attribute,omitempty" maxLength:"128" doc:"Normalized key, e.g. deploy.command"`
	Text       string     `json:"text" minLength:"1" maxLength:"2000"`
	ObservedAt *time.Time `json:"observedAt,omitempty"`
	ValidFrom  *time.Time `json:"validFrom,omitempty"`
	ValidUntil *time.Time `json:"validUntil,omitempty"`
	Confidence *float64   `json:"confidence,omitempty" minimum:"0" maximum:"1"`
	Supersedes string     `json:"supersedes,omitempty" doc:"A fact of the same scope this one replaces; it becomes superseded"`
}

// Origin says who writes and on what evidence. Every write records it as a source.
type Origin struct {
	Tier          string // trust tier of the written fact or page
	SourceKind    string // defaults to Tier
	AuthorPersona string // "" for the user
	SessionRef    string
	Evidence      string
}

// UserOrigin is the origin of edits made in the UI.
var UserOrigin = Origin{Tier: TierUser}

func (o Origin) validate() error {
	if err := oneOf("tier", o.Tier, tiers); err != nil {
		return err
	}
	if o.SourceKind != "" {
		if err := oneOf("source kind", o.SourceKind, sourceKinds); err != nil {
			return err
		}
	}
	return validatePersona(o.AuthorPersona)
}

func (in *FactInput) validate() error {
	if err := ValidateScope(in.Scope); err != nil {
		return err
	}
	if err := oneOf("kind", in.Kind, factKinds); err != nil {
		return err
	}
	if err := notEmpty("text", in.Text, 2000); err != nil {
		return err
	}
	if in.Confidence != nil && (*in.Confidence < 0 || *in.Confidence > 1) {
		return fmt.Errorf("%w: confidence must be between 0 and 1", ErrInvalid)
	}
	if in.ValidFrom != nil && in.ValidUntil != nil && in.ValidUntil.Before(*in.ValidFrom) {
		return fmt.Errorf("%w: validUntil is before validFrom", ErrInvalid)
	}
	if in.EntityIDs == nil {
		in.EntityIDs = []string{}
	}
	return nil
}

const factCols = `id, scope, kind, entity_ids, attribute, text, observed_at, valid_from, valid_until, confidence, tier,
	support_count, status, IFNULL(supersedes, ''), author_persona, created_at, updated_at`

func scanFact(row interface{ Scan(...any) error }) (Fact, error) {
	var f Fact
	var entities string
	var observed, created, updated int64
	var from, until sql.NullInt64
	err := row.Scan(&f.ID, &f.Scope, &f.Kind, &entities, &f.Attribute, &f.Text, &observed, &from, &until, &f.Confidence,
		&f.Tier, &f.SupportCount, &f.Status, &f.Supersedes, &f.AuthorPersona, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return f, ErrNotFound
	}
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal([]byte(entities), &f.EntityIDs); err != nil || f.EntityIDs == nil {
		f.EntityIDs = []string{}
	}
	f.ObservedAt, f.CreatedAt, f.UpdatedAt = time.Unix(observed, 0).UTC(), time.Unix(created, 0).UTC(), time.Unix(updated, 0).UTC()
	f.ValidFrom, f.ValidUntil = timePtr(from), timePtr(until)
	f.SourceIDs = []string{}
	return f, nil
}

func (s *Service) insertSource(ctx context.Context, tx execer, envID, scope string, o Origin) (string, error) {
	kind := o.SourceKind
	if kind == "" {
		kind = o.Tier
	}
	id := newID()
	_, err := tx.ExecContext(ctx, `INSERT INTO memory_sources (id, environment_id, scope, kind, session_ref, author_persona, evidence, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, id, envID, scope, kind, o.SessionRef, o.AuthorPersona, o.Evidence, s.unix())
	return id, err
}

// CreateFact writes a fact with its source. With Supersedes set, the old fact (same
// environment and scope) is marked superseded in the same transaction.
func (s *Service) CreateFact(ctx context.Context, envID string, in FactInput, o Origin) (Fact, error) {
	if err := in.validate(); err != nil {
		return Fact{}, err
	}
	if err := o.validate(); err != nil {
		return Fact{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Fact{}, err
	}
	defer tx.Rollback()
	if in.Supersedes != "" {
		var scope string
		err := tx.QueryRowContext(ctx, "SELECT scope FROM memory_facts WHERE environment_id = ? AND id = ?", envID, in.Supersedes).Scan(&scope)
		if errors.Is(err, sql.ErrNoRows) {
			return Fact{}, fmt.Errorf("superseded fact %s: %w", in.Supersedes, ErrNotFound)
		}
		if err != nil {
			return Fact{}, err
		}
		if scope != in.Scope {
			return Fact{}, fmt.Errorf("%w: a fact can only supersede a fact of its own scope (%s)", ErrInvalid, scope)
		}
		if _, err := tx.ExecContext(ctx, "UPDATE memory_facts SET status = ?, updated_at = ? WHERE id = ?", StatusSuperseded, s.unix(), in.Supersedes); err != nil {
			return Fact{}, err
		}
	}
	sourceID, err := s.insertSource(ctx, tx, envID, in.Scope, o)
	if err != nil {
		return Fact{}, err
	}
	now := s.unix()
	observed := now
	if in.ObservedAt != nil && !in.ObservedAt.IsZero() {
		observed = in.ObservedAt.Unix()
	}
	confidence := 1.0
	if in.Confidence != nil {
		confidence = *in.Confidence
	}
	entities, _ := json.Marshal(in.EntityIDs)
	id := newID()
	if _, err := tx.ExecContext(ctx, `INSERT INTO memory_facts (id, environment_id, scope, kind, entity_ids, attribute, text, observed_at,
		valid_from, valid_until, confidence, tier, status, supersedes, author_persona, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'active', ?, ?, ?, ?)`,
		id, envID, in.Scope, in.Kind, string(entities), in.Attribute, in.Text, observed, nullTime(in.ValidFrom), nullTime(in.ValidUntil),
		confidence, o.Tier, nullString(in.Supersedes), o.AuthorPersona, now, now); err != nil {
		return Fact{}, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO memory_fact_sources (fact_id, source_id) VALUES (?, ?)", id, sourceID); err != nil {
		return Fact{}, err
	}
	chunk, err := s.chunkFact(ctx, tx, envID, id, in.Scope, in.Attribute, in.Text)
	if err != nil {
		return Fact{}, err
	}
	if err := tx.Commit(); err != nil {
		return Fact{}, err
	}
	s.enqueue(chunk)
	return s.Fact(ctx, envID, id)
}

// UpdateFact edits a fact's claim. The editor's tier replaces the fact's tier, so a UI edit
// makes the fact user-confirmed, and the edit is recorded as another source.
func (s *Service) UpdateFact(ctx context.Context, envID, id string, in FactInput, o Origin) (Fact, error) {
	old, err := s.Fact(ctx, envID, id)
	if err != nil {
		return Fact{}, err
	}
	in.Scope = old.Scope // scopes are fixed; moving to shared is a share, not an edit
	in.Supersedes = ""
	if err := in.validate(); err != nil {
		return Fact{}, err
	}
	if err := o.validate(); err != nil {
		return Fact{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Fact{}, err
	}
	defer tx.Rollback()
	observed := old.ObservedAt.Unix()
	if in.ObservedAt != nil && !in.ObservedAt.IsZero() {
		observed = in.ObservedAt.Unix()
	}
	confidence := old.Confidence
	if in.Confidence != nil {
		confidence = *in.Confidence
	}
	entities, _ := json.Marshal(in.EntityIDs)
	if _, err := tx.ExecContext(ctx, `UPDATE memory_facts SET kind = ?, entity_ids = ?, attribute = ?, text = ?, observed_at = ?,
		valid_from = ?, valid_until = ?, confidence = ?, tier = ?, updated_at = ? WHERE environment_id = ? AND id = ?`,
		in.Kind, string(entities), in.Attribute, in.Text, observed, nullTime(in.ValidFrom), nullTime(in.ValidUntil), confidence, o.Tier,
		s.unix(), envID, id); err != nil {
		return Fact{}, err
	}
	sourceID, err := s.insertSource(ctx, tx, envID, old.Scope, o)
	if err != nil {
		return Fact{}, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO memory_fact_sources (fact_id, source_id) VALUES (?, ?)", id, sourceID); err != nil {
		return Fact{}, err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM memory_chunks WHERE fact_id = ?", id); err != nil {
		return Fact{}, err
	}
	chunk, err := s.chunkFact(ctx, tx, envID, id, old.Scope, in.Attribute, in.Text)
	if err != nil {
		return Fact{}, err
	}
	if err := tx.Commit(); err != nil {
		return Fact{}, err
	}
	s.enqueue(chunk)
	return s.Fact(ctx, envID, id)
}

// SetFactStatus changes a fact's status, e.g. to retract it.
func (s *Service) SetFactStatus(ctx context.Context, envID, id, status string) (Fact, error) {
	if err := oneOf("status", status, []string{StatusActive, StatusSuperseded, StatusDisputed, StatusRetracted}); err != nil {
		return Fact{}, err
	}
	if err := oneRow(s.db.ExecContext(ctx, "UPDATE memory_facts SET status = ?, updated_at = ? WHERE environment_id = ? AND id = ?",
		status, s.unix(), envID, id)); err != nil {
		return Fact{}, err
	}
	return s.Fact(ctx, envID, id)
}

// DeleteFact removes a fact, its chunks and vectors. Retracting keeps a record; deleting doesn't.
func (s *Service) DeleteFact(ctx context.Context, envID, id string) error {
	return oneRow(s.db.ExecContext(ctx, "DELETE FROM memory_facts WHERE environment_id = ? AND id = ?", envID, id))
}

// Fact returns one fact with its source IDs.
func (s *Service) Fact(ctx context.Context, envID, id string) (Fact, error) {
	f, err := scanFact(s.db.QueryRowContext(ctx, "SELECT "+factCols+" FROM memory_facts WHERE environment_id = ? AND id = ?", envID, id))
	if err != nil {
		return f, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT source_id FROM memory_fact_sources WHERE fact_id = ? ORDER BY rowid", id)
	if err != nil {
		return f, err
	}
	defer rows.Close()
	for rows.Next() {
		var sid string
		if err := rows.Scan(&sid); err != nil {
			return f, err
		}
		f.SourceIDs = append(f.SourceIDs, sid)
	}
	return f, rows.Err()
}

// FactFilter narrows Facts. Empty fields match everything.
type FactFilter struct {
	Scope  string
	Status string
}

// Facts lists facts of an environment, newest first.
func (s *Service) Facts(ctx context.Context, envID string, f FactFilter) ([]Fact, error) {
	q := "SELECT " + factCols + " FROM memory_facts WHERE environment_id = ?"
	args := []any{envID}
	if f.Scope != "" {
		if err := ValidateScope(f.Scope); err != nil {
			return nil, err
		}
		q += " AND scope = ?"
		args = append(args, f.Scope)
	}
	if f.Status != "" {
		q += " AND status = ?"
		args = append(args, f.Status)
	}
	rows, err := s.db.QueryContext(ctx, q+" ORDER BY observed_at DESC, created_at DESC, id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Fact{}
	for rows.Next() {
		fact, err := scanFact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, fact)
	}
	return out, rows.Err()
}

// Source returns one source.
func (s *Service) Source(ctx context.Context, envID, id string) (Source, error) {
	var src Source
	var created int64
	err := s.db.QueryRowContext(ctx, `SELECT id, scope, kind, session_ref, author_persona, evidence, created_at
		FROM memory_sources WHERE environment_id = ? AND id = ?`, envID, id).
		Scan(&src.ID, &src.Scope, &src.Kind, &src.SessionRef, &src.AuthorPersona, &src.Evidence, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return src, ErrNotFound
	}
	src.CreatedAt = time.Unix(created, 0).UTC()
	return src, err
}
