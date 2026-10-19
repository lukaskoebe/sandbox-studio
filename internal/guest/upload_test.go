//go:build linux

package guest

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// uploadOwner is the account the tests write as: their own, so chown succeeds unprivileged.
var uploadOwner = [2]int{os.Getuid(), os.Getgid()}

func storeTestUpload(t *testing.T, workspace string, req agentproto.UploadRequest, body string) (agentproto.UploadReply, error) {
	t.Helper()
	req.Size = int64(len(body))
	return storeUpload(workspace, uploadOwner, req, strings.NewReader(body))
}

// streamUpload runs serveUpload over a pipe the way the agent does, sending req and body from
// the host side, and returns the reply line the host reads.
func streamUpload(t *testing.T, workspace string, req agentproto.UploadRequest, body string) []byte {
	t.Helper()
	return streamUploadInto(t, workspace, "", req, body)
}

func streamUploadInto(t *testing.T, workspace, into string, req agentproto.UploadRequest, body string) []byte {
	t.Helper()
	host, guest := net.Pipe()
	defer host.Close()
	defer guest.Close()
	served := make(chan error, 1)
	go func() { served <- serveUpload(guest, bufio.NewReader(guest), workspace, into, uploadOwner) }()
	go func() {
		_ = agentproto.WriteJSONLine(host, req)
		_, _ = host.Write([]byte(body))
	}()
	line, err := bufio.NewReader(host).ReadBytes('\n')
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	if err := <-served; err != nil {
		t.Fatalf("serve: %v", err)
	}
	return line
}

func TestStoreUploadWritesIntoAFolder(t *testing.T) {
	ws := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(ws, "src", "deep"), 0o755))

	reply, err := storeTestUpload(t, ws, agentproto.UploadRequest{Dir: "src/deep", Name: "notes.txt"}, "hello\n")
	if err != nil || reply.Name != "notes.txt" || reply.Size != 6 {
		t.Fatalf("reply %+v, %v", reply, err)
	}
	data, err := os.ReadFile(filepath.Join(ws, "src", "deep", "notes.txt"))
	if err != nil || string(data) != "hello\n" {
		t.Fatalf("content %q, %v", data, err)
	}
	if st, err := os.Stat(filepath.Join(ws, "src", "deep", "notes.txt")); err != nil || st.Mode().Perm() != 0o644 {
		t.Fatalf("mode %v, %v", st, err)
	}
	// The workspace root takes uploads too, and an empty body makes an empty file.
	if _, err := storeTestUpload(t, ws, agentproto.UploadRequest{Name: "empty"}, ""); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(filepath.Join(ws, "empty")); err != nil || st.Size() != 0 {
		t.Fatalf("empty file %v, %v", st, err)
	}
	if left := leftoverUploads(t, ws); len(left) != 0 {
		t.Fatalf("temporary files left behind: %v", left)
	}
}

func TestStoreUploadRefusesBadNames(t *testing.T) {
	ws := t.TempDir()
	for _, name := range []string{"", ".", "..", "../escape", "a/b", "/abs", "nul\x00byte", strings.Repeat("n", 256)} {
		if _, err := storeTestUpload(t, ws, agentproto.UploadRequest{Name: name}, "x"); err == nil {
			t.Errorf("name %q accepted", name)
		}
	}
	if _, err := storeTestUpload(t, ws, agentproto.UploadRequest{Name: strings.Repeat("n", 255)}, "x"); err != nil {
		t.Fatalf("a 255-byte name: %v", err)
	}
}

func TestStoreUploadRefusesBadFolders(t *testing.T) {
	outside := t.TempDir()
	ws := t.TempDir()
	must(t, os.Symlink(outside, filepath.Join(ws, "link")))
	must(t, os.MkdirAll(filepath.Join(ws, "real"), 0o755))
	must(t, os.Symlink(outside, filepath.Join(ws, "real", "inner")))
	must(t, os.WriteFile(filepath.Join(ws, "plain"), nil, 0o644))

	for _, dir := range []string{"/etc", "../", "a/../b", "./x", "link", "real/inner", "real/inner/deeper", "missing", "plain"} {
		if _, err := storeTestUpload(t, ws, agentproto.UploadRequest{Dir: dir, Name: "x.txt"}, "x"); err == nil {
			t.Errorf("folder %q accepted", dir)
		}
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("an upload escaped the workspace: %v", entries)
	}
}

