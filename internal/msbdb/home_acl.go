package msbdb

import "strings"

// mutatingACL are the permissions of a macOS access list entry, as `ls -e` names them, that
// let its holder change a directory or swap what's in it.
var mutatingACL = map[string]bool{
	"write": true, "add_file": true, "append": true, "add_subdirectory": true,
	"delete": true, "delete_child": true, "writeattr": true, "writeextattr": true,
	"writesecurity": true, "chown": true,
}

// parseACLGrants reads `ls -lde` output and returns the first principal other than the
// trusted ones that an allow entry grants a mutating permission, such as "group:staff".
// Entries look like " 0: group:everyone deny delete", maybe with "inherited" before the kind.
func parseACLGrants(out string, trusted map[string]bool) string {
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) > 2 && f[2] == "inherited" {
			f = append(f[:2:2], f[3:]...)
		}
		if len(f) < 4 || !strings.HasSuffix(f[0], ":") || f[2] != "allow" || trusted[f[1]] {
			continue
		}
		for _, perm := range strings.Split(f[3], ",") {
			if mutatingACL[perm] {
				return f[1]
			}
		}
	}
	return ""
}
