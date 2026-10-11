//go:build linux

package guest

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// The chat's read-only file browser: lists directories and reads files of /workspace.
// Paths resolve beneath /workspace without following any symlink; symlinks are listed as
// such and never opened. A file is only shown if the agent user may read it.

// files serves a KindFiles stream.
func (a *Agent) files(st net.Conn, br *bufio.Reader) error {
	var req agentproto.FilesRequest
	if err := readBody(br, 64<<10, &req); err != nil {
		return replyError(st, err)
	}
	reply, err := browse(Workdir, [2]int{int(a.user.uid), int(a.user.gid)}, req)
	if err != nil {
		return replyError(st, err)
	}
	return agentproto.WriteJSONLine(st, reply)
}

func browse(workspace string, owner [2]int, req agentproto.FilesRequest) (agentproto.FilesReply, error) {
	name := "."
	if req.Path != "" {
		var err error
		if name, err = cleanName(req.Path); err != nil {
			return agentproto.FilesReply{}, errors.New("invalid path")
		}
	}
	root, err := unix.Open(workspace, unix.O_DIRECTORY|unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return agentproto.FilesReply{}, err
	}
	defer unix.Close(root)
	switch req.Op {
	case agentproto.FilesList:
		return listDir(root, name)
	case agentproto.FilesRead:
		if name == "." {
			return agentproto.FilesReply{}, errors.New("no file to read")
		}
		return readFile(root, name, owner)
	}
	return agentproto.FilesReply{}, errors.New("unknown files op")
}

// browseError turns the errors of a symlink or a missing path into plain ones.
func browseError(err error) error {
	switch {
	case errors.Is(err, unix.ENOENT):
		return errors.New("no such file or directory")
	case errors.Is(err, unix.ELOOP), errors.Is(err, unix.EXDEV):
		return errors.New("the path goes through a symlink")
	case errors.Is(err, unix.ENOTDIR):
		return errors.New("not a directory")
	}
	return err
}

func listDir(root int, name string) (agentproto.FilesReply, error) {
	fd, err := unix.Openat2(root, name, beneath)
	if err != nil {
		return agentproto.FilesReply{}, browseError(err)
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	names, err := f.Readdirnames(-1)
	if err != nil {
		return agentproto.FilesReply{}, err
	}
	var reply agentproto.FilesReply
	reply.Entries = []agentproto.FileEntry{}
	for _, n := range names {
		if name == "." && n == lostFound {
			continue
		}
		var fs unix.Stat_t
		if unix.Fstatat(fd, n, &fs, unix.AT_SYMLINK_NOFOLLOW) != nil {
			continue
		}
		e := agentproto.FileEntry{Name: n, Type: "other", ModTime: fs.Mtim.Sec}
		switch fs.Mode & unix.S_IFMT {
		case unix.S_IFREG:
			e.Type, e.Size = "file", fs.Size
		case unix.S_IFDIR:
			e.Type = "dir"
		case unix.S_IFLNK:
			e.Type = "symlink"
		}
		reply.Entries = append(reply.Entries, e)
	}
	slices.SortFunc(reply.Entries, func(a, b agentproto.FileEntry) int {
		if (a.Type == "dir") != (b.Type == "dir") {
			if a.Type == "dir" {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
	if len(reply.Entries) > agentproto.MaxListEntries {
		reply.Entries, reply.Truncated = reply.Entries[:agentproto.MaxListEntries], true
	}
	return reply, nil
}

// previewable are the image types the browser shows; anything else that isn't text is
// only described.
var previewable = []string{"image/png", "image/jpeg", "image/gif", "image/webp"}

func readFile(root int, name string, owner [2]int) (agentproto.FilesReply, error) {
	fd, err := unix.Openat2(root, name, &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
	})
	if err != nil {
		return agentproto.FilesReply{}, browseError(err)
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	var fs unix.Stat_t
	if err := unix.Fstat(fd, &fs); err != nil {
		return agentproto.FilesReply{}, err
	}
	if fs.Mode&unix.S_IFMT != unix.S_IFREG {
		return agentproto.FilesReply{}, errors.New("not a regular file")
	}
	reply := agentproto.FilesReply{Size: fs.Size}
	mode := fs.Mode
	if int(fs.Uid) != owner[0] && mode&0o004 == 0 && (int(fs.Gid) != owner[1] || mode&0o040 == 0) {
		reply.Unreadable = true
		return reply, nil
	}
	if fs.Size > agentproto.MaxReadBytes {
		reply.TooLarge = true
		return reply, nil
	}
	data := make([]byte, agentproto.MaxReadBytes+1)
	n, err := readFull(f, data)
	if err != nil {
		return agentproto.FilesReply{}, err
	}
	if n > agentproto.MaxReadBytes {
		reply.TooLarge = true
		return reply, nil
	}
	data = data[:n]
	reply.Size = int64(n)
	reply.MimeType = http.DetectContentType(data)
	switch {
	case isText(data):
		reply.MimeType = "text/plain; charset=utf-8"
		reply.Data = data
	case slices.Contains(previewable, reply.MimeType):
		reply.Data = data
	default:
		reply.Binary = true
	}
	return reply, nil
}

// readFull reads until buf is full or the file ends.
func readFull(f *os.File, buf []byte) (int, error) {
	n := 0
	for n < len(buf) {
		m, err := f.Read(buf[n:])
		n += m
		if err != nil {
			if m == 0 && errors.Is(err, os.ErrClosed) {
				return n, err
			}
			break
		}
	}
	return n, nil
}
