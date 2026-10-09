//go:build linux

package guest

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Workspace archives are a gzip PAX tar of the workspace with relative names. The first
// entry, "./", carries the metadata of the workspace root itself.

const lostFound = "lost+found"

// ExportWorkspace writes root as a workspace archive to w. It keeps regular files,
// directories, symlinks and hardlinks with their mode, owner and mtime. Sockets, FIFOs and
// devices are skipped with a warning on warn. A file that changes while it is read is
// copied as far as it can be, with a warning; the archive stays well-formed.
func ExportWorkspace(root string, w io.Writer, warn io.Writer) error {
	gz, err := gzip.NewWriterLevel(w, gzip.BestSpeed)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(gz)
	links := map[[2]uint64]string{} // device and inode → first archived name
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && p != root {
				fmt.Fprintf(warn, "skipped %s: it disappeared during the export\n", p)
				return nil
			}
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == lostFound && d.IsDir() {
			return fs.SkipDir
		}
		info, err := d.Info()
		if errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(warn, "skipped %s: it disappeared during the export\n", rel)
			return nil
		}
		if err != nil {
			return err
		}
		return exportEntry(tw, p, rel, info, links, warn)
	})
	if err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func exportEntry(tw *tar.Writer, p, rel string, info fs.FileInfo, links map[[2]uint64]string, warn io.Writer) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("%s: no file status", rel)
	}
	h := &tar.Header{
		Name:    rel,
		Mode:    int64(st.Mode & 0o7777),
		Uid:     int(st.Uid),
		Gid:     int(st.Gid),
		ModTime: time.Unix(st.Mtim.Sec, st.Mtim.Nsec),
		Format:  tar.FormatPAX,
	}
	switch st.Mode & unix.S_IFMT {
	case unix.S_IFDIR:
		h.Typeflag = tar.TypeDir
		h.Name += "/"
		return tw.WriteHeader(h)
	case unix.S_IFLNK:
		target, err := os.Readlink(p)
		if err != nil {
			return skipVanished(err, rel, warn)
		}
		h.Typeflag, h.Linkname = tar.TypeSymlink, target
		return tw.WriteHeader(h)
	case unix.S_IFREG:
		key := [2]uint64{uint64(st.Dev), st.Ino}
		if first, ok := links[key]; ok && st.Nlink > 1 {
			h.Typeflag, h.Linkname = tar.TypeLink, first
			return tw.WriteHeader(h)
		}
		f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			return skipVanished(err, rel, warn)
		}
		defer f.Close()
		if st.Nlink > 1 {
			links[key] = rel
		}
		h.Typeflag, h.Size = tar.TypeReg, st.Size
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		n, err := io.CopyN(tw, f, st.Size)
		if errors.Is(err, io.EOF) {
			// The file shrank during the export. Pad it to the size in its header.
			fmt.Fprintf(warn, "%s changed during the export\n", rel)
			_, err = io.CopyN(tw, zeroReader{}, st.Size-n)
		}
		return err
	default:
		fmt.Fprintf(warn, "skipped %s: sockets, FIFOs and devices are not copied\n", rel)
		return nil
	}
}

func skipVanished(err error, rel string, warn io.Writer) error {
	if errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(warn, "skipped %s: it disappeared during the export\n", rel)
		return nil
	}
	return err
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

// ImportWorkspace extracts a workspace archive from r into root, which must be empty apart
// from lost+found. It rejects absolute or escaping names, names that would resolve through
// a symlink, entries whose parent directory is not in the archive, duplicate entries, and
// entry types other than files, directories, symlinks and hardlinks. It restores owners,
// modes and mtimes. A failed import leaves a partial tree; the caller discards it.
func ImportWorkspace(root string, r io.Reader) error {
	rootFD, err := unix.Open(root, unix.O_DIRECTORY|unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open workspace: %w", err)
	}
	defer unix.Close(rootFD)
	if err := checkEmpty(root); err != nil {
		return err
	}
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("read archive: %w", err)
	}
	im := &importer{root: rootFD, seen: map[string]byte{}}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read archive: %w", err)
		}
		if err := im.entry(h, tr); err != nil {
			return fmt.Errorf("%q: %w", h.Name, err)
		}
	}
	// Reading to the end verifies the gzip checksum and length.
	if _, err := io.Copy(io.Discard, gz); err != nil {
		return fmt.Errorf("read archive: %w", err)
	}
	return im.finishDirs()
}

func checkEmpty(root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() != lostFound {
			return fmt.Errorf("workspace is not empty: it contains %q", e.Name())
		}
	}
	return nil
}

type importer struct {
	root int
	seen map[string]byte // archive name → type flag
	dirs []*tar.Header   // in archive order; their metadata is applied last
}

