package webauth

import (
	"net/http"
	"strings"
	"testing"
)

// TestKeyedHostLetsInItsOwnPreviewOnly: a preview host that carries its key needs no cookie,
// keeps the same key, and a wrong or moved key gets nothing.
func TestKeyedHostLetsInItsOwnPreviewOnly(t *testing.T) {
	a := testAuth(t)
	host, err := a.KeyedHost("3000-ABC.localhost:7878")
	if err != nil {
		t.Fatal(err)
	}
	again, _ := a.KeyedHost("3000-abc.localhost:7878")
	first, _, _ := strings.Cut(host, ".")
	if host != again || !strings.HasPrefix(host, "3000-abc-") || !strings.HasSuffix(host, ".localhost:7878") || len(first) > 63 {
		t.Fatalf("keyed host %q, again %q", host, again)
	}
	if _, err := a.KeyedHost(host); err == nil {
		t.Fatal("a keyed host took a second key")
	}
	if _, err := a.KeyedHost("localhost:7878"); err == nil {
		t.Fatal("Studio's own host took a key")
	}
	if rec := request(a, http.MethodGet, "http://"+host+"/app.js", host, nil, "", downstream()); rec.Code != http.StatusNoContent {
		t.Fatalf("keyed host without a cookie: %d", rec.Code)
	}
	other, _ := a.KeyedHost("3001-abc.localhost:7878")
	_, otherKey, _ := strings.Cut(strings.SplitN(other, ".", 2)[0], "abc-")
	for _, bad := range []string{
		"3000-abc-" + otherKey + ".localhost:7878", // another port's key
		"3000-abc-aaaaaaaaaaaaaaaaaaaaaaaaaa.localhost:7878",
		strings.Replace(host, ":7878", ":7879", 1), // another Studio port
	} {
		if rec := request(a, http.MethodGet, "http://"+bad+"/", bad, nil, "", downstream()); rec.Code != http.StatusForbidden {
			t.Errorf("%s: %d, want 403", bad, rec.Code)
		}
	}
	// The plain host still needs its cookie.
	if rec := request(a, http.MethodGet, "http://3000-abc.localhost:7878/", "3000-abc.localhost:7878", nil, "", downstream()); rec.Code != http.StatusForbidden {
		t.Fatalf("plain host without a cookie: %d", rec.Code)
	}
	// Its worker patch comes from the keyed host too.
	if rec := request(a, http.MethodGet, "http://"+host+WorkerPatchPath, host, nil, "", downstream()); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), a.workerToken(host)) {
		t.Fatalf("worker patch on a keyed host: %d", rec.Code)
	}
}
