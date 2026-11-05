// Package agentbin embeds the Linux studio-agent binaries that Studio mounts into sandboxes.
// `make agent` writes them to dist/ before `go build`.
package agentbin

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed all:dist
var dist embed.FS

// loginShell is what msb's SSH server runs for a session, as the VM's shell (runtime.create): with
// no arguments for an interactive session and with -c and a command for any other. Its own default,
// /bin/sh without a profile, has neither the CA variables, the secret placeholders nor mise. A
// command gets bash in the home, as sshd would start it, whatever the terminal shell's setup does;
// an interactive session gets the terminal shell where terminals open.
const loginShell = `#!/bin/sh
if [ ! -x /bin/bash ]; then
	exec /bin/sh "$@"
fi
if [ "$#" -gt 0 ]; then
	cd "$HOME" 2>/dev/null
	export SHELL=/bin/bash
	exec /bin/bash -l "$@"
fi
cd /workspace 2>/dev/null
shell=$(cat /etc/sandbox-studio/terminal-shell 2>/dev/null)
if [ ! -x "$shell" ]; then
	shell=/bin/bash
fi
export SHELL="$shell"
exec "$shell" -l
`

// Install writes the agent for arch to dir/bin/studio-agent, and the SSH sessions' login shell
// to dir/bin/login-shell, if their content changed.
func Install(dir, arch string) error {
	if err := install(filepath.Join(dir, "bin", "login-shell"), []byte(loginShell)); err != nil {
		return err
	}
	name := "dist/studio-agent-linux-" + arch
	data, err := dist.ReadFile(name)
	if err != nil {
		if os.Getenv("STUDIO_AGENT_BIN") != "" {
			data, err = os.ReadFile(os.Getenv("STUDIO_AGENT_BIN"))
		}
		if err != nil {
			return fmt.Errorf("guest agent for linux/%s not embedded; run `make agent` before building: %w", arch, err)
		}
	}
	return install(filepath.Join(dir, "bin", "studio-agent"), data)
}

// install writes an executable to dst, unless it already holds data.
func install(dst string, data []byte) error {
	if cur, err := os.ReadFile(dst); err == nil && string(cur) == string(data) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, data, 0o755); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// Embedded lists the embedded agent binaries.
func Embedded() []string {
	entries, _ := fs.ReadDir(dist, "dist")
	var out []string
	for _, e := range entries {
		if e.Name() != ".keep" {
			out = append(out, e.Name())
		}
	}
	return out
}
