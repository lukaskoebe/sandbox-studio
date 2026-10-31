package sandboxes

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/lukaskoebe/sandbox-studio/internal/dnsproxy"
	"github.com/lukaskoebe/sandbox-studio/internal/gateway"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// TransferHost is where a source VM sends the archive of a copy between VMs, through the
// gateway: a guest's writes over msb exec run at about 5 MB/s, its TCP through the gateway
// at 85 MB/s and more (docs/rebase.md#workspace-and-home-copy).
const TransferHost = "transfer." + dnsproxy.VirtualDomain

// Transfers hands the archives sandboxes upload to TransferHost to the copies waiting for
// them. Each upload is expected under a one-time token, from one sandbox.
type Transfers struct {
	mu      sync.Mutex
	waiting map[string]*upload
}

type upload struct {
	sandboxID string
	body      chan io.Reader // the request's body, once it arrives
	done      chan error     // what the copy made of it, for the response
}

// VirtualHost serves TransferHost.
func (t *Transfers) VirtualHost() gateway.VirtualHost {
	return gateway.VirtualHost{Name: TransferHost, Serve: t.serve}
}

// expect waits for an upload from the sandbox sandboxID, under the token it returns.
func (t *Transfers) expect(sandboxID string) (string, *upload) {
	b := make([]byte, 20)
	_, _ = rand.Read(b)
	token := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))
	u := &upload{sandboxID: sandboxID, body: make(chan io.Reader, 1), done: make(chan error, 1)}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.waiting == nil {
		t.waiting = map[string]*upload{}
	}
	t.waiting[token] = u
	return token, u
}

// take removes the upload expected under token from sandboxID.
func (t *Transfers) take(token, sandboxID string) *upload {
	t.mu.Lock()
	defer t.mu.Unlock()
	u := t.waiting[token]
	if u == nil || u.sandboxID != sandboxID {
		return nil
	}
	delete(t.waiting, token)
	return u
}

func (t *Transfers) forget(token string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.waiting, token)
}

// serve takes `POST /<token>` with an archive as its body and answers once the copy has read
// all of it.
func (t *Transfers) serve(sb store.Sandbox) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST an archive", http.StatusMethodNotAllowed)
			return
		}
		u := t.take(strings.TrimPrefix(r.URL.Path, "/"), sb.ID)
		if u == nil {
			http.Error(w, "no copy waits for this upload", http.StatusNotFound)
			return
		}
		u.body <- r.Body
		select {
		case err := <-u.done:
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case <-r.Context().Done():
		}
	})
}

// treeUploader is a runtime whose guests can send an export to TransferHost.
type treeUploader interface {
	UploadTree(ctx context.Context, owned runtime.OwnedVM, tree runtime.Tree, url string, caPEM []byte) error
}

var _ treeUploader = (*runtime.Runtime)(nil)

// uploadingRuntime exports a tree by having the source VM upload it to TransferHost, and
// writes what arrives on to the import. If the guest ends without uploading anything, it
// exports over msb exec instead.
type uploadingRuntime struct {
	transferRuntime
	up        treeUploader
	transfers *Transfers
	ca        func(ctx context.Context, envID string) ([]byte, error)
	log       *slog.Logger
}

func (u uploadingRuntime) ExportTree(ctx context.Context, owned runtime.OwnedVM, tree runtime.Tree, fast bool, w io.Writer) error {
	ca, err := u.ca(ctx, owned.Labels["studio.environment-id"])
	if err != nil {
		return err
	}
	token, up := u.transfers.expect(owned.Labels["studio.sandbox-id"])
	defer u.transfers.forget(token)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ran := make(chan error, 1)
	go func() { ran <- u.up.UploadTree(ctx, owned, tree, "https://"+TransferHost+"/"+token, ca) }()
	select {
	case body := <-up.body:
		_, err := io.Copy(w, body)
		up.done <- err
		if err != nil {
			cancel()
			<-ran
			return err
		}
		return <-ran
	case err := <-ran:
		if err == nil {
			err = errors.New("it ended without uploading")
		}
		if u.log != nil {
			u.log.Warn("the guest couldn't upload its export; copying over msb exec", "vm", owned.Name, "tree", tree, "err", err)
		}
		return u.transferRuntime.ExportTree(ctx, owned, tree, fast, w)
	case <-ctx.Done():
		return fmt.Errorf("export %s: %w", tree, ctx.Err())
	}
}
