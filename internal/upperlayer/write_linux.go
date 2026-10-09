//go:build linux

// Package upperlayer streams a frozen OverlayFS upper directory into an OCI
// layer tar without extracting or mounting it.
package upperlayer

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lukaskoebe/sandbox-studio/internal/ocilayer"
	"golang.org/x/sys/unix"
)

const (
	directoryReadBuffer               = 32 << 10
	fileReadBuffer                    = 32 << 10
	maxSymlinkTargetBytes       int64 = 1 << 20
	maxSymlinkPathComponents          = 4096
	maxSymlinkExpansions              = 40
	maxSymlinkResolverPathBytes       = 8 << 20
)

var excludedPaths = []string{
	"workspace",
	"var/lib/docker",
	"var/lib/containerd",
	"opt/studio",
	"run",
	"tmp",
	"var/tmp",
	"proc",
	"sys",
	"dev",
	".msb",
	".msbinit",
	"lost+found",
	"etc/sandbox-studio",
	"etc/ssl/certs",
	"usr/local/share/ca-certificates/sandbox-studio.crt",
	"etc/profile.d/sandbox-studio-env.sh",
	"etc/hostname",
	"etc/hosts",
	"etc/resolv.conf",
	"etc/machine-id",
	"var/lib/dbus/machine-id",
	"var/log",
	"var/cache/apt/archives",
	"var/lib/apt/lists",
}

// Write streams upper as a deterministic, uncompressed OCI layer tar. The
// caller must keep upper frozen and pinned for the duration of this call.
// Output may be partial on error and must then be discarded.
func Write(ctx context.Context, w io.Writer, upper *os.File, limits ocilayer.Limits) error {
	if ctx == nil {
		return errors.New("nil context")
	}
	if w == nil || upper == nil {
		return errors.New("nil upper-layer reader or writer")
	}
	limits, err := withDefaults(limits)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	var supplied unix.Stat_t
	if err := unix.Fstat(int(upper.Fd()), &supplied); err != nil {
		return fmt.Errorf("stat pinned upper directory: %w", err)
	}
	if supplied.Mode&unix.S_IFMT != unix.S_IFDIR {
		return errors.New("pinned upper descriptor is not a directory")
	}

	rootFD, err := unix.Openat(int(upper.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_NOATIME|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("reopen pinned upper directory with O_NOATIME: %w", err)
	}
	defer unix.Close(rootFD)
	var rootStat unix.Stat_t
	if err := unix.Fstat(rootFD, &rootStat); err != nil {
		return fmt.Errorf("stat reopened upper directory: %w", err)
	}
	if rootStat.Mode&unix.S_IFMT != unix.S_IFDIR || rootStat.Dev != supplied.Dev || rootStat.Ino != supplied.Ino {
		return errors.New("reopened upper directory identity changed")
	}

	out := &boundedWriter{ctx: ctx, dst: w, max: limits.MaxOutputBytes}
	tw := tar.NewWriter(out)
	walker := &walker{
		ctx:       ctx,
		tar:       tw,
		limits:    limits,
		device:    rootStat.Dev,
		entries:   make(map[string]entryKind),
		symlinks:  make(map[string]string),
		hardlinks: make(map[inodeKey]string),
	}
	if err := walker.writeTree(rootFD); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("finish upper-layer tar: %w", err)
	}
	return nil
}

type boundedWriter struct {
	ctx   context.Context
	dst   io.Writer
	max   int64
	count int64
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if int64(len(p)) > w.max-w.count {
		return 0, ocilayer.ErrOutputLimit
	}
	n, err := w.dst.Write(p)
	if n < 0 || n > len(p) {
		return 0, errors.New("invalid write count from OCI layer destination")
	}
	w.count += int64(n)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	return n, err
}

type walker struct {
	ctx            context.Context
	tar            *tar.Writer
	limits         ocilayer.Limits
	device         uint64
	entries        map[string]entryKind
	symlinks       map[string]string
	hardlinks      map[inodeKey]string
	entryCount     int64
	visitedCount   int64
	inputBytes     int64
	pathIndexBytes int64
	directoryBytes int64
}

type inodeKey struct {
	device uint64
	inode  uint64
}

type dirFrame struct {
	fd    int
	path  string
	items []string
	bytes int64
	next  int
}

func (w *walker) writeTree(rootFD int) error {
	rootAttrs, err := listFDXattrs(rootFD, w.limits.MaxMetadataBytes)
	if err != nil {
		return fmt.Errorf("inspect upper root attributes: %w", err)
	}
	if err := validateRootAttributes(rootAttrs); err != nil {
		return fmt.Errorf("inspect upper root attributes: %w", err)
	}

	names, namesBytes, err := w.readSortedNames(rootFD)
	if err != nil {
		return fmt.Errorf("enumerate upper root: %w", err)
	}
	stack := []dirFrame{{fd: rootFD, path: "", items: names, bytes: namesBytes}}
	// The root descriptor is owned by Write; child descriptors are owned by
	// their frames and closed as traversal unwinds.
	for len(stack) > 0 {
		if err := w.ctx.Err(); err != nil {
			closeFrames(stack)
			return err
		}
		top := &stack[len(stack)-1]
		if top.next == len(top.items) {
			w.directoryBytes -= top.bytes
			if top.path != "" {
				_ = unix.Close(top.fd)
			}
			stack = stack[:len(stack)-1]
			continue
		}
		name := top.items[top.next]
		top.next++
		full := name
		if top.path != "" {
			full = top.path + "/" + name
		}
		if err := w.visit(top.fd, name, full, &stack); err != nil {
			closeFrames(stack)
			return err
		}
	}
	return nil
}

