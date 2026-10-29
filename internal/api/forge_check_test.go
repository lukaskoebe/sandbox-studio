package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/lukaskoebe/sandbox-studio/internal/gitreview"
)

// fakeForges answers for any host it is dialed as: git.example.org is a Forgejo,
// lab.example.org a GitLab and ghe.example.org a GitHub Enterprise Server, each taking the
// token good; www.example.org is a web site.
func fakeForges(t *testing.T) *http.Client {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, _ := strings.Cut(r.Host, ":")
		good := strings.HasSuffix(r.Header.Get("Authorization"), " good")
		send := func(status int, v any) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(v)
		}
		switch host + r.URL.Path {
		case "git.example.org/api/v1/version":
			send(http.StatusOK, map[string]string{"version": "11.0.1+gitea-1.22.0"})
		case "git.example.org/api/v1/user":
			if !good {
				send(http.StatusUnauthorized, map[string]string{"message": "user does not exist [uid: 0, name: ]"})
				return
			}
			send(http.StatusOK, map[string]string{"login": "ada"})
		case "lab.example.org/api/v4/version":
			send(http.StatusUnauthorized, map[string]string{"message": "401 Unauthorized"})
		case "ghe.example.org/api/v3/meta":
			send(http.StatusOK, map[string]any{"verifiable_password_authentication": false})
		case "www.example.org/api/v1/user":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<!doctype html><title>Welcome</title>"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	tr := srv.Client().Transport.(*http.Transport).Clone()
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, srv.Listener.Addr().String())
	}
	tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // a loopback fake
	return &http.Client{Transport: tr}
}

func TestForgeCheck(t *testing.T) {
	h, s := newTestServer(t)
	s.Git = &gitreview.Service{Store: s.Store, Secrets: s.Vault, Bus: s.Bus, Dir: t.TempDir(), Client: fakeForges(t)}
	env := newEnvironment(t, s, "work")
	base := testOrigin + "/api/environments/" + env.ID + "/forges"

	// A token the forge refuses, or an address that isn't the forge's API, is a 422 the form
	// can offer to skip.
	for _, body := range []string{
		`{"name":"fj","kind":"forgejo","baseUrl":"https://git.example.org","token":"bad"}`,
		`{"name":"fj","kind":"forgejo","baseUrl":"https://www.example.org","token":"good"}`,
		`{"name":"fj","kind":"forgejo","baseUrl":"https://lab.example.org","token":"good"}`,
	} {
		rec := do(h, "POST", base, body)
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `"location":"body.skipCheck"`) {
			t.Fatalf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}
	if forges, _ := s.Store.Forges(context.Background(), env.ID); len(forges) != 0 {
		t.Fatalf("a refused forge was added: %+v", forges)
	}
	if rec := do(h, "POST", base, `{"name":"fj","kind":"forgejo","baseUrl":"https://git.example.org","token":"good"}`); rec.Code != http.StatusCreated {
		t.Fatalf("good token: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", base, `{"name":"later","kind":"forgejo","baseUrl":"https://www.example.org","token":"x","skipCheck":true}`); rec.Code != http.StatusCreated {
		t.Fatalf("skipped check: %d %s", rec.Code, rec.Body)
	}
	// An SSH forge's API is checked too, when it is created and when it is given one.
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	sp, _ := ssh.NewPublicKey(pub)
	hk, _ := json.Marshal(strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sp))))
	sshForge := `{"name":"fjssh","kind":"ssh","baseUrl":"git@git.example.org","hostKey":` + string(hk) + `,"apiKind":"forgejo","apiUrl":"https://git.example.org","token":"`
	if rec := do(h, "POST", base, sshForge+`bad"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("ssh api, bad token: %d %s", rec.Code, rec.Body)
	}
	rec := do(h, "POST", base, sshForge+`good"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("ssh api: %d %s", rec.Code, rec.Body)
	}
	f := decode[struct{ ID string }](t, rec)
	if rec := do(h, "PUT", base+"/"+f.ID+"/api", `{"kind":"gitlab","baseUrl":"https://lab.example.org","token":"good"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("set api, wrong kind: %d %s", rec.Code, rec.Body)
	}

	// With detect, a host Studio doesn't know says which forge it runs.
	for _, tc := range []struct{ url, kind, apiKind, apiURL string }{
		{"https://git.example.org/team/app", gitreview.KindForgejo, "", ""},
		{"https://lab.example.org/group/sub/app.git", gitreview.KindGitLab, "", ""},
		{"https://ghe.example.org/team/app/tree/main", gitreview.KindGitHub, "", ""},
		{"https://www.example.org/team/app.git", gitreview.KindHTTPS, "", ""},
		{"git@git.example.org:team/app.git", gitreview.KindSSH, gitreview.KindForgejo, "https://git.example.org"},
		{"ssh://git@lab.example.org:2222/group/app.git", gitreview.KindSSH, gitreview.KindGitLab, "https://lab.example.org"},
	} {
		r := decode[gitreview.RepoURL](t, do(h, "POST", base+"/parse-url", `{"url":"`+tc.url+`","detect":true}`))
		if r.Kind != tc.kind || r.APIKind != tc.apiKind || r.APIURL != tc.apiURL {
			t.Errorf("%s: %+v", tc.url, r)
		}
	}
	if r := decode[gitreview.RepoURL](t, do(h, "POST", base+"/parse-url", `{"url":"https://ghe.example.org/team/app/tree/main","detect":true}`)); r.Path != "team/app" {
		t.Errorf("a browser URL of a detected forge: %+v", r)
	}
	// Without, only the hosts Studio knows.
	if r := decode[gitreview.RepoURL](t, do(h, "POST", base+"/parse-url", `{"url":"https://git.example.org/team/app"}`)); r.Kind != gitreview.KindHTTPS {
		t.Errorf("without detect: %+v", r)
	}
}
