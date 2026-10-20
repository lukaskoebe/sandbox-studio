//go:build e2e

package e2e

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/format/pktline"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/server"
)

// Upstream stands in for the forges: every HTTPS request Studio's git remote makes to a forge
// reaches it, whatever the host. It serves git smart HTTP from bare repositories on disk,
// <Dir>/<host>/<path>.git, which the tests create through the control API. It ignores
// credentials and has no forge API, so forges of kind https suit it.
type Upstream struct {
	Dir string

	once sync.Once
	srv  *httptest.Server
	mu   sync.Mutex // one request at a time: pushes rewrite refs
}

// NewUpstream returns an upstream that keeps its repositories under dir.
func NewUpstream(dir string) *Upstream { return &Upstream{Dir: dir} }

// Client reaches the upstream for every address. It skips TLS verification: the forge's
// name is whatever the test gave it.
func (u *Upstream) Client() *http.Client {
	u.once.Do(func() { u.srv = httptest.NewTLSServer(u) })
	addr := u.srv.Listener.Addr().String()
	tr := u.srv.Client().Transport.(*http.Transport).Clone()
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}
	tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // a loopback fake
	return &http.Client{
		Transport:     tr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

var upstreamPath = regexp.MustCompile(`^[a-z0-9.-]+(/[A-Za-z0-9_][A-Za-z0-9._-]*){1,8}$`)

// repoDir is the bare repository of host/path, path without .git.
func (u *Upstream) repoDir(name string) (string, error) {
	name = strings.TrimSuffix(name, ".git")
	if !upstreamPath.MatchString(name) || strings.Contains(name, "..") {
		return "", fmt.Errorf("bad repository %q: want <host>/<path>", name)
	}
	return filepath.Join(u.Dir, filepath.FromSlash(name)+".git"), nil
}

// Branch is a branch of an upstream repository and the commit at its head.
type Branch struct {
	Name    string `json:"name"`
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
}

// Create makes host/path a fresh bare repository whose main branch holds one commit with a
// README, replacing any repository there.
func (u *Upstream) Create(name string) ([]Branch, error) {
	dir, err := u.repoDir(name)
	if err != nil {
		return nil, err
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if err := os.RemoveAll(dir); err != nil {
		return nil, err
	}
	repo, err := git.PlainInitWithOptions(dir, &git.PlainInitOptions{Bare: true, InitOptions: git.InitOptions{DefaultBranch: plumbing.Main}})
	if err != nil {
		return nil, err
	}
	st := repo.Storer
	blob, err := storeObject(st, plumbing.BlobObject, func(w io.Writer) error {
		_, err := io.WriteString(w, "# "+name+"\n")
		return err
	})
	if err != nil {
		return nil, err
	}
	tree := &object.Tree{Entries: []object.TreeEntry{{Name: "README.md", Mode: filemode.Regular, Hash: blob}}}
	treeHash, err := encode(st, tree)
	if err != nil {
		return nil, err
	}
	sig := object.Signature{Name: "Upstream", Email: "upstream@example.com", When: time.Now()}
	commit := &object.Commit{Author: sig, Committer: sig, Message: "Initial commit\n", TreeHash: treeHash}
	head, err := encode(st, commit)
	if err != nil {
		return nil, err
	}
	if err := st.SetReference(plumbing.NewHashReference(plumbing.NewBranchReferenceName("main"), head)); err != nil {
		return nil, err
	}
	return branches(repo)
}

// Branches lists the branches of host/path.
func (u *Upstream) Branches(name string) ([]Branch, error) {
	dir, err := u.repoDir(name)
	if err != nil {
		return nil, err
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	repo, err := git.PlainOpen(dir)
	if err != nil {
		return nil, err
	}
	return branches(repo)
}

func branches(repo *git.Repository) ([]Branch, error) {
	refs, err := repo.Branches()
	if err != nil {
		return nil, err
	}
	out := []Branch{}
	err = refs.ForEach(func(r *plumbing.Reference) error {
		b := Branch{Name: r.Name().Short(), SHA: r.Hash().String()}
		if c, err := repo.CommitObject(r.Hash()); err == nil {
			b.Subject, _, _ = strings.Cut(c.Message, "\n")
		}
		out = append(out, b)
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, err
}

func storeObject(st storer.EncodedObjectStorer, typ plumbing.ObjectType, write func(io.Writer) error) (plumbing.Hash, error) {
	o := st.NewEncodedObject()
	o.SetType(typ)
	w, err := o.Writer()
	if err != nil {
		return plumbing.ZeroHash, err
	}
	if err := write(w); err != nil {
		return plumbing.ZeroHash, err
	}
	if err := w.Close(); err != nil {
		return plumbing.ZeroHash, err
	}
	return st.SetEncodedObject(o)
}

func encode(st storer.EncodedObjectStorer, o interface {
	Encode(plumbing.EncodedObject) error
}) (plumbing.Hash, error) {
	eo := st.NewEncodedObject()
	if err := o.Encode(eo); err != nil {
		return plumbing.ZeroHash, err
	}
	return st.SetEncodedObject(eo)
}

// ServeHTTP answers git smart HTTP for https://<host>/<path>.git/{info/refs,git-upload-pack,
// git-receive-pack}.
func (u *Upstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// An OpenAI-compatible endpoint's model list, for the provider form's suggestions.
	if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/models") {
		if r.Header.Get("Authorization") == "" {
			http.Error(w, "no key", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"e2e-coder"},{"id":"e2e-chat"}]}`)
		return
	}
	prefix, rest, ok := strings.Cut(r.URL.Path, ".git/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	dir, err := u.repoDir(hostOnly(r.Host) + prefix)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	repo, err := git.PlainOpen(dir)
	if errors.Is(err, git.ErrRepositoryNotExists) {
		http.Error(w, "repository not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	srv := server.NewServer(server.MapLoader{"file:///repo": repo.Storer})
	ep, _ := transport.NewEndpoint("file:///repo")
	ctx := r.Context()
	switch {
	case rest == "info/refs" && r.URL.Query().Get("service") == "git-upload-pack":
		sess, err := srv.NewUploadPackSession(ep, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		ar, err := sess.AdvertisedReferencesContext(ctx)
		advertise(w, "git-upload-pack", ar, err)
	case rest == "info/refs" && r.URL.Query().Get("service") == "git-receive-pack":
		sess, err := srv.NewReceivePackSession(ep, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		ar, err := sess.AdvertisedReferencesContext(ctx)
		advertise(w, "git-receive-pack", ar, err)
	case rest == "git-upload-pack" && r.Method == http.MethodPost:
		req := packp.NewUploadPackRequest()
		if err := req.Decode(r.Body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// go-git's server insists on knowing every "have"; a real forge ignores the ones it
		// lacks.
		var haves []plumbing.Hash
		for _, h := range req.Haves {
			if repo.Storer.HasEncodedObject(h) == nil {
				haves = append(haves, h)
			}
		}
		req.Haves = haves
		sess, err := srv.NewUploadPackSession(ep, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		resp, err := sess.UploadPack(ctx, req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/x-git-upload-pack-result")
		resp.Encode(w)
	case rest == "git-receive-pack" && r.Method == http.MethodPost:
		req := packp.NewReferenceUpdateRequest()
		if err := req.Decode(r.Body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		sess, err := srv.NewReceivePackSession(ep, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if _, err := sess.AdvertisedReferencesContext(ctx); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		rs, err := sess.ReceivePack(ctx, req)
		if rs == nil {
			http.Error(w, fmt.Sprint(err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/x-git-receive-pack-result")
		rs.Encode(w)
	default:
		http.NotFound(w, r)
	}
}

func advertise(w http.ResponseWriter, service string, ar *packp.AdvRefs, err error) {
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	ar.Prefix = [][]byte{[]byte("# service=" + service), pktline.Flush}
	w.Header().Set("Content-Type", "application/x-"+service+"-advertisement")
	w.Header().Set("Cache-Control", "no-cache")
	ar.Encode(w)
}

func hostOnly(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return hostport
}
