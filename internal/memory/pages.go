package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Page is an entity or topic page: compiled truth plus an append-only timeline.
type Page struct {
	ID            string          `json:"id"`
	Scope         string          `json:"scope"`
	Slug          string          `json:"slug"`
	Title         string          `json:"title"`
	Kind          string          `json:"kind" enum:"person,project,topic,procedure,persona-self"`
	Compiled      string          `json:"compiled" doc:"The compiled truth: a regenerated synthesis, as markdown"`
	AlwaysLoad    bool            `json:"alwaysLoad" doc:"A core page, loaded into every session of the scope"`
	Tier          string          `json:"tier" enum:"user,verified,document,inferred"`
	AuthorPersona string          `json:"authorPersona,omitempty"`
	CreatedAt     time.Time       `json:"createdAt"`
	UpdatedAt     time.Time       `json:"updatedAt"`
	Timeline      []TimelineEntry `json:"timeline,omitempty" doc:"Oldest first; only returned for a single page"`
}

// TimelineEntry is one dated line under a page. Entries are never edited.
type TimelineEntry struct {
	ID            string    `json:"id"`
	PageID        string    `json:"pageId"`
	At            time.Time `json:"at"`
	Text          string    `json:"text"`
	FactID        string    `json:"factId,omitempty"`
	SourceID      string    `json:"sourceId,omitempty"`
	AuthorPersona string    `json:"authorPersona,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
}

// PageInput creates or edits a page. Scope and slug are fixed after creation.
type PageInput struct {
	Scope      string `json:"scope,omitempty" doc:"Required on create"`
	Slug       string `json:"slug,omitempty" doc:"Required on create; lowercase words joined by - or /, e.g. project/sandbox-studio"`
	Title      string `json:"title" minLength:"1" maxLength:"200"`
	Kind       string `json:"kind" enum:"person,project,topic,procedure,persona-self"`
	Compiled   string `json:"compiled,omitempty" maxLength:"100000"`
	AlwaysLoad bool   `json:"alwaysLoad,omitempty"`
}

// TimelineInput appends an entry.
type TimelineInput struct {
	Text     string     `json:"text" minLength:"1" maxLength:"2000"`
	At       *time.Time `json:"at,omitempty" doc:"When it happened; defaults to now"`
	FactID   string     `json:"factId,omitempty"`
	SourceID string     `json:"sourceId,omitempty"`
}

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*(/[a-z0-9][a-z0-9._-]*)*$`)

func (in *PageInput) validate() error {
	if err := notEmpty("title", in.Title, 200); err != nil {
		return err
	}
	if err := oneOf("kind", in.Kind, pageKinds); err != nil {
		return err
	}
	if utf8.RuneCountInString(in.Compiled) > 100000 {
		return fmt.Errorf("%w: compiled truth is longer than 100000 characters", ErrInvalid)
	}
	return nil
}

const pageCols = "id, scope, slug, title, kind, compiled, always_load, tier, author_persona, created_at, updated_at"

