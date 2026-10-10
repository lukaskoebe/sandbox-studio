package gitreview

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-git/go-billy/v5/memfs"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/pktline"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/server"
	"github.com/go-git/go-git/v5/storage/memory"

	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

const forgeToken = "forgejo-secret-token-1234567890"

// --- fake forge ----------------------------------------------------------------------------

// fakeForge is a Forgejo stand-in: smart HTTP over a go-git repository for owner/repo,
// plus the few API calls Studio makes.
type fakeForge struct {
	t    *testing.T
	repo *git.Repository
	srv  *httptest.Server

	mu    sync.Mutex
	auths []string
	pulls []PullRequest
}

func newFakeForge(t *testing.T) *fakeForge {
	t.Helper()
	repo, err := git.InitWithOptions(memory.NewStorage(), memfs.New(), git.InitOptions{DefaultBranch: plumbing.Main})
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeForge{t: t, repo: repo}
	commitFile(t, repo, "README.md", "hello\n", "Initial commit")
	f.srv = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeForge) head(t *testing.T, branch string) plumbing.Hash {
	t.Helper()
	ref, err := f.repo.Storer.Reference(plumbing.NewBranchReferenceName(branch))
	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		return plumbing.ZeroHash
	}
	if err != nil {
		t.Fatal(err)
	}
	return ref.Hash()
}