func (im *importer) entry(h *tar.Header, r io.Reader) error {
	if h.Name == "./" || h.Name == "." {
		if h.Typeflag != tar.TypeDir {
			return errors.New("the workspace root must be a directory")
		}
		if _, dup := im.seen["."]; dup {
			return errors.New("duplicate entry")
		}
		im.seen["."] = tar.TypeDir
		im.dirs = append(im.dirs, h)
		return nil
	}
	name, err := cleanName(h.Name)
	if err != nil {
		return err
	}
	if _, dup := im.seen[name]; dup {
		return errors.New("duplicate entry")
	}
	if name == lostFound || strings.HasPrefix(name, lostFound+"/") {
		return errors.New("lost+found is reserved")
	}
	dir, base := path.Split(name)
	dir = strings.TrimSuffix(dir, "/")
	if dir == "" {
		dir = "."
	} else if im.seen[dir] != tar.TypeDir {
		return errors.New("its parent directory is not in the archive")
	}
	parent, err := im.openDir(dir)
	if err != nil {
		return err
	}
	defer unix.Close(parent)

	switch h.Typeflag {
	case tar.TypeDir:
		if err := unix.Mkdirat(parent, base, 0o700); err != nil {
			return err
		}
		im.dirs = append(im.dirs, h)
	case tar.TypeReg:
		fd, err := unix.Openat(parent, base, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
		if err != nil {
			return err
		}
		f := os.NewFile(uintptr(fd), name)
		_, err = io.Copy(f, r)
		if err == nil {
			err = setMetadata(fd, h)
		}
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
	case tar.TypeSymlink:
		if h.Linkname == "" {
			return errors.New("empty symlink target")
		}
		if err := unix.Symlinkat(h.Linkname, parent, base); err != nil {
			return err
		}
		if err := unix.Fchownat(parent, base, h.Uid, h.Gid, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return err
		}
		ts := []unix.Timespec{unix.NsecToTimespec(h.ModTime.UnixNano()), unix.NsecToTimespec(h.ModTime.UnixNano())}
		if err := unix.UtimesNanoAt(parent, base, ts, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return err
		}
	case tar.TypeLink:
		target, err := cleanName(h.Linkname)
		if err != nil {
			return fmt.Errorf("hardlink target: %w", err)
		}
		if im.seen[target] != tar.TypeReg {
			return errors.New("hardlink target is not a regular file earlier in the archive")
		}
		tdir, tbase := path.Split(target)
		tparent, err := im.openDir(strings.TrimSuffix(tdir, "/"))
		if err != nil {
			return err
		}
		err = unix.Linkat(tparent, tbase, parent, base, 0)
		unix.Close(tparent)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported entry type %q", h.Typeflag)
	}
	im.seen[name] = h.Typeflag
	return nil
}

// openDir opens a directory beneath the workspace root without following any symlink.
func (im *importer) openDir(name string) (int, error) {
	if name == "" {
		name = "."
	}
	return unix.Openat2(im.root, name, &unix.OpenHow{
		Flags:   unix.O_DIRECTORY | unix.O_RDONLY | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
	})
}

// finishDirs applies directory metadata, deepest first, once nothing more is written.
func (im *importer) finishDirs() error {
	for i := len(im.dirs) - 1; i >= 0; i-- {
		h := im.dirs[i]
		name := "."
		if h.Name != "./" && h.Name != "." {
			name, _ = cleanName(h.Name)
		}
		fd, err := im.openDir(name)
		if err != nil {
			return fmt.Errorf("%q: %w", h.Name, err)
		}
		err = setMetadata(fd, h)
		unix.Close(fd)
		if err != nil {
			return fmt.Errorf("%q: %w", h.Name, err)
		}
	}
	return nil
}

// setMetadata sets the owner before the mode, since chown clears setuid and setgid bits.
func setMetadata(fd int, h *tar.Header) error {
	if err := unix.Fchown(fd, h.Uid, h.Gid); err != nil {
		return err
	}
	if err := unix.Fchmod(fd, uint32(h.Mode&0o7777)); err != nil {
		return err
	}
	// futimens: utimensat with a null path sets the times of fd itself.
	ts := [2]unix.Timespec{unix.NsecToTimespec(h.ModTime.UnixNano()), unix.NsecToTimespec(h.ModTime.UnixNano())}
	if _, _, errno := unix.Syscall6(unix.SYS_UTIMENSAT, uintptr(fd), 0, uintptr(unsafe.Pointer(&ts[0])), 0, 0, 0); errno != 0 {
		return errno
	}
	return nil
}

// cleanName validates a relative archive name and returns it without a trailing slash.
func cleanName(name string) (string, error) {
	name = strings.TrimSuffix(name, "/")
	if name == "" || strings.HasPrefix(name, "/") || strings.ContainsRune(name, 0) {
		return "", errors.New("invalid name")
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return "", errors.New("invalid name")
		}
	}
	return name, nil
}
