package ocilayer

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

type member struct {
	header tar.Header
	data   string
}

type cancelAfterRead struct {
	reader io.Reader
	cancel context.CancelFunc
	done   bool
}

func (r *cancelAfterRead) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if !r.done {
		r.done = true
		r.cancel()
	}
	return n, err
}

func makeTar(t *testing.T, members ...member) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, m := range members {
		h := m.header
		if h.Typeflag == 0 {
			h.Typeflag = tar.TypeReg
		}
		if h.Typeflag == tar.TypeReg || h.Typeflag == tar.TypeRegA {
			h.Size = int64(len(m.data))
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatalf("WriteHeader(%q): %v", h.Name, err)
		}
		if len(m.data) > 0 {
			if _, err := io.WriteString(tw, m.data); err != nil {
				t.Fatalf("write data for %q: %v", h.Name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func runNormalize(t *testing.T, input []byte, limits Limits) ([]byte, Stats, error) {
	t.Helper()
	var output bytes.Buffer
	stats, err := Normalize(context.Background(), &output, bytes.NewReader(input), limits)
	return output.Bytes(), stats, err
}

func TestNormalizeDebianLikeLayer(t *testing.T) {
	mtime := time.Date(2024, 2, 3, 4, 5, 6, 123456789, time.FixedZone("test", 3600))
	input := makeTar(t,
		member{header: tar.Header{Name: "usr/", Typeflag: tar.TypeDir, Mode: 0o755}},
		member{header: tar.Header{Name: "usr/bin/", Typeflag: tar.TypeDir, Mode: 0o755}},
		member{header: tar.Header{Name: "usr/bin/dpkg", Mode: 0o755, Uid: 42, Gid: 43, Uname: "build-user", Gname: "build-group", ModTime: mtime, Format: tar.FormatPAX}, data: "binary"},
		member{header: tar.Header{Name: "usr/bin/tool", Typeflag: tar.TypeSymlink, Linkname: "/usr/bin/dpkg", Mode: 0o777}},
		member{header: tar.Header{Name: "usr/bin/current", Typeflag: tar.TypeSymlink, Linkname: "tool", Mode: 0o777}},
		member{header: tar.Header{Name: "usr/bin/dpkg-copy", Typeflag: tar.TypeLink, Linkname: "usr/bin/dpkg", Mode: 0o755}},
	)

	output, stats, err := runNormalize(t, input, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Entries != 6 || stats.FileBytes != int64(len("binary")) || stats.InputBytes != int64(len(input)) || stats.OutputBytes != int64(len(output)) {
		t.Fatalf("unexpected stats: %+v", stats)
	}

	tr := tar.NewReader(bytes.NewReader(output))
	var got []tar.Header
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, *h)
		if h.Name == "usr/bin/dpkg" {
			data, err := io.ReadAll(tr)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != "binary" || h.Uid != 42 || h.Gid != 43 || h.Uname != "" || h.Gname != "" {
				t.Fatalf("normalized file metadata/content: header=%+v data=%q", h, data)
			}
			if !h.ModTime.Equal(mtime) {
				t.Fatalf("mtime was not normalized and preserved: %v", h.ModTime)
			}
		}
	}
	if len(got) != 6 {
		t.Fatalf("got %d output entries", len(got))
	}
	if got[0].Name != "usr" || got[1].Name != "usr/bin" {
		t.Fatalf("directory names were not canonicalized: %q, %q", got[0].Name, got[1].Name)
	}
	if got[3].Linkname != "/usr/bin/dpkg" || got[4].Linkname != "tool" || got[5].Linkname != "usr/bin/dpkg" {
		t.Fatalf("link targets changed: %q, %q, %q", got[3].Linkname, got[4].Linkname, got[5].Linkname)
	}
}

func TestNormalizePathValidation(t *testing.T) {
	for _, name := range []string{"/etc/passwd", "../escape", "a/../b", "a//b", "a/./b", `a\b`, "C:/boot.ini"} {
		t.Run(name, func(t *testing.T) {
			input := makeTar(t, member{header: tar.Header{Name: name}, data: "x"})
			_, _, err := runNormalize(t, input, Limits{})
			if err == nil {
				t.Fatalf("accepted unsafe path %q", name)
			}
		})
	}
	for _, tc := range []struct {
		name string
		flag byte
	}{
		{name: "a\x00b", flag: tar.TypeReg},
		{name: "a/../../b", flag: tar.TypeReg},
		{name: "a//b", flag: tar.TypeReg},
	} {
		if _, err := normalizeEntryName(tc.name, tc.flag, 4096); err == nil {
			t.Errorf("accepted unsafe path %q", tc.name)
		}
	}
}

func TestNormalizeLinkSafety(t *testing.T) {
	for _, target := range []string{"../../../escape", "a/../../escape", `..\outside`, "C:/outside", "//server/share"} {
		t.Run(target, func(t *testing.T) {
			input := makeTar(t, member{header: tar.Header{Name: "usr/bin/link", Typeflag: tar.TypeSymlink, Linkname: target, Mode: 0o777}})
			_, _, err := runNormalize(t, input, Limits{})
			if err == nil {
				t.Fatalf("accepted unsafe symlink target %q", target)
			}
		})
	}
	input := makeTar(t,
		member{header: tar.Header{Name: "usr/", Typeflag: tar.TypeDir}},
		member{header: tar.Header{Name: "usr/bin/", Typeflag: tar.TypeDir}},
		member{header: tar.Header{Name: "usr/bin/alias", Typeflag: tar.TypeSymlink, Linkname: "../lib", Mode: 0o777}},
		member{header: tar.Header{Name: "usr/bin/child", Typeflag: tar.TypeSymlink, Linkname: "alias/include", Mode: 0o777}},
	)
	if _, _, err := runNormalize(t, input, Limits{}); err != nil {
		t.Fatalf("rejected confined relative links: %v", err)
	}

	for _, entries := range [][]member{
		{
			{header: tar.Header{Name: "a", Typeflag: tar.TypeSymlink, Linkname: "dir", Mode: 0o777}},
			{header: tar.Header{Name: "a/file"}, data: "x"},
		},
		{
			{header: tar.Header{Name: "usr/", Typeflag: tar.TypeDir}},
			{header: tar.Header{Name: "usr/bin/", Typeflag: tar.TypeDir}},
			{header: tar.Header{Name: "usr/bin/alias", Typeflag: tar.TypeSymlink, Linkname: "somewhere", Mode: 0o777}},
			{header: tar.Header{Name: "usr/bin/escape", Typeflag: tar.TypeSymlink, Linkname: "alias/../../outside", Mode: 0o777}},
		},
		{
			{header: tar.Header{Name: "usr/bin/file"}, data: "x"},
			{header: tar.Header{Name: "usr/bin/link", Typeflag: tar.TypeSymlink, Linkname: "file/child", Mode: 0o777}},
		},
	} {
		_, _, err := runNormalize(t, makeTar(t, entries...), Limits{})
		if err == nil {
			t.Fatal("accepted an entry traversing a symlink parent or escaping through a symlink")
		}
	}
}

func TestNormalizeDuplicateAndHardlinkRules(t *testing.T) {
	for _, entries := range [][]member{
		{{header: tar.Header{Name: "same"}, data: "a"}, {header: tar.Header{Name: "same"}, data: "b"}},
		{{header: tar.Header{Name: "a"}, data: "x"}, {header: tar.Header{Name: "a/b"}, data: "y"}},
		{{header: tar.Header{Name: "a/b"}, data: "x"}, {header: tar.Header{Name: "a", Typeflag: tar.TypeSymlink, Linkname: "b"}}},
		{{header: tar.Header{Name: "later", Typeflag: tar.TypeLink, Linkname: "target"}}, {header: tar.Header{Name: "target"}, data: "x"}},
		{
			{header: tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "target"}},
			{header: tar.Header{Name: "target"}, data: "x"},
			{header: tar.Header{Name: "hard", Typeflag: tar.TypeLink, Linkname: "link"}},
		},
	} {
		_, _, err := runNormalize(t, makeTar(t, entries...), Limits{})
		if err == nil {
			t.Fatal("accepted duplicate, conflicting, or invalid hard-link entries")
		}
	}
}

func TestNormalizeWhiteouts(t *testing.T) {
	valid := makeTar(t,
		member{header: tar.Header{Name: "usr/.wh.old"}},
		member{header: tar.Header{Name: "usr/.wh..wh..opq"}},
	)
	if _, stats, err := runNormalize(t, valid, Limits{}); err != nil || stats.Entries != 2 {
		t.Fatalf("valid whiteouts rejected: stats=%+v err=%v", stats, err)
	}
	replacement := makeTar(t,
		member{header: tar.Header{Name: "usr/.wh.old"}},
		member{header: tar.Header{Name: "usr/old"}, data: "replacement"},
	)
	if _, _, err := runNormalize(t, replacement, Limits{}); err != nil {
		t.Fatalf("whiteout with same-layer replacement rejected: %v", err)
	}
	for _, e := range []member{
		{header: tar.Header{Name: "usr/.wh."}},
		{header: tar.Header{Name: "usr/.wh.."}},
		{header: tar.Header{Name: "usr/.wh..wh"}},
		{header: tar.Header{Name: "usr/.wh..wh.foo"}},
		{header: tar.Header{Name: "usr/.wh.old/child"}},
		{header: tar.Header{Name: "usr/.wh..wh..opq/child"}},
		{header: tar.Header{Name: "usr/.wh/child"}},
		{header: tar.Header{Name: "usr/.wh.old"}, data: "not empty"},
		{header: tar.Header{Name: "usr/.wh.old", Typeflag: tar.TypeDir}},
		{header: tar.Header{Name: "usr/.wh..wh..opq"}, data: "x"},
	} {
		_, _, err := runNormalize(t, makeTar(t, e), Limits{})
		if err == nil {
			t.Fatalf("accepted malformed whiteout %q", e.header.Name)
		}
	}
}

func TestNormalizeMetadataAllowlist(t *testing.T) {
	capability := string([]byte{1, 0, 0, 2, 0, 0, 0, 0})
	input := makeTar(t, member{header: tar.Header{
		Name: "bin/helper", Mode: 0o755,
		PAXRecords: map[string]string{"SCHILY.xattr.security.capability": capability},
	}, data: "x"})
	output, _, err := runNormalize(t, input, Limits{})
	if err != nil {
		t.Fatalf("allowed capability xattr rejected: %v", err)
	}
	tr := tar.NewReader(bytes.NewReader(output))
	h, err := tr.Next()
	if err != nil {
		t.Fatal(err)
	}
	if got := h.PAXRecords["SCHILY.xattr.security.capability"]; got != capability {
		t.Fatalf("capability xattr changed: %q", got)
	}

	for _, key := range []string{"SCHILY.xattr.user.note", "SCHILY.xattr.trusted.overlay.opaque", "LIBARCHIVE.xattr.security.capability", "comment"} {
		input := makeTar(t, member{header: tar.Header{Name: "file", PAXRecords: map[string]string{key: "value"}}, data: "x"})
		_, _, err := runNormalize(t, input, Limits{})
		if err == nil || !strings.Contains(err.Error(), "metadata") && !strings.Contains(err.Error(), "attribute") {
			t.Errorf("unsupported metadata %q was not clearly rejected: %v", key, err)
		}
	}
	if _, err := entryType(tar.TypeChar); err == nil {
		t.Error("accepted character device")
	}
	if _, err := entryType(tar.TypeFifo); err == nil {
		t.Error("accepted FIFO")
	}
	if _, err := entryType(tar.TypeGNUSparse); err == nil {
		t.Error("accepted sparse entry")
	}
}

func TestNormalizeLimits(t *testing.T) {
	base := DefaultLimits()
	input := makeTar(t, member{header: tar.Header{Name: "file"}, data: "four"})
	tests := []struct {
		name   string
		limits Limits
		input  []byte
		want   error
	}{
		{name: "input", limits: func() Limits { l := base; l.MaxInputBytes = 512; return l }(), input: input, want: ErrInputLimit},
		{name: "output", limits: func() Limits { l := base; l.MaxOutputBytes = 512; return l }(), input: makeTar(t), want: ErrOutputLimit},
		{name: "entries", limits: func() Limits { l := base; l.MaxEntries = 1; return l }(), input: makeTar(t, member{header: tar.Header{Name: "a"}}, member{header: tar.Header{Name: "b"}}), want: ErrEntryLimit},
		{name: "file", limits: func() Limits { l := base; l.MaxFileBytes = 3; return l }(), input: input, want: ErrFileLimit},
		{name: "path", limits: func() Limits { l := base; l.MaxPathBytes = 3; return l }(), input: makeTar(t, member{header: tar.Header{Name: "long"}}), want: ErrPathLimit},
		{name: "path depth", limits: func() Limits { l := base; l.MaxPathDepth = 2; return l }(), input: makeTar(t, member{header: tar.Header{Name: "a/b/c"}}), want: ErrPathDepthLimit},
		{name: "metadata", limits: func() Limits { l := base; l.MaxMetadataBytes = 4; return l }(), input: makeTar(t, member{header: tar.Header{Name: "f", PAXRecords: map[string]string{"comment": "value"}}}), want: ErrMetadataLimit},
		{name: "path nodes", limits: func() Limits { l := base; l.MaxPathNodes = 1; return l }(), input: makeTar(t, member{header: tar.Header{Name: "a/b"}}), want: ErrPathIndexLimit},
		{name: "path index bytes", limits: func() Limits { l := base; l.MaxPathIndexBytes = 2; return l }(), input: makeTar(t, member{header: tar.Header{Name: "a/b"}}), want: ErrPathIndexLimit},
		{name: "trailing padding", limits: func() Limits { l := base; l.MaxTrailingPaddingBytes = 1; return l }(), input: append(makeTar(t), 0, 0), want: ErrPaddingLimit},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := runNormalize(t, tc.input, tc.limits)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want errors.Is(_, %v)", err, tc.want)
			}
		})
	}
}

