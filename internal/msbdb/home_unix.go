//go:build unix

package msbdb

import (
	"fmt"
	"os"
	"syscall"
)

// inspectDir applies msb's rule for a directory above its database (crates/db private_storage
// verify_ancestors): it must belong to this account or root, and no other account may write to
// it unless it is sticky, as /tmp is. why says what's wrong, empty when nothing is; private is
// whether it keeps other accounts out of everything below it.
func inspectDir(dir string) (why string, mine, private bool, err error) {
	fi, err := os.Lstat(dir)
	if err != nil {
		return "", false, false, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return "", false, false, nil
	}
	uid := uint32(os.Geteuid())
	mine = st.Uid == uid
	mode := uint32(st.Mode)
	acl := aclGrants(dir)
	switch {
	case !fi.IsDir():
		return "isn't a directory", mine, false, nil
	case !mine && st.Uid != 0:
		return fmt.Sprintf("belongs to another account (uid %d)", st.Uid), false, false, nil
	case mode&0o022 != 0 && mode&0o1000 == 0:
		who := "its group"
		if mode&0o002 != 0 {
			who = "everyone"
		}
		return fmt.Sprintf("is writable by %s (mode %o)", who, mode&0o777), mine, false, nil
	case acl != "":
		return "has an access list that lets " + acl + " change it", mine, false, nil
	}
	return "", mine, mine && mode&0o077 == 0, nil
}

// makePrivate makes dir 0700 if it belongs to this account and isn't already private.
func makePrivate(dir string) (bool, error) {
	fi, err := os.Stat(dir)
	if err != nil {
		return false, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != uint32(os.Geteuid()) || fi.Mode().Perm()&0o077 == 0 {
		return false, nil
	}
	return true, os.Chmod(dir, 0o700)
}