func TestStoreUploadConflicts(t *testing.T) {
	ws := t.TempDir()
	must(t, os.WriteFile(filepath.Join(ws, "a.txt"), []byte("old"), 0o600))

	if _, err := storeTestUpload(t, ws, agentproto.UploadRequest{Name: "a.txt"}, "new"); !errors.Is(err, errTaken) {
		t.Fatalf("fail: %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(ws, "a.txt")); string(data) != "old" {
		t.Fatalf("fail replaced the file: %q", data)
	}

	keep := agentproto.UploadRequest{Name: "a.txt", Conflict: agentproto.ConflictKeep}
	if reply, err := storeTestUpload(t, ws, keep, "second"); err != nil || reply.Name != "a (2).txt" {
		t.Fatalf("keep: %+v, %v", reply, err)
	}
	if reply, err := storeTestUpload(t, ws, keep, "third"); err != nil || reply.Name != "a (3).txt" {
		t.Fatalf("keep again: %+v, %v", reply, err)
	}
	must(t, os.WriteFile(filepath.Join(ws, ".bashrc"), nil, 0o644))
	if reply, err := storeTestUpload(t, ws, agentproto.UploadRequest{Name: ".bashrc", Conflict: agentproto.ConflictKeep}, "x"); err != nil || reply.Name != ".bashrc (2)" {
		t.Fatalf("dot file copy: %+v, %v", reply, err)
	}

	replace := agentproto.UploadRequest{Name: "a.txt", Conflict: agentproto.ConflictReplace}
	if reply, err := storeTestUpload(t, ws, replace, "replaced"); err != nil || reply.Name != "a.txt" {
		t.Fatalf("replace: %+v, %v", reply, err)
	}
	if data, _ := os.ReadFile(filepath.Join(ws, "a.txt")); string(data) != "replaced" {
		t.Fatalf("replace content %q", data)
	}
	if _, err := storeTestUpload(t, ws, agentproto.UploadRequest{Name: "a.txt", Conflict: "merge"}, "x"); err == nil {
		t.Fatal("an unknown policy accepted")
	}
	if left := leftoverUploads(t, ws); len(left) != 0 {
		t.Fatalf("temporary files left behind: %v", left)
	}
}

func TestStoreUploadReplaceNeverTouchesFoldersOrLinks(t *testing.T) {
	ws := t.TempDir()
	victim := filepath.Join(t.TempDir(), "victim")
	must(t, os.WriteFile(victim, []byte("keep me"), 0o600))
	must(t, os.Mkdir(filepath.Join(ws, "dir"), 0o755))
	must(t, os.Symlink(victim, filepath.Join(ws, "link")))

	for _, name := range []string{"dir", "link"} {
		if _, err := storeTestUpload(t, ws, agentproto.UploadRequest{Name: name, Conflict: agentproto.ConflictReplace}, "x"); err == nil {
			t.Errorf("replaced %s", name)
		}
	}
	if data, _ := os.ReadFile(victim); string(data) != "keep me" {
		t.Fatalf("the link's target changed: %q", data)
	}
	if st, err := os.Lstat(filepath.Join(ws, "link")); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the link was replaced: %v, %v", st, err)
	}
	if left := leftoverUploads(t, ws); len(left) != 0 {
		t.Fatalf("temporary files left behind: %v", left)
	}
}

func TestStoreUploadReportsAShortBody(t *testing.T) {
	ws := t.TempDir()
	if _, err := storeUpload(ws, uploadOwner, agentproto.UploadRequest{Name: "short", Size: 10}, strings.NewReader("abc")); err == nil {
		t.Fatal("a short body was stored")
	}
	if _, err := os.Stat(filepath.Join(ws, "short")); !os.IsNotExist(err) {
		t.Fatalf("a partial file was kept: %v", err)
	}
	if left := leftoverUploads(t, ws); len(left) != 0 {
		t.Fatalf("temporary files left behind: %v", left)
	}
}

