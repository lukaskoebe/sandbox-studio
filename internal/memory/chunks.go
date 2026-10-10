package memory

import (
	"context"
	"strings"
)

// maxChunkRunes keeps chunks well inside the embedding model's context (2048 tokens).
const maxChunkRunes = 1200

func insertChunk(ctx context.Context, tx execer, envID, scope, ownerCol, ownerID, title, text string) (int64, error) {
	res, err := tx.ExecContext(ctx, "INSERT INTO memory_chunks (environment_id, scope, "+ownerCol+", title, text) VALUES (?, ?, ?, ?, ?)",
		envID, scope, ownerID, title, text)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Service) chunkFact(ctx context.Context, tx execer, envID, id, scope, attribute, text string) (int64, error) {
	return insertChunk(ctx, tx, envID, scope, "fact_id", id, attribute, text)
}

func (s *Service) chunkPage(ctx context.Context, tx execer, envID, id, scope, title, compiled string) ([]int64, error) {
	parts := splitChunks(compiled)
	if len(parts) == 0 {
		parts = []string{title}
	}
	ids := make([]int64, 0, len(parts))
	for _, p := range parts {
		cid, err := insertChunk(ctx, tx, envID, scope, "page_id", id, title, p)
		if err != nil {
			return nil, err
		}
		ids = append(ids, cid)
	}
	return ids, nil
}

// splitChunks groups paragraphs into chunks of at most maxChunkRunes, splitting a longer
// paragraph at word boundaries.
func splitChunks(text string) []string {
	var out []string
	var cur []rune
	flush := func() {
		if s := strings.TrimSpace(string(cur)); s != "" {
			out = append(out, s)
		}
		cur = cur[:0]
	}
	for _, para := range strings.Split(text, "\n\n") {
		p := []rune(strings.TrimSpace(para))
		if len(p) == 0 {
			continue
		}
		if len(cur) > 0 && len(cur)+2+len(p) > maxChunkRunes {
			flush()
		}
		for len(p) > maxChunkRunes {
			cut := maxChunkRunes
			for i := maxChunkRunes; i > maxChunkRunes/2; i-- {
				if p[i] == ' ' || p[i] == '\n' {
					cut = i
					break
				}
			}
			cur = append(cur, p[:cut]...)
			flush()
			p = []rune(strings.TrimSpace(string(p[cut:])))
		}
		if len(cur) > 0 {
			cur = append(cur, '\n', '\n')
		}
		cur = append(cur, p...)
	}
	flush()
	return out
}