func closeFrames(frames []dirFrame) {
	for i := range frames {
		if frames[i].path != "" {
			_ = unix.Close(frames[i].fd)
		}
	}
}

func (w *walker) visit(parentFD int, name, full string, stack *[]dirFrame) error {
	if err := w.ctx.Err(); err != nil {
		return err
	}
	if name == "" || name == "." || name == ".." || strings.ContainsRune(name, '/') || strings.ContainsRune(name, '\x00') {
		return fmt.Errorf("unsafe directory entry name %q", name)
	}
	// Excluded paths are omitted before inspecting their type or attributes.
	// In particular, reserved names and whiteouts beneath an excluded path do
	// not affect the base runtime tree.
	if isExcluded(full) {
		return nil
	}
	if int64(len(name)) > w.limits.MaxPathBytes {
		return fmt.Errorf("entry %q: %w", full, ocilayer.ErrPathLimit)
	}
	if !utf8.ValidString(name) || strings.Contains(name, "\\") {
		return fmt.Errorf("entry %q: non-portable path component", full)
	}
	if err := validateArchivePath(full, w.limits); err != nil {
		return fmt.Errorf("entry %q: %w", full, err)
	}
	if strings.HasPrefix(name, ".wh.") || name == ".wh" {
		return fmt.Errorf("entry %q: reserved .wh.* input filename", full)
	}
	var st unix.Stat_t
	if err := unix.Fstatat(parentFD, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return fmt.Errorf("stat upper entry %q without following links: %w", full, err)
	}
	if st.Dev != w.device {
		return fmt.Errorf("entry %q is on a different filesystem device", full)
	}
	if isAncestorOfExcluded(full) && st.Mode&unix.S_IFMT != unix.S_IFDIR {
		return fmt.Errorf("non-directory ancestor %q could replace an excluded runtime subtree", full)
	}
	if st.Mode&unix.S_IFMT == unix.S_IFCHR && unix.Major(st.Rdev) == 0 && unix.Minor(st.Rdev) == 0 {
		if excludedWhiteout(full) {
			return fmt.Errorf("whiteout %q could delete an excluded runtime path", full)
		}
		return w.writeWhiteout(full, &st, parentFD, name)
	}
	if w.visitedCount >= w.limits.MaxEntries {
		return fmt.Errorf("%w: upper traversal exceeds maximum of %d entries", ocilayer.ErrEntryLimit, w.limits.MaxEntries)
	}
	w.visitedCount++

	switch st.Mode & unix.S_IFMT {
	case unix.S_IFDIR:
		return w.writeDirectory(parentFD, name, full, &st, stack)
	case unix.S_IFREG:
		return w.writeRegular(parentFD, name, full, &st)
	case unix.S_IFLNK:
		return w.writeSymlink(parentFD, name, full, &st)
	case unix.S_IFCHR, unix.S_IFBLK:
		return fmt.Errorf("entry %q: unsupported device node", full)
	case unix.S_IFIFO:
		return fmt.Errorf("entry %q: unsupported FIFO", full)
	case unix.S_IFSOCK:
		return fmt.Errorf("entry %q: unsupported socket", full)
	default:
		return fmt.Errorf("entry %q: unsupported filesystem type %#o", full, st.Mode&unix.S_IFMT)
	}
}

func (w *walker) writeDirectory(parentFD int, name, full string, st *unix.Stat_t, stack *[]dirFrame) error {
	fd, err := unix.Openat(parentFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_NOATIME|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open upper directory %q with O_NOATIME: %w", full, err)
	}
	var opened unix.Stat_t
	if err := unix.Fstat(fd, &opened); err != nil {
		_ = unix.Close(fd)
		return fmt.Errorf("stat opened directory %q: %w", full, err)
	}
	if !sameObject(st, &opened) || opened.Dev != w.device || opened.Mode&unix.S_IFMT != unix.S_IFDIR {
		_ = unix.Close(fd)
		return fmt.Errorf("directory %q changed during traversal", full)
	}
	attrs, err := listFDXattrs(fd, w.limits.MaxMetadataBytes)
	if err != nil {
		_ = unix.Close(fd)
		return fmt.Errorf("inspect attributes on directory %q: %w", full, err)
	}
	features, capability, err := inspectXattrs(attrs, true)
	if err != nil {
		_ = unix.Close(fd)
		return fmt.Errorf("directory %q: %w", full, err)
	}
	if features.whiteout {
		_ = unix.Close(fd)
		return fmt.Errorf("directory %q: overlay whiteout attribute on a directory", full)
	}
	if features.opaque && opaqueDeletesExcluded(full) {
		_ = unix.Close(fd)
		return fmt.Errorf("opaque directory %q could delete an excluded runtime subtree", full)
	}
	items, namesBytes, err := w.readSortedNames(fd)
	if err != nil {
		_ = unix.Close(fd)
		return fmt.Errorf("enumerate directory %q: %w", full, err)
	}
	if err := w.emit(headerFor(st, full, tar.TypeDir, 0, "", capability)); err != nil {
		_ = unix.Close(fd)
		return err
	}
	if features.opaque {
		if err := w.writeOpaqueMarker(full, st); err != nil {
			_ = unix.Close(fd)
			return err
		}
	}
	*stack = append(*stack, dirFrame{fd: fd, path: full, items: items, bytes: namesBytes})
	return nil
}

