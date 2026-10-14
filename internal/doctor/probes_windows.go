package doctor

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

func hostVirtualization(p *Probes) {
	p.WHP = whpEnabled
	p.JobBlocksBreakaway = jobBlocksBreakaway
}

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

// jobBlocksBreakaway reports whether this process is in a job object that doesn't let its
// children leave it. msb starts each sandbox's VM detached, with CREATE_BREAKAWAY_FROM_JOB,
// which such a job refuses with "Access is denied".
func jobBlocksBreakaway() (bool, error) {
	var in int32
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("IsProcessInJob")
	if r, _, err := proc.Call(uintptr(windows.CurrentProcess()), 0, uintptr(unsafe.Pointer(&in))); r == 0 {
		return false, err
	}
	if in == 0 {
		return false, nil
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if err := windows.QueryInformationJobObject(0, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil); err != nil {
		return false, err
	}
	const breakaway = windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK | windows.JOB_OBJECT_LIMIT_SILENT_BREAKAWAY_OK
	return info.BasicLimitInformation.LimitFlags&breakaway == 0, nil
}
