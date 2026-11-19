package gitreview

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A page of 100 pull requests from a large GitHub repository runs to a few MiB, each carrying
// its head and base repositories: it is read whole. An answer past the bound fails, rather
// than being decoded cut short.
func TestForgeAnswersAreReadWholeOrRefused(t *testing.T) {
	page := func(pad int) []byte {
		var list []map[string]any
		for i := 1; i <= 100; i++ {
			list = append(list, map[string]any{"number": i, "title": "PR", "state": "open", "html_url": "https://github.com/acme/web/pull/1",
				"head": map[string]any{"ref": "feature", "repo": map[string]any{"description": strings.Repeat("x", pad)}}})
		}
		b, _ := json.Marshal(list)
		return b
	}
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) }))
	t.Cleanup(srv.Close)
	g := &GitHub{BaseURL: srv.URL, Client: srv.Client()}
	ctx := context.Background()

	body = page(30 << 10) // about 3 MiB, as kubernetes/kubernetes's page is
	prs, err := g.ListPullRequests(ctx, "t", "acme", "web", "open", 100)
	if err != nil || len(prs) != 100 {
		t.Fatalf("a 3 MiB page: %d pull requests, %v", len(prs), err)
	}
	body = page(90 << 10)
	if _, err := g.ListPullRequests(ctx, "t", "acme", "web", "open", 100); err == nil || !strings.Contains(err.Error(), "larger than 8 MiB") {
		t.Fatalf("a 9 MiB page: %v", err)
	}
}