func TestAddEntryPreflightsImplicitPathIndex(t *testing.T) {
	for _, tc := range []struct {
		name   string
		limits Limits
	}{
		{name: "nodes", limits: func() Limits { l := DefaultLimits(); l.MaxPathNodes = 2; return l }()},
		{name: "bytes", limits: func() Limits { l := DefaultLimits(); l.MaxPathIndexBytes = 2; return l }()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries := make(map[string]entryInfo)
			_, err := addEntry(entries, "a/b/c", kindRegular, "", 0, tc.limits)
			if !errors.Is(err, ErrPathIndexLimit) {
				t.Fatalf("error = %v, want ErrPathIndexLimit", err)
			}
			if len(entries) != 0 {
				t.Fatalf("rejected path inserted %d tracked nodes", len(entries))
			}
		})
	}
}

func TestNormalizeBoundsOldGNUSparseExtensionsDuringNext(t *testing.T) {
	const extensionBlocks = 128
	input := make([]byte, 512+extensionBlocks*512+1024)
	header := input[:512]
	copy(header[:100], "sparse")
	writeOctalField(header[100:108], 0o644)
	writeOctalField(header[108:116], 0)
	writeOctalField(header[116:124], 0)
	writeOctalField(header[124:136], 0)
	writeOctalField(header[136:148], 0)
	header[156] = tar.TypeGNUSparse
	copy(header[257:263], "ustar ")
	copy(header[263:265], " \x00")
	header[482] = 1 // The old GNU sparse map continues in extension blocks.
	writeOctalField(header[483:495], 0)
	for i := 148; i < 156; i++ {
		header[i] = ' '
	}
	var checksum int
	for _, b := range header {
		checksum += int(b)
	}
	copy(header[148:156], fmt.Sprintf("%06o\x00 ", checksum))
	for i := 0; i < extensionBlocks; i++ {
		if i+1 < extensionBlocks {
			input[512+i*512+504] = 1
		}
	}

	var output bytes.Buffer
	limits := DefaultLimits()
	limits.MaxMetadataBytes = 1
	stats, err := Normalize(context.Background(), &output, bytes.NewReader(input), limits)
	if !errors.Is(err, ErrMetadataLimit) {
		t.Fatalf("error = %v, want ErrMetadataLimit", err)
	}
	if stats.InputBytes >= int64(len(input)) || stats.InputBytes > nextReadBudget(limits.MaxMetadataBytes, 0) {
		t.Fatalf("sparse extension chain consumed %d of %d bytes before rejection", stats.InputBytes, len(input))
	}
}

