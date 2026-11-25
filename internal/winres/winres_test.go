package winres

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestObjectHoldsTheManifest reads an object back the way a linker does: one .rsrc
// section, a relocation on the data entry's address, and the manifest under
// RT_MANIFEST / 1 / en-US.
func TestObjectHoldsTheManifest(t *testing.T) {
	manifest := Manifest("Test.App")
	for arch, m := range machines {
		b, err := Object(arch, manifest)
		if err != nil {
			t.Fatal(err)
		}
		f, err := pe.NewFile(bytes.NewReader(b))
		if err != nil {
			t.Fatalf("%s: %v", arch, err)
		}
		if f.Machine != m.machine || len(f.Sections) != 1 || f.Sections[0].Name != ".rsrc" {
			t.Fatalf("%s: machine %#x, sections %v", arch, f.Machine, f.Sections)
		}
		s := f.Sections[0]
		if len(s.Relocs) != 1 || s.Relocs[0].Type != m.reloc || s.Relocs[0].SymbolTableIndex != 0 {
			t.Fatalf("%s: relocations %+v", arch, s.Relocs)
		}
		if len(f.Symbols) != 1 || f.Symbols[0].Name != ".rsrc" || f.Symbols[0].SectionNumber != 1 {
			t.Fatalf("%s: symbols %+v", arch, f.Symbols)
		}
		data, err := s.Data()
		if err != nil {
			t.Fatal(err)
		}
		u32 := func(at uint32) uint32 { return binary.LittleEndian.Uint32(data[at:]) }
		at := uint32(0)
		for _, id := range []uint32{rtManifest, manifestID, langEnUS} {
			if n := binary.LittleEndian.Uint16(data[at+14:]); n != 1 {
				t.Fatalf("%s: directory at %d has %d entries", arch, at, n)
			}
			if got := u32(at + 16); got != id {
				t.Fatalf("%s: entry ID %d, want %d", arch, got, id)
			}
			at = u32(at+20) &^ subdir
		}
		if s.Relocs[0].VirtualAddress != at {
			t.Fatalf("%s: relocation at %d, data entry at %d", arch, s.Relocs[0].VirtualAddress, at)
		}
		off, size := u32(at), u32(at+4)
		if got := data[off : off+size]; !bytes.Equal(got, manifest) {
			t.Fatalf("%s: manifest is %q", arch, got)
		}
	}
	if !strings.Contains(string(manifest), "permonitorv2") {
		t.Fatal("the manifest doesn't ask for per-monitor DPI awareness")
	}
}

// TestCommittedObjectsAreCurrent fails when a manifest changed without go generate.
func TestCommittedObjectsAreCurrent(t *testing.T) {
	for _, o := range Outputs {
		want, err := Object(o.Arch, Manifest(o.Name))
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(o.Path)))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s is stale (%v); run go generate ./internal/winres", o.Path, err)
		}
	}
}
