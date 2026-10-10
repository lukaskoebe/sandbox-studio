package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/gitreview"
	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func TestForges(t *testing.T) {
	h, s := newTestServer(t)
	env := newEnvironment(t, s, "work")
	base := testOrigin + "/api/environments/" + env.ID + "/forges"
	const token = "forge-token-value-123"

	for _, body := range []string{
		`{"name":"cb","kind":"forgejo","baseUrl":"http://codeberg.org","token":"x"}`,
		`{"name":"cb","kind":"forgejo","baseUrl":"https://10.1.2.3","token":"x"}`,
		`{"name":"cb","kind":"forgejo","baseUrl":"https://git.studio.internal","token":"x"}`,
		`{"name":"cb","kind":"github","baseUrl":"https://github.com","token":"x"}`,
		`{"name":"Bad","kind":"forgejo","baseUrl":"https://codeberg.org","token":"x"}`,
	} {
		if rec := do(h, "POST", base, body); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}
	rec := do(h, "POST", base, `{"name":"code-berg","kind":"forgejo","baseUrl":"https://codeberg.org/","token":"`+token+`"}`)
	if rec.Code != http.StatusCreated || strings.Contains(rec.Body.String(), token) {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	f := decode[store.Forge](t, rec)
	if f.BaseURL != "https://codeberg.org" || f.SecretID == "" {
		t.Fatalf("forge: %+v", f)
	}
	if rec := do(h, "POST", base, `{"name":"code-berg","kind":"forgejo","baseUrl":"https://codeberg.org","token":"y"}`); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate: %d %s", rec.Code, rec.Body)
	}
	if got := decode[[]store.Forge](t, do(h, "GET", base, "")); len(got) != 1 || got[0].ID != f.ID {
		t.Fatalf("list: %+v", got)
	}

	// The token is studio-only: it never binds to sandboxes, can't be edited as a secret
	// and rules can't refer to it.
	if v, err := s.Vault.Value(context.Background(), env.ID, f.SecretID); err != nil || v != token {
		t.Fatalf("token = %q, %v", v, err)
	}
	if b, err := s.Vault.Bindings(context.Background(), env.ID); err != nil || len(b) != 0 {
		t.Fatalf("the forge token is bound for sandboxes: %+v, %v", b, err)
	}
	secretURL := testOrigin + "/api/environments/" + env.ID + "/secrets/" + f.SecretID
	if rec := do(h, "DELETE", secretURL, ""); rec.Code != http.StatusConflict {
		t.Fatalf("delete the token as a secret: %d %s", rec.Code, rec.Body)
	}
	rules := testOrigin + "/api/environments/" + env.ID + "/rules"
	if rec := do(h, "POST", rules, `{"host":"codeberg.org","action":"proxy","config":{"headers":{"authorization":"token {secret.FORGE_CODE_BERG_TOKEN}"}}}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("rule using the token: %d %s", rec.Code, rec.Body)
	}

	if rec := do(h, "DELETE", base+"/"+f.ID, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if _, err := s.Store.SecretByID(context.Background(), env.ID, f.SecretID); err == nil {
		t.Fatal("the token outlived its forge")
	}
}

func TestGitApprovalsAreSettledByTheIntegration(t *testing.T) {
	h, s := newTestServer(t)
	s.Git = &gitreview.Service{Store: s.Store, Secrets: s.Vault, Bus: s.Bus, Dir: t.TempDir()}
	s.Integrations = integrations.Set{s.Git}
	ctx := context.Background()
	env := newEnvironment(t, s, "work")
	sb, err := s.Store.CreateSandbox(ctx, store.Sandbox{EnvironmentID: env.ID, Name: "one"})
	if err != nil {
		t.Fatal(err)
	}
	f, err := s.Store.CreateForge(ctx, store.Forge{ID: store.NewID(), EnvironmentID: env.ID, Name: "cb", Kind: "forgejo", BaseURL: "https://codeberg.org"},
		store.Secret{ID: store.NewID(), Name: "FORGE_CB_TOKEN", Sealed: []byte("x"), Hosts: []string{"codeberg.org"}})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(gitreview.PushReview{Sandbox: "one", Branch: "main", New: strings.Repeat("b", 40), Diff: "+x\n"})
	a, _, err := s.Store.RequestApproval(ctx, store.Approval{EnvironmentID: env.ID, SandboxID: sb.ID, Kind: gitreview.KindPush, Subject: "cb/o/r:main", Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.CreateGitPush(ctx, store.GitPush{ID: store.NewID(), EnvironmentID: env.ID, SandboxID: sb.ID, ForgeID: f.ID, ForgeName: "cb",
		Owner: "o", Repo: "r", Ref: "refs/heads/main", OldSHA: strings.Repeat("a", 40), NewSHA: strings.Repeat("b", 40), ApprovalID: a.ID}); err != nil {
		t.Fatal(err)
	}

	list := decode[[]ApprovalView](t, do(h, "GET", testOrigin+"/api/approvals", ""))
	if len(list) != 1 || list[0].Git == nil || list[0].Git.Review == nil || list[0].Git.Review.Diff != "+x\n" || list[0].Git.Push.State != store.PushPending {
		t.Fatalf("inbox: %+v", list)
	}
	url := testOrigin + "/api/environments/" + env.ID + "/approvals/" + a.ID + "/decide"
	rec := do(h, "POST", url, `{"action":"deny","note":"split it up"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("decide: %d %s", rec.Code, rec.Body)
	}
	v := decode[ApprovalView](t, rec)
	if v.Status != store.StatusDenied || v.Git == nil || v.Git.Push.State != store.PushRejected || v.Git.Push.Note != "split it up" {
		t.Fatalf("decided: %+v", v)
	}
	if rec := do(h, "POST", url, `{"action":"allow"}`); rec.Code != http.StatusConflict {
		t.Fatalf("second decision: %d %s", rec.Code, rec.Body)
	}
}
