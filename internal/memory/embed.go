package memory

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
)

// Embedder turns text into unit-length vectors. Implementations own their prompt formats
// (embeddinggemma distinguishes queries from documents).
type Embedder interface {
	// Model names the model and its version. It is stored with every vector; vectors of
	// another model are ignored by search and re-embedded in the background.
	Model() string
	// Prepare makes the embedder usable, downloading and loading assets on first use. It
	// is called by the worker only, so the first memory write activates it lazily.
	Prepare(ctx context.Context) error
	// Ready reports whether Prepare has succeeded. Search uses BM25 alone until it has.
	Ready() bool
	EmbedDocument(ctx context.Context, title, text string) ([]float32, error)
	EmbedQuery(ctx context.Context, text string) ([]float32, error)
}

// --- int8 vectors ------------------------------------------------------------------------

// quantize stores v as int8 with one scale: v[i] ≈ q[i] * scale.
func quantize(v []float32) (float32, []byte) {
	var maxAbs float32
	for _, x := range v {
		maxAbs = max(maxAbs, float32(math.Abs(float64(x))))
	}
	q := make([]byte, len(v))
	if maxAbs == 0 {
		return 0, q
	}
	scale := maxAbs / 127
	for i, x := range v {
		q[i] = byte(int8(math.Round(float64(x / scale))))
	}
	return scale, q
}

// dotInt8 is the dot product of a float query with a quantized vector.
func dotInt8(query []float32, scale float32, q []byte) float64 {
	n := min(len(query), len(q))
	var sum float32
	for i := range n {
		sum += query[i] * float32(int8(q[i]))
	}
	return float64(sum * scale)
}

func normalize(v []float32) []float32 {
	var s float64
	for _, x := range v {
		s += float64(x) * float64(x)
	}
	if s == 0 {
		return v
	}
	n := float32(1 / math.Sqrt(s))
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = x * n
	}
	return out
}

// --- background worker -------------------------------------------------------------------

// retryDelay is how long the worker waits after the embedder failed to prepare or embed.
var retryDelay = time.Minute

// enqueue hands chunks to the worker without blocking; overflow asks for a sweep instead.
func (s *Service) enqueue(ids ...int64) {
	for _, id := range ids {
		select {
		case s.queue <- id:
		default:
			s.requestSweep()
			return
		}
	}
}

func (s *Service) requestSweep() {
	select {
	case s.sweep <- struct{}{}:
	default:
	}
}

// Run embeds chunks in the background until ctx ends. It starts with a sweep, so chunks
// written while Studio was down, or vectors of a previous model, are (re-)embedded.
func (s *Service) Run(ctx context.Context) {
	s.requestSweep()
	var retry <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-retry:
			retry = nil
			s.requestSweep()
		case id := <-s.queue:
			if err := s.embedChunks(ctx, []int64{id}); err != nil && ctx.Err() == nil {
				s.log.Warn("memory: embedding failed; retrying later", "err", err)
				retry = time.After(retryDelay)
			}
		case <-s.sweep:
			if _, err := s.EmbedPending(ctx); err != nil && ctx.Err() == nil {
				s.log.Warn("memory: embedding failed; retrying later", "err", err)
				retry = time.After(retryDelay)
			}
		}
	}
}

// EmbedPending embeds every chunk, in all environments, that lacks a vector of the current
// model. It prepares the embedder only when there is work, so an unused memory never
// downloads the model.
func (s *Service) EmbedPending(ctx context.Context) (int, error) {
	total := 0
	for {
		ids, err := s.pendingChunks(ctx, 32)
		if err != nil || len(ids) == 0 {
			return total, err
		}
		if err := s.embedChunks(ctx, ids); err != nil {
			return total, err
		}
		total += len(ids)
	}
}

func (s *Service) pendingChunks(ctx context.Context, limit int) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT c.id FROM memory_chunks c LEFT JOIN memory_embeddings e ON e.chunk_id = c.id
		WHERE e.chunk_id IS NULL OR e.model != ? ORDER BY c.id LIMIT ?`, s.emb.Model(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Service) embedChunks(ctx context.Context, ids []int64) error {
	if !s.emb.Ready() {
		s.log.Info("memory: preparing the embedding model", "model", s.emb.Model())
		if err := s.emb.Prepare(ctx); err != nil {
			return fmt.Errorf("prepare %s: %w", s.emb.Model(), err)
		}
	}
	model := s.emb.Model()
	for _, id := range ids {
		var title, text string
		var current int
		err := s.db.QueryRowContext(ctx, `SELECT c.title, c.text, COUNT(e.chunk_id) FROM memory_chunks c
			LEFT JOIN memory_embeddings e ON e.chunk_id = c.id AND e.model = ? WHERE c.id = ? GROUP BY c.id`, model, id).Scan(&title, &text, &current)
		if errors.Is(err, sql.ErrNoRows) || current > 0 {
			continue // deleted meanwhile, or already embedded
		}
		if err != nil {
			return err
		}
		v, err := s.emb.EmbedDocument(ctx, title, text)
		if err != nil {
			return fmt.Errorf("embed chunk %d: %w", id, err)
		}
		scale, q := quantize(v)
		// The chunk may have been deleted while embedding; the foreign key then refuses the row.
		_, err = s.db.ExecContext(ctx, `INSERT INTO memory_embeddings (chunk_id, model, dims, scale, vec, created_at)
			SELECT ?, ?, ?, ?, ?, ? WHERE EXISTS (SELECT 1 FROM memory_chunks WHERE id = ?)
			ON CONFLICT (chunk_id) DO UPDATE SET model = excluded.model, dims = excluded.dims, scale = excluded.scale,
				vec = excluded.vec, created_at = excluded.created_at`,
			id, model, len(v), scale, q, s.unix(), id)
		if err != nil {
			return err
		}
	}
	return nil
}

// --- deterministic fake ------------------------------------------------------------------

// FakeEmbedder is a deterministic embedder for tests: it hashes lowercased words, after
// mapping them through Aliases, into Dims buckets. Aliases let a test express "meaning"
// that BM25 cannot see, e.g. node → javascript.
type FakeEmbedder struct {
	Name    string
	Dims    int
	Aliases map[string]string
	Fail    error // returned by Prepare when set
	ready   atomic.Bool
}

func (f *FakeEmbedder) Model() string {
	if f.Name == "" {
		return "fake-v1"
	}
	return f.Name
}

func (f *FakeEmbedder) Prepare(context.Context) error {
	if f.Fail != nil {
		return f.Fail
	}
	f.ready.Store(true)
	return nil
}

func (f *FakeEmbedder) Ready() bool { return f.ready.Load() }

func (f *FakeEmbedder) EmbedDocument(_ context.Context, title, text string) ([]float32, error) {
	return f.embed(title + " " + text), nil
}

func (f *FakeEmbedder) EmbedQuery(_ context.Context, text string) ([]float32, error) {
	return f.embed(text), nil
}

func (f *FakeEmbedder) embed(text string) []float32 {
	dims := f.Dims
	if dims == 0 {
		dims = 64
	}
	v := make([]float32, dims)
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if a, ok := f.Aliases[w]; ok {
			w = a
		}
		h := fnv.New64a()
		h.Write([]byte(w))
		sum := h.Sum(nil)
		bucket := binary.BigEndian.Uint32(sum[:4]) % uint32(dims)
		sign := float32(1)
		if sum[4]&1 == 1 {
			sign = -1
		}
		v[bucket] += sign
	}
	return normalize(v)
}
