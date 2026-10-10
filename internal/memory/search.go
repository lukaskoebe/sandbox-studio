package memory

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Retrieval tuning. One set of values, no knobs (PLAN §2).
const (
	rrfK          = 60  // reciprocal-rank fusion constant
	candidates    = 200 // chunks taken from each ranker before fusion
	minSimilarity = 0.2 // cosine below this is not a vector match
	maxLimit      = 50
)

// tierBoost multiplies the fused score by trust tier.
var tierBoost = map[string]float64{TierUser: 1.3, TierVerified: 1.2, TierDocument: 1.1, TierInferred: 1.0}

// halfLife is how fast recency decays per fact kind: preferences hold, events fade. Pages
// decay like procedures, from their last update.
var halfLife = map[string]time.Duration{
	KindPreference: 365 * 24 * time.Hour,
	KindDecision:   180 * 24 * time.Hour,
	KindProcedure:  180 * 24 * time.Hour,
	KindFact:       90 * 24 * time.Hour,
	KindEvent:      14 * 24 * time.Hour,
	"page":         180 * 24 * time.Hour,
}

// recencyBoost is 1 for brand-new items and approaches 0.5 for very old ones.
func recencyBoost(age, half time.Duration) float64 {
	if age < 0 {
		age = 0
	}
	return 0.5 + 0.5*math.Pow(0.5, age.Hours()/half.Hours())
}

// SearchRequest is a hybrid search. Without Persona it covers the shared scope only; with
// it, that persona's private scope plus shared.
type SearchRequest struct {
	Query             string
	Persona           string
	IncludeSuperseded bool
	Limit             int
}

// Hit is one search result with the reasons for its rank.
type Hit struct {
	Type         string  `json:"type" enum:"fact,page"`
	ID           string  `json:"id"`
	Scope        string  `json:"scope"`
	Snippet      string  `json:"snippet" doc:"The best matching chunk"`
	Score        float64 `json:"score"`
	Disputed     bool    `json:"disputed,omitempty" doc:"The fact is part of an open conflict; treat it with care"`
	Superseded   bool    `json:"superseded,omitempty" doc:"Only with includeSuperseded: the fact was replaced or expired"`
	OverriddenBy string  `json:"overriddenBy,omitempty" doc:"A shared fact this persona fact has an open conflict with; the shared fact wins until the user resolves it"`
	Fact         *Fact   `json:"fact,omitempty"`
	Page         *Page   `json:"page,omitempty"`
	Why          Why     `json:"why"`
}

// Why explains a hit's score: score = rrf × tierBoost × recencyBoost.
type Why struct {
	BM25Rank     int     `json:"bm25Rank,omitempty" doc:"1-based rank among keyword matches; absent when it did not match"`
	VectorRank   int     `json:"vectorRank,omitempty" doc:"1-based rank among vector matches; absent when it did not match"`
	Similarity   float64 `json:"similarity,omitempty"`
	Vector       string  `json:"vector" enum:"used,not_ready,not_embedded" doc:"not_ready: the embedding model is not loaded yet; not_embedded: this item has no vector yet. Both fall back to BM25 alone."`
	RRF          float64 `json:"rrf"`
	Tier         string  `json:"tier"`
	TierBoost    float64 `json:"tierBoost"`
	AgeDays      float64 `json:"ageDays"`
	HalfLifeDays float64 `json:"halfLifeDays"`
	RecencyBoost float64 `json:"recencyBoost"`
	Summary      string  `json:"summary"`
}

type candidate struct {
	hit      Hit
	chunk    string // text of the best chunk
	embedded bool
	age      time.Duration
	half     time.Duration
}

type chunkHit struct {
	chunkID int64
	owner   string // "fact:<id>" or "page:<id>"
	text    string
	sim     float64
}

// ftsQuery turns free text into an FTS5 OR-query of quoted terms.
func ftsQuery(q string) string {
	seen := map[string]bool{}
	var terms []string
	for _, w := range strings.FieldsFunc(strings.ToLower(q), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if !seen[w] {
			seen[w] = true
			terms = append(terms, `"`+w+`"`)
		}
	}
	return strings.Join(terms, " OR ")
}

