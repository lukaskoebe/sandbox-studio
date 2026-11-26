package gitreview

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/pktline"

	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// gitForge is a forge over HTTPS run by the host's git. Like GitHub, it filters fetches
// and hands out objects by ID, which the go-git server behind fakeForge doesn't.
type gitForge struct {
	t      *testing.T
	gitBin string
	dir    string // the bare repository, served as owner/repo
	work   string // a working copy of it, to commit to it
	run    func(dir string, args ...string) (string, error)
	srv    *httptest.Server
	forge  store.Forge

	mu      sync.Mutex
	fetches []string // the body of each git-upload-pack request
	noBlobs bool     // fail fetches by ID
}

func newGitForge(t *testing.T, h *harness) *gitForge {
	t.Helper()
	f := &gitForge{t: t, run: gitRunner(t)}
	f.gitBin, _ = exec.LookPath("git")
	root := t.TempDir()
	f.dir, f.work = filepath.Join(root, "repo.git"), filepath.Join(root, "work")
	f.must(f.run(root, "init", "-q", "--bare", "-b", "main", f.dir))
	f.must(f.run(f.dir, "config", "uploadpack.allowFilter", "true"))
	f.must(f.run(f.dir, "config", "uploadpack.allowAnySHA1InWant", "true"))
	f.must(f.run(root, "init", "-q", "-b", "main", f.work))
	f.must(f.run(f.work, "remote", "add", "origin", f.dir))
	f.srv = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	var err error
	f.forge, err = h.st.CreateForge(h.ctx, store.Forge{ID: store.NewID(), EnvironmentID: h.env.ID, Name: "gitforge", Kind: KindHTTPS, BaseURL: f.srv.URL, Username: "deploy"},
		store.Secret{ID: store.NewID(), Name: "FORGE_GITFORGE_TOKEN", Sealed: []byte("sealed"), Placeholder: "gitforge-placeholder"})
	if err != nil {
		t.Fatal(err)
	}
	h.svc.Secrets.(fakeSecrets)[h.env.ID+"/"+f.forge.SecretID] = forgeToken
	return f
}

func (f *gitForge) must(out string, err error) string {
	f.t.Helper()
	if err != nil {
		f.t.Fatalf("%v\n%s", err, out)
	}
	return strings.TrimSpace(out)
}

