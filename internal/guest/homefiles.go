//go:build linux

package guest

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// writeHomeFiles writes files below home, owned by uid and gid. Paths are relative and are
// resolved with openat2 beneath home without following any symlink, so a file in the
// untrusted home can't redirect a write elsewhere. Each file is written to a temporary
// file in its directory and renamed over the target, so readers never see half a file and
// a hardlinked target is replaced rather than written through. Missing directories are
// created with mode 0755.
func writeHomeFiles(home string, uid, gid int, files []agentproto.HomeFile) error {
	if err := checkHomeFiles(files); err != nil {
		return err
	}
	root, err := unix.Open(home, unix.O_DIRECTORY|unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open home: %w", err)
	}
	defer unix.Close(root)
	for _, f := range files {
		if err := writeHomeFile(root, uid, gid, f); err != nil {
			return fmt.Errorf("%s: %w", f.Path, err)
		}
	}
	return nil
}

func checkHomeFiles(files []agentproto.HomeFile) error {
	if len(files) == 0 || len(files) > agentproto.MaxHomeFiles {
		return fmt.Errorf("between 1 and %d files can be written at once", agentproto.MaxHomeFiles)
	}
	for _, f := range files {
		if _, err := cleanName(f.Path); err != nil || f.Path != strings.TrimSuffix(f.Path, "/") {
			return fmt.Errorf("invalid path %q", f.Path)
		}
		if len(f.Content) > agentproto.MaxHomeFileBytes {
			return fmt.Errorf("%s is larger than %d KiB", f.Path, agentproto.MaxHomeFileBytes>>10)
		}
		if f.Mode&^0o755 != 0 {
			return fmt.Errorf("%s: mode %o is not allowed", f.Path, f.Mode)
		}
		if f.Block && (!strings.HasPrefix(f.Content, agentproto.BlockBegin) ||
			!strings.HasSuffix(strings.TrimRight(f.Content, "\n"), agentproto.BlockEnd) ||
			strings.Count(f.Content, agentproto.BlockBegin) != 1 || strings.Count(f.Content, agentproto.BlockEnd) != 1) {
			return fmt.Errorf("%s: a managed block must start and end with its markers", f.Path)
		}
	}
	return nil
}

var resolveHome = &unix.OpenHow{
	Flags:   unix.O_DIRECTORY | unix.O_RDONLY | unix.O_CLOEXEC,
	Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
}

func writeHomeFile(root, uid, gid int, f agentproto.HomeFile) error {
	parts := strings.Split(f.Path, "/")
	dir, err := unix.Dup(root)
	if err != nil {
		return err
	}
	defer func() { unix.Close(dir) }()
	for _, part := range parts[:len(parts)-1] {
		next, err := unix.Openat2(dir, part, resolveHome)
		if errors.Is(err, unix.ENOENT) {
			if err := unix.Mkdirat(dir, part, 0o755); err != nil && !errors.Is(err, unix.EEXIST) {
				return err
			}
			if err := unix.Fchownat(dir, part, uid, gid, unix.AT_SYMLINK_NOFOLLOW); err != nil {
				return err
			}
			next, err = unix.Openat2(dir, part, resolveHome)
		}
		if err != nil {
			return err
		}
		unix.Close(dir)
		dir = next
	}
	base := parts[len(parts)-1]

	content := f.Content
	if f.Block {
		existing, err := readExisting(dir, base)
		if err != nil {
			return err
		}
		if content, err = mergeBlock(existing, f.Content); err != nil {
			return err
		}
		if len(content) > 2*agentproto.MaxHomeFileBytes {
			return errors.New("the file would grow too large")
		}
	}

	var suffix [6]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return err
	}
	tmp := "." + base + ".studio-" + hex.EncodeToString(suffix[:])
	fd, err := unix.Openat(dir, tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), tmp)
	_, err = io.WriteString(file, content)
	if err == nil {
		err = unix.Fchown(fd, uid, gid)
	}
	if err == nil {
		err = unix.Fchmod(fd, f.Mode)
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = unix.Renameat(dir, tmp, dir, base)
	}
	if err != nil {
		unix.Unlinkat(dir, tmp, 0)
	}
	return err
}

// readExisting returns the content of a regular file base in dir, or "" if there is none.
// A symlink, directory or other special file is refused.
func readExisting(dir int, base string) (string, error) {
	fd, err := unix.Openat2(dir, base, &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
	})
	if errors.Is(err, unix.ENOENT) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	file := os.NewFile(uintptr(fd), base)
	defer file.Close()
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return "", err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		return "", errors.New("the existing file is not a regular file")
	}
	b, err := io.ReadAll(io.LimitReader(file, agentproto.MaxHomeFileBytes+1))
	if err != nil {
		return "", err
	}
	if len(b) > agentproto.MaxHomeFileBytes {
		return "", fmt.Errorf("the existing file is larger than %d KiB", agentproto.MaxHomeFileBytes>>10)
	}
	return string(b), nil
}

// mergeBlock replaces the managed block of existing with block, or puts block first if
// existing has none. Text outside the block is kept as it is.
func mergeBlock(existing, block string) (string, error) {
	block = strings.TrimRight(block, "\n") + "\n"
	begin := strings.Index(existing, agentproto.BlockBegin)
	if begin < 0 {
		if strings.Contains(existing, agentproto.BlockEnd) {
			return "", errors.New("the file has an end marker without a begin marker")
		}
		if existing == "" {
			return block, nil
		}
		return block + "\n" + existing, nil
	}
	end := strings.Index(existing[begin:], agentproto.BlockEnd)
	if end < 0 {
		return "", errors.New("the managed block in the file has no end marker")
	}
	end += begin + len(agentproto.BlockEnd)
	if end < len(existing) && existing[end] == '\n' {
		end++
	}
	return existing[:begin] + block + existing[end:], nil
}
