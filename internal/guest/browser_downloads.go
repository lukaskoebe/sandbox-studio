//go:build linux

package guest

import (
	"bufio"
	"cmp"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// The files a browser saved are its session's downloads folder (browserSlot.downloads). Studio
// lists them and reads one at a time; nothing here opens, runs or extracts a file.

// browserDownloads serves a KindBrowserDownloads stream.
func (a *Agent) browserDownloads(st net.Conn, br *bufio.Reader) error {
	return serveBrowserDownloads(st, br, func(ephemeral int) string { return browserSlotFor(ephemeral).downloads })
}

// serveBrowserDownloads answers one KindBrowserDownloads request. folder gives a session's
// downloads folder; a folder that does not exist yet has no downloads.
func serveBrowserDownloads(st net.Conn, br *bufio.Reader, folder func(ephemeral int) string) error {
	var req agentproto.BrowserDownloadsRequest
	if err := agentproto.ReadJSONLineLimit(br, &req, 64<<10); err != nil {
		return replyError(st, err)
	}
	if req.Ephemeral < 0 || req.Ephemeral > agentproto.MaxBrowserEphemeral {
		return replyError(st, errors.New("bad browser session"))
	}
	dir := folder(req.Ephemeral)
	if req.Name == "" {
		files, err := listDownloads(dir)
		if err != nil {
			return replyError(st, err)
		}
		return agentproto.WriteJSONLine(st, agentproto.BrowserDownloadList{Files: files})
	}
	f, d, err := openDownload(dir, req.Name)
	if err != nil {
		return replyError(st, err)
	}
	defer f.Close()
	if err := agentproto.WriteJSONLine(st, d); err != nil {
		return err
	}
	_, err = io.CopyN(st, f, d.Size)
	return err
}

// listDownloads lists the downloads in dir, newest first, at most MaxBrowserDownloads. Only
// regular files a download may be named are listed: a link or a folder is not a download.
func listDownloads(dir string) ([]agentproto.BrowserDownload, error) {
	out := []agentproto.BrowserDownload{}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !downloadName(e.Name()) || !e.Type().IsRegular() {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		out = append(out, agentproto.BrowserDownload{Name: e.Name(), Size: info.Size(), Modified: info.ModTime().Unix()})
	}
	slices.SortFunc(out, func(a, b agentproto.BrowserDownload) int {
		return cmp.Or(cmp.Compare(b.Modified, a.Modified), strings.Compare(a.Name, b.Name))
	})
	return out[:min(len(out), agentproto.MaxBrowserDownloads)], nil
}

// openDownload opens the download name in dir and describes it. The file is opened without
// following a link, and only a regular file is read, so what is read is what was listed.
func openDownload(dir, name string) (*os.File, agentproto.BrowserDownload, error) {
	if !downloadName(name) {
		return nil, agentproto.BrowserDownload{}, fmt.Errorf("invalid download name %q", name)
	}
	fd, err := syscall.Open(filepath.Join(dir, name), syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, agentproto.BrowserDownload{}, errors.New("no such download")
	}
	f := os.NewFile(uintptr(fd), name)
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, agentproto.BrowserDownload{}, errors.New("no such download")
	}
	if info.Size() > agentproto.MaxUploadBytes {
		f.Close()
		return nil, agentproto.BrowserDownload{}, fmt.Errorf("a download is at most %d MiB", agentproto.MaxUploadBytes>>20)
	}
	return f, agentproto.BrowserDownload{Name: name, Size: info.Size(), Modified: info.ModTime().Unix()}, nil
}

// downloadName reports whether name can be a finished download: one plain file name that is
// not hidden and not one Chromium is still writing.
func downloadName(name string) bool {
	return checkUploadName(name) == nil && !strings.HasPrefix(name, ".") && !strings.HasSuffix(name, ".crdownload")
}
