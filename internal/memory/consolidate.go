package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// Primitives for consolidation (the dream job in package agentmem): merging duplicates,
// supersession, conflicts and compiled truth. Each is idempotent so an interrupted dream
// can run again.

func factTx(ctx context.Context, tx execer, envID, id string) (Fact, error) {
	f, err := scanFact(tx.QueryRowContext(ctx, "SELECT "+factCols+" FROM memory_facts WHERE environment_id = ? AND id = ?", envID, id))
	if err != nil {
		return f, err
	}
	f.SourceIDs = nil
	return f, nil
}

func containsString(list []string, v string) bool { return slices.Contains(list, v) }

func jsonMarshal(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

func tierRank(t string) float64 { return tierBoost[t] }

// EntitySlug is the page slug for an entity: the entity itself when it already has a
// prefix (project/api), else topic/<entity>. "" when the entity makes no valid slug.
func EntitySlug(entity string) string {
	e := strings.ToLower(strings.TrimSpace(entity))
	if !strings.Contains(e, "/") {
		e = "topic/" + e
	}
	if !slugPattern.MatchString(e) || len(e) > 128 {
		return ""
	}
	return e
}

func slugKind(slug string) string {
	prefix, _, _ := strings.Cut(slug, "/")
	switch prefix {
	case "project", "person", "procedure":
		return prefix
	}
	return "topic"
}

// pageEntity is the entity whose page records changes of f: its first entity, else the
// first part of its attribute.
func pageEntity(f Fact) string {
	for _, e := range f.EntityIDs {
		if EntitySlug(e) != "" {
			return e
		}
	}
	if f.Attribute != "" {
		head, _, _ := strings.Cut(f.Attribute, ".")
		if EntitySlug(head) != "" {
			return head
		}
	}
	return ""
}

// entityPage returns the page of entity in scope, creating an empty one when missing.
func (s *Service) entityPage(ctx context.Context, tx execer, envID, scope, entity string) (Page, []int64, error) {
	slug := EntitySlug(entity)
	if slug == "" {
		return Page{}, nil, fmt.Errorf("%w: entity %q makes no page slug", ErrInvalid, entity)
	}
	p, err := scanPage(tx.QueryRowContext(ctx, "SELECT "+pageCols+" FROM memory_pages WHERE environment_id = ? AND scope = ? AND slug = ?", envID, scope, slug))
	if err == nil || !errors.Is(err, ErrNotFound) {
		return p, nil, err
	}
	id, now := newID(), s.unix()
	title := entity
	if i := strings.LastIndex(entity, "/"); i >= 0 && i < len(entity)-1 {
		title = entity[i+1:]
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO memory_pages (id, environment_id, scope, slug, title, kind, compiled, always_load, tier, author_persona, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, '', 0, 'inferred', '', ?, ?)`, id, envID, scope, slug, title, slugKind(slug), now, now); err != nil {
		return Page{}, nil, err
	}
	chunks, err := s.chunkPage(ctx, tx, envID, id, scope, title, "")
	if err != nil {
		return Page{}, nil, err
	}
	p, err = scanPage(tx.QueryRowContext(ctx, "SELECT "+pageCols+" FROM memory_pages WHERE id = ?", id))
	return p, chunks, err
}

// noteOnPage appends a timeline entry about home (and other) to the page of their entity
// in home's scope. Facts without an entity leave no entry.
func (s *Service) noteOnPage(ctx context.Context, tx execer, envID string, home, other Fact, at time.Time, text string) ([]int64, error) {
	entity := pageEntity(home)
	if entity == "" {
		entity = pageEntity(other)
	}
	if entity == "" {
		return nil, nil
	}
	p, chunks, err := s.entityPage(ctx, tx, envID, home.Scope, entity)
	if err != nil {
		return nil, err
	}
	id, now := newID(), s.unix()
	text = truncate(text, 2000)
	if _, err := tx.ExecContext(ctx, `INSERT INTO memory_timeline (id, environment_id, page_id, at, text, fact_id, source_id, author_persona, created_at)
		VALUES (?, ?, ?, ?, ?, ?, NULL, '', ?)`, id, envID, p.ID, at.Unix(), text, home.ID, now); err != nil {
		return nil, err
	}
	c, err := insertChunk(ctx, tx, envID, p.Scope, "timeline_id", id, p.Title, text)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE memory_pages SET updated_at = ? WHERE id = ?", now, p.ID); err != nil {
		return nil, err
	}
	return append(chunks, c), nil
}

// MergeFacts folds drop into keep, both of one scope: drop's sources move to keep, the
// support counts add up, keep gets the better tier, and drop is deleted. A fact that is
// part of a conflict is left alone. merged is false when there was nothing to do (drop is
// gone already, or in a conflict).
func (s *Service) MergeFacts(ctx context.Context, envID, keepID, dropID string) (merged bool, err error) {
	if keepID == dropID {
		return false, fmt.Errorf("%w: a fact cannot merge with itself", ErrInvalid)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	keep, err := factTx(ctx, tx, envID, keepID)
	if err != nil {
		return false, err
	}
	drop, err := factTx(ctx, tx, envID, dropID)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if keep.Scope != drop.Scope {
		return false, fmt.Errorf("%w: only facts of one scope merge", ErrInvalid)
	}
	var n int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM memory_conflicts WHERE fact_a IN (?, ?) OR fact_b IN (?, ?)",
		keepID, dropID, keepID, dropID).Scan(&n); err != nil {
		return false, err
	}
	if n > 0 {
		return false, nil
	}
	tier := keep.Tier
	if tierRank(drop.Tier) > tierRank(tier) {
		tier = drop.Tier
	}
	now := s.unix()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{"INSERT OR IGNORE INTO memory_fact_sources (fact_id, source_id) SELECT ?, source_id FROM memory_fact_sources WHERE fact_id = ?", []any{keepID, dropID}},
		{"UPDATE memory_timeline SET fact_id = ? WHERE fact_id = ?", []any{keepID, dropID}},
		{"UPDATE memory_facts SET supersedes = ? WHERE supersedes = ? AND id != ?", []any{keepID, dropID, keepID}},
		{`UPDATE memory_facts SET support_count = ?, tier = ?, confidence = MAX(confidence, ?), observed_at = MAX(observed_at, ?), updated_at = ?
			WHERE id = ?`, []any{keep.SupportCount + drop.SupportCount, tier, drop.Confidence, drop.ObservedAt.Unix(), now, keepID}},
		{"DELETE FROM memory_facts WHERE id = ?", []any{dropID}},
	} {
		if _, err := tx.ExecContext(ctx, q.sql, q.args...); err != nil {
			return false, err
		}
	}
	return true, tx.Commit()
}

// Supersede marks old as replaced by new: old becomes superseded, valid until new was
// observed, and a timeline entry on the entity page in old's scope records the change.
// Within one scope new also links back to old. changed is false when old was superseded
// or retracted already.
func (s *Service) Supersede(ctx context.Context, envID, oldID, newID, reason string) (changed bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	old, err := factTx(ctx, tx, envID, oldID)
	if err != nil {
		return false, err
	}
	nf, err := factTx(ctx, tx, envID, newID)
	if err != nil {
		return false, err
	}
	if old.Status == StatusSuperseded || old.Status == StatusRetracted {
		return false, nil
	}
	if old.Scope != nf.Scope && old.Scope == SharedScope {
		return false, fmt.Errorf("%w: a persona fact cannot supersede a shared fact", ErrInvalid)
	}
	now := s.unix()
	until := nf.ObservedAt.Unix()
	if _, err := tx.ExecContext(ctx, `UPDATE memory_facts SET status = 'superseded', updated_at = ?,
		valid_until = CASE WHEN valid_until IS NULL OR valid_until > ? THEN ? ELSE valid_until END WHERE id = ?`, now, until, until, oldID); err != nil {
		return false, err
	}
	if old.Scope == nf.Scope && nf.Supersedes == "" {
		if _, err := tx.ExecContext(ctx, "UPDATE memory_facts SET supersedes = ? WHERE id = ?", oldID, newID); err != nil {
			return false, err
		}
	}
	text := fmt.Sprintf("Superseded: %q is now %q.", truncate(old.Text, 300), truncate(nf.Text, 300))
	if nf.Scope != old.Scope {
		text = fmt.Sprintf("Superseded by shared memory: %q is now %q.", truncate(old.Text, 300), truncate(nf.Text, 300))
	}
	if reason = strings.TrimSpace(reason); reason != "" {
		text += " " + truncate(reason, 500)
	}
	home := old
	if nf.Scope == old.Scope {
		home = nf // link the entry to the current fact
	}
	chunks, err := s.noteOnPage(ctx, tx, envID, home, old, nf.ObservedAt, text)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	s.enqueue(chunks...)
	return true, nil
}

// FactsCreatedBetween lists the active and disputed facts of a scope created in [from,
// to), oldest first.
func (s *Service) FactsCreatedBetween(ctx context.Context, envID, scope string, from, to time.Time) ([]Fact, error) {
	return s.queryFacts(ctx, `scope = ? AND created_at >= ? AND created_at < ? AND status IN ('active', 'disputed')
		ORDER BY created_at, id`, envID, scope, from.Unix(), to.Unix())
}

// CountFactsSince counts the facts of a scope created at or after since.
func (s *Service) CountFactsSince(ctx context.Context, envID, scope string, since time.Time) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM memory_facts WHERE environment_id = ? AND scope = ? AND created_at >= ?",
		envID, scope, since.Unix()).Scan(&n)
	return n, err
}

// RelatedFacts lists the active and disputed facts in scopes that share an entity or the
// attribute with f, other than f itself, newest first.
func (s *Service) RelatedFacts(ctx context.Context, envID string, f Fact, scopes []string, limit int) ([]Fact, error) {
	if len(scopes) == 0 || (len(f.EntityIDs) == 0 && f.Attribute == "") {
		return []Fact{}, nil
	}
	args := []any{envID, f.ID}
	scopeIn := "(?" + strings.Repeat(", ?", len(scopes)-1) + ")"
	for _, sc := range scopes {
		args = append(args, sc)
	}
	var match []string
	if f.Attribute != "" {
		match = append(match, "attribute = ?")
		args = append(args, f.Attribute)
	}
	if len(f.EntityIDs) > 0 {
		match = append(match, "EXISTS (SELECT 1 FROM json_each(entity_ids) WHERE value IN (?"+strings.Repeat(", ?", len(f.EntityIDs)-1)+"))")
		for _, e := range f.EntityIDs {
			args = append(args, e)
		}
	}
	args = append(args, limit)
	return s.queryFacts(ctx, `id != ? AND scope IN `+scopeIn+` AND status IN ('active', 'disputed') AND (`+strings.Join(match, " OR ")+`)
		ORDER BY observed_at DESC, id LIMIT ?`, args...)
}

// EntityFacts lists the active and disputed facts of a scope about entity.
func (s *Service) EntityFacts(ctx context.Context, envID, scope, entity string) ([]Fact, error) {
	return s.queryFacts(ctx, `scope = ? AND status IN ('active', 'disputed') AND EXISTS (SELECT 1 FROM json_each(entity_ids) WHERE value = ?)
		ORDER BY id`, envID, scope, entity)
}

// queryFacts runs a fact query; where follows "environment_id = ? AND".
func (s *Service) queryFacts(ctx context.Context, where string, args ...any) ([]Fact, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+factCols+" FROM memory_facts WHERE environment_id = ? AND "+where, args...)
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

// Compiled truth is kept between markers that carry a hash of what Studio wrote. Text
// outside the markers is the user's and is never touched; a block whose text no longer
// matches its hash was edited by the user, so it is kept (without markers) and a fresh
// block is appended.
var compiledBlock = regexp.MustCompile(`(?s)<!-- studio:compiled sha=([0-9a-f]{8}) -->\n(.*?)\n?<!-- /studio:compiled -->`)

func shortHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:4])
}

func compiledBody(facts []Fact) string {
	sort.SliceStable(facts, func(i, j int) bool {
		a, b := facts[i], facts[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if tierRank(a.Tier) != tierRank(b.Tier) {
			return tierRank(a.Tier) > tierRank(b.Tier)
		}
		if !a.ObservedAt.Equal(b.ObservedAt) {
			return a.ObservedAt.After(b.ObservedAt)
		}
		return a.ID < b.ID
	})
	var b strings.Builder
	for _, f := range facts {
		line := strings.Join(strings.Fields(f.Text), " ")
		fmt.Fprintf(&b, "- %s (%s, %s", line, f.Tier, f.ObservedAt.Format(time.DateOnly))
		if f.Status == StatusDisputed {
			b.WriteString(", disputed")
		}
		b.WriteString(")\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// mergeCompiled puts body into the compiled truth old, keeping the user's text.
func mergeCompiled(old, body string) string {
	block := fmt.Sprintf("<!-- studio:compiled sha=%s -->\n%s\n<!-- /studio:compiled -->", shortHash(body), body)
	replaced := false
	out := compiledBlock.ReplaceAllStringFunc(old, func(m string) string {
		sub := compiledBlock.FindStringSubmatch(m)
		if sub[1] != shortHash(sub[2]) { // edited by the user: keep as plain text
			return sub[2]
		}
		if replaced {
			return ""
		}
		replaced = true
		return block
	})
	if replaced {
		return strings.TrimSpace(out)
	}
	if out = strings.TrimSpace(out); out == "" {
		return block
	}
	return out + "\n\n" + block
}

// CompileResult says what CompileEntityPage did.
type CompileResult struct {
	PageID  string `json:"pageId,omitempty"`
	Created bool   `json:"created,omitempty"`
	Changed bool   `json:"changed,omitempty"`
	Note    string `json:"note,omitempty" doc:"Why the page was left alone, e.g. the core budget"`
}

// CompileEntityPage regenerates the compiled truth of an entity's page in scope from its
// current facts. A page is created once the entity has two facts. User text and the page's
// tier and author are kept; a core page that would outgrow the budget is left as it is.
func (s *Service) CompileEntityPage(ctx context.Context, envID, scope, entity string) (CompileResult, error) {
	slug := EntitySlug(entity)
	if slug == "" {
		return CompileResult{Note: "no valid page slug"}, nil
	}
	facts, err := s.EntityFacts(ctx, envID, scope, entity)
	if err != nil {
		return CompileResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CompileResult{}, err
	}
	defer tx.Rollback()
	var res CompileResult
	var chunks []int64
	p, err := scanPage(tx.QueryRowContext(ctx, "SELECT "+pageCols+" FROM memory_pages WHERE environment_id = ? AND scope = ? AND slug = ?", envID, scope, slug))
	switch {
	case errors.Is(err, ErrNotFound):
		if len(facts) < 2 {
			return CompileResult{}, nil
		}
		if p, chunks, err = s.entityPage(ctx, tx, envID, scope, entity); err != nil {
			return CompileResult{}, err
		}
		res.Created = true
	case err != nil:
		return CompileResult{}, err
	}
	res.PageID = p.ID
	if len(facts) == 0 && !compiledBlock.MatchString(p.Compiled) {
		return res, tx.Commit()
	}
	compiled := mergeCompiled(p.Compiled, compiledBody(facts))
	if compiled == p.Compiled {
		if err := tx.Commit(); err != nil {
			return CompileResult{}, err
		}
		s.enqueue(chunks...)
		return res, nil
	}
	if len([]rune(compiled)) > 100000 {
		res.Note = "compiled truth would exceed 100000 characters"
		return res, tx.Commit()
	}
	if p.AlwaysLoad {
		if err := checkCoreBudget(ctx, tx, envID, scope, p.ID, compiled); err != nil {
			if !errors.Is(err, ErrCoreBudget) {
				return CompileResult{}, err
			}
			res.Note = err.Error()
			if err := tx.Commit(); err != nil {
				return CompileResult{}, err
			}
			s.enqueue(chunks...)
			return res, nil
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE memory_pages SET compiled = ?, updated_at = ? WHERE id = ?", compiled, s.unix(), p.ID); err != nil {
		return CompileResult{}, err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM memory_chunks WHERE page_id = ?", p.ID); err != nil {
		return CompileResult{}, err
	}
	more, err := s.chunkPage(ctx, tx, envID, p.ID, scope, p.Title, compiled)
	if err != nil {
		return CompileResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return CompileResult{}, err
	}
	s.enqueue(append(chunks, more...)...)
	res.Changed = true
	return res, nil
}

// OpenConflictsWithShared maps each fact of scope that has an open conflict with a shared
// fact to that shared fact's ID. Shared facts that are superseded or retracted don't win.
func (s *Service) OpenConflictsWithShared(ctx context.Context, envID, scope string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT fa.id, fa.scope, fb.id, fb.scope FROM memory_conflicts c
		JOIN memory_facts fa ON fa.id = c.fact_a JOIN memory_facts fb ON fb.id = c.fact_b
		WHERE c.environment_id = ? AND c.status = 'open'
		AND ((fa.scope = ? AND fb.scope = 'shared' AND fb.status IN ('active', 'disputed'))
		  OR (fb.scope = ? AND fa.scope = 'shared' AND fa.status IN ('active', 'disputed')))
		ORDER BY c.created_at, c.id`, envID, scope, scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var a, sa, b, sb string
		if err := rows.Scan(&a, &sa, &b, &sb); err != nil {
			return nil, err
		}
		if sa == SharedScope {
			a, b = b, a
		}
		if _, ok := out[a]; !ok {
			out[a] = b
		}
	}
	return out, rows.Err()
}
