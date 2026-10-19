//go:build linux

package guest

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// Uploads put a file the host streams into a folder of /workspace. The file is written under
// a hidden temporary name and then linked or renamed to its final name, so readers never see
// half a file and two uploads can't both take a name. The folder is resolved beneath the
// workspace without following any symlink, like the browser's reads.

// keepTries bounds the "name (n).ext" candidates that ConflictKeep tries.
const keepTries = 1000

// errTaken marks an upload refused because its name is taken under ConflictFail.
var errTaken = errors.New("already exists")

// upload serves a KindUpload stream into the workspace.
func (a *Agent) upload(st net.Conn, br *bufio.Reader) error {
	return serveUpload(st, br, Workdir, "", [2]int{int(a.user.uid), int(a.user.gid)})
}

// paste serves a KindPaste stream into agentproto.PasteDir, which it makes for the agent user
// beneath /tmp.
func (a *Agent) paste(st net.Conn, br *bufio.Reader) error {
	return serveUpload(st, br, path.Dir(agentproto.PasteDir), path.Base(agentproto.PasteDir), [2]int{int(a.user.uid), int(a.user.gid)})
}

// serveUpload stores the stream's file in the folder its request names beneath workspace, or,
// with into set, in the folder into beneath it, which is made when missing.
func serveUpload(st net.Conn, br *bufio.Reader, workspace, into string, owner [2]int) error {
	var req agentproto.UploadRequest
	if err := agentproto.ReadJSONLineLimit(br, &req, 64<<10); err != nil {
		return replyError(st, err)
	}
	if into != "" {
		req.Dir, req.CreateDir = into, true
	}
	if req.Size < 0 || req.Size > agentproto.MaxUploadBytes {
		return replyError(st, fmt.Errorf("a file is at most %d MiB", agentproto.MaxUploadBytes>>20))
	}
	body := &countingReader{r: br}
	reply, err := storeUpload(workspace, owner, req, body)
	// Whatever storeUpload left unread is drained, so the host's writes complete and it can
	// read the reply.
	if rest := req.Size - body.n; rest > 0 {
		_, _ = io.CopyN(io.Discard, br, rest)
	}
	switch {
	case errors.Is(err, errTaken):
		return agentproto.WriteJSONLine(st, agentproto.Error{Error: err.Error(), Code: agentproto.CodeExists})
	case err != nil:
		return replyError(st, err)
	}
	return agentproto.WriteJSONLine(st, reply)
}

// countingReader counts the bytes read, so the rest of a body can be drained.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// storeUpload writes body, which must be req.Size bytes, as the file req names in the folder
// req.Dir of workspace. The body is read only once the request is valid.
func storeUpload(workspace string, owner [2]int, req agentproto.UploadRequest, body io.Reader) (agentproto.UploadReply, error) {
	if err := checkUploadName(req.Name); err != nil {
		return agentproto.UploadReply{}, err
	}
	switch req.Conflict {
	case "", agentproto.ConflictFail, agentproto.ConflictReplace, agentproto.ConflictKeep:
	default:
		return agentproto.UploadReply{}, fmt.Errorf("unknown conflict policy %q", req.Conflict)
	}
	rel := "."
	if req.Dir != "" {
		var err error
		if rel, err = cleanName(req.Dir); err != nil {
			return agentproto.UploadReply{}, errors.New("invalid folder")
		}
	}
	root, err := unix.Open(workspace, unix.O_DIRECTORY|unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return agentproto.UploadReply{}, err
	}
	defer unix.Close(root)
	dir, err := openUploadDir(root, rel, req.CreateDir, owner)
	if err != nil {
		return agentproto.UploadReply{}, browseError(err)
	}
	defer unix.Close(dir)

	var suffix [6]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return agentproto.UploadReply{}, err
	}
	tmp := ".studio-upload-" + hex.EncodeToString(suffix[:])
	fd, err := unix.Openat(dir, tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return agentproto.UploadReply{}, err
	}
	file := os.NewFile(uintptr(fd), tmp)
	n, err := io.CopyN(file, body, req.Size)
	if errors.Is(err, io.EOF) {
		err = io.ErrUnexpectedEOF
	}
	if err == nil {
		err = unix.Fchown(fd, owner[0], owner[1])
	}
	if err == nil {
		err = unix.Fchmod(fd, 0o644)
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	var final string
	if err == nil {
		final, err = placeUpload(dir, tmp, req.Name, req.Conflict)
	}
	if err != nil {
		unix.Unlinkat(dir, tmp, 0)
		return agentproto.UploadReply{}, err
	}
	return agentproto.UploadReply{Name: final, Size: n}, nil
}