func (f *fakeForge) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.auths = append(f.auths, r.Header.Get("Authorization"))
	f.mu.Unlock()
	if strings.HasPrefix(r.URL.Path, "/api/v1/") {
		f.api(w, r)
		return
	}
	user, pass, ok := r.BasicAuth()
	if !ok || pass != forgeToken || user == "" {
		w.Header().Set("WWW-Authenticate", `Basic realm="forge"`)
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	rest, ok := strings.CutPrefix(r.URL.Path, "/owner/repo.git/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	tr := server.NewServer(server.MapLoader{"file:///repo": f.repo.Storer})
	ep, _ := transport.NewEndpoint("file:///repo")
	ctx := r.Context()
	switch {
	case rest == "info/refs" && r.URL.Query().Get("service") == "git-upload-pack":
		sess, _ := tr.NewUploadPackSession(ep, nil)
		ar, err := sess.AdvertisedReferencesContext(ctx)
		f.advertise(w, "git-upload-pack", ar, err)
	case rest == "info/refs" && r.URL.Query().Get("service") == "git-receive-pack":
		sess, _ := tr.NewReceivePackSession(ep, nil)
		ar, err := sess.AdvertisedReferencesContext(ctx)
		f.advertise(w, "git-receive-pack", ar, err)
	case rest == "git-upload-pack":
		req := packp.NewUploadPackRequest()
		if err := req.Decode(r.Body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// The go-git server insists on knowing every "have"; a real forge just ignores
		// the ones it lacks.
		var haves []plumbing.Hash
		for _, h := range req.Haves {
			if f.repo.Storer.HasEncodedObject(h) == nil {
				haves = append(haves, h)
			}
		}
		req.Haves = haves
		sess, _ := tr.NewUploadPackSession(ep, nil)
		resp, err := sess.UploadPack(ctx, req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/x-git-upload-pack-result")
		resp.Encode(w)
	case rest == "git-receive-pack":
		req := packp.NewReferenceUpdateRequest()
		if err := req.Decode(r.Body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		sess, _ := tr.NewReceivePackSession(ep, nil)
		sess.AdvertisedReferencesContext(ctx)
		rs, err := sess.ReceivePack(ctx, req)
		if rs == nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/x-git-receive-pack-result")
		rs.Encode(w)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeForge) advertise(w http.ResponseWriter, service string, ar *packp.AdvRefs, err error) {
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	ar.Prefix = [][]byte{[]byte("# service=" + service), pktline.Flush}
	w.Header().Set("Content-Type", "application/x-"+service+"-advertisement")
	ar.Encode(w)
}

func (f *fakeForge) api(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "token "+forgeToken {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"message":"token is required"}`)
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/user":
		io.WriteString(w, `{"login":"studio-bot"}`)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/owner/repo":
		io.WriteString(w, `{"default_branch":"main"}`)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/owner/repo/pulls":
		var pr PullRequest
		if err := json.NewDecoder(r.Body).Decode(&pr); err != nil {
			http.Error(w, "bad", http.StatusUnprocessableEntity)
			return
		}
		f.mu.Lock()
		f.pulls = append(f.pulls, pr)
		n := len(f.pulls)
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"number": n, "html_url": f.srv.URL + "/owner/repo/pulls/1"})
	default:
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"message":"not found"}`)
	}
}

func commitFile(t *testing.T, repo *git.Repository, name, content, msg string) plumbing.Hash {
	t.Helper()
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	fh, err := wt.Filesystem.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	io.WriteString(fh, content)
	fh.Close()
	if _, err := wt.Add(name); err != nil {
		t.Fatal(err)
	}
	h, err := wt.Commit(msg, &git.CommitOptions{Author: &object.Signature{Name: "Ada", Email: "ada@example.com", When: time.Now()}})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// --- harness ------------------------------------------------------------------------------

type fakeSecrets map[string]string

func (f fakeSecrets) Value(_ context.Context, envID, id string) (string, error) {
	v, ok := f[envID+"/"+id]
	if !ok {
		return "", store.ErrNotFound
	}
	return v, nil
}

type harness struct {
	ctx   context.Context
	st    *store.Store
	svc   *Service
	forge *fakeForge
	env   store.Environment
	f     store.Forge
}

// guest is one sandbox's view of the virtual remote, recording everything it receives.
type guest struct {
	sb  store.Sandbox
	srv *httptest.Server
	mu  sync.Mutex
	got bytes.Buffer
}

func (g *guest) url() string { return g.srv.URL + "/forge/owner/repo.git" }

func (g *guest) seen() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.got.String()
}

type recorder struct {
	http.ResponseWriter
	g *guest
}

func (r recorder) Write(p []byte) (int, error) {
	r.g.mu.Lock()
	r.g.got.Write(p)
	r.g.mu.Unlock()
	return r.ResponseWriter.Write(p)
}

func (r recorder) WriteHeader(code int) {
	r.g.mu.Lock()
	for k, v := range r.Header() {
		r.g.got.WriteString(k + ": " + strings.Join(v, ",") + "\n")
	}
	r.g.mu.Unlock()
	r.ResponseWriter.WriteHeader(code)
}

func (r recorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	env, err := st.CreateEnvironment(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	ff := newFakeForge(t)
	secrets := fakeSecrets{}
	h := &harness{ctx: ctx, st: st, forge: ff, env: env}
	h.f = h.addForge(t, env.ID, "forge", secrets)
	h.svc = &Service{Store: st, Secrets: secrets, Dir: t.TempDir(), Client: ff.srv.Client()}
	return h
}

func (h *harness) addForge(t *testing.T, envID, name string, secrets fakeSecrets) store.Forge {
	t.Helper()
	sec := store.Secret{ID: store.NewID(), Name: "FORGE_" + strings.ToUpper(name) + "_TOKEN", Sealed: []byte("sealed"), Hosts: []string{"127.0.0.1"}}
	f, err := h.st.CreateForge(h.ctx, store.Forge{ID: store.NewID(), EnvironmentID: envID, Name: name, Kind: KindForgejo, BaseURL: h.forge.srv.URL}, sec)
	if err != nil {
		t.Fatal(err)
	}
	secrets[envID+"/"+f.SecretID] = forgeToken
	return f
}

func (h *harness) guest(t *testing.T, envID, name string) *guest {
	t.Helper()
	sb, err := h.st.CreateSandbox(h.ctx, store.Sandbox{EnvironmentID: envID, Name: name})
	if err != nil {
		t.Fatal(err)
	}
	g := &guest{sb: sb}
	handler := h.svc.Serve(sb)
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(recorder{w, g}, r)
	}))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *guest) clone(t *testing.T) *git.Repository {
	t.Helper()
	repo, err := git.Clone(memory.NewStorage(), memfs.New(), &git.CloneOptions{URL: g.url()})
	if err != nil {
		t.Fatalf("clone: %v", err)
	}
	return repo
}

func push(repo *git.Repository, spec string, force bool) error {
	return repo.Push(&git.PushOptions{RefSpecs: []config.RefSpec{config.RefSpec(spec)}, Force: force})
}

func (h *harness) approvals(t *testing.T, kind, status string) []store.Approval {
	t.Helper()
	all, err := h.st.Approvals(h.ctx, "", status, 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []store.Approval
	for _, a := range all {
		if a.Kind == kind {
			out = append(out, a)
		}
	}
	return out
}

func (h *harness) decide(t *testing.T, a store.Approval, action, note string) {
	t.Helper()
	k, ok := integrations.Set{h.svc}.Kind(a.Kind)
	if !ok {
		t.Fatalf("no settler for %s", a.Kind)
	}
	if err := k.Decide(h.ctx, a, integrations.Decision{Action: action, Note: note}); err != nil {
		t.Fatalf("decide %s: %v", a.Kind, err)
	}
}

func (h *harness) assertTokenHidden(t *testing.T, guests ...*guest) {
	t.Helper()
	basic := base64.StdEncoding.EncodeToString([]byte("sandbox-studio:" + forgeToken))
	for _, g := range guests {
		if s := g.seen(); strings.Contains(s, forgeToken) || strings.Contains(s, basic) {
			t.Fatalf("the token reached sandbox %s", g.sb.Name)
		}
	}
}

// --- tests --------------------------------------------------------------------------------

func TestCloneAndFetchInjectTheToken(t *testing.T) {
	h := newHarness(t)
	g := h.guest(t, h.env.ID, "one")
	repo := g.clone(t)
	head, err := repo.Head()
	if err != nil || head.Hash() != h.forge.head(t, "main") {
		t.Fatalf("clone head = %v, %v; forge main = %s", head, err, h.forge.head(t, "main"))
	}
	commitFile(t, h.forge.repo, "more.txt", "more\n", "Upstream change")
	if err := repo.Fetch(&git.FetchOptions{}); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	ref, _ := repo.Reference(plumbing.NewRemoteReferenceName("origin", "main"), true)
	if ref.Hash() != h.forge.head(t, "main") {
		t.Fatalf("fetch did not bring the upstream change")
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("sandbox-studio:"+forgeToken))
	h.forge.mu.Lock()
	for _, a := range h.forge.auths {
		if a != want {
			t.Errorf("forge saw Authorization %q", a)
		}
	}
	h.forge.mu.Unlock()
	h.assertTokenHidden(t, g)
}

func TestPushIsStagedAndApprovalPushesUpstream(t *testing.T) {
	h := newHarness(t)
	g := h.guest(t, h.env.ID, "one")
	repo := g.clone(t)
	before := h.forge.head(t, "main")
	c1 := commitFile(t, repo, "main.go", "package main\n", "Add main\n\nWith a body.")
	if err := push(repo, "refs/heads/main:refs/heads/main", false); err != nil {
		t.Fatalf("push: %v", err)
	}
	if h.forge.head(t, "main") != before {
		t.Fatal("the push reached the forge before approval")
	}
	pending := h.approvals(t, KindPush, store.StatusPending)
	if len(pending) != 1 {
		t.Fatalf("pending git.push approvals = %d", len(pending))
	}
	a := pending[0]
	if a.SandboxID != g.sb.ID || a.Subject != "forge/owner/repo:main" {
		t.Fatalf("approval = %+v", a)
	}
	d, err := h.svc.GitDetail(h.ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	rv := d.Review
	if rv.Branch != "main" || rv.Sandbox != "one" || rv.Old != before.String() || rv.New != c1.String() ||
		len(rv.Commits) != 1 || rv.Commits[0].Subject != "Add main" || rv.Commits[0].Body != "With a body." {
		t.Fatalf("review = %+v", rv)
	}
	if !strings.Contains(rv.Diff, "+package main") || rv.Additions != 1 || rv.DiffTruncated {
		t.Fatalf("diff = %q (+%d)", rv.Diff, rv.Additions)
	}
	staged, err := git.PlainOpen(filepath.Join(h.svc.Dir, h.env.ID, g.sb.ID, h.f.ID, "owner", "repo.git"))
	if err != nil {
		t.Fatal(err)
	}
	if ref, err := staged.Reference(plumbing.ReferenceName("refs/studio/push/"+d.Push.ID), false); err != nil || ref.Hash() != c1 {
		t.Fatalf("staged ref = %v, %v", ref, err)
	}

	h.decide(t, a, integrations.Allow, "")
	if h.forge.head(t, "main") != c1 {
		t.Fatal("approval did not update the forge")
	}
	p, _ := h.st.GitPush(h.ctx, h.env.ID, d.Push.ID)
	if p.State != store.PushPushed || !strings.Contains(p.Result, "pushed") {
		t.Fatalf("push = %+v", p)
	}
	if got := h.approvals(t, KindPR, ""); len(got) != 0 {
		t.Fatal("a push to the default branch proposed a pull request")
	}
	// The agent sees its commit upstream on the next fetch.
	if err := repo.Fetch(&git.FetchOptions{}); err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		t.Fatalf("fetch: %v", err)
	}
	h.assertTokenHidden(t, g)
}

func TestApprovedBranchPushProposesAPullRequest(t *testing.T) {
	h := newHarness(t)
	g := h.guest(t, h.env.ID, "one")
	repo := g.clone(t)
	commitFile(t, repo, "a.txt", "a\n", "Add a")
	commitFile(t, repo, "b.txt", "b\n", "Add b")
	if err := push(repo, "refs/heads/main:refs/heads/feature", false); err != nil {
		t.Fatalf("push: %v", err)
	}
	a := h.approvals(t, KindPush, store.StatusPending)[0]
	d, _ := h.svc.GitDetail(h.ctx, a)
	if d.Review.Old != "" || len(d.Review.Commits) != 2 || d.Review.Files != 2 {
		t.Fatalf("review of a new branch = %+v", d.Review)
	}
	h.decide(t, a, integrations.Allow, "")
	prs := h.approvals(t, KindPR, store.StatusPending)
	if len(prs) != 1 {
		t.Fatalf("git.pr approvals = %d", len(prs))
	}
	pd, err := h.svc.GitDetail(h.ctx, prs[0])
	if err != nil || pd.PullRequest.Head != "feature" || pd.PullRequest.Base != "main" ||
		!strings.Contains(pd.PullRequest.Body, "Add a") || !strings.Contains(pd.PullRequest.Body, "Add b") {
		t.Fatalf("PR detail = %+v, %v", pd, err)
	}
	h.decide(t, prs[0], integrations.Allow, "")
	if len(h.forge.pulls) != 1 || h.forge.pulls[0].Head != "feature" {
		t.Fatalf("forge pulls = %+v", h.forge.pulls)
	}
	p, _ := h.st.GitPush(h.ctx, h.env.ID, d.Push.ID)
	if p.PRURL == "" || p.PRResult != "opened" {
		t.Fatalf("push = %+v", p)
	}
}

func TestRejectionIsShownOnTheNextFetch(t *testing.T) {
	h := newHarness(t)
	g := h.guest(t, h.env.ID, "one")
	other := h.guest(t, h.env.ID, "two")
	repo := g.clone(t)
	otherRepo := other.clone(t)
	commitFile(t, repo, "x.txt", "x\n", "Add x")
	if err := push(repo, "refs/heads/main:refs/heads/main", false); err != nil {
		t.Fatalf("push: %v", err)
	}
	a := h.approvals(t, KindPush, store.StatusPending)[0]
	h.decide(t, a, integrations.Deny, "please add tests")
	p, _ := h.st.GitPushByApproval(h.ctx, h.env.ID, a.ID)
	if p.State != store.PushRejected || p.Note != "please add tests" {
		t.Fatalf("push = %+v", p)
	}
	// Another sandbox of the same environment hears nothing.
	if err := otherRepo.Fetch(&git.FetchOptions{}); err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		t.Fatalf("other sandbox's fetch: %v", err)
	}
	err := repo.Fetch(&git.FetchOptions{})
	if err == nil || !strings.Contains(g.seen(), "rejected by the reviewer: please add tests") {
		t.Fatalf("fetch after rejection: %v", err)
	}
	if strings.Contains(other.seen(), "rejected") {
		t.Fatal("another sandbox was told about the rejection")
	}
	// Once only.
	if err := repo.Fetch(&git.FetchOptions{}); err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		t.Fatalf("second fetch: %v", err)
	}
}

