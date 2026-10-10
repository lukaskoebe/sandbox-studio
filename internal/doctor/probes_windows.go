package doctor

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

func hostVirtualization(p *Probes) { p.WHP = whpEnabled }

// whpEnabled asks the Windows Hypervisor Platform whether a hypervisor is present.
// WinHvPlatform.dll exists only while the optional feature is turned on.
func whpEnabled() (bool, error) {
	proc := windows.NewLazySystemDLL("WinHvPlatform.dll").NewProc("WHvGetCapability")
	if err := proc.Find(); err != nil {
		return false, nil
	}
	const capabilityHypervisorPresent = 0
	var present, written uint32
	hr, _, _ := proc.Call(capabilityHypervisorPresent, uintptr(unsafe.Pointer(&present)), unsafe.Sizeof(present), uintptr(unsafe.Pointer(&written)))
	if int32(hr) < 0 {
		return false, fmt.Errorf("WHvGetCapability failed with HRESULT 0x%08x", uint32(hr))
	}
	return present != 0, nil
}