func (w *walker) writeRegular(parentFD int, name, full string, st *unix.Stat_t) error {
	if st.Size < 0 {
		return fmt.Errorf("entry %q: negative file size", full)
	}
	fd, err := unix.Openat(parentFD, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NOATIME|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return fmt.Errorf("open upper file %q with O_NOATIME: %w", full, err)
	}
	defer unix.Close(fd)
	var opened unix.Stat_t
	if err := unix.Fstat(fd, &opened); err != nil {
		return fmt.Errorf("stat opened file %q: %w", full, err)
	}
	if !sameObject(st, &opened) || opened.Dev != w.device || opened.Mode&unix.S_IFMT != unix.S_IFREG || opened.Size != st.Size {
		return fmt.Errorf("file %q changed during traversal", full)
	}
	attrs, err := listFDXattrs(fd, w.limits.MaxMetadataBytes)
	if err != nil {
		return fmt.Errorf("inspect attributes on file %q: %w", full, err)
	}
	features, capability, err := inspectXattrs(attrs, false)
	if err != nil {
		return fmt.Errorf("file %q: %w", full, err)
	}
	if features.whiteout {
		if st.Size != 0 {
			return fmt.Errorf("file %q: overlay whiteout marker must be zero-size", full)
		}
		if excludedWhiteout(full) {
			return fmt.Errorf("whiteout %q could delete an excluded runtime path", full)
		}
		return w.writeWhiteoutHeader(full, st, capability)
	}
	if st.Size > w.limits.MaxFileBytes {
		return fmt.Errorf("entry %q: %w: %d bytes (maximum %d)", full, ocilayer.ErrFileLimit, st.Size, w.limits.MaxFileBytes)
	}
	key := inodeKey{device: opened.Dev, inode: opened.Ino}
	if first, ok := w.hardlinks[key]; ok {
		return w.emit(headerFor(st, full, tar.TypeLink, 0, first, capability))
	}
	if st.Size > w.limits.MaxInputBytes-w.inputBytes {
		return fmt.Errorf("%w: upper file contents exceed maximum of %d bytes", ocilayer.ErrInputLimit, w.limits.MaxInputBytes)
	}
	w.inputBytes += st.Size
	if err := w.emit(headerFor(st, full, tar.TypeReg, st.Size, "", capability)); err != nil {
		return err
	}
	w.hardlinks[key] = full
	if err := copyFileData(w.ctx, fd, w.tar, st.Size); err != nil {
		return fmt.Errorf("stream contents of %q: %w", full, err)
	}
	var after unix.Stat_t
	if err := unix.Fstat(fd, &after); err != nil {
		return fmt.Errorf("restat streamed file %q: %w", full, err)
	}
	if !sameObject(&opened, &after) || after.Size != opened.Size {
		return fmt.Errorf("file %q changed while streaming", full)
	}
	return nil
}

func (w *walker) writeSymlink(parentFD int, name, full string, st *unix.Stat_t) error {
	target, err := readlinkAtBounded(parentFD, name, w.limits.MaxPathBytes)
	if err != nil {
		return fmt.Errorf("read symlink %q without following it: %w", full, err)
	}
	if err := validateSymlink(full, target, w.limits, w.symlinks, w.entries); err != nil {
		return fmt.Errorf("symlink %q: %w", full, err)
	}
	attrs, err := listAtXattrs(parentFD, name, w.limits.MaxMetadataBytes)
	if err != nil {
		return fmt.Errorf("inspect attributes on symlink %q: %w", full, err)
	}
	features, capability, err := inspectXattrs(attrs, false)
	if err != nil {
		return fmt.Errorf("symlink %q: %w", full, err)
	}
	if features.whiteout {
		return fmt.Errorf("symlink %q: overlay whiteout attribute on a symlink", full)
	}
	if err := w.emit(headerFor(st, full, tar.TypeSymlink, 0, target, capability)); err != nil {
		return err
	}
	w.symlinks[full] = target
	return nil
}

func (w *walker) writeWhiteout(full string, st *unix.Stat_t, parentFD int, name string) error {
	attrs, err := listAtXattrs(parentFD, name, w.limits.MaxMetadataBytes)
	if err != nil {
		return fmt.Errorf("inspect attributes on whiteout %q: %w", full, err)
	}
	features, capability, err := inspectXattrs(attrs, false)
	if err != nil {
		return fmt.Errorf("whiteout %q: %w", full, err)
	}
	if features.whiteout {
		return fmt.Errorf("character whiteout %q also has a regular-file whiteout attribute", full)
	}
	return w.writeWhiteoutHeader(full, st, capability)
}

