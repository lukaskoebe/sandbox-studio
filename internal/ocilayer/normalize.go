// Package ocilayer validates and rewrites uncompressed OCI layer tar streams.
// It never extracts layer contents to the host filesystem.
package ocilayer

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"path"
	"strings"
	"unicode/utf8"
)

const (
	defaultMaxInputBytes           int64 = 8 << 30
	defaultMaxOutputBytes          int64 = 8 << 30
	defaultMaxEntries              int64 = 250_000
	defaultMaxFileBytes            int64 = 2 << 30
	defaultMaxMetadataBytes        int64 = 256 << 10
	defaultMaxPathBytes            int64 = 4096
	defaultMaxPathDepth            int64 = 256
	defaultMaxPathNodes            int64 = 500_000
	defaultMaxPathIndexBytes       int64 = 64 << 20
	defaultMaxTrailingPaddingBytes int64 = 1 << 20
	endMarkerBytes                       = 1024
	nextHeaderAllowance                  = 6*512 + 511
	maxSymlinkPathComponents             = 4096
	maxSymlinkExpansions                 = 40
	maxSymlinkResolverPathBytes          = 8 << 20
)

var (
	ErrInputLimit     = errors.New("OCI layer input limit exceeded")
	ErrOutputLimit    = errors.New("OCI layer output limit exceeded")
	ErrEntryLimit     = errors.New("OCI layer entry limit exceeded")
	ErrFileLimit      = errors.New("OCI layer file limit exceeded")
	ErrMetadataLimit  = errors.New("OCI layer metadata limit exceeded")
	ErrPathLimit      = errors.New("OCI layer path limit exceeded")
	ErrPathDepthLimit = errors.New("OCI layer path depth limit exceeded")
	ErrPathIndexLimit = errors.New("OCI layer path index limit exceeded")
	ErrPaddingLimit   = errors.New("OCI layer trailing padding limit exceeded")
)

// Limits bounds resource use while normalizing a layer. A zero field uses its
// default; negative values are rejected. MaxMetadataBytes applies to parsed
// header metadata and bounds the bytes consumed by any single tar.Reader.Next
// call, with a fixed allowance for record headers and padding. MaxPathDepth,
// MaxPathNodes, and MaxPathIndexBytes bound path traversal and the in-memory
// path index. Go's archive/tar reader independently caps each PAX or GNU
// special header at 1 MiB before allocating it.
type Limits struct {
	MaxInputBytes           int64
	MaxOutputBytes          int64
	MaxEntries              int64
	MaxFileBytes            int64
	MaxMetadataBytes        int64
	MaxPathBytes            int64
	MaxPathDepth            int64
	MaxPathNodes            int64
	MaxPathIndexBytes       int64
	MaxTrailingPaddingBytes int64
}

// DefaultLimits returns the package's resource limits.
func DefaultLimits() Limits {
	return Limits{
		MaxInputBytes:           defaultMaxInputBytes,
		MaxOutputBytes:          defaultMaxOutputBytes,
		MaxEntries:              defaultMaxEntries,
		MaxFileBytes:            defaultMaxFileBytes,
		MaxMetadataBytes:        defaultMaxMetadataBytes,
		MaxPathBytes:            defaultMaxPathBytes,
		MaxPathDepth:            defaultMaxPathDepth,
		MaxPathNodes:            defaultMaxPathNodes,
		MaxPathIndexBytes:       defaultMaxPathIndexBytes,
		MaxTrailingPaddingBytes: defaultMaxTrailingPaddingBytes,
	}
}

// Stats describes the bytes and logical entries processed. InputBytes includes
// the tar end marker and accepted trailing zero padding. FileBytes counts the
// payload bytes of regular files only.
type Stats struct {
	InputBytes  int64
	OutputBytes int64
	Entries     int64
	FileBytes   int64
}