func TestOversizePushIsRefused(t *testing.T) {
	h := newHarness(t)
	h.svc.httpClient()
	h.svc.Limits.MaxPack = 16 << 10
	g := h.guest(t, h.env.ID, "one")
	repo := g.clone(t)
	noise := make([]byte, 64<<10)
	rand.Read(noise)
	commitFile(t, repo, "big.bin", base64.StdEncoding.EncodeToString(noise), "Big")
	err := push(repo, "refs/heads/main:refs/heads/main", false)
	if err == nil {
		t.Fatal("an oversize push was accepted")
	}
	if n := len(h.approvals(t, KindPush, "")); n != 0 {
		t.Fatalf("oversize push raised %d approvals", n)
	}
}

func TestNonFastForwardAndDeletesAreRefused(t *testing.T) {
	h := newHarness(t)
	g := h.guest(t, h.env.ID, "one")
	repo := g.clone(t)
	commitFile(t, h.forge.repo, "up.txt", "up\n", "Upstream moved")
	commitFile(t, repo, "mine.txt", "mine\n", "Mine")
	err := push(repo, "+refs/heads/main:refs/heads/main", true)
	if err == nil || !strings.Contains(err.Error(), "non-fast-forward") {
		t.Fatalf("forced push: %v", err)
	}
	err = push(repo, ":refs/heads/main", false)
	if err == nil {
		t.Fatal("a delete was accepted")
	}
	if n := len(h.approvals(t, KindPush, "")); n != 0 {
		t.Fatalf("refused pushes raised %d approvals", n)
	}
}