// Search runs BM25 and vector retrieval over the request's scopes, fuses them with
// reciprocal-rank fusion and boosts by tier and recency. Retracted facts never match;
// superseded and expired ones only on request; disputed ones carry a marker.
func (s *Service) Search(ctx context.Context, envID string, req SearchRequest) ([]Hit, error) {
	scopes := []string{SharedScope}
	if req.Persona != "" {
		if err := validatePersona(req.Persona); err != nil {
			return nil, err
		}
		scopes = append(scopes, PersonaScope(req.Persona))
	}
	if req.Limit <= 0 {
		req.Limit = 10
	}
	req.Limit = min(req.Limit, maxLimit)
	match := ftsQuery(req.Query)
	if match == "" {
		return nil, fmt.Errorf("%w: the query has no words", ErrInvalid)
	}
	scopeIn := "(?" + strings.Repeat(", ?", len(scopes)-1) + ")"
	scopeArgs := make([]any, len(scopes))
	for i, sc := range scopes {
		scopeArgs[i] = sc
	}

	bm25, err := s.bm25(ctx, envID, match, scopeIn, scopeArgs)
	if err != nil {
		return nil, err
	}
	vectorState := "used"
	var vec []chunkHit
	embedded := map[string]bool{}
	if s.emb != nil && s.emb.Ready() {
		qv, err := s.emb.EmbedQuery(ctx, req.Query)
		if err != nil {
			return nil, err
		}
		if vec, err = s.vectors(ctx, envID, scopeIn, scopeArgs, qv, embedded); err != nil {
			return nil, err
		}
	} else {
		vectorState = "not_ready"
	}

	cands := map[string]*candidate{}
	var order []string
	for _, h := range append(append([]chunkHit{}, bm25...), vec...) {
		if _, ok := cands[h.owner]; !ok {
			cands[h.owner] = &candidate{chunk: h.text}
			order = append(order, h.owner)
		}
	}
	if err := s.loadOwners(ctx, envID, order, cands, req.IncludeSuperseded); err != nil {
		return nil, err
	}

	// Ranks count owners, not chunks, and skip owners that were filtered out.
	rank := func(list []chunkHit, set func(c *candidate, rank int, h chunkHit)) {
		r := 0
		seen := map[string]bool{}
		for _, h := range list {
			c := cands[h.owner]
			if c == nil || seen[h.owner] {
				continue
			}
			seen[h.owner] = true
			r++
			set(c, r, h)
		}
	}
	rank(bm25, func(c *candidate, r int, h chunkHit) { c.hit.Why.BM25Rank, c.chunk = r, h.text })
	rank(vec, func(c *candidate, r int, h chunkHit) {
		c.hit.Why.VectorRank, c.hit.Why.Similarity = r, round(h.sim, 4)
		if c.hit.Why.BM25Rank == 0 {
			c.chunk = h.text
		}
	})

	out := make([]Hit, 0, len(cands))
	for _, key := range order {
		c := cands[key]
		if c == nil {
			continue
		}
		w := &c.hit.Why
		if w.BM25Rank > 0 {
			w.RRF += 1.0 / float64(rrfK+w.BM25Rank)
		}
		if w.VectorRank > 0 {
			w.RRF += 1.0 / float64(rrfK+w.VectorRank)
		}
		switch {
		case vectorState != "used":
			w.Vector = vectorState
		case !embedded[key]:
			w.Vector = "not_embedded"
		default:
			w.Vector = "used"
		}
		w.TierBoost = tierBoost[w.Tier]
		w.RecencyBoost = recencyBoost(c.age, c.half)
		w.AgeDays = round(c.age.Hours()/24, 1)
		w.HalfLifeDays = c.half.Hours() / 24
		c.hit.Score = round(w.RRF*w.TierBoost*w.RecencyBoost, 6)
		w.RRF = round(w.RRF, 6)
		w.RecencyBoost = round(w.RecencyBoost, 4)
		w.Summary = summarize(c.hit)
		c.hit.Snippet = truncate(c.chunk, 300)
		out = append(out, c.hit)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if req.Persona != "" {
		if out, err = s.sharedWins(ctx, envID, PersonaScope(req.Persona), out); err != nil {
			return nil, err
		}
	}
	if len(out) > req.Limit {
		out = out[:req.Limit]
	}
	return out, nil
}

// sharedWins applies "shared wins" to sorted hits: a persona fact with an open conflict
// against a shared fact is marked disputed and overridden, and the shared fact is placed
// right above it, pulled in if the search did not find it.
func (s *Service) sharedWins(ctx context.Context, envID, scope string, hits []Hit) ([]Hit, error) {
	winners, err := s.OpenConflictsWithShared(ctx, envID, scope)
	if err != nil || len(winners) == 0 {
		return hits, err
	}
	for i := 0; i < len(hits); i++ {
		h := hits[i]
		winner, ok := winners[h.ID]
		if h.Type != "fact" || h.Scope != scope || !ok || h.OverriddenBy != "" {
			continue
		}
		hits[i].Disputed, hits[i].OverriddenBy = true, winner
		hits[i].Why.Summary += "; overridden: an open conflict with shared fact " + winner + ", which wins until resolved"
		at := slices.IndexFunc(hits, func(x Hit) bool { return x.Type == "fact" && x.ID == winner })
		var w Hit
		switch {
		case at >= 0 && at < i:
			continue
		case at > i:
			w = hits[at]
			hits = slices.Delete(hits, at, at+1)
		default:
			f, err := s.Fact(ctx, envID, winner)
			if errors.Is(err, ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			w = Hit{Type: "fact", ID: f.ID, Scope: f.Scope, Snippet: truncate(f.Text, 300), Score: h.Score, Disputed: f.Status == StatusDisputed, Fact: &f,
				Why: Why{Vector: h.Why.Vector, Tier: f.Tier, TierBoost: tierBoost[f.Tier]}}
			w.Why.Summary = "added: wins an open conflict with persona fact " + h.ID
		}
		w.Score = max(w.Score, h.Score)
		w.Why.Summary += "; shared fact wins an open conflict with persona fact " + h.ID
		hits = slices.Insert(hits, i, w)
		i++
	}
	return hits, nil
}

func (s *Service) bm25(ctx context.Context, envID, match, scopeIn string, scopeArgs []any) ([]chunkHit, error) {
	args := append([]any{match, envID}, scopeArgs...)
	rows, err := s.db.QueryContext(ctx, `SELECT c.id, IFNULL(c.fact_id, ''), IFNULL(c.page_id, IFNULL(t.page_id, '')), c.text
		FROM memory_fts JOIN memory_chunks c ON c.id = memory_fts.rowid
		LEFT JOIN memory_timeline t ON t.id = c.timeline_id
		WHERE memory_fts MATCH ? AND c.environment_id = ? AND c.scope IN `+scopeIn+`
		ORDER BY bm25(memory_fts), c.id LIMIT `+fmt.Sprint(candidates), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []chunkHit
	for rows.Next() {
		var h chunkHit
		var fact, page string
		if err := rows.Scan(&h.chunkID, &fact, &page, &h.text); err != nil {
			return nil, err
		}
		h.owner = ownerKey(fact, page)
		out = append(out, h)
	}
	return out, rows.Err()
}

// vectors scans every current-model vector of the scopes in memory (brute force; fine to
// about 100k chunks) and returns the best matches. embedded collects the owners that have
// at least one vector.
func (s *Service) vectors(ctx context.Context, envID, scopeIn string, scopeArgs []any, qv []float32, embedded map[string]bool) ([]chunkHit, error) {
	args := append([]any{s.emb.Model(), envID}, scopeArgs...)
	rows, err := s.db.QueryContext(ctx, `SELECT c.id, IFNULL(c.fact_id, ''), IFNULL(c.page_id, IFNULL(t.page_id, '')), c.text, e.scale, e.vec
		FROM memory_embeddings e JOIN memory_chunks c ON c.id = e.chunk_id
		LEFT JOIN memory_timeline t ON t.id = c.timeline_id
		WHERE e.model = ? AND c.environment_id = ? AND c.scope IN `+scopeIn, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []chunkHit
	for rows.Next() {
		var h chunkHit
		var fact, page string
		var scale float64
		var q []byte
		if err := rows.Scan(&h.chunkID, &fact, &page, &h.text, &scale, &q); err != nil {
			return nil, err
		}
		h.owner = ownerKey(fact, page)
		embedded[h.owner] = true
		if h.sim = dotInt8(qv, float32(scale), q); h.sim >= minSimilarity {
			out = append(out, h)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].sim != out[j].sim {
			return out[i].sim > out[j].sim
		}
		return out[i].chunkID < out[j].chunkID
	})
	if len(out) > candidates {
		out = out[:candidates]
	}
	return out, nil
}

func ownerKey(fact, page string) string {
	if fact != "" {
		return "fact:" + fact
	}
	return "page:" + page
}

// loadOwners fills the candidates' facts and pages and removes those the request excludes.
func (s *Service) loadOwners(ctx context.Context, envID string, keys []string, cands map[string]*candidate, includeSuperseded bool) error {
	now := s.now()
	for _, key := range keys {
		c := cands[key]
		typ, id, _ := strings.Cut(key, ":")
		c.hit.Type, c.hit.ID = typ, id
		if typ == "fact" {
			f, err := s.Fact(ctx, envID, id)
			if errors.Is(err, ErrNotFound) { // deleted meanwhile
				delete(cands, key)
				continue
			}
			if err != nil {
				return err
			}
			expired := f.ValidUntil != nil && !f.ValidUntil.After(now)
			superseded := f.Status == StatusSuperseded || expired
			if f.Status == StatusRetracted || (superseded && !includeSuperseded) {
				delete(cands, key)
				continue
			}
			c.hit.Scope, c.hit.Fact = f.Scope, &f
			c.hit.Disputed, c.hit.Superseded = f.Status == StatusDisputed, superseded
			c.hit.Why.Tier = f.Tier
			c.age, c.half = now.Sub(f.ObservedAt), halfLife[f.Kind]
			continue
		}
		p, err := scanPage(s.db.QueryRowContext(ctx, "SELECT "+pageCols+" FROM memory_pages WHERE environment_id = ? AND id = ?", envID, id))
		if errors.Is(err, ErrNotFound) {
			delete(cands, key)
			continue
		}
		if err != nil {
			return err
		}
		c.hit.Scope, c.hit.Page = p.Scope, &p
		c.hit.Why.Tier = p.Tier
		c.age, c.half = now.Sub(p.UpdatedAt), halfLife["page"]
	}
	return nil
}

func summarize(h Hit) string {
	var parts []string
	if h.Why.BM25Rank > 0 {
		parts = append(parts, fmt.Sprintf("keyword match #%d", h.Why.BM25Rank))
	}
	if h.Why.VectorRank > 0 {
		parts = append(parts, fmt.Sprintf("meaning match #%d (similarity %.2f)", h.Why.VectorRank, h.Why.Similarity))
	}
	switch h.Why.Vector {
	case "not_ready":
		parts = append(parts, "vector search off until the embedding model is ready")
	case "not_embedded":
		parts = append(parts, "not embedded yet, keyword only")
	}
	parts = append(parts, fmt.Sprintf("tier %s ×%.2f", h.Why.Tier, h.Why.TierBoost),
		fmt.Sprintf("%.0f days old ×%.2f", h.Why.AgeDays, h.Why.RecencyBoost))
	if h.Disputed {
		parts = append(parts, "disputed: an open conflict involves this fact")
	}
	if h.Superseded {
		parts = append(parts, "superseded")
	}
	return strings.Join(parts, "; ")
}

func round(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}