func (w *walker) writeWhiteoutHeader(full string, st *unix.Stat_t, capability []byte) error {
	nameOut := whiteoutTarName(full)
	if err := validateArchivePath(nameOut, w.limits); err != nil {
		return fmt.Errorf("whiteout %q output path: %w", full, err)
	}
	return w.emit(headerFor(st, nameOut, tar.TypeReg, 0, "", capability))
}

func (w *walker) writeOpaqueMarker(directory string, st *unix.Stat_t) error {
	return w.emit(headerFor(st, directory+"/.wh..wh..opq", tar.TypeReg, 0, "", nil))
}

func (w *walker) emit(h *tar.Header) error {
	if err := w.ctx.Err(); err != nil {
		return err
	}
	if err := validateArchivePath(h.Name, w.limits); err != nil {
		return fmt.Errorf("tar entry %q: %w", h.Name, err)
	}
	if h.Mode < 0 || h.Mode&^int64(0o7777) != 0 || h.Uid < 0 || h.Gid < 0 {
		return fmt.Errorf("tar entry %q: unsupported owner or mode", h.Name)
	}
	if h.Typeflag == tar.TypeSymlink {
		if err := validateSymlink(h.Name, h.Linkname, w.limits, w.symlinks, w.entries); err != nil {
			return fmt.Errorf("tar symlink %q: %w", h.Name, err)
		}
	}
	if h.Typeflag == tar.TypeLink {
		target, ok := w.entries[h.Linkname]
		if !ok || target != kindRegular {
			return fmt.Errorf("hard link %q does not target an earlier regular file", h.Name)
		}
	}
	if h.PAXRecords != nil {
		if _, ok := h.PAXRecords["SCHILY.xattr.security.capability"]; !ok && len(h.PAXRecords) != 0 {
			return fmt.Errorf("tar entry %q contains unsupported PAX metadata", h.Name)
		}
	}
	metadataBytes := int64(len(h.Name) + len(h.Linkname))
	if capability, ok := h.PAXRecords["SCHILY.xattr.security.capability"]; ok {
		metadataBytes += int64(len("SCHILY.xattr.security.capability") + len(capability))
	}
	if h.ModTime.Nanosecond() != 0 || h.ModTime.Unix() < 0 || h.ModTime.Unix() > 0o77777777777 {
		metadataBytes += int64(len("mtime") + len(formatPAXTime(h.ModTime)))
	}
	if h.Uid > 0o7777777 {
		metadataBytes += int64(len("uid") + len(strconv.Itoa(h.Uid)))
	}
	if h.Gid > 0o7777777 {
		metadataBytes += int64(len("gid") + len(strconv.Itoa(h.Gid)))
	}
	if h.Size > 0o77777777777 {
		metadataBytes += int64(len("size") + len(strconv.FormatInt(h.Size, 10)))
	}
	if len(h.Name) > 100 {
		metadataBytes += int64(len("path") + len(h.Name))
	}
	if len(h.Linkname) > 100 {
		metadataBytes += int64(len("linkpath") + len(h.Linkname))
	}
	if metadataBytes > w.limits.MaxMetadataBytes {
		return fmt.Errorf("tar entry %q: %w", h.Name, ocilayer.ErrMetadataLimit)
	}
	if err := w.recordEntry(h.Name, headerKind(h.Typeflag), h.Linkname); err != nil {
		return err
	}
	if w.entryCount >= w.limits.MaxEntries {
		return fmt.Errorf("%w: maximum is %d", ocilayer.ErrEntryLimit, w.limits.MaxEntries)
	}
	w.entryCount++
	if err := w.tar.WriteHeader(h); err != nil {
		return fmt.Errorf("write tar header for %q: %w", h.Name, err)
	}
	return nil
}

type entryKind uint8

const (
	kindRegular entryKind = iota + 1
	kindDirectory
	kindSymlink
	kindHardlink
)

func headerKind(flag byte) entryKind {
	switch flag {
	case tar.TypeDir:
		return kindDirectory
	case tar.TypeSymlink:
		return kindSymlink
	case tar.TypeLink:
		return kindHardlink
	default:
		return kindRegular
	}
}

func (w *walker) recordEntry(name string, kind entryKind, linkname string) error {
	for i := 0; i < len(name); i++ {
		if name[i] != '/' {
			continue
		}
		parent := name[:i]
		if existing, ok := w.entries[parent]; ok && existing != kindDirectory {
			return fmt.Errorf("tar path %q traverses non-directory entry %q", name, parent)
		}
	}
	if _, exists := w.entries[name]; exists {
		return fmt.Errorf("duplicate tar path %q", name)
	}
	for i := 0; i < len(name); i++ {
		if name[i] == '/' {
			parent := name[:i]
			if _, ok := w.entries[parent]; !ok {
				return fmt.Errorf("tar entry %q has no earlier directory parent %q", name, parent)
			}
		}
	}
	newBytes := int64(len(name))
	if kind == kindSymlink {
		newBytes += int64(len(name) + len(linkname))
	}
	if int64(len(w.entries)) >= w.limits.MaxPathNodes {
		return fmt.Errorf("%w: maximum is %d path nodes", ocilayer.ErrPathIndexLimit, w.limits.MaxPathNodes)
	}
	if newBytes > w.limits.MaxPathIndexBytes-w.pathIndexBytes {
		return fmt.Errorf("%w: maximum is %d bytes", ocilayer.ErrPathIndexLimit, w.limits.MaxPathIndexBytes)
	}
	w.entries[name] = kind
	w.pathIndexBytes += newBytes
	return nil
}

