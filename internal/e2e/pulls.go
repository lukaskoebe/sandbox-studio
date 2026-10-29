//go:build e2e

package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
)

// The upstream's Forgejo API: the account e2e-user with the repositories acme/web and acme/api,
// and on every repository a pull request 1, "Apply coupon codes", opened by someone else.
// Reviews posted to it are kept for the tests (GET /api/e2e/reviews).

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

var pullPath = regexp.MustCompile(`^/api/v1/repos/([^/]+)/([^/]+)/(pulls|issues)/1(\.diff|/commits|/files|/reviews|/comments)?$`)

type pulls struct {
	mu     sync.Mutex
	posted []PostedReview
}

func (p *pulls) reviews() []PostedReview {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]PostedReview{}, p.posted...)
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
	if !strings.HasPrefix(r.URL.Path, "/api/v1/") {
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
	if m := pullsPath.FindStringSubmatch(r.URL.Path); m != nil {
		repo := hostOnly(r.Host) + "/" + m[1] + "/" + m[2]
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
	case "issues/comments":
		send([]any{})
	default:
		return false
	}
	return true
}