// openUploadDir opens the folder rel beneath the workspace root. With create, a missing folder is
// made first, owned by owner; its parent must exist. No link on the way is followed.
func openUploadDir(root int, rel string, create bool, owner [2]int) (int, error) {
	dir, err := unix.Openat2(root, rel, beneath)
	if !create || !errors.Is(err, unix.ENOENT) {
		return dir, err
	}
	parent, base := ".", rel
	if i := strings.LastIndexByte(rel, '/'); i >= 0 {
		parent, base = rel[:i], rel[i+1:]
	}
	p, err := unix.Openat2(root, parent, beneath)
	if err != nil {
		return 0, err
	}
	defer unix.Close(p)
	switch err := unix.Mkdirat(p, base, 0o755); {
	case err == nil:
		if err := unix.Fchownat(p, base, owner[0], owner[1], unix.AT_SYMLINK_NOFOLLOW); err != nil {
			unix.Unlinkat(p, base, unix.AT_REMOVEDIR)
			return 0, err
		}
	case !errors.Is(err, unix.EEXIST):
		return 0, err
	}
	return unix.Openat2(root, rel, beneath)
}

// checkUploadName refuses anything but one file name: a separator, a NUL byte, "." and "..",
// and names longer than a directory entry can be.
func checkUploadName(name string) error {
	if name == "" || name == "." || name == ".." || len(name) > agentproto.MaxUploadName || strings.ContainsAny(name, "/\x00") {
		return fmt.Errorf("invalid file name %q", name)
	}
	return nil
}

// placeUpload moves the written file tmp to name, or under ConflictKeep to the first free
// "name (n).ext", and returns the name it took. Under ConflictFail and ConflictKeep the name is
// taken by a hard link, which fails rather than replacing anything, so no two uploads can take
// one name.
func placeUpload(dir int, tmp, name, conflict string) (string, error) {
	switch conflict {
	case agentproto.ConflictReplace:
		var st unix.Stat_t
		switch err := unix.Fstatat(dir, name, &st, unix.AT_SYMLINK_NOFOLLOW); {
		case err == nil && st.Mode&unix.S_IFMT != unix.S_IFREG:
			return "", fmt.Errorf("%s is a folder or a link: upload it under another name", name)
		case err != nil && !errors.Is(err, unix.ENOENT):
			return "", err
		}
		return name, unix.Renameat(dir, tmp, dir, name)
	case agentproto.ConflictKeep:
		stem, ext := splitUploadName(name)
		for i := 1; i <= keepTries; i++ {
			candidate := name
			if i > 1 {
				candidate = fmt.Sprintf("%s (%d)%s", stem, i, ext)
			}
			if len(candidate) > agentproto.MaxUploadName {
				break
			}
			switch err := linkUpload(dir, tmp, candidate); {
			case err == nil:
				return candidate, nil
			case !errors.Is(err, unix.EEXIST):
				return "", err
			}
		}
		return "", fmt.Errorf("%s: no free name for a copy: %w", name, errTaken)
	}
	if err := linkUpload(dir, tmp, name); err != nil {
		if errors.Is(err, unix.EEXIST) {
			return "", fmt.Errorf("%s: %w", name, errTaken)
		}
		return "", err
	}
	return name, nil
}

// linkUpload gives tmp the name, failing if the name exists, and drops the temporary name.
func linkUpload(dir int, tmp, name string) error {
	if err := unix.Linkat(dir, tmp, dir, name, 0); err != nil {
		return err
	}
	// The temporary name is hidden and harmless if this fails.
	unix.Unlinkat(dir, tmp, 0)
	return nil
}

// splitUploadName splits name before its last extension, so that "name (2).ext" can be built
// from the parts. A dot file keeps its whole name.
func splitUploadName(name string) (stem, ext string) {
	ext = path.Ext(name)
	stem = strings.TrimSuffix(name, ext)
	if stem == "" {
		return name, ""
	}
	return stem, ext
}