// Normalize validates r as an uncompressed OCI layer tar and writes a
// normalized tar stream to w. It does not close either stream. Output may have
// been partially written when an error is returned; callers must treat all
// output from a failed call as unusable and discard it. Context cancellation is
// checked around I/O, but cannot interrupt a Reader or Writer blocked inside
// its Read or Write method. Callers needing prompt cancellation must close or
// otherwise unblock both streams; Normalize does not close them.
func Normalize(ctx context.Context, w io.Writer, r io.Reader, limits Limits) (Stats, error) {
	var stats Stats
	if ctx == nil {
		return stats, errors.New("nil context")
	}
	if w == nil || r == nil {
		return stats, errors.New("nil OCI layer reader or writer")
	}
	limits, err := withDefaults(limits)
	if err != nil {
		return stats, err
	}
	if err := ctx.Err(); err != nil {
		return stats, err
	}

	in := &boundedReader{ctx: ctx, src: r, max: limits.MaxInputBytes}
	out := &boundedWriter{ctx: ctx, dst: w, max: limits.MaxOutputBytes}
	tr := tar.NewReader(in)
	tw := tar.NewWriter(out)
	entries := make(map[string]entryInfo)
	symlinks := make(map[string]string)
	var pathIndexBytes int64
	var previousPadding int64
	for {
		if err := ctx.Err(); err != nil {
			stats = currentStats(in, out, stats)
			return stats, err
		}
		beforeNext := in.count
		in.beginNext(nextReadBudget(limits.MaxMetadataBytes, previousPadding))
		h, nextErr := tr.Next()
		in.endNext()
		if nextErr == io.EOF {
			// Prove that Next itself consumed two zero records. The tail may
			// otherwise be zero file data, so this check must precede draining.
			consumed := in.count - beforeNext
			expected := previousPadding + endMarkerBytes
			if consumed != expected || !in.hasZeroEndMarker() {
				stats = currentStats(in, out, stats)
				return stats, fmt.Errorf("OCI layer end marker incomplete: Next consumed %d bytes after the previous entry, want %d", consumed, expected)
			}
			break
		}
		if nextErr != nil {
			stats = currentStats(in, out, stats)
			return stats, fmt.Errorf("read OCI layer entry: %w", nextErr)
		}
		if stats.Entries >= limits.MaxEntries {
			stats = currentStats(in, out, stats)
			return stats, fmt.Errorf("%w: maximum is %d", ErrEntryLimit, limits.MaxEntries)
		}
		stats.Entries++

		name, err := normalizeEntryName(h.Name, h.Typeflag, limits.MaxPathBytes)
		if err != nil {
			stats = currentStats(in, out, stats)
			return stats, fmt.Errorf("entry %q: %w", h.Name, err)
		}
		if err := validatePathDepth(name, limits.MaxPathDepth); err != nil {
			stats = currentStats(in, out, stats)
			return stats, fmt.Errorf("entry %q: %w", name, err)
		}
		if err := validateMetadata(h, limits); err != nil {
			stats = currentStats(in, out, stats)
			return stats, fmt.Errorf("entry %q: %w", name, err)
		}
		if h.Format != tar.FormatUSTAR && h.Format != tar.FormatPAX {
			stats = currentStats(in, out, stats)
			return stats, fmt.Errorf("entry %q: unsupported tar format %v", name, h.Format)
		}
		if h.Mode < 0 || h.Mode & ^int64(0o7777) != 0 {
			stats = currentStats(in, out, stats)
			return stats, fmt.Errorf("entry %q: unsupported mode %#o", name, h.Mode)
		}
		if h.Uid < 0 || h.Gid < 0 {
			stats = currentStats(in, out, stats)
			return stats, fmt.Errorf("entry %q: negative uid or gid", name)
		}
		if !h.AccessTime.IsZero() || !h.ChangeTime.IsZero() {
			stats = currentStats(in, out, stats)
			return stats, fmt.Errorf("entry %q: atime and ctime are unsupported metadata", name)
		}
		if h.Devmajor != 0 || h.Devminor != 0 {
			stats = currentStats(in, out, stats)
			return stats, fmt.Errorf("entry %q: device numbers on a non-device entry", name)
		}

		kind, err := entryType(h.Typeflag)
		if err != nil {
			stats = currentStats(in, out, stats)
			return stats, fmt.Errorf("entry %q: %w", name, err)
		}
		if kind != kindRegular && h.Size != 0 {
			stats = currentStats(in, out, stats)
			return stats, fmt.Errorf("entry %q: non-regular entry has size %d", name, h.Size)
		}
		if h.Size < 0 {
			stats = currentStats(in, out, stats)
			return stats, fmt.Errorf("entry %q: negative size", name)
		}
		if kind == kindRegular && h.Size > limits.MaxFileBytes {
			stats = currentStats(in, out, stats)
			return stats, fmt.Errorf("%w: entry %q is %d bytes (maximum %d)", ErrFileLimit, name, h.Size, limits.MaxFileBytes)
		}

		whiteout, err := isWhiteout(name, kind, h.Size)
		if err != nil {
			stats = currentStats(in, out, stats)
			return stats, fmt.Errorf("entry %q: %w", name, err)
		}
		if whiteout {
			kind = kindWhiteout
		}

		var linkname string
		if kind == kindSymlink {
			linkname, err = validateSymlinkTarget(name, h.Linkname, symlinks, entries, limits.MaxPathBytes, limits.MaxPathDepth)
			if err != nil {
				stats = currentStats(in, out, stats)
				return stats, fmt.Errorf("entry %q: %w", name, err)
			}
		}
		if kind == kindHardlink {
			linkname, err = normalizeEntryName(h.Linkname, tar.TypeReg, limits.MaxPathBytes)
			if err != nil {
				stats = currentStats(in, out, stats)
				return stats, fmt.Errorf("entry %q hard link target: %w", name, err)
			}
			if err := validatePathDepth(linkname, limits.MaxPathDepth); err != nil {
				stats = currentStats(in, out, stats)
				return stats, fmt.Errorf("entry %q hard link target: %w", name, err)
			}
			target, ok := entries[linkname]
			if !ok || target.kind != kindRegular {
				stats = currentStats(in, out, stats)
				return stats, fmt.Errorf("entry %q: hard link target %q is not an earlier regular file", name, linkname)
			}
		}

		newPathBytes, err := addEntry(entries, name, kind, linkname, pathIndexBytes, limits)
		if err != nil {
			stats = currentStats(in, out, stats)
			return stats, fmt.Errorf("entry %q: %w", name, err)
		}
		pathIndexBytes += newPathBytes
		if kind == kindSymlink {
			symlinks[name] = linkname
		}

		outHeader := &tar.Header{
			Name:     name,
			Typeflag: h.Typeflag,
			Mode:     h.Mode,
			Uid:      h.Uid,
			Gid:      h.Gid,
			ModTime:  h.ModTime.UTC(),
			Size:     h.Size,
			Format:   tar.FormatPAX,
		}
		if kind == kindSymlink || kind == kindHardlink {
			outHeader.Linkname = linkname
		}
		if kind == kindSymlink || kind == kindHardlink {
			outHeader.Size = 0
		}
		if capability, ok := h.PAXRecords["SCHILY.xattr.security.capability"]; ok {
			outHeader.PAXRecords = map[string]string{"SCHILY.xattr.security.capability": capability}
		}
		if err := tw.WriteHeader(outHeader); err != nil {
			stats = currentStats(in, out, stats)
			return stats, fmt.Errorf("write normalized header for %q: %w", name, err)
		}
		if kind == kindRegular {
			if _, err := io.CopyN(tw, tr, h.Size); err != nil {
				stats = currentStats(in, out, stats)
				return stats, fmt.Errorf("copy contents of %q: %w", name, err)
			}
			if stats.FileBytes > math.MaxInt64-h.Size {
				stats = currentStats(in, out, stats)
				return stats, errors.New("OCI layer file byte count overflow")
			}
			stats.FileBytes += h.Size
			previousPadding = (512 - h.Size%512) % 512
		} else {
			previousPadding = 0
		}
	}

	if err := drainPadding(in, limits.MaxTrailingPaddingBytes); err != nil {
		stats = currentStats(in, out, stats)
		return stats, fmt.Errorf("after OCI layer end: %w", err)
	}
	if err := ctx.Err(); err != nil {
		stats = currentStats(in, out, stats)
		return stats, err
	}
	if err := tw.Close(); err != nil {
		stats = currentStats(in, out, stats)
		return stats, fmt.Errorf("finish normalized OCI layer: %w", err)
	}
	return currentStats(in, out, stats), nil
}

