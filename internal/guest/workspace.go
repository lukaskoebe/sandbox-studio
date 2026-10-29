//go:build linux

package guest

import (
	"archive/tar"
	"bufio"
	"bytes"
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

	"github.com/klauspost/compress/zstd"
	"golang.org/x/sys/unix"
)

// Workspace archives are a PAX tar of the workspace with relative names, compressed with
// gzip or zstd. The first entry, "./", carries the metadata of the workspace root itself.

// Compression is how an export compresses its archive. An import reads either.
type Compression int

const (
	// Gzip is for export files, which a Studio from before zstd reads too.
	Gzip Compression = iota
	// Zstd is for forks and rebases, where this guest agent runs on both sides. It compresses
	// on every CPU and decompresses several times faster than gzip.
	Zstd
)

var zstdMagic = []byte{0x28, 0xb5, 0x2f, 0xfd}

const lostFound = "lost+found"

// ExportWorkspace writes root as a workspace archive to w. It keeps regular files,
// directories, symlinks and hardlinks with their mode, owner and mtime. Sockets, FIFOs and
// devices are skipped with a warning on warn. A file that changes while it is read is
// copied as far as it can be, with a warning; the archive stays well-formed.
func ExportWorkspace(root string, w io.Writer, warn io.Writer) error {
	return ExportWorkspaceAs(root, w, warn, Gzip)
}

// ExportWorkspaceAs is ExportWorkspace with the archive compressed as c.
func ExportWorkspaceAs(root string, w io.Writer, warn io.Writer, c Compression) error {
	return exportTree(root, w, warn, treeOptions{}, c)
}

func compressor(w io.Writer, c Compression) (io.WriteCloser, error) {
	if c == Zstd {
		return zstd.NewWriter(w, zstd.WithEncoderLevel(zstd.SpeedFastest))
	}
	return gzip.NewWriterLevel(w, gzip.BestSpeed)
}

// treeOptions are what an archive of a tree keeps beyond a workspace's.
type treeOptions struct {
	// skip, if set, leaves out what it reports for a relative name.
	skip func(rel string) bool
	// special keeps devices, FIFOs and extended attributes, as Docker's data needs: overlay
	// whiteouts are character devices and opaque directories an xattr.
	special bool
}

// exportTree is ExportWorkspaceAs with opts.
func exportTree(root string, w io.Writer, warn io.Writer, opts treeOptions, c Compression) error {
	gz, err := compressor(w, c)
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
		if opts.skip != nil && opts.skip(rel) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(warn, "skipped %s: it disappeared during the export\n", rel)
			return nil
		}
		if err != nil {
			return err
		}
		return exportEntry(tw, p, rel, info, links, opts.special, warn)
	})
	if err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func exportEntry(tw *tar.Writer, p, rel string, info fs.FileInfo, links map[[2]uint64]string, special bool, warn io.Writer) error {
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
	if special {
		attrs, err := xattrs(p)
		if err != nil {
			return skipVanished(err, rel, warn)
		}
		for name, value := range attrs {
			if h.PAXRecords == nil {
				h.PAXRecords = map[string]string{}
			}
			h.PAXRecords[xattrPrefix+name] = value
		}
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
	case unix.S_IFCHR, unix.S_IFBLK, unix.S_IFIFO:
		if !special {
			fmt.Fprintf(warn, "skipped %s: sockets, FIFOs and devices are not copied\n", rel)
			return nil
		}
		h.Typeflag = map[uint32]byte{unix.S_IFCHR: tar.TypeChar, unix.S_IFBLK: tar.TypeBlock, unix.S_IFIFO: tar.TypeFifo}[st.Mode&unix.S_IFMT]
		h.Devmajor, h.Devminor = int64(unix.Major(st.Rdev)), int64(unix.Minor(st.Rdev))
		return tw.WriteHeader(h)
	default:
		fmt.Fprintf(warn, "skipped %s: sockets are not copied\n", rel)
		return nil
	}
}

// xattrPrefix names an extended attribute in a PAX record, as GNU tar and Go's reader do.
const xattrPrefix = "SCHILY.xattr."

