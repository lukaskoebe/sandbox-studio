package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

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
		`{"name":"Bad","kind":"forgejo","baseUrl":"https://codeberg.org","token":"x"}`,
		`{"name":"lab","kind":"https","baseUrl":"https://git.example.com","username":"a:b","token":"x"}`,
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
	rec = do(h, "POST", base, `{"name":"lab","kind":"gitlab","baseUrl":"https://gitlab.com/","token":"`+token+`"}`)
	if rec.Code != http.StatusCreated || decode[store.Forge](t, rec).Kind != "gitlab" {
		t.Fatalf("gitlab forge: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "DELETE", base+"/"+decode[store.Forge](t, rec).ID, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete the gitlab forge: %d %s", rec.Code, rec.Body)
	}

	// The token is studio-only: it never binds to sandboxes, can't be edited as a secret
	// and rules can't refer to it.
	if v, err := s.Vault.Value(context.Background(), env.ID, f.SecretID); err != nil || v != token {
		t.Fatalf("token = %q, %v", v, err)
	}
	if b, err := s.Vault.Bindings(context.Background(), store.Sandbox{EnvironmentID: env.ID}); err != nil || len(b) != 0 {
		t.Fatalf("the forge token is bound for sandboxes: %+v, %v", b, err)
	}
	if rec := do(h, "PUT", base+"/"+f.ID+"/token", `{"token":"new-forge-token-456"}`); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "new-forge-token") {
		t.Fatalf("replace token: %d %s", rec.Code, rec.Body)
	}
	if v, err := s.Vault.Value(context.Background(), env.ID, f.SecretID); err != nil || v != "new-forge-token-456" {
		t.Fatalf("replaced token = %q, %v", v, err)
	}
	if rec := do(h, "PUT", base+"/nope/token", `{"token":"x"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("replace token of a missing forge: %d %s", rec.Code, rec.Body)
	}
	secretURL := testOrigin + "/api/environments/" + env.ID + "/secrets/" + f.SecretID
	if rec := do(h, "DELETE", secretURL, ""); rec.Code != http.StatusConflict {
		t.Fatalf("delete the token as a secret: %d %s", rec.Code, rec.Body)
	}
	rules := testOrigin + "/api/environments/" + env.ID + "/rules"
	if rec := do(h, "POST", rules, `{"host":"codeberg.org","action":"proxy","config":{"headers":{"authorization":"token {secret.FORGE_CODE_BERG_TOKEN}"}}}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("rule using the token: %d %s", rec.Code, rec.Body)
	}

	// Kind https: a token and a username, defaulting to git.
	rec = do(h, "POST", base, `{"name":"lab","kind":"https","baseUrl":"https://git.example.com/scm","token":"lab-token"}`)
	if lab := decode[store.Forge](t, rec); rec.Code != http.StatusCreated || lab.Username != "git" || lab.BaseURL != "https://git.example.com/scm" {
		t.Fatalf("create https: %d %s", rec.Code, rec.Body)
	}

	if rec := do(h, "DELETE", base+"/"+f.ID, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if _, err := s.Store.SecretByID(context.Background(), env.ID, f.SecretID); err == nil {
		t.Fatal("the token outlived its forge")
	}
}

// testSSHHost is an SSH server that only completes handshakes, for scanning its host key.
func testSSHHost(t *testing.T) (ssh.PublicKey, func(context.Context, string, string) (net.Conn, error)) {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(priv)
	cfg := &ssh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				if conn, _, _, err := ssh.NewServerConn(c, cfg); err == nil {
					conn.Close()
				}
				c.Close()
			}()
		}
	}()
	return signer.PublicKey(), func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, ln.Addr().String())
	}
}

