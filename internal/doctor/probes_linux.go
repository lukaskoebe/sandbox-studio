package doctor

import (
	"os"
	"os/user"
	"slices"
	"strconv"

	"golang.org/x/sys/unix"
)

func hostVirtualization(p *Probes) {
	p.Access = func(path string) error { return unix.Access(path, unix.R_OK|unix.W_OK) }
	p.KVMGroup = kvmGroup
}

// kvmGroup reports whether the current user is listed in the kvm group and whether this
// process already carries it. A user added with usermod gets it only in new sessions.
func kvmGroup() (member, active bool, err error) {
	g, err := user.LookupGroup("kvm")
	if err != nil {
		return false, false, err
	}
	u, err := user.Current()
	if err != nil {
		return false, false, err
	}
	ids, err := u.GroupIds()
	if err != nil {
		return false, false, err
	}
	member = slices.Contains(ids, g.Gid)
	gid, err := strconv.Atoi(g.Gid)
	if err != nil {
		return member, false, err
	}
	groups, err := os.Getgroups()
	if err != nil {
		return member, false, err
	}
	return member, slices.Contains(groups, gid) || os.Getegid() == gid, nil
}