func (w *walker) readSortedNames(fd int) ([]string, int64, error) {
	buf := make([]byte, directoryReadBuffer)
	items := make([]string, 0)
	var bytesUsed int64
	for {
		if err := w.ctx.Err(); err != nil {
			return nil, 0, err
		}
		n, err := unix.ReadDirent(fd, buf)
		if err != nil {
			return nil, 0, err
		}
		if n == 0 {
			break
		}
		maxNames := len(buf)/18 + 1
		_, _, parsed := unix.ParseDirent(buf[:n], maxNames, nil)
		for _, item := range parsed {
			if item == "." || item == ".." {
				continue
			}
			itemBytes := int64(len(item))
			maxInt64 := int64(^uint64(0) >> 1)
			if itemBytes > maxInt64-bytesUsed || itemBytes > maxInt64-w.directoryBytes {
				return nil, 0, fmt.Errorf("%w: directory entry name byte count overflow", ocilayer.ErrPathIndexLimit)
			}
			if itemBytes > w.limits.MaxPathIndexBytes-w.directoryBytes {
				return nil, 0, fmt.Errorf("%w: active directory entry names exceed %d bytes", ocilayer.ErrPathIndexLimit, w.limits.MaxPathIndexBytes)
			}
			if int64(len(items)) >= w.limits.MaxEntries {
				return nil, 0, fmt.Errorf("%w: upper directory exceeds maximum of %d entries", ocilayer.ErrEntryLimit, w.limits.MaxEntries)
			}
			bytesUsed += itemBytes
			w.directoryBytes += itemBytes
			items = append(items, item)
		}
	}
	sort.Strings(items)
	return items, bytesUsed, nil
}

func copyFileData(ctx context.Context, fd int, dst io.Writer, size int64) error {
	buf := make([]byte, fileReadBuffer)
	remaining := size
	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		want := int64(len(buf))
		if remaining < want {
			want = remaining
		}
		n, err := unix.Read(fd, buf[:int(want)])
		if n > 0 {
			written, writeErr := dst.Write(buf[:n])
			if writeErr != nil {
				return writeErr
			}
			if written != n {
				return io.ErrShortWrite
			}
			remaining -= int64(n)
		}
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			return err
		}
		if n == 0 {
			return io.ErrUnexpectedEOF
		}
	}
	return nil
}

func headerFor(st *unix.Stat_t, name string, flag byte, size int64, linkname string, capability []byte) *tar.Header {
	h := &tar.Header{
		Name:     name,
		Typeflag: flag,
		Mode:     int64(st.Mode & 0o7777),
		Uid:      int(st.Uid),
		Gid:      int(st.Gid),
		ModTime:  time.Unix(st.Mtim.Sec, st.Mtim.Nsec).UTC(),
		Size:     size,
		Linkname: linkname,
		Format:   tar.FormatPAX,
	}
	if capability != nil {
		h.PAXRecords = map[string]string{"SCHILY.xattr.security.capability": string(capability)}
	}
	return h
}

func formatPAXTime(value time.Time) string {
	seconds, nanos := value.Unix(), value.Nanosecond()
	if nanos == 0 {
		return strconv.FormatInt(seconds, 10)
	}
	sign := ""
	if seconds < 0 {
		sign = "-"
		seconds = -(seconds + 1)
		nanos = -(nanos - 1_000_000_000)
	}
	return strings.TrimRight(fmt.Sprintf("%s%d.%09d", sign, seconds, nanos), "0")
}

func sameObject(a, b *unix.Stat_t) bool {
	return a.Dev == b.Dev && a.Ino == b.Ino && a.Mode&unix.S_IFMT == b.Mode&unix.S_IFMT
}

func validateArchivePath(name string, limits ocilayer.Limits) error {
	if name == "" || int64(len(name)) > limits.MaxPathBytes {
		if int64(len(name)) > limits.MaxPathBytes {
			return ocilayer.ErrPathLimit
		}
		return errors.New("empty path")
	}
	if !utf8.ValidString(name) || strings.ContainsRune(name, '\x00') {
		return errors.New("path is not valid UTF-8 or contains NUL")
	}
	if strings.Contains(name, "\\") || strings.HasPrefix(name, "/") || hasDrivePrefix(name) || path.Clean(name) != name {
		return errors.New("non-canonical relative path")
	}
	parts := strings.Split(name, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return errors.New("empty or traversal path component")
		}
	}
	if int64(len(parts)) > limits.MaxPathDepth {
		return ocilayer.ErrPathDepthLimit
	}
	return nil
}

