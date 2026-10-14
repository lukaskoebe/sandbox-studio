package doctor

import (
	"errors"

	"golang.org/x/sys/unix"
)

func hostVirtualization(p *Probes) {
	p.HVF = func() (bool, error) {
		v, err := unix.SysctlUint32("kern.hv_support")
		if errors.Is(err, unix.ENOENT) {
			return false, nil // macOS VMs without nested virtualization lack the key
		}
		return v == 1, err
	}
}
