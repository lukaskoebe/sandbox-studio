package sandboxes

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// uploadFake is a transferFake whose guests upload their exports to a test server serving
// Transfers for the sandbox they claim, as the gateway would for the one it authenticated.
type uploadFake struct {
	*transferFake
	server  *httptest.Server
	claim   string // the sandbox ID the upload authenticates as, if not its own
	refuse  error  // UploadTree fails with this without sending anything
	uploads int
	status  int // the last upload's answer
}

func newUploadFake(t *testing.T, tf *transferFake, transfers *Transfers) *uploadFake {
	u := &uploadFake{transferFake: tf}
	u.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		transfers.serve(store.Sandbox{ID: r.Header.Get("X-Test-Sandbox")}).ServeHTTP(w, r)
	}))
	t.Cleanup(u.server.Close)
	return u
}

func (u *uploadFake) UploadTree(ctx context.Context, owned runtime.OwnedVM, tree runtime.Tree, url string, caPEM []byte) error {
	if !strings.HasPrefix(url, "https://"+TransferHost+"/") || string(caPEM) != "CA" {
		return fmt.Errorf("url %s, CA %q", url, caPEM)
	}
	if u.refuse != nil {
		return u.refuse
	}
	trees, _ := u.trees(tree)
	u.mu.Lock()
	data := trees[owned.Name]
	u.mu.Unlock()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.server.URL+strings.TrimPrefix(url, "https://"+TransferHost), bytes.NewReader(data))
	if err != nil {
		return err
	}
	id := owned.Labels["studio.sandbox-id"]
	if u.claim != "" {
		id = u.claim
	}
	req.Header.Set("X-Test-Sandbox", id)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(resp.Body)
	u.uploads++
	u.status = resp.StatusCode
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("upload: %s: %s", resp.Status, msg)
	}
	return nil
}

func uploading(u *uploadFake, transfers *Transfers) uploadingRuntime {
	return uploadingRuntime{transferRuntime: u, up: u, transfers: transfers,
		ca: func(context.Context, string) ([]byte, error) { return []byte("CA"), nil }}
}

func TestCopyTreeByUpload(t *testing.T) {
	f := newCheckpointFixture(t)
	tf := newTransferFake(f)
	transfers := &Transfers{}
	u := newUploadFake(t, tf, transfers)
	rt := uploading(u, transfers)
	src := runtime.OwnedVM{Name: "src", Labels: map[string]string{"studio.sandbox-id": "sb1", "studio.environment-id": "env"}}
	dst := runtime.OwnedVM{Name: "dst"}
	tf.setStatus("src", runtime.StatusRunning)
	tf.setStatus("dst", runtime.StatusRunning)
	tf.setWorkspace("src", "0123456789")

	if err := copyTree(f.ctx, rt, src, dst, runtime.TreeWorkspace, &limitWriter{left: 10, err: ErrWorkspaceTooLarge}); err != nil {
		t.Fatal(err)
	}
	if tf.workspace("dst") != "0123456789" || u.uploads != 1 || u.status != http.StatusNoContent {
		t.Fatalf("workspace %q, %d uploads, status %d", tf.workspace("dst"), u.uploads, u.status)
	}
	if tf.saw("export:src") {
		t.Fatal("it exported over msb exec too")
	}

	// Past the limit, the copy ends the upload and nothing is imported.
	tf.setWorkspace("dst", "")
	err := copyTree(f.ctx, rt, src, dst, runtime.TreeWorkspace, &limitWriter{left: 9, err: ErrWorkspaceTooLarge})
	if !errors.Is(err, ErrWorkspaceTooLarge) || tf.workspace("dst") != "" {
		t.Fatalf("err %v, workspace %q", err, tf.workspace("dst"))
	}

	// Another sandbox can't deliver it: the copy gets nothing and falls back to msb exec.
	u.claim = "sb2"
	if err := copyTree(f.ctx, rt, src, dst, runtime.TreeWorkspace, &limitWriter{left: 10, err: ErrWorkspaceTooLarge}); err != nil {
		t.Fatal(err)
	}
	if u.status != http.StatusNotFound || !tf.saw("export:src") || tf.workspace("dst") != "0123456789" {
		t.Fatalf("status %d, workspace %q", u.status, tf.workspace("dst"))
	}
	if len(transfers.waiting) != 0 {
		t.Fatalf("%d uploads still expected", len(transfers.waiting))
	}
}

func TestCopyTreeFallsBackWhenTheGuestCantUpload(t *testing.T) {
	f := newCheckpointFixture(t)
	tf := newTransferFake(f)
	transfers := &Transfers{}
	u := newUploadFake(t, tf, transfers)
	u.refuse = errors.New("no route to transfer.studio.internal")
	src := runtime.OwnedVM{Name: "src", Labels: map[string]string{"studio.sandbox-id": "sb1"}}
	tf.setStatus("src", runtime.StatusRunning)
	tf.setStatus("dst", runtime.StatusRunning)
	tf.setHome("src", "home")
	if err := copyTree(f.ctx, uploading(u, transfers), src, runtime.OwnedVM{Name: "dst"}, runtime.TreeHome, &limitWriter{left: 10, err: ErrHomeTooLarge}); err != nil {
		t.Fatal(err)
	}
	if tf.home("dst") != "home" || !tf.saw("export-home:src") {
		t.Fatalf("home %q", tf.home("dst"))
	}
}

func TestTransfersServeOnlyPOST(t *testing.T) {
	transfers := &Transfers{}
	token, _ := transfers.expect("sb1")
	rec := httptest.NewRecorder()
	transfers.serve(store.Sandbox{ID: "sb1"}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/"+token, nil))
	if rec.Code != http.StatusMethodNotAllowed || transfers.take(token, "sb1") == nil {
		t.Fatalf("status %d, or the GET took the upload", rec.Code)
	}
}

// saw reports whether the runtime was called with call.
func (f *fakeSandboxRuntime) saw(call string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Contains(f.calls, call)
}