// TestServeUploadReadsRefusedBodies checks that a refused upload still reads its body, so the
// host's writes complete and the reply reaches it on the same stream.
func TestServeUploadReadsRefusedBodies(t *testing.T) {
	ws := t.TempDir()
	body := strings.Repeat("x", 300<<10) // more than one pipe write
	line := streamUpload(t, ws, agentproto.UploadRequest{Dir: "missing", Name: "a.txt", Size: int64(len(body))}, body)
	var reply agentproto.Error
	if err := json.Unmarshal(line, &reply); err != nil || reply.Error == "" || reply.Code != "" {
		t.Fatalf("reply %s, %v", line, err)
	}
	if entries, _ := os.ReadDir(ws); len(entries) != 0 {
		t.Fatalf("a refused upload wrote %v", entries)
	}

	must(t, os.WriteFile(filepath.Join(ws, "a.txt"), []byte("x"), 0o600))
	line = streamUpload(t, ws, agentproto.UploadRequest{Name: "a.txt", Size: 2}, "hi")
	if err := json.Unmarshal(line, &reply); err != nil || reply.Code != agentproto.CodeExists {
		t.Fatalf("taken name: %s, %v", line, err)
	}
	if data, _ := os.ReadFile(filepath.Join(ws, "a.txt")); string(data) != "x" {
		t.Fatalf("the taken file changed: %q", data)
	}
}

func TestServeUploadRefusesOversizedFiles(t *testing.T) {
	line := streamUpload(t, t.TempDir(), agentproto.UploadRequest{Name: "big", Size: agentproto.MaxUploadBytes + 1}, "")
	var reply agentproto.Error
	if err := json.Unmarshal(line, &reply); err != nil || !strings.Contains(reply.Error, "MiB") {
		t.Fatalf("reply %s, %v", line, err)
	}
}

func TestServeUploadReplyOnSuccess(t *testing.T) {
	ws := t.TempDir()
	line := streamUpload(t, ws, agentproto.UploadRequest{Name: "ok.bin", Size: 3}, "abc")
	var reply agentproto.UploadReply
	if err := json.Unmarshal(line, &reply); err != nil || reply.Name != "ok.bin" || reply.Size != 3 {
		t.Fatalf("reply %s, %v", line, err)
	}
	if data, _ := os.ReadFile(filepath.Join(ws, "ok.bin")); string(data) != "abc" {
		t.Fatalf("content %q", data)
	}
}

func leftoverUploads(t *testing.T, dir string) []string {
	t.Helper()
	var left []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && strings.HasPrefix(d.Name(), ".studio-upload-") {
			left = append(left, p)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return left
}

// A folder is created only when asked for, under an existing parent.
func TestStoreUploadCreatesItsFolderOnRequest(t *testing.T) {
	ws := t.TempDir()
	if _, err := storeTestUpload(t, ws, agentproto.UploadRequest{Dir: "downloads", Name: "a.txt"}, "x"); err == nil {
		t.Fatal("a missing folder was created without CreateDir")
	}
	if _, err := storeTestUpload(t, ws, agentproto.UploadRequest{Dir: "downloads", Name: "a.txt", CreateDir: true}, "x"); err != nil {
		t.Fatalf("with CreateDir: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(ws, "downloads", "a.txt")); err != nil || string(b) != "x" {
		t.Fatalf("file %q, %v", b, err)
	}
	if _, err := storeTestUpload(t, ws, agentproto.UploadRequest{Dir: "missing/deeper", Name: "a.txt", CreateDir: true}, "x"); err == nil {
		t.Fatal("CreateDir made a folder whose parent does not exist")
	}
}

// A paste goes into its own folder, made when missing, whatever folder the request names.
func TestServeUploadIntoPasteFolder(t *testing.T) {
	tmp := t.TempDir()
	for range 2 {
		line := streamUploadInto(t, tmp, "studio-pastes", agentproto.UploadRequest{Dir: "../elsewhere", Name: "image.png", Size: 3, Conflict: agentproto.ConflictKeep}, "png")
		var reply agentproto.UploadReply
		if err := json.Unmarshal(line, &reply); err != nil || reply.Name == "" {
			t.Fatalf("reply %s, %v", line, err)
		}
		if data, err := os.ReadFile(filepath.Join(tmp, "studio-pastes", reply.Name)); err != nil || string(data) != "png" {
			t.Fatalf("pasted file %q, %v", data, err)
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(tmp, "studio-pastes")); len(entries) != 2 {
		t.Fatalf("pastes %v", entries)
	}
}
