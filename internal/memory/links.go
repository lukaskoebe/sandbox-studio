package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Links between pages (docs/design/memory-links.md). [[slug]] or [[slug|text]] in a page's
// compiled truth, a timeline entry or a fact's text links to the page with that slug in the
// same scope, else in shared memory; a slug without "/" is also looked up as topic/<slug>, as
// an entity is. A fact's entities link it to their pages too. Links are read from the text
// when a page or the index is read, so every writer (the user, consolidation, imports,
// teammates) makes them without keeping a table in step.

var linkPattern = regexp.MustCompile(`\[\[([^\[\]|\n]{1,128})(?:\|([^\[\]\n]{1,200}))?\]\]`)

// LinkTargets returns the slugs text links to, in order and without repeats. They are
// lowercased and trimmed but not resolved: see the package comment.
func LinkTargets(text string) []string {
	var out []string
	for _, m := range linkPattern.FindAllStringSubmatch(text, -1) {
		t := strings.ToLower(strings.TrimSpace(m[1]))
		if t != "" && !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out
}

// PageLink is a link from a page to another, or to a page nobody has written yet.
type PageLink struct {
	Target  string `json:"target" doc:"The slug as the link names it"`
	Exists  bool   `json:"exists" doc:"False for a wanted page: a link to a page that doesn't exist"`
	PageID  string `json:"pageId,omitempty"`
	Scope   string `json:"scope,omitempty"`
	Slug    string `json:"slug,omitempty"`
	Title   string `json:"title,omitempty"`
	Summary string `json:"summary,omitempty" doc:"The first sentence of the page's compiled truth"`
}

// Backlink is a page or fact that links to a page.
type Backlink struct {
	Type  string    `json:"type" enum:"page,fact"`
	ID    string    `json:"id" doc:"The page or fact"`
	Scope string    `json:"scope"`
	Slug  string    `json:"slug,omitempty" doc:"Pages only"`
	Title string    `json:"title,omitempty" doc:"Pages only"`
	Text  string    `json:"text" doc:"The line with the link, or the fact"`
	Via   string    `json:"via" enum:"link,entity" doc:"A [[link]] in the text, or the fact's entity"`
	At    time.Time `json:"at"`
}

// linkSource is text that may link: a page's compiled truth, a timeline entry or a fact.
type linkSource struct {
	typ          string // page or fact
	id           string // the page (also for its timeline entries) or the fact
	scope        string
	text         string
	at           time.Time
	targets      []string
	entitySlugs  []string // facts: the slugs of their entities
	factPageSlug string   // facts: the page that records them
}

// linkGraph is every page and link source of some scopes.
type linkGraph struct {
	pages   map[string]Page // by scope + "\x00" + slug
	byID    map[string]Page
	sources []linkSource
}

func pageKey(scope, slug string) string { return scope + "\x00" + slug }

// resolve finds the page a link from scope names: in scope first, then shared.
func (g *linkGraph) resolve(scope, target string) (Page, bool) {
	cands := []string{target}
	if !strings.Contains(target, "/") {
		cands = append(cands, "topic/"+target)
	}
	for _, sc := range []string{scope, SharedScope} {
		for _, t := range cands {
			if p, ok := g.pages[pageKey(sc, t)]; ok {
				return p, true
			}
		}
	}
	return Page{}, false
}

// linksTo reports how src reaches p: "link", "entity" or "".
func (g *linkGraph) linksTo(src linkSource, p Page) string {
	if src.typ == "page" && src.id == p.ID {
		return ""
	}
	for _, t := range src.targets {
		if q, ok := g.resolve(src.scope, t); ok && q.ID == p.ID {
			return "link"
		}
	}
	for _, slug := range src.entitySlugs {
		if q, ok := g.resolve(src.scope, slug); ok && q.ID == p.ID {
			return "entity"
		}
	}
	return ""
}

// graphScopes are the scopes whose pages and sources a view of scope needs: a teammate's
// links resolve in its scope and shared; shared pages are linked from every scope.
func graphScopes(scope string) []string {
	if scope == SharedScope || scope == "" {
		return nil // every scope
	}
	return []string{scope, SharedScope}
}

func scopeFilter(col string, scopes []string) (string, []any) {
	if len(scopes) == 0 {
		return "", nil
	}
	args := make([]any, len(scopes))
	for i, s := range scopes {
		args[i] = s
	}
	return " AND " + col + " IN (?" + strings.Repeat(", ?", len(scopes)-1) + ")", args
}

// graph loads the pages and link sources of scopes (every scope when nil).
func (s *Service) graph(ctx context.Context, envID string, scopes []string) (*linkGraph, error) {
	g := &linkGraph{pages: map[string]Page{}, byID: map[string]Page{}}
	in, args := scopeFilter("scope", scopes)
	rows, err := s.db.QueryContext(ctx, "SELECT "+pageCols+" FROM memory_pages WHERE environment_id = ?"+in, append([]any{envID}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		p, err := scanPage(rows)
		if err != nil {
			return nil, err
		}
		g.pages[pageKey(p.Scope, p.Slug)] = p
		g.byID[p.ID] = p
		if t := LinkTargets(p.Compiled); len(t) > 0 {
			g.sources = append(g.sources, linkSource{typ: "page", id: p.ID, scope: p.Scope, text: p.Compiled, at: p.UpdatedAt, targets: t})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	in, args = scopeFilter("p.scope", scopes)
	rows, err = s.db.QueryContext(ctx, `SELECT t.page_id, p.scope, t.text, t.at FROM memory_timeline t JOIN memory_pages p ON p.id = t.page_id
		WHERE t.environment_id = ? AND t.text LIKE '%[[%'`+in+" ORDER BY t.at, t.rowid", append([]any{envID}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var src linkSource
		var at int64
		if err := rows.Scan(&src.id, &src.scope, &src.text, &at); err != nil {
			return nil, err
		}
		src.typ, src.at, src.targets = "page", time.Unix(at, 0).UTC(), LinkTargets(src.text)
		if len(src.targets) > 0 {
			g.sources = append(g.sources, src)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	in, args = scopeFilter("scope", scopes)
	rows, err = s.db.QueryContext(ctx, `SELECT id, scope, text, entity_ids, IFNULL(attribute, ''), updated_at FROM memory_facts
		WHERE environment_id = ? AND status IN ('active', 'disputed') AND (entity_ids != '[]' OR text LIKE '%[[%' OR IFNULL(attribute, '') != '')`+in,
		append([]any{envID}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var f Fact
		var entities string
		var updated int64
		if err := rows.Scan(&f.ID, &f.Scope, &f.Text, &entities, &f.Attribute, &updated); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(entities), &f.EntityIDs)
		src := linkSource{typ: "fact", id: f.ID, scope: f.Scope, text: f.Text, at: time.Unix(updated, 0).UTC(), targets: LinkTargets(f.Text), factPageSlug: FactPageSlug(f)}
		for _, e := range f.EntityIDs {
			if slug := EntitySlug(e); slug != "" && !slices.Contains(src.entitySlugs, slug) {
				src.entitySlugs = append(src.entitySlugs, slug)
			}
		}
		g.sources = append(g.sources, src)
	}
	return g, rows.Err()
}

// addLinks fills a page's Links and LinkedFrom.
func (s *Service) addLinks(ctx context.Context, envID string, p *Page) error {
	g, err := s.graph(ctx, envID, graphScopes(p.Scope))
	if err != nil {
		return err
	}
	g.fill(p)
	return nil
}

// fill sets p's Links, from its compiled truth and timeline, and LinkedFrom.
func (g *linkGraph) fill(p *Page) {
	texts := []string{p.Compiled}
	for _, e := range p.Timeline {
		texts = append(texts, e.Text)
	}
	p.Links = []PageLink{}
	for _, t := range LinkTargets(strings.Join(texts, "\n")) {
		l := PageLink{Target: t}
		if q, ok := g.resolve(p.Scope, t); ok {
			if q.ID == p.ID {
				continue
			}
			l = PageLink{Target: t, Exists: true, PageID: q.ID, Scope: q.Scope, Slug: q.Slug, Title: q.Title, Summary: FirstSentence(q.Compiled)}
		}
		p.Links = append(p.Links, l)
	}
	p.LinkedFrom = []Backlink{}
	seen := map[string]int{} // a page that links from its truth and its timeline is listed once, at its latest
	for _, src := range g.sources {
		via := g.linksTo(src, *p)
		if via == "" || (src.typ == "fact" && via == "entity" && src.factPageSlug == p.Slug && src.scope == p.Scope) {
			continue // a fact the page records is one of its facts, not a backlink
		}
		b := Backlink{Type: src.typ, ID: src.id, Scope: src.scope, Via: via, At: src.at, Text: linkLine(src.text, src.targets)}
		if src.typ == "page" {
			b.Slug, b.Title = g.byID[src.id].Slug, g.byID[src.id].Title
		}
		if i, ok := seen[src.id]; ok {
			if b.At.After(p.LinkedFrom[i].At) {
				p.LinkedFrom[i] = b
			}
			continue
		}
		seen[src.id] = len(p.LinkedFrom)
		p.LinkedFrom = append(p.LinkedFrom, b)
	}
	sort.SliceStable(p.LinkedFrom, func(i, j int) bool { return p.LinkedFrom[i].At.After(p.LinkedFrom[j].At) })
}

// PagesWithLinks lists the pages of a scope as Page returns one: with their timelines, links
// and backlinks, from one read of the scope's links. Core pages come first.
func (s *Service) PagesWithLinks(ctx context.Context, envID, scope string) ([]Page, error) {
	pages, err := s.Pages(ctx, envID, scope)
	if err != nil || len(pages) == 0 {
		return pages, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT t.id, t.page_id, t.at, t.text, IFNULL(t.fact_id, ''), IFNULL(t.source_id, ''), t.author_teammate, t.created_at
		FROM memory_timeline t JOIN memory_pages p ON p.id = t.page_id WHERE t.environment_id = ? AND p.scope = ? ORDER BY t.at, t.created_at, t.rowid`, envID, scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	timelines := map[string][]TimelineEntry{}
	for rows.Next() {
		var e TimelineEntry
		var at, created int64
		if err := rows.Scan(&e.ID, &e.PageID, &at, &e.Text, &e.FactID, &e.SourceID, &e.AuthorTeammate, &created); err != nil {
			return nil, err
		}
		e.At, e.CreatedAt = time.Unix(at, 0).UTC(), time.Unix(created, 0).UTC()
		timelines[e.PageID] = append(timelines[e.PageID], e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	g, err := s.graph(ctx, envID, graphScopes(scope))
	if err != nil {
		return nil, err
	}
	for i := range pages {
		pages[i].Timeline = timelines[pages[i].ID]
		if pages[i].Timeline == nil {
			pages[i].Timeline = []TimelineEntry{}
		}
		g.fill(&pages[i])
	}
	return pages, nil
}

// linkLine is the line of text with its first link, cut to 200 characters around it.
func linkLine(text string, targets []string) string {
	line := text
	if loc := linkPattern.FindStringIndex(text); loc != nil {
		start := strings.LastIndex(text[:loc[0]], "\n") + 1
		end := len(text)
		if i := strings.Index(text[loc[1]:], "\n"); i >= 0 {
			end = loc[1] + i
		}
		line = text[start:end]
		if utf8.RuneCountInString(line) > 200 && loc[0]-start > 80 {
			line = "…" + strings.TrimLeft(text[loc[0]-80:end], " ")
			if !utf8.ValidString(line) {
				line = strings.ToValidUTF8(line, "")
			}
		}
	}
	return truncate(strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "-*# ")), 200)
}

// FirstSentence is the first sentence of markdown, without headings or list marks, at most
// 160 characters.
func FirstSentence(md string) string {
	for _, line := range strings.Split(md, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "---") || strings.HasPrefix(line, "```") {
			continue
		}
		line = strings.TrimSpace(strings.TrimLeft(line, "-*>0123456789. "))
		if line == "" {
			continue
		}
		for _, end := range []string{". ", "! ", "? "} {
			if i := strings.Index(line, end); i > 0 {
				line = line[:i+1]
			}
		}
		return truncate(line, 160)
	}
	return ""
}

// PageCounts is what the pages list shows beside each page.
type PageCounts struct {
	Facts      int `json:"facts" doc:"Active facts the page records: those whose entity is the page"`
	LinkedFrom int `json:"linkedFrom" doc:"Pages and facts that link to the page"`
}

// counts fills the Facts and LinkedFrom counts of pages from one graph.
func (g *linkGraph) counts(pages []Page) {
	byID := map[string]*Page{}
	for i := range pages {
		byID[pages[i].ID] = &pages[i]
	}
	for _, src := range g.sources {
		if src.typ != "fact" || src.factPageSlug == "" {
			continue
		}
		if q, ok := g.pages[pageKey(src.scope, src.factPageSlug)]; ok && byID[q.ID] != nil {
			byID[q.ID].Counts.Facts++
		}
	}
	for id, n := range g.backlinkCounts() {
		if p := byID[id]; p != nil {
			p.Counts.LinkedFrom = n
		}
	}
}

// AddCounts fills Counts on pages of scope ("" for every scope), as the pages list shows them.
func (s *Service) AddCounts(ctx context.Context, envID, scope string, pages []Page) error {
	g, err := s.graph(ctx, envID, graphScopes(scope))
	if err != nil {
		return err
	}
	for i := range pages {
		pages[i].Counts = &PageCounts{}
	}
	g.counts(pages)
	return nil
}

// backlinkCounts counts, per page ID, the distinct pages and facts that link to it.
func (g *linkGraph) backlinkCounts() map[string]int {
	from := map[string]map[string]bool{}
	for _, src := range g.sources {
		for _, t := range append(slices.Clone(src.targets), src.entitySlugs...) {
			q, ok := g.resolve(src.scope, t)
			if !ok || (src.typ == "page" && q.ID == src.id) {
				continue
			}
			if src.typ == "fact" && q.Slug == src.factPageSlug && q.Scope == src.scope && !slices.Contains(src.targets, t) {
				continue // the page records the fact
			}
			if from[q.ID] == nil {
				from[q.ID] = map[string]bool{}
			}
			from[q.ID][src.id] = true
		}
	}
	out := make(map[string]int, len(from))
	for id, m := range from {
		out[id] = len(m)
	}
	return out
}

// BacklinkBoost raises a page in search by how many pages and facts link to it, on a log
// scale: ×1.1 for one, ×1.3 for seven.
func BacklinkBoost(n int) float64 { return 1 + 0.1*math.Log2(1+float64(n)) }

var indexKinds = []struct{ kind, heading string }{
	{"project", "Projects"}, {"topic", "Topics"}, {"person", "People"}, {"procedure", "Procedures"},
}

// Index is the catalog of a scope's pages, generated and never stored: one line per page with
// its slug, title and first sentence, grouped by kind. The teammate's own page about itself
// isn't listed; it is in the harness's instructions.
func (s *Service) Index(ctx context.Context, envID, scope string) (string, error) {
	if err := ValidateScope(scope); err != nil {
		return "", err
	}
	pages, err := s.Pages(ctx, envID, scope)
	if err != nil {
		return "", err
	}
	return indexMarkdown(pages), nil
}

func indexMarkdown(pages []Page) string {
	var b strings.Builder
	for _, k := range indexKinds {
		var lines []string
		for _, p := range pages {
			if p.Kind != k.kind {
				continue
			}
			line := fmt.Sprintf("- [[%s]] %s", p.Slug, p.Title)
			if sum := FirstSentence(p.Compiled); sum != "" {
				line += ": " + sum
			}
			lines = append(lines, line)
		}
		if len(lines) == 0 {
			continue
		}
		sort.Strings(lines)
		fmt.Fprintf(&b, "## %s\n\n%s\n\n", k.heading, strings.Join(lines, "\n"))
	}
	return strings.TrimSpace(b.String())
}

// Lint findings (docs/design/memory-links.md#6-lint). Lint reads the database and changes
// nothing; the memory panel lists the findings under Needs you.
const (
	LintWanted  = "wanted"  // a link to a page that doesn't exist
	LintOrphan  = "orphan"  // a page nothing links to, not core, untouched for 30 days
	LintUnpaged = "unpaged" // an entity with 3 or more active facts and no page
)

// OrphanAge is how long a page nothing links to stays untouched before lint names it.
const OrphanAge = 30 * 24 * time.Hour

// LintFinding is one thing in a scope's memory that wants tidying.
type LintFinding struct {
	Kind   string     `json:"kind" enum:"wanted,orphan,unpaged"`
	Slug   string     `json:"slug" doc:"The wanted page, the orphan or the entity's page"`
	PageID string     `json:"pageId,omitempty" doc:"The orphan"`
	Title  string     `json:"title,omitempty" doc:"The orphan's title"`
	Count  int        `json:"count" doc:"Wanted: the pages and facts that link to it; unpaged: its active facts"`
	From   []Backlink `json:"from,omitempty" doc:"Wanted: up to three of the pages and facts that link to it"`
}

// Lint lists a scope's wanted pages, orphans and entities without a page.
func (s *Service) Lint(ctx context.Context, envID, scope string) ([]LintFinding, error) {
	if err := ValidateScope(scope); err != nil {
		return nil, err
	}
	g, err := s.graph(ctx, envID, graphScopes(scope))
	if err != nil {
		return nil, err
	}
	out := []LintFinding{}
	wanted := map[string]*LintFinding{}
	var wantedOrder []string
	unpaged := map[string]int{}
	for _, src := range g.sources {
		if src.scope != scope {
			continue
		}
		for _, t := range src.targets {
			if _, ok := g.resolve(src.scope, t); ok {
				continue
			}
			w := wanted[t]
			if w == nil {
				w = &LintFinding{Kind: LintWanted, Slug: t}
				wanted[t] = w
				wantedOrder = append(wantedOrder, t)
			}
			w.Count++
			if len(w.From) < 3 {
				b := Backlink{Type: src.typ, ID: src.id, Scope: src.scope, Via: "link", At: src.at, Text: linkLine(src.text, src.targets)}
				if src.typ == "page" {
					b.Slug, b.Title = g.byID[src.id].Slug, g.byID[src.id].Title
				}
				w.From = append(w.From, b)
			}
		}
		if src.typ == "fact" && src.factPageSlug != "" {
			if _, ok := g.resolve(src.scope, src.factPageSlug); !ok {
				unpaged[src.factPageSlug]++
			}
		}
	}
	for _, t := range wantedOrder {
		out = append(out, *wanted[t])
	}
	links := g.backlinkCounts()
	cutoff := s.now().Add(-OrphanAge)
	var orphans []LintFinding
	for _, p := range g.pages {
		if p.Scope == scope && !p.AlwaysLoad && p.Kind != "teammate-self" && links[p.ID] == 0 && p.UpdatedAt.Before(cutoff) {
			orphans = append(orphans, LintFinding{Kind: LintOrphan, Slug: p.Slug, PageID: p.ID, Title: p.Title})
		}
	}
	sort.Slice(orphans, func(i, j int) bool { return orphans[i].Slug < orphans[j].Slug })
	out = append(out, orphans...)
	var entities []string
	for slug, n := range unpaged {
		if n >= 3 {
			entities = append(entities, slug)
		}
	}
	sort.Strings(entities)
	for _, slug := range entities {
		out = append(out, LintFinding{Kind: LintUnpaged, Slug: slug, Count: unpaged[slug]})
	}
	return out, nil
}