func validateSymlink(name, target string, limits ocilayer.Limits, symlinks map[string]string, entries map[string]entryKind) error {
	if target == "" || int64(len(target)) > limits.MaxPathBytes {
		if int64(len(target)) > limits.MaxPathBytes {
			return ocilayer.ErrPathLimit
		}
		return errors.New("empty symlink target")
	}
	if !utf8.ValidString(target) || strings.ContainsRune(target, '\x00') {
		return errors.New("symlink target is not valid UTF-8 or contains NUL")
	}
	if strings.Contains(target, "\\") {
		return errors.New("backslash in symlink target")
	}
	if hasDrivePrefix(target) {
		return errors.New("drive-qualified symlink target")
	}
	if strings.HasPrefix(target, "//") {
		return errors.New("ambiguous double-slash symlink target")
	}
	if int64(strings.Count(strings.TrimPrefix(target, "/"), "/"))+1 > limits.MaxPathDepth {
		return ocilayer.ErrPathDepthLimit
	}
	parent := path.Dir(name)
	var initial []string
	if parent != "." {
		initial = strings.Split(parent, "/")
	}
	return resolveSymlinkWithinRoot(initial, name, target, symlinks, entries)
}

func resolveSymlinkWithinRoot(initial []string, selfName, target string, symlinks map[string]string, entries map[string]entryKind) error {
	type frame struct {
		parts []string
		next  int
	}
	partsFor := func(value string) []string {
		return strings.Split(strings.TrimPrefix(value, "/"), "/")
	}
	stack := append([]string(nil), initial...)
	stackBytes := 0
	for i, part := range stack {
		stackBytes += len(part)
		if i > 0 {
			stackBytes++
		}
	}
	if strings.HasPrefix(target, "/") {
		stack = nil
		stackBytes = 0
	}
	initialDepth := len(stack)
	firstParts := partsFor(target)
	frames := []frame{{parts: firstParts}}
	remaining := len(firstParts)
	componentBytes := int64(len(target))
	components := 0
	expansions := 0
	var materializedPathBytes int64
	materialize := func() (string, error) {
		if int64(stackBytes) > maxSymlinkResolverPathBytes-materializedPathBytes {
			return "", errors.New("symlink resolution exceeds its path work budget")
		}
		materializedPathBytes += int64(stackBytes)
		return strings.Join(stack, "/"), nil
	}
	for len(frames) > 0 {
		last := len(frames) - 1
		if frames[last].next == len(frames[last].parts) {
			frames = frames[:last]
			continue
		}
		part := frames[last].parts[frames[last].next]
		frames[last].next++
		remaining--
		components++
		if components > maxSymlinkPathComponents {
			return errors.New("symlink target has too many resolved path components")
		}
		if len(stack) > 0 {
			current, err := materialize()
			if err != nil {
				return err
			}
			if entry, ok := entries[current]; ok && entry != kindDirectory {
				return fmt.Errorf("symlink target traverses non-directory entry %q", current)
			}
		}
		switch part {
		case "", ".":
			continue
		case "..":
			if len(stack) == 0 {
				return errors.New("symlink target escapes image root")
			}
			if len(stack) > initialDepth {
				candidate, err := materialize()
				if err != nil {
					return err
				}
				entry, ok := entries[candidate]
				if !ok {
					return errors.New("symlink target traverses an unresolved component before '..'")
				}
				if entry != kindDirectory {
					return fmt.Errorf("symlink target traverses non-directory entry %q before '..'", candidate)
				}
			}
			stackBytes -= len(stack[len(stack)-1])
			stack = stack[:len(stack)-1]
			if len(stack) > 0 {
				stackBytes--
			}
			if len(stack) < initialDepth {
				initialDepth = len(stack)
			}
		default:
			if len(stack) > 0 {
				stackBytes++
			}
			stack = append(stack, part)
			stackBytes += len(part)
			candidate, err := materialize()
			if err != nil {
				return err
			}
			linkTarget, followsLink := symlinks[candidate]
			if candidate == selfName {
				linkTarget, followsLink = target, true
			}
			if followsLink {
				expansions++
				if expansions > maxSymlinkExpansions {
					return errors.New("symlink chain is too deep or cyclic")
				}
				stackBytes -= len(stack[len(stack)-1])
				stack = stack[:len(stack)-1]
				if len(stack) > 0 {
					stackBytes--
				}
				if strings.HasPrefix(linkTarget, "/") {
					stack = nil
					stackBytes = 0
				}
				initialDepth = 0
				if int64(len(linkTarget)) > maxSymlinkResolverPathBytes-componentBytes {
					return errors.New("symlink resolution exceeds its component work budget")
				}
				componentBytes += int64(len(linkTarget))
				linkParts := partsFor(linkTarget)
				remaining += len(linkParts)
				frames = append(frames, frame{parts: linkParts})
				continue
			}
			if entry, known := entries[candidate]; known && entry != kindDirectory && remaining > 0 {
				return fmt.Errorf("symlink target traverses non-directory entry %q", candidate)
			}
		}
	}
	return nil
}

func hasDrivePrefix(s string) bool {
	return len(s) >= 2 && ((s[0] >= 'a' && s[0] <= 'z') || (s[0] >= 'A' && s[0] <= 'Z')) && s[1] == ':'
}

