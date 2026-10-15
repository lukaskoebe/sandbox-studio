package memory

import (
	"context"
	"os"
	"testing"
)

// TestLlamaEmbeds loads the real model from STUDIO_EMBED_ASSETS, a directory with the pinned
// assets (such as Studio's data dir's memory/), and embeds three texts: later calls must not trip
// over state the first left, and the query must be closest to its text. Without the variable it is skipped, as it needs the
// 300 MB model.
func TestLlamaEmbeds(t *testing.T) {
	dir := os.Getenv("STUDIO_EMBED_ASSETS")
	if dir == "" {
		t.Skip("STUDIO_EMBED_ASSETS not set")
	}
	l := &Llama{Assets: DefaultAssets(dir)}
	if err := l.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	a, err := l.EmbedDocument(context.Background(), "deploy", "We deploy on Fridays with make release.")
	if err != nil {
		t.Fatal(err)
	}
	other, err := l.EmbedDocument(context.Background(), "lunch", "The cafeteria serves soup on Mondays.")
	if err != nil {
		t.Fatal(err)
	}
	q, err := l.EmbedQuery(context.Background(), "when do we deploy?")
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != len(q) || len(a) == 0 {
		t.Fatalf("dims %d and %d", len(a), len(q))
	}
	dot := func(x, y []float32) (s float32) {
		for i := range x {
			s += x[i] * y[i]
		}
		return s
	}
	if dot(q, a) <= dot(q, other) {
		t.Errorf("the query is closer to the unrelated text: %.3f <= %.3f", dot(q, a), dot(q, other))
	}
}
