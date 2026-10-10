// Package memory is Studio's built-in memory (PLAN §6.7): sources, facts, pages with an
// append-only timeline, conflicts, and hybrid retrieval over chunks with BM25 (FTS5) and
// local int8 embeddings fused by reciprocal rank.
//
// Every operation takes an environment ID and filters by it, so nothing crosses
// environments. Within an environment, rows live in one scope: "shared" or
// "persona:<id>".
package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Errors. ErrNotFound and ErrExists wrap the store's, so the API maps them the same way.
var (
	ErrNotFound   = store.ErrNotFound
	ErrExists     = store.ErrExists
	ErrInvalid    = errors.New("invalid memory request")
	ErrCoreBudget = errors.New("core page budget exceeded")
)

// SharedScope is the team-wide scope every persona can read.
const SharedScope = "shared"

// CoreBudget is the most characters of compiled truth that the always-load (core) pages of
// one scope may hold together. Core pages go into every session's context pack.
const CoreBudget = 4000

var personaIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// ValidateScope checks a scope: "shared" or "persona:<id>".
func ValidateScope(scope string) error {
	if scope == SharedScope {
		return nil
	}
	if id, ok := strings.CutPrefix(scope, "persona:"); ok && personaIDPattern.MatchString(id) {
		return nil
	}
	return fmt.Errorf("%w: scope %q must be %q or \"persona:<id>\"", ErrInvalid, scope, SharedScope)
}

// PersonaScope returns the private scope of a persona.
func PersonaScope(personaID string) string { return "persona:" + personaID }

func validatePersona(id string) error {
	if id != "" && !personaIDPattern.MatchString(id) {
		return fmt.Errorf("%w: persona ID %q", ErrInvalid, id)
	}
	return nil
}

// Fact kinds.
const (
	KindPreference = "preference"
	KindDecision   = "decision"
	KindFact       = "fact"
	KindProcedure  = "procedure"
	KindEvent      = "event"
)

// Trust tiers, highest first. Source kinds are the tiers plus "consolidation".
const (
	TierUser     = "user"
	TierVerified = "verified"
	TierDocument = "document"
	TierInferred = "inferred"

	SourceConsolidation = "consolidation"
)

// Fact statuses.
const (
	StatusActive     = "active"
	StatusSuperseded = "superseded"
	StatusDisputed   = "disputed"
	StatusRetracted  = "retracted"
)

var (
	factKinds   = []string{KindPreference, KindDecision, KindFact, KindProcedure, KindEvent}
	tiers       = []string{TierUser, TierVerified, TierDocument, TierInferred}
	sourceKinds = []string{TierUser, TierVerified, TierDocument, TierInferred, SourceConsolidation}
	pageKinds   = []string{"person", "project", "topic", "procedure", "persona-self"}
	verdicts    = []string{"contradiction", "temporal_supersession", "context_dependent", "duplicate"}
	resolutions = []string{"keep_a", "keep_b", "keep_both", "edit"}
)

func oneOf(field, v string, allowed []string) error {
	for _, a := range allowed {
		if v == a {
			return nil
		}
	}
	return fmt.Errorf("%w: %s %q must be one of %s", ErrInvalid, field, v, strings.Join(allowed, ", "))
}

func notEmpty(field, v string, max int) error {
	if strings.TrimSpace(v) == "" {
		return fmt.Errorf("%w: %s is empty", ErrInvalid, field)
	}
	if utf8.RuneCountInString(v) > max {
		return fmt.Errorf("%w: %s is longer than %d characters", ErrInvalid, field, max)
	}
	return nil
}

// Service owns the memory tables and the background embedding worker.
type Service struct {
	db  *sql.DB
	emb Embedder
	log *slog.Logger
	now func() time.Time

	queue chan int64    // chunk IDs to embed; bounded, overflow falls back to a sweep
	sweep chan struct{} // asks the worker to look for every chunk missing a vector
}

// QueueSize bounds the embedding queue. Chunks that do not fit are found by the next sweep.
const QueueSize = 256

// New returns a service over the catalog database. Call Run to embed in the background;
// until vectors exist, search uses BM25 alone.
func New(db *sql.DB, emb Embedder, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{
		db: db, emb: emb, log: log, now: time.Now,
		queue: make(chan int64, QueueSize),
		sweep: make(chan struct{}, 1),
	}
}

func (s *Service) unix() int64 { return s.now().Unix() }

func newID() string { return store.NewID() }

func timePtr(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := time.Unix(v.Int64, 0).UTC()
	return &t
}

func nullTime(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return t.Unix()
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func oneRow(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Scope summarizes one scope that holds data.
type Scope struct {
	Scope     string `json:"scope"`
	Facts     int    `json:"facts"`
	Pages     int    `json:"pages"`
	CoreChars int    `json:"coreChars" doc:"Characters used by always-load pages, out of coreBudget"`
	CoreLimit int    `json:"coreBudget"`
}

// Scopes lists the scopes of an environment that hold facts or pages. shared is always
// listed, first.
func (s *Service) Scopes(ctx context.Context, envID string) ([]Scope, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT scope, SUM(facts), SUM(pages), SUM(core) FROM (
			SELECT scope, 1 AS facts, 0 AS pages, 0 AS core FROM memory_facts WHERE environment_id = ?
			UNION ALL
			SELECT scope, 0, 1, CASE WHEN always_load THEN length(compiled) ELSE 0 END FROM memory_pages WHERE environment_id = ?
		) GROUP BY scope ORDER BY scope != 'shared', scope`, envID, envID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Scope{}
	for rows.Next() {
		var sc Scope
		if err := rows.Scan(&sc.Scope, &sc.Facts, &sc.Pages, &sc.CoreChars); err != nil {
			return nil, err
		}
		sc.CoreLimit = CoreBudget
		out = append(out, sc)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 || out[0].Scope != SharedScope {
		out = append([]Scope{{Scope: SharedScope, CoreLimit: CoreBudget}}, out...)
	}
	return out, nil
}

// checkPersonas makes sure that the personas a write names belong to the environment: the
// author and, on create, the persona of a persona scope (pass "" for scope otherwise).
// Memory has no foreign key to personas, so a deleted persona's memory stays readable and
// editable by the user; only new writes for it are refused.
func checkPersonas(ctx context.Context, tx execer, envID, scope, author string) error {
	ids := []string{author}
	if id, ok := strings.CutPrefix(scope, "persona:"); ok {
		ids = append(ids, id)
	}
	for _, id := range ids {
		if id == "" {
			continue
		}
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM personas WHERE environment_id = ? AND id = ?", envID, id).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("%w: persona %q does not exist in this environment", ErrInvalid, id)
		}
	}
	return nil
}
