package msbdb

import (
	"context"
	"os/exec"
	"os/user"
	"time"
)

// aclGrants returns who besides this account and root an access list entry of dir lets
// change it, as msb's macOS check finds them (macos_acl.rs verify_ancestor_acl): an allow
// entry with a write, append, delete, attribute, security or owner permission. Denials, such
// as the "everyone deny delete" on home folders, are harmless. Empty if there is none, or
// `ls` can't say.
func aclGrants(dir string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/bin/ls", "-lde", dir).Output()
	if err != nil {
		return ""
	}
	return parseACLGrants(string(out), trustedACLNames())
}

func trustedACLNames() map[string]bool {
	trusted := map[string]bool{"user:root": true}
	if u, err := user.Current(); err == nil {
		trusted["user:"+u.Username] = true
	}
	return trusted
}
