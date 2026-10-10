package doctor

import "golang.org/x/sys/unix"

func hostVirtualization(p *Probes) {
	p.HVF = func() (bool, error) {
		v, err := unix.SysctlUint32("kern.hv_support")
		return v == 1, err
	}
}