func withDefaults(l Limits) (Limits, error) {
	d := DefaultLimits()
	values := []*int64{
		&l.MaxInputBytes, &l.MaxOutputBytes, &l.MaxEntries, &l.MaxFileBytes,
		&l.MaxMetadataBytes, &l.MaxPathBytes, &l.MaxPathDepth, &l.MaxPathNodes,
		&l.MaxPathIndexBytes, &l.MaxTrailingPaddingBytes,
	}
	defaults := []int64{
		d.MaxInputBytes, d.MaxOutputBytes, d.MaxEntries, d.MaxFileBytes,
		d.MaxMetadataBytes, d.MaxPathBytes, d.MaxPathDepth, d.MaxPathNodes,
		d.MaxPathIndexBytes, d.MaxTrailingPaddingBytes,
	}
	for i, value := range values {
		if *value < 0 {
			return Limits{}, errors.New("OCI layer limits must not be negative")
		}
		if *value == 0 {
			*value = defaults[i]
		}
	}
	return l, nil
}

func currentStats(in *boundedReader, out *boundedWriter, stats Stats) Stats {
	stats.InputBytes = in.count
	stats.OutputBytes = out.count
	return stats
}

func nextReadBudget(maxMetadataBytes, precedingPadding int64) int64 {
	extra := int64(nextHeaderAllowance) + precedingPadding
	if maxMetadataBytes > math.MaxInt64-extra {
		return math.MaxInt64
	}
	return maxMetadataBytes + extra
}

