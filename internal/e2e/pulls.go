//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// The upstream's Forgejo API: the account e2e-user with the repositories acme/web and acme/api,
// and on every repository a pull request 1, "Apply coupon codes", opened by someone else from
// feature/coupons, whose CI passed.
// Reviews and conversation comments posted to it are kept for the tests (GET /api/e2e/reviews),
// and so are the pull requests opened on it (GET /api/e2e/pulls), numbered from 2. Every
// repository's default branch is main.
// A GitHub forge's REST API (/api/v3) answers as the Forgejo one, and pull request 1 has a
// review thread on web/coupon.ts:22 there, which its GraphQL API (/api/graphql) resolves;
// a resolved thread is kept with the reviews, as {"resolved": id}.

const pullDiff = `diff --git a/web/coupon.ts b/web/coupon.ts
--- a/web/coupon.ts
+++ b/web/coupon.ts
@@ -20,4 +20,5 @@ export async function applyCoupon(cart: Cart, code: string) {
 export async function applyCoupon(cart: Cart, code: string) {
-  const total = await finalTotal(cart)
+  const total = cart.subtotal
+  const coupon = await validateCoupon(code, total)
   if (!coupon) throw new CouponError("unknown code")
 }
`

// PostedReview is a review posted to the upstream's pull request.
type PostedReview struct {
	Repo string         `json:"repo"`
	Body map[string]any `json:"body"`
}

var pullsPath = regexp.MustCompile(`^/api/v1/repos/([^/]+)/([^/]+)/pulls$`)

var repoPath = regexp.MustCompile(`^/api/v1/repos/[^/]+/[^/]+$`)

// statusPath is a commit's combined status; every commit's CI passed.
var statusPath = regexp.MustCompile(`^/api/v1/repos/[^/]+/[^/]+/commits/[^/]+/status$`)

var pullPath = regexp.MustCompile(`^/api/v[13]/repos/([^/]+)/([^/]+)/(pulls|issues)/1(\.diff|/commits|/files|/reviews|/comments)?$`)

type pulls struct {
	mu     sync.Mutex
	posted []PostedReview
	opened []PostedReview
	// threads are the review threads GraphQL named, by ID: their repository and whether
	// they are resolved.
	threads map[string]*thread
}

type thread struct {
	repo     string
	resolved bool
}

func (p *pulls) reviews() []PostedReview {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]PostedReview{}, p.posted...)
}

func (p *pulls) openedPulls() []PostedReview {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]PostedReview{}, p.opened...)
}

