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
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/klauspost/compress/zstd"
	"golang.org/x/sys/unix"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
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
	if c == Zstd {
		// Only a copy between VMs is zstd; an export file stays as older Studios read it.
		err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeXGlobalHeader, Format: tar.FormatPAX,
			PAXRecords: map[string]string{agentproto.ArchiveSizeRecord: strconv.FormatInt(treeSize(root, opts), 10)}})
		if err != nil {
			return err
		}
	}
	links := map[[2]uint64]string{} // device and inode → first archived name
	err = readAhead(root, opts, func(e walked) error {
		if e.err != nil {
			if errors.Is(e.err, fs.ErrNotExist) && e.p != root {
				fmt.Fprintf(warn, "skipped %s: it disappeared during the export\n", e.p)
				return nil
			}
			return e.err
		}
		if errors.Is(e.infoErr, fs.ErrNotExist) {
			fmt.Fprintf(warn, "skipped %s: it disappeared during the export\n", e.rel)
			return nil
		}
		if e.infoErr != nil {
			return e.infoErr
		}
		return exportEntry(tw, e, links, opts.special, warn)
	})
	if err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// walked is an entry of an export's walk, as readAhead hands it on.
type walked struct {
	p, rel  string
	err     error // the walk's, for p
	info    fs.FileInfo
	infoErr error
	// f is a regular file opened ahead, and cold says none of it was in the cache before its
	// read-ahead, which copyOut drops. Without f, exportEntry opens the file itself.
	f    *os.File
	cold bool
}

const (
	// aheadFiles is how many entries the walk runs ahead of the archive, and aheadBytes how much
	// of each file it asks the disk for meanwhile: a disk that reads several files at a time
	// beats one that waits for each, and the cache holds at most aheadFiles × aheadBytes more.
	aheadFiles = 32
	aheadBytes = 1 << 20
)

// readAhead walks root as walkTree does and calls fn with each entry in order, while the walk
// runs ahead: it reads the next entries' status and starts reading their files. fn's first
// error ends it.
func readAhead(root string, opts treeOptions, fn func(walked) error) error {
	entries := make(chan walked, aheadFiles)
	stop := make(chan struct{})
	walkErr := make(chan error, 1)
	errStopped := errors.New("stopped")
	go func() {
		defer close(entries)
		seen := map[[2]uint64]bool{} // files with several names: only the first is opened ahead
		walkErr <- walkTree(root, opts, func(p, rel string, d fs.DirEntry, err error) error {
			e := walked{p: p, rel: rel, err: err}
			if err == nil {
				e.info, e.infoErr = d.Info()
				if e.infoErr == nil && d.Type().IsRegular() && e.info.Size() > 0 {
					st, _ := e.info.Sys().(*syscall.Stat_t)
					if st == nil || st.Nlink <= 1 || !seen[[2]uint64{uint64(st.Dev), st.Ino}] {
						if st != nil && st.Nlink > 1 {
							seen[[2]uint64{uint64(st.Dev), st.Ino}] = true
						}
						e.f, e.cold = openAhead(p, e.info.Size())
					}
				}
			}
			select {
			case entries <- e:
				return nil
			case <-stop:
				if e.f != nil {
					e.f.Close()
				}
				return errStopped
			}
		})
	}()
	var err error
	for e := range entries {
		if err == nil {
			err = fn(e)
			if err != nil {
				close(stop)
			}
		}
		if e.f != nil {
			e.f.Close()
		}
	}
	if werr := <-walkErr; err == nil && !errors.Is(werr, errStopped) {
		err = werr
	}
	return err
}

// openAhead opens the regular file p of size bytes for the export, and asks the disk for its
// start. It returns nil when it can't open it, which exportEntry then tries itself. O_NONBLOCK
// keeps a file swapped for a FIFO meanwhile from holding up the walk; a regular file ignores it.
func openAhead(p string, size int64) (*os.File, bool) {
	f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, false
	}
	fd := int(f.Fd())
	cold := !cached(fd)
	_ = unix.Fadvise(fd, 0, min(size, aheadBytes), unix.FADV_WILLNEED)
	return f, cold
}

// walkTree walks root as an export does, leaving out lost+found and what opts skips, with each
// entry's name relative to root.
func walkTree(root string, opts treeOptions, fn func(p, rel string, d fs.DirEntry, err error) error) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return fn(p, "", d, err)
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
		return fn(p, rel, d, nil)
	})
}

// treeSize is the bytes of the regular files an export of root writes, counting a file with
// several names once. It reads only the files' status, so it is quick next to the copy; a tree
// that changes meanwhile makes it an estimate.
func treeSize(root string, opts treeOptions) int64 {
	var total int64
	seen := map[[2]uint64]bool{}
	_ = walkTree(root, opts, func(p, rel string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if st, ok := info.Sys().(*syscall.Stat_t); ok && st.Nlink > 1 {
			key := [2]uint64{uint64(st.Dev), st.Ino}
			if seen[key] {
				return nil
			}
			seen[key] = true
		}
		total += info.Size()
		return nil
	})
	return total
}

func exportEntry(tw *tar.Writer, e walked, links map[[2]uint64]string, special bool, warn io.Writer) error {
	p, rel := e.p, e.rel
	st, ok := e.info.Sys().(*syscall.Stat_t)
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
		f, cold := e.f, e.cold
		if f == nil {
			var err error
			if f, err = os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW, 0); err != nil {
				return skipVanished(err, rel, warn)
			}
			defer f.Close()
			cold = st.Size > 0 && !cached(int(f.Fd()))
		}
		if st.Nlink > 1 {
			links[key] = rel
		}
		h.Typeflag, h.Size = tar.TypeReg, st.Size
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		n, err := copyOut(tw, f, st.Size, cold)
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
// With progress set it writes there how far it got, as agentproto.CopyProgressPrefix says.
func ImportWorkspace(root string, r io.Reader, progress io.Writer) error {
	return importTree(root, r, false, progress)
}

// importTree is ImportWorkspace. With special it also creates devices and FIFOs and sets
// extended attributes, the entries an export with treeOptions.special writes.
func importTree(root string, r io.Reader, special bool, progress io.Writer) error {
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
	defer im.cache.drop()
	if progress != nil {
		defer im.report(progress)()
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read archive: %w", err)
		}
		if h.Typeflag == tar.TypeXGlobalHeader {
			if n, err := strconv.ParseInt(h.PAXRecords[agentproto.ArchiveSizeRecord], 10, 64); err == nil && n > 0 {
				im.total.Store(n)
			}
			continue
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
	cache   cacheDropper
	// copied counts the bytes of files written, total those the archive says it holds.
	copied, total atomic.Int64
}

// report writes how far the import got to w about once a second, until the function it
// returns is called, which writes it a last time.
func (im *importer) report(w io.Writer) func() {
	line := func() {
		fmt.Fprintf(w, "%s%d %d\n", agentproto.CopyProgressPrefix, im.copied.Load(), im.total.Load())
	}
	stop, ended := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(ended)
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-tick.C:
				line()
			case <-stop:
				line()
				return
			}
		}
	}()
	return func() {
		close(stop)
		<-ended
	}
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
		var n int64
		w := im.cache.writer(f, &im.copied)
		n, err = io.Copy(w, r)
		if err == nil {
			err = w.finish()
		}
		if err == nil {
			im.cache.written(fd, n)
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
