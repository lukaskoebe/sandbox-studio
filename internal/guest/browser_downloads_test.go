//go:build linux

package guest

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// downloadReply runs serveBrowserDownloads for one request over a pipe and returns the reply
// reader, which holds the header and the body.
func downloadReply(t *testing.T, folder string, req agentproto.BrowserDownloadsRequest) *bufio.Reader {
	t.Helper()
	host, guest := net.Pipe()
	t.Cleanup(func() { host.Close(); guest.Close() })
	go func() { _ = serveBrowserDownloads(guest, bufio.NewReader(guest), func(int) string { return folder }) }()
	go func() { _ = agentproto.WriteJSONLine(host, req) }()
	return bufio.NewReader(host)
}

func writeFile(t *testing.T, path, content string, mtime time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func TestBrowserDownloadsListNewestFirstAndOnlyFiles(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	writeFile(t, filepath.Join(dir, "old.pdf"), "1", now.Add(-time.Hour))
	writeFile(t, filepath.Join(dir, "new.zip"), "22", now)
	writeFile(t, filepath.Join(dir, ".hidden"), "x", now)
	writeFile(t, filepath.Join(dir, "partial.bin.crdownload"), "x", now)
	writeFile(t, filepath.Join(dir, "target"), "x", now)
	if err := os.Symlink(filepath.Join(dir, "target"), filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "folder"), 0o755); err != nil {
		t.Fatal(err)
	}

	var list agentproto.BrowserDownloadList
	line, err := downloadReply(t, dir, agentproto.BrowserDownloadsRequest{}).ReadBytes('\n')
	if err != nil || json.Unmarshal(line, &list) != nil {
		t.Fatalf("listing: %s, %v", line, err)
	}
	var names []string
	for _, f := range list.Files {
		names = append(names, f.Name)
	}
	if strings.Join(names, ",") != "new.zip,target,old.pdf" || list.Files[0].Size != 2 {
		t.Fatalf("listed %v", names)
	}

	// A session that has no folder yet has no downloads.
	line, err = downloadReply(t, filepath.Join(dir, "missing"), agentproto.BrowserDownloadsRequest{}).ReadBytes('\n')
	if err != nil || string(line) != "{\"files\":[]}\n" {
		t.Fatalf("missing folder: %s, %v", line, err)
	}
}

func TestBrowserDownloadReadsTheFileAfterItsHeader(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "report.pdf"), "%PDF-body", time.Now())
	r := downloadReply(t, dir, agentproto.BrowserDownloadsRequest{Name: "report.pdf"})
	line, err := r.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var d agentproto.BrowserDownload
	if err := json.Unmarshal(line, &d); err != nil || d.Name != "report.pdf" || d.Size != 9 {
		t.Fatalf("header %s, %v", line, err)
	}
	body, err := io.ReadAll(io.LimitReader(r, d.Size))
	if err != nil || string(body) != "%PDF-body" {
		t.Fatalf("body %q, %v", body, err)
	}
}

func TestBrowserDownloadRefusesWhatIsNotADownload(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "target"), "secret", time.Now())
	if err := os.Symlink(filepath.Join(dir, "target"), filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "folder"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "part.crdownload"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"link", "folder", "part.crdownload", ".hidden", "../target", "a/b", ""} {
		line, err := downloadReply(t, dir, agentproto.BrowserDownloadsRequest{Name: name}).ReadBytes('\n')
		if name == "" {
			// An empty name lists the folder, which is not a refusal.
			continue
		}
		var e agentproto.Error
		if err != nil || json.Unmarshal(line, &e) != nil || e.Error == "" {
			t.Errorf("name %q: %s, %v; want a refusal", name, line, err)
		}
	}
	if line, _ := downloadReply(t, dir, agentproto.BrowserDownloadsRequest{Ephemeral: agentproto.MaxBrowserEphemeral + 1}).ReadBytes('\n'); !strings.Contains(string(line), "bad browser session") {
		t.Fatalf("a session out of range: %s", line)
	}
}

func TestBrowserDownloadRefusesAFileOverTheUploadLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "huge.iso")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, agentproto.MaxUploadBytes+1); err != nil {
		t.Skipf("sparse files: %v", err)
	}
	if _, _, err := openDownload(dir, "huge.iso"); err == nil {
		t.Fatal("a download over the upload limit was opened")
	}
}