// serve answers the Forgejo API requests it knows, and the user of GitHub's and GitLab's,
// which a forge's token is checked with; ok is false for the rest. A token with rejected in it
// is refused.
func (p *pulls) serve(w http.ResponseWriter, r *http.Request) (ok bool) {
	send := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	if strings.Contains(r.Header.Get("Authorization"), "rejected") {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"message":"Bad credentials"}`)
		return true
	}
	switch r.URL.Path {
	case "/user", "/api/v3/user":
		send(map[string]string{"login": "e2e-user"})
		return true
	case "/api/v4/user":
		send(map[string]string{"username": "e2e-user"})
		return true
	}
	if r.URL.Path == "/api/graphql" && r.Method == http.MethodPost {
		p.graphql(w, r)
		return true
	}
	if !strings.HasPrefix(r.URL.Path, "/api/v1/") && !strings.HasPrefix(r.URL.Path, "/api/v3/") {
		return false
	}
	// Every host is a Forgejo to the forge dialog's detection.
	if r.URL.Path == "/api/v1/version" {
		send(map[string]string{"version": "11.0.1+gitea-1.22.0"})
		return true
	}
	if r.URL.Path == "/api/v1/user" {
		send(map[string]string{"login": "e2e-user"})
		return true
	}
	if r.URL.Path == "/api/v1/user/repos" {
		send([]map[string]any{{"full_name": "acme/web", "description": "The shop"}, {"full_name": "acme/api", "private": true}})
		return true
	}
	if repoPath.MatchString(r.URL.Path) {
		send(map[string]string{"default_branch": "main"})
		return true
	}
	if m := pullsPath.FindStringSubmatch(r.URL.Path); m != nil {
		repo := hostOnly(r.Host) + "/" + m[1] + "/" + m[2]
		if r.Method == http.MethodPost {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			p.mu.Lock()
			p.opened = append(p.opened, PostedReview{Repo: repo, Body: body})
			n := len(p.opened) + 1
			p.mu.Unlock()
			send(map[string]any{"number": n, "html_url": "https://" + repo + "/pulls/" + strconv.Itoa(n)})
			return true
		}
		if r.URL.Query().Get("state") == "closed" {
			send([]any{})
			return true
		}
		send([]map[string]any{{"number": 1, "title": "Apply coupon codes", "state": "open",
			"html_url": "https://" + repo + "/pulls/1", "user": map[string]string{"login": "someone"},
			"base": map[string]string{"ref": "main"}, "head": map[string]string{"ref": "feature/coupons"},
			"updated_at": "2026-10-27T09:00:00Z"}})
		return true
	}
	if statusPath.MatchString(r.URL.Path) {
		send(map[string]any{"state": "success", "statuses": []map[string]any{{"context": "ci/test", "status": "success", "description": "All tests passed"}}})
		return true
	}
	m := pullPath.FindStringSubmatch(r.URL.Path)
	if m == nil {
		return false
	}
	repo := hostOnly(r.Host) + "/" + m[1] + "/" + m[2]
	switch m[3] + m[4] {
	case "pulls":
		send(map[string]any{"number": 1, "title": "Apply coupon codes", "state": "open",
			"html_url": "https://" + repo + "/pulls/1", "user": map[string]string{"login": "someone"},
			"base": map[string]string{"ref": "main", "sha": "b0b0b0"}, "head": map[string]string{"ref": "feature/coupons", "sha": "c0ffee"}})
	case "pulls.diff":
		_, _ = io.WriteString(w, pullDiff)
	case "pulls/commits":
		send([]map[string]any{{"sha": "c0ffee", "commit": map[string]any{"message": "Apply coupons", "author": map[string]string{"name": "Someone"}}}})
	case "pulls/files":
		send([]map[string]any{{"filename": "web/coupon.ts", "status": "modified", "additions": 2, "deletions": 1}})
	case "pulls/reviews":
		if r.Method != http.MethodPost {
			send([]any{})
			break
		}
		var body map[string]any
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return true
		}
		p.mu.Lock()
		p.posted = append(p.posted, PostedReview{Repo: repo, Body: body})
		n := len(p.posted)
		p.mu.Unlock()
		send(map[string]any{"id": n, "html_url": "https://" + repo + "/pulls/1#review"})
	case "pulls/comments":
		// GitHub's review comments of the whole pull request.
		send([]map[string]any{{"id": 501, "user": map[string]string{"login": "reviewer"}, "path": "web/coupon.ts",
			"body": "Validate against finalTotal(cart).", "line": 22, "original_line": 22, "side": "RIGHT", "created_at": "2026-10-27T10:00:00Z"}})
	case "issues/comments":
		if r.Method != http.MethodPost {
			send([]any{})
			break
		}
		// A conversation comment is kept with the reviews, as {"comment": body}.
		var body struct {
			Body string `json:"body"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return true
		}
		p.mu.Lock()
		p.posted = append(p.posted, PostedReview{Repo: repo, Body: map[string]any{"comment": body.Body}})
		n := len(p.posted)
		p.mu.Unlock()
		send(map[string]any{"id": n, "html_url": "https://" + repo + "/pulls/1#issuecomment-" + strconv.Itoa(n)})
	default:
		return false
	}
	return true
}

// graphql answers the two GraphQL calls Studio makes: a pull request's review threads, and
// resolving one.
func (p *pulls) graphql(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.threads == nil {
		p.threads = map[string]*thread{}
	}
	if strings.Contains(in.Query, "reviewThreads") {
		repo := hostOnly(r.Host) + "/" + fmt.Sprint(in.Variables["owner"]) + "/" + fmt.Sprint(in.Variables["repo"])
		id := ""
		for k, t := range p.threads {
			if t.repo == repo {
				id = k
			}
		}
		if id == "" {
			id = "PRRT_e2e" + strconv.Itoa(len(p.threads)+1)
			p.threads[id] = &thread{repo: repo}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": map[string]any{
			"reviewThreads": map[string]any{"nodes": []map[string]any{{"id": id, "isResolved": p.threads[id].resolved,
				"comments": map[string]any{"nodes": []map[string]any{{"databaseId": 501}}}}}}}}}})
		return
	}
	id := fmt.Sprint(in.Variables["id"])
	t, ok := p.threads[id]
	if !ok || !strings.Contains(in.Query, "resolveReviewThread") {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": nil, "errors": []map[string]any{{"message": "Could not resolve to a node with the global id of '" + id + "'"}}})
		return
	}
	t.resolved = true
	p.posted = append(p.posted, PostedReview{Repo: t.repo, Body: map[string]any{"resolved": id}})
	_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"resolveReviewThread": map[string]any{"thread": map[string]any{"isResolved": true}}}})
}
