package ocilayer

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"testing"
)

func TestNormalizeRequiresActualEndBlocks(t *testing.T) {
	var src bytes.Buffer
	tw := tar.NewWriter(&src)
	if err := tw.WriteHeader(&tar.Header{Name: "zeros", Mode: 0600, Size: 1024}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(make([]byte, 1024)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	for _, missing := range []int{512, 1024} {
		if _, err := Normalize(context.Background(), io.Discard, bytes.NewReader(src.Bytes()[:src.Len()-missing]), Limits{}); err == nil {
			t.Errorf("accepted archive missing %d footer bytes after zero file content", missing)
		}
	}
}

func TestNormalizeMaxPaddingLimitDoesNotOverflow(t *testing.T) {
	_, err := Normalize(context.Background(), io.Discard, bytes.NewReader(make([]byte, 1024)), Limits{MaxTrailingPaddingBytes: math.MaxInt64})
	if err != nil {
		t.Fatal(err)
	}
}

func TestNormalizeBoundsHiddenSparseMetadata(t *testing.T) {
	var initial bytes.Buffer
	tw := tar.NewWriter(&initial)
	if err := tw.WriteHeader(&tar.Header{Name: "sparse", Mode: 0600, Typeflag: tar.TypeReg, Format: tar.FormatGNU}); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	hdr := append([]byte(nil), initial.Bytes()[:512]...)
	hdr[156] = 'S'
	hdr[482] = 1
	copy(hdr[483:495], []byte("00000000001\x00"))
	for i := 148; i < 156; i++ {
		hdr[i] = ' '
	}
	var sum int
	for _, b := range hdr {
		sum += int(b)
	}
	copy(hdr[148:156], []byte(fmt.Sprintf("%06o\x00 ", sum)))
	raw := append([]byte(nil), hdr...)
	for i := 0; i < 128; i++ {
		block := make([]byte, 512)
		copy(block[:12], []byte("00000000000\x00"))
		copy(block[12:24], []byte("00000000001\x00"))
		if i < 127 {
			block[504] = 1
		}
		raw = append(raw, block...)
	}
	reader := bytes.NewReader(raw)
	_, err := Normalize(context.Background(), io.Discard, reader, Limits{MaxMetadataBytes: 512})
	if !errors.Is(err, ErrMetadataLimit) {
		t.Fatalf("hidden sparse metadata: got %v after %d bytes, want metadata bound", err, len(raw)-reader.Len())
	}
	if consumed := len(raw) - reader.Len(); consumed > 8192 {
		t.Fatalf("consumed %d hidden metadata bytes before refusal", consumed)
	}
}

func TestNormalizeForwardSymlinkChain(t *testing.T) {
	var src bytes.Buffer
	tw := tar.NewWriter(&src)
	for _, h := range []*tar.Header{
		{Name: "usr/bin/python", Typeflag: tar.TypeSymlink, Linkname: "python3", Mode: 0777},
		{Name: "usr/bin/python3", Typeflag: tar.TypeSymlink, Linkname: "python3.13", Mode: 0777},
		{Name: "usr/bin/python3.13", Typeflag: tar.TypeReg, Mode: 0755},
	} {
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Normalize(context.Background(), io.Discard, bytes.NewReader(src.Bytes()), Limits{}); err != nil {
		t.Fatalf("valid forward symlink chain refused: %v", err)
	}
}
