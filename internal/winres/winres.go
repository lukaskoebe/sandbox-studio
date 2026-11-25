// Package winres writes the Windows resource objects (.syso) that give studiow.exe and
// studio-app.exe their application manifest. go build links a .syso next to a package's
// sources into the executable, and the manifest is what tells Windows the app draws at the
// screen's own scale: without it Windows renders the window at 100% and stretches it, so
// at 125% or 150% the app looks blurry. Wails sets the same awareness at run time too, but
// only once the app runs, after its single-instance lock has made a window.
//
// Run `go generate ./internal/winres` after changing a manifest; a test checks that the
// committed objects match.
package winres

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

//go:generate go run ./gen

// Outputs are the objects go generate writes, by path from the repository's root: the
// tray launcher and the app, for each Windows architecture Go builds for.
var Outputs = func() []struct{ Path, Name, Arch string } {
	var out []struct{ Path, Name, Arch string }
	for _, exe := range []struct{ dir, name string }{
		{"cmd/studiow", "SandboxStudio.Tray"},
		{"cmd/studio-app", "SandboxStudio.App"},
	} {
		for _, arch := range []string{"amd64", "arm64"} {
			out = append(out, struct{ Path, Name, Arch string }{
				exe.dir + "/rsrc_windows_" + arch + ".syso", exe.name, arch,
			})
		}
	}
	return out
}()

// Manifest is the application manifest of an executable called name: per-monitor (v2)
// DPI awareness, the version 6 common controls and no elevation.
func Manifest(name string) []byte {
	return fmt.Appendf(nil, `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly manifestVersion="1.0" xmlns="urn:schemas-microsoft-com:asm.v1" xmlns:asmv3="urn:schemas-microsoft-com:asm.v3">
  <assemblyIdentity type="win32" name="%s" version="1.0.0.0" processorArchitecture="*"/>
  <dependency>
    <dependentAssembly>
      <assemblyIdentity type="win32" name="Microsoft.Windows.Common-Controls" version="6.0.0.0" processorArchitecture="*" publicKeyToken="6595b64144ccf1df" language="*"/>
    </dependentAssembly>
  </dependency>
  <asmv3:application>
    <asmv3:windowsSettings>
      <dpiAware xmlns="http://schemas.microsoft.com/SMI/2005/WindowsSettings">true/pm</dpiAware>
      <dpiAwareness xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">permonitorv2,permonitor</dpiAwareness>
    </asmv3:windowsSettings>
  </asmv3:application>
  <trustInfo xmlns="urn:schemas-microsoft-com:asm.v3">
    <security>
      <requestedPrivileges>
        <requestedExecutionLevel level="asInvoker" uiAccess="false"/>
      </requestedPrivileges>
    </security>
  </trustInfo>
</assembly>
`, name)
}

// Machines are the COFF machine types and the relocation type of an image-relative
// address, by GOARCH.
var machines = map[string]struct{ machine, reloc uint16 }{
	"amd64": {0x8664, 3}, // IMAGE_FILE_MACHINE_AMD64, IMAGE_REL_AMD64_ADDR32NB
	"arm64": {0xaa64, 2}, // IMAGE_FILE_MACHINE_ARM64, IMAGE_REL_ARM64_ADDR32NB
}

const (
	rtManifest = 24     // RT_MANIFEST
	manifestID = 1      // CREATEPROCESS_MANIFEST_RESOURCE_ID
	langEnUS   = 0x0409 // the language the manifest is filed under
	subdir     = 1 << 31
)

// Object is a COFF object for goarch whose one section, .rsrc, holds manifest as the
// executable's manifest resource.
func Object(goarch string, manifest []byte) ([]byte, error) {
	m, ok := machines[goarch]
	if !ok {
		return nil, fmt.Errorf("winres: no machine type for %s", goarch)
	}

	// The resource tree: type → ID → language → data entry → the manifest. Each
	// directory is 16 bytes and one 8-byte entry; the data entry 16 bytes.
	const (
		typeDir  = 0
		idDir    = typeDir + 24
		langDir  = idDir + 24
		entry    = langDir + 24
		data     = entry + 16
		dataAddr = entry // the field the linker turns into an address
	)
	var rsrc bytes.Buffer
	dir := func(id, target uint32) {
		le(&rsrc, uint32(0), uint32(0), uint16(0), uint16(0), uint16(0), uint16(1))
		le(&rsrc, id, target)
	}
	dir(rtManifest, subdir|idDir)
	dir(manifestID, subdir|langDir)
	dir(langEnUS, entry)
	le(&rsrc, uint32(data), uint32(len(manifest)), uint32(0), uint32(0))
	rsrc.Write(manifest)
	for rsrc.Len()%8 != 0 {
		rsrc.WriteByte(0)
	}

	const (
		fileHeader    = 20
		sectionHeader = 40
		relocSize     = 10
	)
	raw := fileHeader + sectionHeader
	relocs := raw + rsrc.Len()
	symbols := relocs + relocSize

	var b bytes.Buffer
	// IMAGE_FILE_HEADER: one section, one symbol, no optional header.
	le(&b, m.machine, uint16(1), uint32(0), uint32(symbols), uint32(1), uint16(0), uint16(0))
	// IMAGE_SECTION_HEADER: initialised, readable data.
	b.WriteString(".rsrc\x00\x00\x00")
	le(&b, uint32(0), uint32(0), uint32(rsrc.Len()), uint32(raw), uint32(relocs), uint32(0),
		uint16(1), uint16(0), uint32(0x40000040))
	b.Write(rsrc.Bytes())
	// IMAGE_RELOCATION: the data entry's address, relative to the section's symbol.
	le(&b, uint32(dataAddr), uint32(0), m.reloc)
	// IMAGE_SYMBOL: the section itself, static; then an empty string table.
	b.WriteString(".rsrc\x00\x00\x00")
	le(&b, uint32(0), int16(1), uint16(0), uint8(3), uint8(0))
	le(&b, uint32(4))
	return b.Bytes(), nil
}

func le(b *bytes.Buffer, v ...any) {
	for _, x := range v {
		_ = binary.Write(b, binary.LittleEndian, x)
	}
}