func TestNewerPushSupersedesPending(t *testing.T) {
	h := newHarness(t)
	g := h.guest(t, h.env.ID, "one")
	repo := g.clone(t)
	commitFile(t, repo, "1.txt", "1\n", "One")
	if err := push(repo, "refs/heads/main:refs/heads/topic", false); err != nil {
		t.Fatal(err)
	}
	first := h.approvals(t, KindPush, store.StatusPending)[0]
	commitFile(t, repo, "2.txt", "2\n", "Two")
	// The staged push isn't on the forge, so the agent's view of the remote branch is
	// stale; it pushes the whole branch again.
	if err := push(repo, "+refs/heads/main:refs/heads/topic", true); err != nil {
		t.Fatal(err)
	}
	pending := h.approvals(t, KindPush, store.StatusPending)
	if len(pending) != 1 || pending[0].ID == first.ID {
		t.Fatalf("pending = %+v", pending)
	}
	old, _ := h.st.GitPushByApproval(h.ctx, h.env.ID, first.ID)
	if old.State != store.PushSuperseded {
		t.Fatalf("first push = %s", old.State)
	}
	d, _ := h.svc.GitDetail(h.ctx, pending[0])
	if len(d.Review.Commits) != 2 {
		t.Fatalf("second review has %d commits", len(d.Review.Commits))
	}
}