func TestSSHForges(t *testing.T) {
	// Inspect temporary key files without scanning unrelated processes' files.
	tmp := t.TempDir()
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, tmp)
	}
	// No SSH agent: the test must never reach the developer's. On Windows an empty
	// SSH_AUTH_SOCK means OpenSSH's own pipe, so it names a pipe nobody serves.
	noAgent, noAgentHint := "", "SSH_AUTH_SOCK"
	if runtime.GOOS == "windows" {
		noAgent = `\\.\pipe\studio-test-no-agent`
		noAgentHint = noAgent
	}
	t.Setenv("SSH_AUTH_SOCK", noAgent)
	h, s := newTestServer(t)
	hostKey, dial := testSSHHost(t)
	staging := t.TempDir()
	s.Git = &gitreview.Service{Store: s.Store, Secrets: s.Vault, Bus: s.Bus, Dir: staging, Dial: dial}
	ctx := context.Background()
	env := newEnvironment(t, s, "work")
	base := testOrigin + "/api/environments/" + env.ID + "/forges"

	rec := do(h, "POST", base+"/scan", `{"baseUrl":"gitolite@git.example.org:"}`)
	scan := decode[ForgeScan](t, rec)
	if rec.Code != http.StatusOK || scan.Fingerprint != ssh.FingerprintSHA256(hostKey) || scan.BaseURL != "ssh://gitolite@git.example.org" {
		t.Fatalf("scan: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", base+"/scan", `{"baseUrl":"git@10.0.0.1"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("scan of a private address: %d %s", rec.Code, rec.Body)
	}
	hk, _ := json.Marshal(scan.Key)
	for _, body := range []string{
		`{"name":"olite","kind":"ssh","baseUrl":"gitolite@git.example.org","token":"x","hostKey":` + string(hk) + `}`,
		`{"name":"olite","kind":"ssh","baseUrl":"gitolite@git.example.org"}`,
		`{"name":"olite","kind":"ssh","baseUrl":"gitolite@git.example.org","hostKey":"ssh-ed25519 nope"}`,
		`{"name":"olite","kind":"ssh","baseUrl":"gitolite@192.168.1.2","hostKey":` + string(hk) + `}`,
		`{"name":"lab","kind":"https","baseUrl":"https://git.example.com"}`,
	} {
		if rec := do(h, "POST", base, body); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}
	// GitHub's keys are built in: a scan that sees another key fails, and so does pinning one.
	if rec := do(h, "POST", base+"/scan", `{"baseUrl":"git@github.com"}`); rec.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(rec.Body.String(), "not one GitHub publishes") {
		t.Fatalf("scan of github.com offering another key: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", base, `{"name":"gh","kind":"ssh","baseUrl":"git@github.com","hostKey":`+string(hk)+`}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("github.com with another key: %d %s", rec.Code, rec.Body)
	}
	rec = do(h, "POST", base, `{"name":"gh","kind":"ssh","baseUrl":"git@github.com","hostKey":"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl"}`)
	if gh := decode[store.Forge](t, rec); rec.Code != http.StatusCreated {
		t.Fatalf("github.com with its key: %d %s", rec.Code, rec.Body)
	} else if rec := do(h, "PUT", base+"/"+gh.ID+"/host-key", `{"hostKey":`+string(hk)+`}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("re-pin github.com to another key: %d %s", rec.Code, rec.Body)
	} else if rec := do(h, "DELETE", base+"/"+gh.ID, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}

	rec = do(h, "POST", base, `{"name":"olite","kind":"ssh","auth":"key","baseUrl":"gitolite@git.example.org","hostKey":`+string(hk)+`}`)
	f := decode[store.Forge](t, rec)
	if rec.Code != http.StatusCreated || f.HostKeyFingerprint != scan.Fingerprint || f.Username != "gitolite" ||
		!strings.HasPrefix(f.PublicKey, "ssh-ed25519 ") || !strings.HasSuffix(f.PublicKey, " sandbox-studio/"+env.ID+"/olite") {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "PRIVATE KEY") {
		t.Fatal("the key was returned")
	}

	// The key exists in the vault only: sealed in the store, never in a file, never bound.
	key, err := s.Vault.Value(ctx, env.ID, f.SecretID)
	if err != nil || !strings.HasPrefix(key, "-----BEGIN OPENSSH PRIVATE KEY-----") {
		t.Fatalf("key = %v", err)
	}
	body := strings.Split(key, "\n")[1]
	sec, err := s.Store.SecretByID(ctx, env.ID, f.SecretID)
	if err != nil || sec.Name != "FORGE_OLITE_SSH_KEY" || strings.Contains(string(sec.Sealed), body) {
		t.Fatalf("secret %+v: %v", sec.Name, err)
	}
	for _, dir := range []string{staging, os.TempDir()} {
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if info, _ := d.Info(); info == nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
				return nil
			}
			if data, _ := os.ReadFile(path); strings.Contains(string(data), body) {
				t.Errorf("the key is in %s", path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if b, err := s.Vault.Bindings(ctx, store.Sandbox{EnvironmentID: env.ID}); err != nil || len(b) != 0 {
		t.Fatalf("the key is bound for sandboxes: %+v, %v", b, err)
	}
	if rec := do(h, "DELETE", testOrigin+"/api/environments/"+env.ID+"/secrets/"+f.SecretID, ""); rec.Code != http.StatusConflict {
		t.Fatalf("delete the key as a secret: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "PUT", base+"/"+f.ID+"/token", `{"token":"x"}`); rec.Code != http.StatusConflict {
		t.Fatalf("token for an ssh forge: %d %s", rec.Code, rec.Body)
	}

	// Without auth, an ssh forge signs with the host's SSH agent and holds no key. Switching
	// to a key generates one; switching back to the agent deletes it.
	rec = do(h, "POST", base, `{"name":"agent","kind":"ssh","baseUrl":"git@git.example.org","hostKey":`+string(hk)+`}`)
	af := decode[store.Forge](t, rec)
	if rec.Code != http.StatusCreated || af.Auth != store.ForgeAuthAgent || af.SecretID != "" || af.PublicKey != "" {
		t.Fatalf("agent forge: %d %s", rec.Code, rec.Body)
	}
	rec = do(h, "PUT", base+"/"+af.ID+"/auth", `{"auth":"key"}`)
	if got := decode[store.Forge](t, rec); rec.Code != http.StatusOK || got.Auth != store.ForgeAuthKey || got.SecretID == "" || !strings.HasPrefix(got.PublicKey, "ssh-ed25519 ") {
		t.Fatalf("switch to a key: %d %s", rec.Code, rec.Body)
	}
	rec = do(h, "PUT", base+"/"+af.ID+"/auth", `{"auth":"agent"}`)
	if got := decode[store.Forge](t, rec); rec.Code != http.StatusOK || got.Auth != store.ForgeAuthAgent || got.SecretID != "" || got.PublicKey != "" {
		t.Fatalf("switch back to the agent: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", base+"/"+af.ID+"/dismiss-agent-hint", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("dismiss the hint: %d %s", rec.Code, rec.Body)
	}
	if got, err := s.Store.Forge(ctx, env.ID, af.ID); err != nil || !got.AgentHintDismissed {
		t.Fatalf("hint not dismissed: %+v, %v", got, err)
	}
	if rec := do(h, "POST", base+"/"+f.ID+"/dismiss-agent-hint", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("dismiss a key forge's hint: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "PUT", base+"/"+f.ID+"/auth", `{"auth":"agent"}`); rec.Code != http.StatusOK {
		t.Fatalf("switch a key forge to the agent: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "PUT", base+"/"+f.ID+"/auth", `{"auth":"key"}`); rec.Code != http.StatusOK {
		t.Fatalf("switch it back to a key: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "DELETE", base+"/"+af.ID, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete the agent forge: %d %s", rec.Code, rec.Body)
	}

	// The sandbox's git config sends ssh URLs of the host to the virtual remote.
	rw, err := s.gitRewrites(ctx, env.ID)
	if err != nil || len(rw) != 1 || rw[0].URL != "https://git.studio.internal/olite/" || strings.Join(rw[0].InsteadOf, " ") != "ssh://gitolite@git.example.org/ gitolite@git.example.org:" {
		t.Fatalf("rewrites = %+v, %v", rw, err)
	}

	// Re-pinning.
	if rec := do(h, "PUT", base+"/"+f.ID+"/host-key", `{"hostKey":"nope"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("pin garbage: %d %s", rec.Code, rec.Body)
	}
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	otherSigner, _ := ssh.NewSignerFromKey(other)
	ok, _ := json.Marshal(string(ssh.MarshalAuthorizedKey(otherSigner.PublicKey())))
	rec = do(h, "PUT", base+"/"+f.ID+"/host-key", `{"hostKey":`+string(ok)+`}`)
	if got := decode[store.Forge](t, rec); rec.Code != http.StatusOK || got.HostKeyFingerprint != ssh.FingerprintSHA256(otherSigner.PublicKey()) {
		t.Fatalf("pin: %d %s", rec.Code, rec.Body)
	}

	// Kinds without an API are tested on a repository.
	rec = do(h, "POST", base, `{"name":"lab","kind":"https","baseUrl":"https://git.example.com","token":"t"}`)
	lab := decode[store.Forge](t, rec)
	if got := decode[ForgeTest](t, do(h, "POST", base+"/"+lab.ID+"/test", "")); got.OK || !strings.Contains(got.Message, "name a repository") {
		t.Fatalf("test without a repo: %+v", got)
	}
	if rec := do(h, "PUT", base+"/"+lab.ID+"/host-key", `{"hostKey":`+string(ok)+`}`); rec.Code != http.StatusConflict {
		t.Fatalf("host key for an https forge: %d %s", rec.Code, rec.Body)
	}

	// A pasted repository URL describes the forge, with a name the environment doesn't have.
	rec = do(h, "POST", base+"/parse-url", `{"url":"git@github.com:owner/repo.git"}`)
	if got := decode[gitreview.RepoURL](t, rec); rec.Code != http.StatusOK || got.Kind != gitreview.KindSSH || got.Name != "github" ||
		got.Path != "owner/repo" || got.BaseURL != "ssh://git@github.com" || got.Auth != store.ForgeAuthAgent {
		t.Fatalf("parse an ssh URL: %d %s", rec.Code, rec.Body)
	}
	rec = do(h, "POST", base+"/parse-url", `{"url":"https://github.com/owner/repo"}`)
	if got := decode[gitreview.RepoURL](t, rec); rec.Code != http.StatusOK || got.Kind != gitreview.KindGitHub || got.Path != "owner/repo" {
		t.Fatalf("parse an https URL: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", base+"/parse-url", `{"url":"http://github.com/owner/repo"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("parse an http URL: %d %s", rec.Code, rec.Body)
	}

	// Without an SSH agent, the status says why and how to fix it.
	st := decode[gitreview.AgentStatus](t, do(h, "GET", testOrigin+"/api/system/ssh-agent", ""))
	if st.Available || !strings.Contains(st.Message, noAgentHint) || st.Fix == "" {
		t.Fatalf("agent status: %+v", st)
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

	// A link to the review still finds it once decided, with its outcome; other
	// environments and unknown IDs don't.
	one := testOrigin + "/api/environments/" + env.ID + "/approvals/" + a.ID
	got := decode[ApprovalView](t, do(h, "GET", one, ""))
	if got.ID != a.ID || got.Status != store.StatusDenied || got.Git == nil || got.Git.Push.Note != "split it up" {
		t.Fatalf("one approval: %+v", got)
	}
	other := newEnvironment(t, s, "other")
	for _, u := range []string{testOrigin + "/api/environments/" + other.ID + "/approvals/" + a.ID, one + "x"} {
		if rec := do(h, "GET", u, ""); rec.Code != http.StatusNotFound {
			t.Fatalf("GET %s: %d %s", u, rec.Code, rec.Body)
		}
	}
}

// TestLocalForges: a local forge is a folder on this computer, with no token, and the
// pasted path of a repository fills one in.
func TestLocalForges(t *testing.T) {
	h, s := newTestServer(t)
	env := newEnvironment(t, s, "work")
	base := testOrigin + "/api/environments/" + env.ID + "/forges"
	folder := t.TempDir()
	if err := os.Mkdir(filepath.Join(folder, "proj"), 0o755); err != nil {
		t.Fatal(err)
	}
	quoted, _ := json.Marshal(folder)
	for _, body := range []string{
		`{"name":"here","kind":"local","baseUrl":"relative/path"}`,
		`{"name":"here","kind":"local","baseUrl":"/"}`,
		`{"name":"here","kind":"local","baseUrl":` + string(quoted) + `,"token":"x"}`,
		`{"name":"here","kind":"local","baseUrl":` + strings.TrimSuffix(string(quoted), `"`) + `/missing"}`,
	} {
		if rec := do(h, "POST", base, body); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}
	rec := do(h, "POST", base, `{"name":"here","kind":"local","baseUrl":`+string(quoted)+`}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	f := decode[store.Forge](t, rec)
	if f.BaseURL != folder || f.Auth != store.ForgeAuthNone || f.SecretID != "" {
		t.Fatalf("forge: %+v", f)
	}
	if rec := do(h, "PUT", base+"/"+f.ID+"/token", `{"token":"x"}`); rec.Code != http.StatusConflict {
		t.Fatalf("token: %d %s", rec.Code, rec.Body)
	}
	repo, _ := json.Marshal(filepath.Join(folder, "proj"))
	r := decode[gitreview.RepoURL](t, do(h, "POST", base+"/parse-url", `{"url":`+string(repo)+`}`))
	if r.Kind != gitreview.KindLocal || r.BaseURL != folder || r.Path != "proj" || r.Name != "local" {
		t.Fatalf("parsed: %+v", r)
	}
	if rec := do(h, "DELETE", base+"/"+f.ID, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
}