type boundedReader struct {
	ctx        context.Context
	src        io.Reader
	max        int64
	count      int64
	nextBudget int64
	nextUsed   int64
	nextActive bool
	tail       [endMarkerBytes]byte
	tailUsed   int
	tailNext   int
}

func (r *boundedReader) beginNext(budget int64) {
	r.nextBudget = budget
	r.nextUsed = 0
	r.nextActive = true
}

func (r *boundedReader) endNext() {
	r.nextActive = false
}

func (r *boundedReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if len(p) == 0 {
		return 0, nil
	}
	if r.nextActive {
		remaining := r.nextBudget - r.nextUsed
		if remaining <= 0 {
			return 0, ErrMetadataLimit
		}
		if int64(len(p)) > remaining {
			p = p[:int(remaining)]
		}
	}
	if r.count >= r.max {
		var probe [1]byte
		n, err := r.src.Read(probe[:])
		if n > 0 {
			r.remember(probe[:n])
			if r.count < math.MaxInt64 {
				r.count += int64(n)
			}
			if r.nextActive {
				r.nextUsed += int64(n)
			}
			return 0, ErrInputLimit
		}
		return 0, err
	}
	if int64(len(p)) > r.max-r.count {
		p = p[:int(r.max-r.count)]
	}
	n, err := r.src.Read(p)
	if n > 0 {
		r.remember(p[:n])
		r.count += int64(n)
		if r.nextActive {
			r.nextUsed += int64(n)
		}
	}
	return n, err
}

func (r *boundedReader) remember(p []byte) {
	for _, b := range p {
		r.tail[r.tailNext] = b
		r.tailNext = (r.tailNext + 1) % len(r.tail)
		if r.tailUsed < len(r.tail) {
			r.tailUsed++
		}
	}
}

func (r *boundedReader) hasZeroEndMarker() bool {
	if r.tailUsed < len(r.tail) {
		return false
	}
	for _, b := range r.tail {
		if b != 0 {
			return false
		}
	}
	return true
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
		return 0, ErrOutputLimit
	}
	n, err := w.dst.Write(p)
	w.count += int64(n)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	return n, err
}

func drainPadding(r *boundedReader, max int64) error {
	var trailing int64
	var buf [32 << 10]byte
	noProgress := 0
	for {
		want := int64(len(buf))
		if remaining := max - trailing; remaining < want {
			want = remaining + 1
		}
		n, err := r.Read(buf[:int(want)])
		for _, b := range buf[:n] {
			if b != 0 {
				return errors.New("non-zero data after tar end marker")
			}
		}
		if int64(n) > max-trailing {
			return ErrPaddingLimit
		}
		trailing += int64(n)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if n == 0 {
			noProgress++
			if noProgress >= 100 {
				return io.ErrNoProgress
			}
		} else {
			noProgress = 0
		}
	}
}

type entryKind uint8

const (
	kindRegular entryKind = iota + 1
	kindDirectory
	kindSymlink
	kindHardlink
	kindWhiteout
)