func TestEnvironmentsAndSandboxesAreIsolated(t *testing.T) {
	h := newHarness(t)
	other, err := h.st.CreateEnvironment(h.ctx, "other")
	if err != nil {
		t.Fatal(err)
	}
	stranger := h.guest(t, other.ID, "stranger")
	_, err = git.Clone(memory.NewStorage(), memfs.New(), &git.CloneOptions{URL: stranger.url()})
	if err == nil || !strings.Contains(err.Error(), "no forge") {
		t.Fatalf("clone from an environment without the forge: %v", err)
	}
	h.assertTokenHidden(t, stranger)

	a, b := h.guest(t, h.env.ID, "a"), h.guest(t, h.env.ID, "b")
	ra := a.clone(t)
	commitFile(t, ra, "a.txt", "a\n", "From a")
	if err := push(ra, "refs/heads/main:refs/heads/main", false); err != nil {
		t.Fatal(err)
	}
	pa := h.approvals(t, KindPush, store.StatusPending)[0]
	pushA, _ := h.st.GitPushByApproval(h.ctx, h.env.ID, pa.ID)
	// b's staging repository has never seen a's commit.
	rb := b.clone(t)
	if _, err := rb.CommitObject(plumbing.NewHash(pushA.NewSHA)); err == nil {
		t.Fatal("sandbox b can read sandbox a's staged commit")
	}
	if _, err := os.Stat(filepath.Join(h.svc.Dir, h.env.ID, b.sb.ID)); err == nil {
		entries, _ := os.ReadDir(filepath.Join(h.svc.Dir, h.env.ID, b.sb.ID, h.f.ID, "owner", "repo.git", "refs", "studio"))
		if len(entries) != 0 {
			t.Fatal("sandbox b's staging repository has staged pushes")
		}
	}
	// Approvals of one environment cannot be settled through another.
	pa.EnvironmentID = other.ID
	k, _ := integrations.Set{h.svc}.Kind(KindPush)
	if err := k.Decide(h.ctx, pa, integrations.Decision{Action: integrations.Allow}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-environment decision: %v", err)
	}
}