// xattrs reads the extended attributes of p without following a symlink.
func xattrs(p string) (map[string]string, error) {
	size, err := unix.Llistxattr(p, nil)
	if errors.Is(err, unix.ENOTSUP) || size == 0 {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	list := make([]byte, size)
	if size, err = unix.Llistxattr(p, list); err != nil {
		return nil, err
	}
	attrs := map[string]string{}
	for _, name := range strings.Split(string(list[:size]), "\x00") {
		if name == "" {
			continue
		}
		n, err := unix.Lgetxattr(p, name, nil)
		if err != nil {
			return nil, fmt.Errorf("xattr %s: %w", name, err)
		}
		value := make([]byte, n)
		if n, err = unix.Lgetxattr(p, name, value); err != nil {
			return nil, fmt.Errorf("xattr %s: %w", name, err)
		}
		attrs[name] = string(value[:n])
	}
	return attrs, nil
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
	return importTree(root, r, false)
}

// importTree is ImportWorkspace. With special it also creates devices and FIFOs and sets
// extended attributes, the entries an export with treeOptions.special writes.
func importTree(root string, r io.Reader, special bool) error {
	rootFD, err := unix.Open(root, unix.O_DIRECTORY|unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open workspace: %w", err)
	}
	defer unix.Close(rootFD)
	if err := checkEmpty(root); err != nil {
		return err
	}
	gz, err := decompressor(r)
	if err != nil {
		return fmt.Errorf("read archive: %w", err)
	}
	defer gz.Close()
	im := &importer{root: rootFD, seen: map[string]byte{}, special: special}
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
	// Reading to the end verifies the checksum, and gzip's length.
	if _, err := io.Copy(io.Discard, gz); err != nil {
		return fmt.Errorf("read archive: %w", err)
	}
	return im.finishDirs()
}

// decompressor reads a gzip or a zstd archive, as its first bytes say.
func decompressor(r io.Reader) (io.ReadCloser, error) {
	br, ok := r.(*bufio.Reader)
	if !ok {
		br = bufio.NewReader(r)
	}
	magic, err := br.Peek(len(zstdMagic))
	if err == nil && bytes.Equal(magic, zstdMagic) {
		zr, err := zstd.NewReader(br, zstd.WithDecoderLowmem(true))
		if err != nil {
			return nil, err
		}
		return zr.IOReadCloser(), nil
	}
	return gzip.NewReader(br)
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
	root    int
	seen    map[string]byte // archive name → type flag
	dirs    []*tar.Header   // in archive order; their metadata is applied last
	special bool
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
		if err == nil && im.special {
			// After the chown, which drops security.capability.
			err = setXattrs(h, func(name string, value []byte) error { return unix.Fsetxattr(fd, name, value, 0) })
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
		if err := im.setXattrsAt(parent, base, h); err != nil {
			return err
		}
	case tar.TypeChar, tar.TypeBlock, tar.TypeFifo:
		if !im.special {
			return fmt.Errorf("unsupported entry type %q", h.Typeflag)
		}
		kind := map[byte]uint32{tar.TypeChar: unix.S_IFCHR, tar.TypeBlock: unix.S_IFBLK, tar.TypeFifo: unix.S_IFIFO}[h.Typeflag]
		if err := unix.Mknodat(parent, base, kind|0o600, int(unix.Mkdev(uint32(h.Devmajor), uint32(h.Devminor)))); err != nil {
			return err
		}
		if err := unix.Fchownat(parent, base, h.Uid, h.Gid, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return err
		}
		if err := unix.Fchmodat(parent, base, uint32(h.Mode&0o7777), 0); err != nil {
			return err
		}
		ts := []unix.Timespec{unix.NsecToTimespec(h.ModTime.UnixNano()), unix.NsecToTimespec(h.ModTime.UnixNano())}
		if err := unix.UtimesNanoAt(parent, base, ts, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return err
		}
		if err := im.setXattrsAt(parent, base, h); err != nil {
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
		if err == nil && im.special {
			err = setXattrs(h, func(name string, value []byte) error { return unix.Fsetxattr(fd, name, value, 0) })
		}
		unix.Close(fd)
		if err != nil {
			return fmt.Errorf("%q: %w", h.Name, err)
		}
	}
	return nil
}

// setXattrsAt sets the extended attributes of an entry that can't be opened, a symlink or a
// device, through its parent's descriptor, which resolves without following symlinks.
func (im *importer) setXattrsAt(parent int, base string, h *tar.Header) error {
	if !im.special {
		return nil
	}
	p := fmt.Sprintf("/proc/self/fd/%d/%s", parent, base)
	return setXattrs(h, func(name string, value []byte) error { return unix.Lsetxattr(p, name, value, 0) })
}

// setXattrs sets the extended attributes an entry's PAX records name.
func setXattrs(h *tar.Header, set func(name string, value []byte) error) error {
	for k, v := range h.PAXRecords {
		name, ok := strings.CutPrefix(k, xattrPrefix)
		if !ok {
			continue
		}
		if err := set(name, []byte(v)); err != nil {
			return fmt.Errorf("xattr %s: %w", name, err)
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