type entryInfo struct {
	kind     entryKind
	explicit bool
}

func entryType(flag byte) (entryKind, error) {
	switch flag {
	case tar.TypeReg, tar.TypeRegA:
		return kindRegular, nil
	case tar.TypeDir:
		return kindDirectory, nil
	case tar.TypeSymlink:
		return kindSymlink, nil
	case tar.TypeLink:
		return kindHardlink, nil
	default:
		return 0, fmt.Errorf("unsupported tar entry type %q", flag)
	}
}

func normalizeEntryName(name string, flag byte, maxPath int64) (string, error) {
	if len(name) == 0 || int64(len(name)) > maxPath {
		if int64(len(name)) > maxPath {
			return "", ErrPathLimit
		}
		return "", errors.New("empty path")
	}
	if !utf8.ValidString(name) || strings.ContainsRune(name, '\x00') {
		return "", errors.New("path is not valid UTF-8 or contains NUL")
	}
	if strings.Contains(name, "\\") {
		return "", errors.New("backslash in path")
	}
	if strings.HasPrefix(name, "/") {
		return "", errors.New("absolute path")
	}
	if hasDrivePrefix(name) {
		return "", errors.New("drive-qualified path")
	}
	if flag == tar.TypeDir && strings.HasSuffix(name, "/") {
		name = strings.TrimSuffix(name, "/")
		if strings.HasSuffix(name, "/") {
			return "", errors.New("non-canonical directory path")
		}
	}
	if name == "" || path.Clean(name) != name {
		return "", errors.New("non-canonical relative path")
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return "", errors.New("empty or traversal path component")
		}
	}
	if int64(len(name)) > maxPath {
		return "", ErrPathLimit
	}
	return name, nil
}

func validatePathDepth(name string, maxDepth int64) error {
	if int64(strings.Count(name, "/"))+1 > maxDepth {
		return ErrPathDepthLimit
	}
	return nil
}

func hasDrivePrefix(s string) bool {
	return len(s) >= 2 && ((s[0] >= 'a' && s[0] <= 'z') || (s[0] >= 'A' && s[0] <= 'Z')) && s[1] == ':'
}

func validateMetadata(h *tar.Header, limits Limits) error {
	var total int64
	add := func(s string) error {
		if int64(len(s)) > limits.MaxMetadataBytes-total {
			return ErrMetadataLimit
		}
		total += int64(len(s))
		return nil
	}
	for _, s := range []string{h.Name, h.Linkname, h.Uname, h.Gname} {
		if !utf8.ValidString(s) || strings.ContainsRune(s, '\x00') {
			return errors.New("header contains non-portable text metadata")
		}
		if err := add(s); err != nil {
			return err
		}
	}
	for key, value := range h.PAXRecords {
		if err := add(key); err != nil {
			return err
		}
		if err := add(value); err != nil {
			return err
		}
		switch key {
		case "path", "linkpath", "size", "uid", "gid", "uname", "gname", "mtime":
			if value == "" {
				return fmt.Errorf("empty PAX value for %q", key)
			}
		case "SCHILY.xattr.security.capability":
			// This is the sole xattr preserved by the output allowlist.
		default:
			if strings.HasPrefix(key, "SCHILY.xattr.") || strings.Contains(key, "xattr") {
				return fmt.Errorf("unsupported extended attribute %q", key)
			}
			return fmt.Errorf("unsupported PAX metadata %q", key)
		}
	}
	return nil
}