func TestBadPathsAndDumbHTTPAreRefused(t *testing.T) {
	h := newHarness(t)
	g := h.guest(t, h.env.ID, "one")
	for _, path := range []string{
		"/forge/owner/repo.git/info/refs", // dumb HTTP
		"/forge/owner/repo.git/HEAD",      // dumb HTTP
		"/forge/../etc/repo.git/info/refs?service=git-upload-pack",
		"/forge/owner/..git/info/refs?service=git-upload-pack",
		"/Forge/owner/repo.git/info/refs?service=git-upload-pack",
	} {
		resp, err := http.Get(g.srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusUnauthorized {
			t.Errorf("%s: %d", path, resp.StatusCode)
		}
	}
}

func TestCheckForge(t *testing.T) {
	ok := []string{"https://codeberg.org", "https://git.example.com/forgejo/"}
	for _, u := range ok {
		if _, _, err := CheckForge("cb", KindForgejo, u); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
	bad := []string{
		"http://codeberg.org", "https://user:pw@codeberg.org", "https://localhost", "https://10.0.0.1",
		"https://127.0.0.1:3000", "https://codeberg.org/?x=1", "https://git.studio.internal", "ftp://codeberg.org", "",
	}
	for _, u := range bad {
		if _, _, err := CheckForge("cb", KindForgejo, u); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: %v", u, err)
		}
	}
	for _, name := range []string{"", "Upper", "-x", "a_b", strings.Repeat("a", 33)} {
		if _, _, err := CheckForge(name, KindForgejo, "https://codeberg.org"); !errors.Is(err, ErrInvalid) {
			t.Errorf("name %q: %v", name, err)
		}
	}
	if _, _, err := CheckForge("gh", KindGitHub, "https://github.com"); !errors.Is(err, ErrNotImplemented) {
		t.Errorf("github: %v", err)
	}
	if _, _, err := CheckForge("x", "gitlab", "https://gitlab.com"); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown kind: %v", err)
	}
}

func TestForgejoAPI(t *testing.T) {
	ff := newFakeForge(t)
	fj := &Forgejo{BaseURL: ff.srv.URL, Client: ff.srv.Client()}
	ctx := context.Background()
	if user, err := fj.Check(ctx, forgeToken); err != nil || user != "studio-bot" {
		t.Fatalf("check = %q, %v", user, err)
	}
	if _, err := fj.Check(ctx, "wrong"); err == nil || !strings.Contains(err.Error(), "token is required") {
		t.Fatalf("check with a wrong token: %v", err)
	}
	if b, err := fj.DefaultBranch(ctx, forgeToken, "owner", "repo"); err != nil || b != "main" {
		t.Fatalf("default branch = %q, %v", b, err)
	}
	url, err := fj.OpenPullRequest(ctx, forgeToken, "owner", "repo", PullRequest{Head: "f", Base: "main", Title: "T", Body: "B"})
	if err != nil || !strings.HasSuffix(url, "/pulls/1") {
		t.Fatalf("open PR = %q, %v", url, err)
	}
	if got := ff.pulls[0]; got != (PullRequest{Head: "f", Base: "main", Title: "T", Body: "B"}) {
		t.Fatalf("forge got %+v", got)
	}
	if _, err := fj.DefaultBranch(ctx, forgeToken, "owner", "missing"); err == nil {
		t.Fatal("missing repository")
	}
	if m := mask("bad "+forgeToken, forgeToken); strings.Contains(m, forgeToken) {
		t.Fatal(m)
	}
}