func scanPage(row interface{ Scan(...any) error }) (Page, error) {
	var p Page
	var created, updated int64
	err := row.Scan(&p.ID, &p.Scope, &p.Slug, &p.Title, &p.Kind, &p.Compiled, &p.AlwaysLoad, &p.Tier, &p.AuthorPersona, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	p.CreatedAt, p.UpdatedAt = time.Unix(created, 0).UTC(), time.Unix(updated, 0).UTC()
	return p, err
}

// checkCoreBudget refuses a write that would push the scope's core pages over CoreBudget.
// pageID is the page being written ("" when new); it is excluded from the current total.
func checkCoreBudget(ctx context.Context, tx execer, envID, scope, pageID, compiled string) error {
	rows, err := tx.QueryContext(ctx, `SELECT slug, length(compiled) FROM memory_pages
		WHERE environment_id = ? AND scope = ? AND always_load AND id != ? ORDER BY length(compiled) DESC`, envID, scope, pageID)
	if err != nil {
		return err
	}
	defer rows.Close()
	used := 0
	var others []string
	for rows.Next() {
		var slug string
		var n int
		if err := rows.Scan(&slug, &n); err != nil {
			return err
		}
		used += n
		others = append(others, fmt.Sprintf("%s (%d)", slug, n))
	}
	if err := rows.Err(); err != nil {
		return err
	}
	size := utf8.RuneCountInString(compiled)
	if used+size <= CoreBudget {
		return nil
	}
	msg := fmt.Sprintf("core pages of %s may hold %d characters together; this page has %d and the others already use %d. "+
		"Shorten the compiled truth to at most %d characters, or keep this page out of always-load and let search find it",
		scope, CoreBudget, size, used, max(CoreBudget-used, 0))
	if len(others) > 0 {
		msg += ", or unmark another core page: " + strings.Join(others, ", ")
	}
	return fmt.Errorf("%w: %s", ErrCoreBudget, msg)
}

// CreatePage writes a page. Core pages are checked against the scope's budget.
func (s *Service) CreatePage(ctx context.Context, envID string, in PageInput, o Origin) (Page, error) {
	if err := ValidateScope(in.Scope); err != nil {
		return Page{}, err
	}
	if !slugPattern.MatchString(in.Slug) || len(in.Slug) > 128 {
		return Page{}, fmt.Errorf("%w: slug %q must be lowercase words joined by - or /, e.g. project/sandbox-studio", ErrInvalid, in.Slug)
	}
	if err := in.validate(); err != nil {
		return Page{}, err
	}
	if err := o.validate(); err != nil {
		return Page{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Page{}, err
	}
	defer tx.Rollback()
	if in.AlwaysLoad {
		if err := checkCoreBudget(ctx, tx, envID, in.Scope, "", in.Compiled); err != nil {
			return Page{}, err
		}
	}
	id, now := newID(), s.unix()
	_, err = tx.ExecContext(ctx, `INSERT INTO memory_pages (id, environment_id, scope, slug, title, kind, compiled, always_load, tier, author_persona, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, envID, in.Scope, in.Slug, in.Title, in.Kind, in.Compiled, in.AlwaysLoad, o.Tier, o.AuthorPersona, now, now)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return Page{}, fmt.Errorf("a page %q in %s: %w", in.Slug, in.Scope, ErrExists)
	}
	if err != nil {
		return Page{}, err
	}
	chunks, err := s.chunkPage(ctx, tx, envID, id, in.Scope, in.Title, in.Compiled)
	if err != nil {
		return Page{}, err
	}
	if err := tx.Commit(); err != nil {
		return Page{}, err
	}
	s.enqueue(chunks...)
	return s.Page(ctx, envID, id)
}

// UpdatePage replaces a page's title, kind, compiled truth and core flag.
func (s *Service) UpdatePage(ctx context.Context, envID, id string, in PageInput, o Origin) (Page, error) {
	if err := in.validate(); err != nil {
		return Page{}, err
	}
	if err := o.validate(); err != nil {
		return Page{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Page{}, err
	}
	defer tx.Rollback()
	old, err := scanPage(tx.QueryRowContext(ctx, "SELECT "+pageCols+" FROM memory_pages WHERE environment_id = ? AND id = ?", envID, id))
	if err != nil {
		return Page{}, err
	}
	if in.AlwaysLoad {
		if err := checkCoreBudget(ctx, tx, envID, old.Scope, id, in.Compiled); err != nil {
			return Page{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE memory_pages SET title = ?, kind = ?, compiled = ?, always_load = ?, tier = ?, author_persona = ?, updated_at = ?
		WHERE id = ?`, in.Title, in.Kind, in.Compiled, in.AlwaysLoad, o.Tier, o.AuthorPersona, s.unix(), id); err != nil {
		return Page{}, err
	}
	var chunks []int64
	if old.Title != in.Title || old.Compiled != in.Compiled {
		if _, err := tx.ExecContext(ctx, "DELETE FROM memory_chunks WHERE page_id = ?", id); err != nil {
			return Page{}, err
		}
		if chunks, err = s.chunkPage(ctx, tx, envID, id, old.Scope, in.Title, in.Compiled); err != nil {
			return Page{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Page{}, err
	}
	s.enqueue(chunks...)
	return s.Page(ctx, envID, id)
}

// DeletePage removes a page and its timeline.
func (s *Service) DeletePage(ctx context.Context, envID, id string) error {
	return oneRow(s.db.ExecContext(ctx, "DELETE FROM memory_pages WHERE environment_id = ? AND id = ?", envID, id))
}

// Page returns a page with its timeline.
func (s *Service) Page(ctx context.Context, envID, id string) (Page, error) {
	p, err := scanPage(s.db.QueryRowContext(ctx, "SELECT "+pageCols+" FROM memory_pages WHERE environment_id = ? AND id = ?", envID, id))
	if err != nil {
		return p, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, page_id, at, text, IFNULL(fact_id, ''), IFNULL(source_id, ''), author_persona, created_at
		FROM memory_timeline WHERE page_id = ? ORDER BY at, created_at, rowid`, id)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	p.Timeline = []TimelineEntry{}
	for rows.Next() {
		var e TimelineEntry
		var at, created int64
		if err := rows.Scan(&e.ID, &e.PageID, &at, &e.Text, &e.FactID, &e.SourceID, &e.AuthorPersona, &created); err != nil {
			return p, err
		}
		e.At, e.CreatedAt = time.Unix(at, 0).UTC(), time.Unix(created, 0).UTC()
		p.Timeline = append(p.Timeline, e)
	}
	return p, rows.Err()
}

// Pages lists the pages of a scope (all scopes when scope is ""), without timelines. Core
// pages come first.
func (s *Service) Pages(ctx context.Context, envID, scope string) ([]Page, error) {
	q, args := "SELECT "+pageCols+" FROM memory_pages WHERE environment_id = ?", []any{envID}
	if scope != "" {
		if err := ValidateScope(scope); err != nil {
			return nil, err
		}
		q += " AND scope = ?"
		args = append(args, scope)
	}
	rows, err := s.db.QueryContext(ctx, q+" ORDER BY always_load DESC, slug", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Page{}
	for rows.Next() {
		p, err := scanPage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// AppendTimeline adds a dated entry under a page. Linked facts and sources must belong to
// the same environment and scope.
func (s *Service) AppendTimeline(ctx context.Context, envID, pageID string, in TimelineInput, o Origin) (TimelineEntry, error) {
	if err := notEmpty("text", in.Text, 2000); err != nil {
		return TimelineEntry{}, err
	}
	if err := validatePersona(o.AuthorPersona); err != nil {
		return TimelineEntry{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TimelineEntry{}, err
	}
	defer tx.Rollback()
	var scope, title string
	err = tx.QueryRowContext(ctx, "SELECT scope, title FROM memory_pages WHERE environment_id = ? AND id = ?", envID, pageID).Scan(&scope, &title)
	if errors.Is(err, sql.ErrNoRows) {
		return TimelineEntry{}, ErrNotFound
	}
	if err != nil {
		return TimelineEntry{}, err
	}
	for _, link := range []struct{ table, id string }{{"memory_facts", in.FactID}, {"memory_sources", in.SourceID}} {
		if link.id == "" {
			continue
		}
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+link.table+" WHERE environment_id = ? AND scope = ? AND id = ?",
			envID, scope, link.id).Scan(&n); err != nil {
			return TimelineEntry{}, err
		}
		if n == 0 {
			return TimelineEntry{}, fmt.Errorf("%w: %s is not in %s", ErrInvalid, link.id, scope)
		}
	}
	now := s.unix()
	e := TimelineEntry{ID: newID(), PageID: pageID, At: time.Unix(now, 0).UTC(), Text: in.Text, FactID: in.FactID, SourceID: in.SourceID,
		AuthorPersona: o.AuthorPersona, CreatedAt: time.Unix(now, 0).UTC()}
	if in.At != nil && !in.At.IsZero() {
		e.At = in.At.UTC().Truncate(time.Second)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO memory_timeline (id, environment_id, page_id, at, text, fact_id, source_id, author_persona, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, e.ID, envID, pageID, e.At.Unix(), e.Text, nullString(e.FactID), nullString(e.SourceID), e.AuthorPersona, now); err != nil {
		return TimelineEntry{}, err
	}
	chunk, err := insertChunk(ctx, tx, envID, scope, "timeline_id", e.ID, title, e.Text)
	if err != nil {
		return TimelineEntry{}, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE memory_pages SET updated_at = ? WHERE id = ?", now, pageID); err != nil {
		return TimelineEntry{}, err
	}
	if err := tx.Commit(); err != nil {
		return TimelineEntry{}, err
	}
	s.enqueue(chunk)
	return e, nil
}