func readlinkAtBounded(dirfd int, name string, maxPath int64) (string, error) {
	max := maxPath
	if max > maxSymlinkTargetBytes {
		max = maxSymlinkTargetBytes
	}
	buf := make([]byte, int(max)+1)
	n, err := unix.Readlinkat(dirfd, name, buf)
	if err != nil {
		return "", err
	}
	if int64(n) > maxPath || n == len(buf) {
		return "", ocilayer.ErrPathLimit
	}
	return string(buf[:n]), nil
}

func isExcluded(name string) bool {
	for _, excluded := range excludedPaths {
		if name == excluded || strings.HasPrefix(name, excluded+"/") {
			return true
		}
	}
	return false
}

func excludedWhiteout(name string) bool {
	return isAncestorOfExcluded(name)
}

func isAncestorOfExcluded(name string) bool {
	for _, excluded := range excludedPaths {
		if strings.HasPrefix(excluded, name+"/") {
			return true
		}
	}
	return false
}

func opaqueDeletesExcluded(directory string) bool {
	for _, excluded := range excludedPaths {
		if strings.HasPrefix(excluded, directory+"/") {
			return true
		}
	}
	return false
}

func whiteoutTarName(full string) string {
	parent := path.Dir(full)
	name := ".wh." + path.Base(full)
	if parent != "." {
		return parent + "/" + name
	}
	return name
}

type overlayFeatures struct {
	opaque   bool
	whiteout bool
}

func inspectXattrs(attrs map[string][]byte, isDirectory bool) (overlayFeatures, []byte, error) {
	var features overlayFeatures
	var capability []byte
	seenOpaque := false
	for name, value := range attrs {
		switch name {
		case "security.capability":
			capability = append([]byte{}, value...)
		case "trusted.overlay.origin", "trusted.overlay.impure":
			// OverlayFS internal hints do not belong in an OCI layer.
		case "trusted.overlay.opaque":
			if seenOpaque {
				return features, nil, errors.New("multiple overlay opaque attributes")
			}
			seenOpaque = true
			switch string(value) {
			case "x":
				// x means the directory's opaque state is only an optimization.
			case "y":
				if !isDirectory {
					return features, nil, errors.New("overlay opaque attribute on a non-directory")
				}
				features.opaque = true
			default:
				return features, nil, fmt.Errorf("unsupported overlay opaque value %q", value)
			}
		case "trusted.overlay.whiteout":
			if len(value) != 0 {
				return features, nil, errors.New("overlay whiteout attribute must have an empty value")
			}
			features.whiteout = true
		default:
			if strings.HasPrefix(name, "trusted.overlay.") {
				return features, nil, fmt.Errorf("unsupported overlay feature attribute %q", name)
			}
			return features, nil, fmt.Errorf("unsupported extended attribute %q", name)
		}
	}
	return features, capability, nil
}

func listFDXattrs(fd int, max int64) (map[string][]byte, error) {
	size, err := unix.Flistxattr(fd, nil)
	if err != nil {
		if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP) {
			return map[string][]byte{}, nil
		}
		return nil, err
	}
	if int64(size) > max {
		return nil, fmt.Errorf("%w: xattr name list is %d bytes", ocilayer.ErrMetadataLimit, size)
	}
	if size == 0 {
		return map[string][]byte{}, nil
	}
	names := make([]byte, size)
	n, err := unix.Flistxattr(fd, names)
	if err != nil {
		return nil, err
	}
	if n > len(names) {
		return nil, errors.New("xattr list changed while reading")
	}
	return readNamedFDAttrs(fd, names[:n], max)
}

func readNamedFDAttrs(fd int, raw []byte, max int64) (map[string][]byte, error) {
	return readNamedAttrs(raw, max, func(name string, remaining int64) ([]byte, error) {
		size, err := unix.Fgetxattr(fd, name, nil)
		if err != nil {
			return nil, err
		}
		if int64(size) > remaining {
			return nil, fmt.Errorf("%w: xattr %q value is %d bytes", ocilayer.ErrMetadataLimit, name, size)
		}
		value := make([]byte, size)
		if size == 0 {
			return value, nil
		}
		n, err := unix.Fgetxattr(fd, name, value)
		if err != nil {
			return nil, err
		}
		if n != len(value) {
			return nil, errors.New("xattr changed while reading")
		}
		return value, nil
	})
}

func readNamedAttrs(raw []byte, max int64, get func(string, int64) ([]byte, error)) (map[string][]byte, error) {
	attrs := make(map[string][]byte)
	remaining := max
	for len(raw) > 0 {
		end := bytes.IndexByte(raw, 0)
		if end <= 0 {
			return nil, errors.New("malformed xattr name list")
		}
		name := string(raw[:end])
		raw = raw[end+1:]
		if !utf8.ValidString(name) {
			return nil, errors.New("xattr name is not valid UTF-8")
		}
		if _, duplicate := attrs[name]; duplicate {
			return nil, fmt.Errorf("duplicate xattr name %q", name)
		}
		if int64(len(name)+1) > remaining {
			return nil, ocilayer.ErrMetadataLimit
		}
		remaining -= int64(len(name) + 1)
		if !knownXattr(name) {
			if strings.HasPrefix(name, "trusted.overlay.") {
				return nil, fmt.Errorf("unsupported overlay feature attribute %q", name)
			}
			return nil, fmt.Errorf("unsupported extended attribute %q", name)
		}
		value, err := get(name, remaining)
		if err != nil {
			return nil, err
		}
		if int64(len(value)) > remaining {
			return nil, ocilayer.ErrMetadataLimit
		}
		remaining -= int64(len(value))
		attrs[name] = value
	}
	return attrs, nil
}

