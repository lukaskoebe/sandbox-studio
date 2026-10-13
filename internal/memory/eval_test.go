package memory

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

// Retrieval eval thresholds for the keyword side. They sit just under the scores measured
// on testdata/eval/corpus.json when the corpus was last changed (docs/memory.md, "Retrieval
// eval"); a change that lowers either score below them fails.
const (
	minRecallAt3 = 0.90
	minMRR       = 0.90
)

// evalCorpus is testdata/eval/corpus.json: memory items keyed by name, and the queries that
// should find them, with the keys of the items they want.
type evalCorpus struct {
	Facts []struct {
		Key  string `json:"key"`
		Kind string `json:"kind"`
		Text string `json:"text"`
	} `json:"facts"`
	Pages []struct {
		Key      string `json:"key"`
		Slug     string `json:"slug"`
		Title    string `json:"title"`
		Kind     string `json:"kind"`
		Compiled string `json:"compiled"`
	} `json:"pages"`
	Queries []struct {
		Query string   `json:"query"`
		Want  []string `json:"want"`
	} `json:"queries"`
}

// TestRetrievalEval reports recall@3 and MRR over the eval corpus and fails below the
// thresholds. It measures the keyword side only: the fixture's embedder is never prepared,
// as in every unit test, so the embedding model is not needed.
func TestRetrievalEval(t *testing.T) {
	raw, err := os.ReadFile("testdata/eval/corpus.json")
	if err != nil {
		t.Fatal(err)
	}
	var c evalCorpus
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	f := newFixture(t)
	ctx := context.Background()
	keys := map[string]string{}
	for _, fact := range c.Facts {
		keys[fact.Key] = f.fact(f.env, SharedScope, fact.Kind, fact.Text, UserOrigin).ID
	}
	for _, p := range c.Pages {
		page, err := f.svc.CreatePage(ctx, f.env, PageInput{
			Scope: SharedScope, Slug: p.Slug, Title: p.Title, Kind: p.Kind, Compiled: p.Compiled,
		}, UserOrigin)
		if err != nil {
			t.Fatal(err)
		}
		keys[p.Key] = page.ID
	}
	names := map[string]string{}
	for k, id := range keys {
		names[id] = k
	}

	var recall, mrr float64
	for _, q := range c.Queries {
		want := map[string]bool{}
		for _, k := range q.Want {
			id, ok := keys[k]
			if !ok {
				t.Fatalf("query %q wants unknown item %q", q.Query, k)
			}
			want[id] = true
		}
		hits := f.search(f.env, SearchRequest{Query: q.Query, Limit: 10})
		found, first := 0, 0.0
		var top []string
		for rank, h := range hits {
			if rank < 3 {
				top = append(top, names[h.ID])
			}
			if want[h.ID] {
				if first == 0 {
					first = 1 / float64(rank+1)
				}
				if rank < 3 {
					found++
				}
			}
		}
		recall += float64(found) / float64(len(want))
		mrr += first
		if found < len(want) {
			t.Logf("%q: want %v, top 3 %v", q.Query, q.Want, top)
		}
	}
	n := float64(len(c.Queries))
	recall, mrr = recall/n, mrr/n
	t.Logf("keyword side over %d items and %d queries: recall@3 %.3f, MRR %.3f",
		len(c.Facts)+len(c.Pages), len(c.Queries), recall, mrr)
	if recall < minRecallAt3 {
		t.Errorf("recall@3 %.3f is below %.2f", recall, minRecallAt3)
	}
	if mrr < minMRR {
		t.Errorf("MRR %.3f is below %.2f", mrr, minMRR)
	}
}