// commit commits files to the forge's main branch.
func (f *gitForge) commit(msg string, files map[string]string) {
	f.t.Helper()
	writeFiles(f.t, f.work, files)
	f.must(f.run(f.work, "add", "-A"))
	f.must(f.run(f.work, "commit", "-q", "-m", msg))
	f.must(f.run(f.work, "push", "-q", "origin", "HEAD:refs/heads/main"))
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func (f *gitForge) serve(w http.ResponseWriter, r *http.Request) {
	prefix, rest, _ := strings.Cut(r.URL.Path, ".git/")
	if prefix != "/owner/repo" {
		http.NotFound(w, r)
		return
	}
	service := strings.TrimPrefix(r.URL.Query().Get("service"), "git-")
	if rest == "info/refs" && (service == "upload-pack" || service == "receive-pack") {
		w.Header().Set("Content-Type", "application/x-git-"+service+"-advertisement")
		e := pktline.NewEncoder(w)
		_ = e.EncodeString("# service=git-" + service + "\n")
		_ = e.Flush()
		_, _ = w.Write(f.git(nil, service, "--stateless-rpc", "--advertise-refs", f.dir))
		return
	}
	if rest != "git-upload-pack" && rest != "git-receive-pack" {
		http.NotFound(w, r)
		return
	}
	var body io.Reader = r.Body
	if r.Header.Get("Content-Encoding") == "gzip" {
		zr, err := gzip.NewReader(body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		body = zr
	}
	in, _ := io.ReadAll(body)
	if rest == "git-upload-pack" {
		f.mu.Lock()
		f.fetches = append(f.fetches, string(in))
		fail := f.noBlobs && !bytes.Contains(in, []byte("filter blob:none"))
		f.mu.Unlock()
		if fail {
			http.Error(w, "no objects today", http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set("Content-Type", "application/x-"+rest+"-result")
	_, _ = w.Write(f.git(in, strings.TrimPrefix(rest, "git-"), "--stateless-rpc", f.dir))
}

// git runs one of the forge's services and returns what it wrote, which says what failed
// if it did.
func (f *gitForge) git(stdin []byte, args ...string) []byte {
	cmd := exec.Command(f.gitBin, args...)
	cmd.Env = gitEnv("")
	cmd.Stdin = bytes.NewReader(stdin)
	out, _ := cmd.Output()
	return out
}

// filtered counts the fetches that left file contents out.
func (f *gitForge) filtered() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, b := range f.fetches {
		if strings.Contains(b, "filter blob:none") {
			n++
		}
	}
	return n
}

// staged reports whether the staging repository of g's sandbox has the object rev names
// on the forge.
func (f *gitForge) staged(h *harness, g *guest, rev string) bool {
	f.t.Helper()
	repo, err := git.PlainOpen(h.svc.stagingPath(target{sb: g.sb, forge: f.forge, owner: "owner", repo: "repo"}))
	if err != nil {
		f.t.Fatal(err)
	}
	return repo.Storer.HasEncodedObject(plumbing.NewHash(f.must(f.run(f.dir, "rev-parse", rev)))) == nil
}

func lines(prefix string, n int) string {
	var b strings.Builder
	for i := range n {
		b.WriteString(prefix + " " + strings.Repeat("x", i%7) + "\n")
	}
	return b.String()
}

// TestPartialMirrorFetchesOnlyTheChangedFiles: a forge that filters fetches is mirrored
// without file contents. The review still diffs the files the push changes and finds a
// rename with an edit, an approved push carries only the new objects though the forge's
// main branch moved meanwhile, and a commit's own diff fetches what it shows.
func TestPartialMirrorFetchesOnlyTheChangedFiles(t *testing.T) {
	h := newHarness(t)
	f := newGitForge(t, h)
	f.commit("First", map[string]string{
		"kept.txt": "kept as it is\n", "edit.txt": "one\ntwo\nthree\n", "flip.txt": "flip\n", "old-name.txt": lines("line", 30),
	})
	g := h.guest(t, h.env.ID, "one")
	work := filepath.Join(t.TempDir(), "work")
	f.must(f.run("", "clone", "-q", g.srv.URL+"/gitforge/owner/repo.git", work))
	writeFiles(t, work, map[string]string{"edit.txt": "one\n2\nthree\n", "flip.txt": "flipped\n"})
	f.must(f.run(work, "mv", "old-name.txt", "new-name.txt"))
	writeFiles(t, work, map[string]string{"new-name.txt": lines("line", 30) + "and one more\n"})
	f.must(f.run(work, "commit", "-q", "-a", "-m", "Edit and rename"))
	writeFiles(t, work, map[string]string{"flip.txt": "flip\n"})
	f.must(f.run(work, "commit", "-q", "-a", "-m", "Flip back"))
	if out := f.must(f.run(work, "push", "origin", "HEAD:refs/heads/agent")); !strings.Contains(out, "queued for review") {
		t.Fatalf("push: %s", out)
	}

	if n := f.filtered(); n != 1 {
		t.Fatalf("fetches without file contents = %d, want 1: %q", n, f.fetches)
	}
	if f.staged(h, g, "main:kept.txt") {
		t.Fatal("the mirror has the content of a file the push doesn't touch")
	}
	if !f.staged(h, g, "main:edit.txt") || !f.staged(h, g, "main:old-name.txt") {
		t.Fatal("the mirror lacks the earlier content of the files the push changes")
	}
	pending := h.approvals(t, KindPush, store.StatusPending)
	if len(pending) != 1 {
		t.Fatalf("approvals = %d", len(pending))
	}
	var rv PushReview
	if err := json.Unmarshal(pending[0].Payload, &rv); err != nil {
		t.Fatal(err)
	}
	if rv.Files != 2 || len(rv.Commits) != 2 || !strings.Contains(rv.Diff, "-two\n+2\n") || !strings.Contains(rv.Diff, "+and one more") ||
		strings.Contains(rv.Diff, "-line") || strings.Contains(rv.Diff, "not shown") {
		t.Fatalf("review: %d files, %d commits\n%s", rv.Files, len(rv.Commits), rv.Diff)
	}

	// The second commit puts back content only the forge has.
	tip := f.must(f.run(work, "rev-parse", "HEAD"))
	d, err := h.svc.CommitDiff(h.ctx, h.env.ID, pending[0].ID, tip)
	if err != nil || d.Files != 1 || !strings.Contains(d.Diff, "-flipped\n+flip\n") {
		t.Fatalf("commit diff: %v\n%+v", err, d)
	}

	// The forge's main branch moves on before the push is approved.
	f.commit("Meanwhile", map[string]string{"meanwhile.txt": "someone else\n"})
	h.decide(t, pending[0], integrations.Allow, "")
	p, err := h.st.GitPushByApproval(h.ctx, h.env.ID, pending[0].ID)
	if err != nil || p.State != store.PushPushed {
		t.Fatalf("push: %+v %v", p, err)
	}
	if got := f.must(f.run(f.dir, "rev-parse", "refs/heads/agent")); got != tip {
		t.Fatalf("agent is %s on the forge, want %s", got, tip)
	}
	// Two commits, two root trees and three new files; nothing of what the forge had.
	if !strings.Contains(p.Result, "(7 objects)") {
		t.Fatalf("result = %q", p.Result)
	}
	f.must(f.run(f.dir, "fsck", "--strict"))
	h.assertTokenHidden(t, g)
}

// TestPartialMirrorLeavesLargeFilesOut: when the changed files' earlier contents are more
// than a push may bring, the review fetches them one by one and shows the small ones.
func TestPartialMirrorLeavesLargeFilesOut(t *testing.T) {
	h := newHarness(t)
	h.svc.Limits.MaxBlob, h.svc.Limits.MaxPack = 32<<10, 256<<10
	f := newGitForge(t, h)
	big := func() string {
		b := make([]byte, 200<<10)
		_, _ = rand.Read(b)
		return string(b)
	}
	f.commit("First", map[string]string{"big1.bin": big(), "big2.bin": big(), "small.txt": "small\n"})
	g := h.guest(t, h.env.ID, "one")
	work := filepath.Join(t.TempDir(), "work")
	f.must(f.run("", "clone", "-q", g.srv.URL+"/gitforge/owner/repo.git", work))
	f.must(f.run(work, "rm", "-q", "big1.bin", "big2.bin"))
	writeFiles(t, work, map[string]string{"small.txt": "changed\n"})
	f.must(f.run(work, "commit", "-q", "-a", "-m", "Drop the big files"))
	if out := f.must(f.run(work, "push", "origin", "HEAD:refs/heads/agent")); !strings.Contains(out, "queued for review") {
		t.Fatalf("push: %s", out)
	}
	pending := h.approvals(t, KindPush, store.StatusPending)
	if len(pending) != 1 {
		t.Fatalf("approvals = %d", len(pending))
	}
	var rv PushReview
	if err := json.Unmarshal(pending[0].Payload, &rv); err != nil {
		t.Fatal(err)
	}
	if rv.Files != 3 || !strings.Contains(rv.Diff, "-small\n+changed\n") ||
		!strings.Contains(rv.Diff, "[big1.bin: not shown") || !strings.Contains(rv.Diff, "[big2.bin: not shown") {
		t.Fatalf("review: %d files\n%s", rv.Files, rv.Diff)
	}
	if f.staged(h, g, "main:big1.bin") || !f.staged(h, g, "main:small.txt") {
		t.Fatal("the mirror has a large file's content, or lacks the small one's")
	}
	// The fetch that was too large left nothing behind.
	for _, dir := range []string{h.svc.Dir, filepath.Join(h.svc.stagingPath(target{sb: g.sb, forge: f.forge, owner: "owner", repo: "repo"}), "objects", "pack")} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "tmp_pack_") || strings.HasPrefix(e.Name(), "studio-receive-pack-") {
				t.Errorf("temporary file remains: %s", e.Name())
			}
		}
	}
	h.decide(t, pending[0], integrations.Allow, "")
	if got, want := f.must(f.run(f.dir, "rev-parse", "refs/heads/agent")), f.must(f.run(work, "rev-parse", "HEAD")); got != want {
		t.Fatalf("agent is %s on the forge, want %s", got, want)
	}
}

// TestPushIsRefusedWhenTheChangedFilesCantBeFetched: a review without the files it is
// about would be approved blind, so the push is refused and the next one asks again.
func TestPushIsRefusedWhenTheChangedFilesCantBeFetched(t *testing.T) {
	h := newHarness(t)
	f := newGitForge(t, h)
	f.commit("First", map[string]string{"edit.txt": "one\n"})
	g := h.guest(t, h.env.ID, "one")
	work := filepath.Join(t.TempDir(), "work")
	f.must(f.run("", "clone", "-q", g.srv.URL+"/gitforge/owner/repo.git", work))
	writeFiles(t, work, map[string]string{"edit.txt": "two\n"})
	f.must(f.run(work, "commit", "-q", "-a", "-m", "Edit"))

	f.mu.Lock()
	f.noBlobs = true
	f.mu.Unlock()
	out, err := f.run(work, "push", "origin", "HEAD:refs/heads/agent")
	if err == nil || !strings.Contains(out, "could not fetch the files the push changes from the forge") || !strings.Contains(out, "push again") {
		t.Fatalf("push: %v\n%s", err, out)
	}
	if strings.Contains(out, forgeToken) {
		t.Fatalf("the token reached the sandbox: %s", out)
	}
	if n := len(h.approvals(t, KindPush, store.StatusPending)); n != 0 {
		t.Fatalf("approvals = %d", n)
	}

	f.mu.Lock()
	f.noBlobs = false
	f.mu.Unlock()
	if out := f.must(f.run(work, "push", "origin", "HEAD:refs/heads/agent")); !strings.Contains(out, "queued for review") {
		t.Fatalf("push again: %s", out)
	}
	pending := h.approvals(t, KindPush, store.StatusPending)
	var rv PushReview
	if len(pending) != 1 || json.Unmarshal(pending[0].Payload, &rv) != nil || !strings.Contains(rv.Diff, "-one\n+two\n") {
		t.Fatalf("pending = %+v", pending)
	}
}