func writeOctalField(dst []byte, value uint64) {
	text := fmt.Sprintf("%0*o", len(dst)-1, value)
	copy(dst, text)
	dst[len(dst)-1] = 0
}

func TestNormalizeTarEndAndTrailingInput(t *testing.T) {
	first := makeTar(t, member{header: tar.Header{Name: "file"}, data: "x"})
	second := makeTar(t, member{header: tar.Header{Name: "other"}, data: "y"})
	for _, input := range [][]byte{
		append(append([]byte(nil), first...), 1),
		append(append([]byte(nil), first...), second...),
		first[:len(first)-1024],
		first[:512+2],
	} {
		_, _, err := runNormalize(t, input, Limits{})
		if err == nil {
			t.Fatal("accepted non-zero trailing data, a second archive, or truncated input")
		}
	}
	withPadding := append(append([]byte(nil), first...), make([]byte, 512)...)
	if _, _, err := runNormalize(t, withPadding, Limits{}); err != nil {
		t.Fatalf("rejected bounded zero padding: %v", err)
	}
}

func TestNormalizeCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var output bytes.Buffer
	_, err := Normalize(ctx, &output, bytes.NewReader(makeTar(t)), Limits{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}

	ctx, cancel = context.WithCancel(context.Background())
	input := makeTar(t, member{header: tar.Header{Name: "file"}, data: "payload"})
	reader := &cancelAfterRead{reader: bytes.NewReader(input), cancel: cancel}
	_, err = Normalize(ctx, io.Discard, reader, Limits{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("mid-stream error = %v, want context.Canceled", err)
	}
}