func addEntry(entries map[string]entryInfo, name string, kind entryKind, linkname string, usedPathBytes int64, limits Limits) (int64, error) {
	for i := 0; i < len(name); i++ {
		if name[i] != '/' {
			continue
		}
		parent := name[:i]
		if existing, ok := entries[parent]; ok && existing.kind != kindDirectory {
			return 0, fmt.Errorf("path traverses non-directory entry %q", parent)
		}
	}
	var newNodes, newBytes int64
	if _, ok := entries[name]; !ok {
		newNodes++
		newBytes += int64(len(name))
	}
	for i := 0; i < len(name); i++ {
		if name[i] != '/' {
			continue
		}
		parent := name[:i]
		if _, ok := entries[parent]; !ok {
			newNodes++
			newBytes += int64(len(parent))
		}
	}
	if existing, ok := entries[name]; ok && (kind != kindDirectory || existing.kind != kindDirectory || existing.explicit) {
		return 0, errors.New("duplicate or conflicting path")
	}
	additional := newBytes
	if kind == kindSymlink {
		additional += int64(len(name)) + int64(len(linkname))
	}
	if newNodes > limits.MaxPathNodes || int64(len(entries)) > limits.MaxPathNodes-newNodes {
		return 0, fmt.Errorf("%w: maximum is %d path nodes", ErrPathIndexLimit, limits.MaxPathNodes)
	}
	if additional > limits.MaxPathIndexBytes-usedPathBytes {
		return 0, fmt.Errorf("%w: maximum is %d bytes", ErrPathIndexLimit, limits.MaxPathIndexBytes)
	}
	if existing, ok := entries[name]; ok {
		if kind == kindDirectory && existing.kind == kindDirectory && !existing.explicit {
			entries[name] = entryInfo{kind: kindDirectory, explicit: true}
		}
	} else {
		entries[name] = entryInfo{kind: kind, explicit: true}
	}
	for i := 0; i < len(name); i++ {
		if name[i] != '/' {
			continue
		}
		parent := name[:i]
		if _, ok := entries[parent]; !ok {
			entries[parent] = entryInfo{kind: kindDirectory}
		}
	}
	return additional, nil
}

func isWhiteout(name string, kind entryKind, size int64) (bool, error) {
	parts := strings.Split(name, "/")
	for _, component := range parts[:len(parts)-1] {
		if component == ".wh" || strings.HasPrefix(component, ".wh.") {
			return false, errors.New("reserved whiteout marker cannot be a directory component")
		}
	}
	base := parts[len(parts)-1]
	if base == ".wh" {
		return false, errors.New("malformed reserved whiteout marker")
	}
	if base != ".wh..wh..opq" && !strings.HasPrefix(base, ".wh.") {
		return false, nil
	}
	if kind != kindRegular || size != 0 {
		return false, errors.New("whiteout markers must be regular zero-size files")
	}
	if base == ".wh..wh..opq" {
		return true, nil
	}
	target := strings.TrimPrefix(base, ".wh.")
	if target == "" || target == "." || target == ".." || target == ".wh" || strings.HasPrefix(target, ".wh.") {
		return false, errors.New("malformed or ambiguous reserved whiteout marker")
	}
	return true, nil
}

func validateSymlinkTarget(name, target string, symlinks map[string]string, entries map[string]entryInfo, maxPath, maxDepth int64) (string, error) {
	if target == "" || int64(len(target)) > maxPath {
		if int64(len(target)) > maxPath {
			return "", ErrPathLimit
		}
		return "", errors.New("empty symlink target")
	}
	if !utf8.ValidString(target) || strings.ContainsRune(target, '\x00') {
		return "", errors.New("symlink target is not valid UTF-8 or contains NUL")
	}
	if strings.Contains(target, "\\") {
		return "", errors.New("backslash in symlink target")
	}
	if hasDrivePrefix(target) {
		return "", errors.New("drive-qualified symlink target")
	}
	if strings.HasPrefix(target, "//") {
		return "", errors.New("ambiguous double-slash symlink target")
	}
	if err := validatePathDepth(strings.TrimPrefix(target, "/"), maxDepth); err != nil {
		return "", err
	}
	parent := path.Dir(name)
	var initial []string
	if parent != "." {
		initial = strings.Split(parent, "/")
	}
	return target, resolveLinkWithinRoot(initial, name, target, symlinks, entries)
}

// resolveLinkWithinRoot follows links already present in this layer using one
// bounded work stack. Unknown path components are rejected when a later ".."
// would traverse through them, because a later symlink could change resolution.
func resolveLinkWithinRoot(initial []string, selfName, target string, symlinks map[string]string, entries map[string]entryInfo) error {
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
			if entry, ok := entries[current]; ok && entry.kind != kindDirectory {
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
				if entry.kind != kindDirectory {
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
			if entry, known := entries[candidate]; known && entry.kind != kindDirectory && remaining > 0 {
				return fmt.Errorf("symlink target traverses non-directory entry %q", candidate)
			}
		}
	}
	return nil
}