func knownXattr(name string) bool {
	switch name {
	case "security.capability",
		"trusted.overlay.origin", "trusted.overlay.impure", "trusted.overlay.opaque", "trusted.overlay.whiteout", "trusted.overlay.uuid":
		return true
	default:
		return false
	}
}

func validateRootAttributes(attrs map[string][]byte) error {
	rootAttrs := attrs
	if uuid, ok := attrs["trusted.overlay.uuid"]; ok {
		if len(uuid) != 16 {
			return fmt.Errorf("root trusted.overlay.uuid must be exactly 16 bytes, got %d", len(uuid))
		}
		// This persistent OverlayFS instance identifier describes the root
		// overlay, not the root directory's OCI filesystem semantics.
		rootAttrs = make(map[string][]byte, len(attrs)-1)
		for name, value := range attrs {
			if name != "trusted.overlay.uuid" {
				rootAttrs[name] = value
			}
		}
	}
	features, capability, err := inspectXattrs(rootAttrs, true)
	if err != nil {
		return err
	}
	if features.whiteout {
		return errors.New("root directory has an overlay whiteout attribute")
	}
	if capability != nil {
		return errors.New("root security.capability cannot be represented because the root is not emitted")
	}
	if features.opaque {
		return errors.New("root opaque directory is unsupported")
	}
	return nil
}

func listAtXattrs(dirfd int, name string, max int64) (map[string][]byte, error) {
	procPath, err := procFDChildPath(dirfd, name)
	if err != nil {
		return nil, err
	}
	size, err := unix.Llistxattr(procPath, nil)
	if err != nil {
		if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP) {
			return map[string][]byte{}, nil
		}
		return nil, err
	}
	if int64(size) > max {
		return nil, fmt.Errorf("%w: xattr name list is %d bytes", ocilayer.ErrMetadataLimit, size)
	}
	if size == 0 {
		return map[string][]byte{}, nil
	}
	raw := make([]byte, size)
	n, err := unix.Llistxattr(procPath, raw)
	if err != nil {
		return nil, err
	}
	if n != len(raw) {
		return nil, errors.New("xattr list changed while reading")
	}
	return readNamedPathAttrs(procPath, raw, max)
}

func procFDChildPath(dirfd int, name string) (string, error) {
	if name == "" || strings.ContainsRune(name, '/') || name == "." || name == ".." {
		return "", errors.New("xattr path must be one trusted basename")
	}
	// The proc-fd indirection resolves the already-open parent; Llistxattr and
	// Lgetxattr leave the final component itself unfollowed.
	return "/proc/self/fd/" + strconv.Itoa(dirfd) + "/" + name, nil
}

func readNamedPathAttrs(procPath string, raw []byte, max int64) (map[string][]byte, error) {
	return readNamedAttrs(raw, max, func(attr string, remaining int64) ([]byte, error) {
		valueSize, err := unix.Lgetxattr(procPath, attr, nil)
		if err != nil {
			return nil, err
		}
		if int64(valueSize) > remaining {
			return nil, fmt.Errorf("%w: xattr %q value is %d bytes", ocilayer.ErrMetadataLimit, attr, valueSize)
		}
		value := make([]byte, valueSize)
		if valueSize == 0 {
			return value, nil
		}
		got, err := unix.Lgetxattr(procPath, attr, value)
		if err != nil {
			return nil, err
		}
		if got != len(value) {
			return nil, errors.New("xattr changed while reading")
		}
		return value, nil
	})
}

func withDefaults(l ocilayer.Limits) (ocilayer.Limits, error) {
	defaults := ocilayer.DefaultLimits()
	values := []*int64{
		&l.MaxInputBytes, &l.MaxOutputBytes, &l.MaxEntries, &l.MaxFileBytes,
		&l.MaxMetadataBytes, &l.MaxPathBytes, &l.MaxPathDepth, &l.MaxPathNodes,
		&l.MaxPathIndexBytes, &l.MaxTrailingPaddingBytes,
	}
	fallbacks := []int64{
		defaults.MaxInputBytes, defaults.MaxOutputBytes, defaults.MaxEntries, defaults.MaxFileBytes,
		defaults.MaxMetadataBytes, defaults.MaxPathBytes, defaults.MaxPathDepth,
		defaults.MaxPathNodes, defaults.MaxPathIndexBytes, defaults.MaxTrailingPaddingBytes,
	}
	for i, value := range values {
		if *value < 0 {
			return ocilayer.Limits{}, errors.New("OCI layer limits must not be negative")
		}
		if *value == 0 {
			*value = fallbacks[i]
		}
	}
	return l, nil
}