func TestReviewTruncatesLargeDiffs(t *testing.T) {
	h := newHarness(t)
	h.svc.httpClient()
	h.svc.Limits.MaxDiff = 2 << 10
	h.svc.Limits.MaxBlob = 4 << 10
	g := h.guest(t, h.env.ID, "one")
	repo := g.clone(t)
	commitFile(t, repo, "a.txt", strings.Repeat("line\n", 600), "Many lines")
	commitFile(t, repo, "huge.txt", strings.Repeat("x", 8<<10), "Huge file")
	if err := push(repo, "refs/heads/main:refs/heads/main", false); err != nil {
		t.Fatal(err)
	}
	d, _ := h.svc.GitDetail(h.ctx, h.approvals(t, KindPush, store.StatusPending)[0])
	if !d.Review.DiffTruncated || !strings.Contains(d.Review.Diff, "diff truncated by Sandbox Studio") || len(d.Review.Diff) > 3<<10 {
		t.Fatalf("diff (%d bytes, truncated=%v)", len(d.Review.Diff), d.Review.DiffTruncated)
	}
}

// TestRealGit drives the remote with the git binary, which uses side-band, the probe
// request and protocol v2 negotiation that go-git's client does not.
func TestRealGit(t *testing.T) {
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git binary")
	}
	h := newHarness(t)
	g := h.guest(t, h.env.ID, "one")
	home := t.TempDir()
	run := func(dir string, args ...string) (string, error) {
		cmd := exec.Command(gitBin, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+home, "GIT_CONFIG_NOSYSTEM=1",
			"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=/bin/false", "LC_ALL=C",
			"GIT_AUTHOR_NAME=Agent", "GIT_AUTHOR_EMAIL=agent@example.com",
			"GIT_COMMITTER_NAME=Agent", "GIT_COMMITTER_EMAIL=agent@example.com")
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	work := filepath.Join(home, "work")
	if out, err := run(home, "clone", g.url(), work); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	os.WriteFile(filepath.Join(work, "agent.txt"), []byte("from the agent\n"), 0o644)
	if out, err := run(work, "add", "agent.txt"); err != nil {
		t.Fatal(out)
	}
	if out, err := run(work, "commit", "-m", "Agent change"); err != nil {
		t.Fatal(out)
	}
	out, err := run(work, "push", "origin", "HEAD:refs/heads/agent")
	if err != nil || !strings.Contains(out, "queued for review") {
		t.Fatalf("push: %v\n%s", err, out)
	}
	a := h.approvals(t, KindPush, store.StatusPending)
	if len(a) != 1 {
		t.Fatalf("approvals = %d", len(a))
	}
	h.decide(t, a[0], integrations.Deny, "wrong branch")
	out, err = run(work, "fetch")
	if err == nil || !strings.Contains(out, "rejected by the reviewer: wrong branch") {
		t.Fatalf("fetch after rejection: %v\n%s", err, out)
	}
	if out, err := run(work, "fetch"); err != nil {
		t.Fatalf("second fetch: %v\n%s", err, out)
	}
	// Push again and approve: the branch lands on the forge.
	out, err = run(work, "push", "origin", "HEAD:refs/heads/agent")
	if err != nil {
		t.Fatalf("push again: %v\n%s", err, out)
	}
	h.decide(t, h.approvals(t, KindPush, store.StatusPending)[0], integrations.Allow, "")
	if h.forge.head(t, "agent").IsZero() {
		t.Fatal("the approved push did not reach the forge")
	}
	h.assertTokenHidden(t, g)
}
