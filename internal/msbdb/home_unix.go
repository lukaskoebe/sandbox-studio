//go:build unix

package msbdb

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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

// privateStorage takes group and other write off the folders msb keeps sandboxes in, below a
// home reached through a link. msb checks the path as it is written as well as the real one,
// and only a real owner-private directory on the way exempts what is below it; the link
// doesn't. So the folders an msb before 0.7.12 made under umask 0002 (mode 775) are refused on
// the written path, and with them the move of the sandboxes' disks to the new layout
// (crates/runtime checkpoint/migration/namespace.rs). It changes the home's own folders, not
// what is in cache, snapshots or volumes, and the sandboxes' folders, not each one's
// runtime/rootfs: never what a guest sees. It reports whether it changed a mode.
func privateStorage(real string) (bool, error) {
	entries, err := os.ReadDir(real)
	if err != nil {
		return false, err
	}
	changed := false
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		c, err := takeOthersWrite(filepath.Join(real, e.Name()))
		if err != nil {
			return changed, err
		}
		changed = changed || c
	}
	sandboxes := filepath.Join(real, "sandboxes")
	err = filepath.WalkDir(sandboxes, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == sandboxes && errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if d.Name() == "rootfs" && filepath.Base(filepath.Dir(path)) == "runtime" {
			return fs.SkipDir
		}
		c, err := takeOthersWrite(path)
		changed = changed || c
		return err
	})
	return changed, err
}

// takeOthersWrite takes group and other write off dir, a directory of this account's.
func takeOthersWrite(dir string) (bool, error) {
	fi, err := os.Lstat(dir)
	if err != nil {
		return false, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || !fi.IsDir() || st.Uid != uint32(os.Geteuid()) || fi.Mode().Perm()&0o022 == 0 {
		return false, nil
	}
	keep := fs.ModePerm | fs.ModeSticky | fs.ModeSetgid | fs.ModeSetuid
	return true, os.Chmod(dir, fi.Mode()&keep&^0o022)
}
