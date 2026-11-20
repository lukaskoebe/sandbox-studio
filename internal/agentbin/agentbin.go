// Package agentbin embeds the Linux studio-agent binaries that Studio mounts into sandboxes.
// `make agent` writes them to dist/ before `go build`.
package agentbin

import (
	"embed"
	"errors"
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
// command gets bash in the home, whatever the terminal shell's setup does, but as a login shell,
// unlike under sshd: that's what gives it the environment, and it means a profile that prints
// corrupts what a client such as rsync reads. An interactive session gets the terminal shell where
// terminals open.
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

// ErrInUse is a file of the guest's that a running sandbox holds open and that Windows lets
// Studio neither replace nor move aside. The sandboxes keep the one there, which Studio still
// talks to, until a start of Studio with them stopped replaces it.
var ErrInUse = errors.New("a running sandbox holds it open")

// rename is os.Rename, which the tests replace with Windows' refusals.
var rename = os.Rename

// Install writes the agent for arch to dir/bin/studio-agent, and the SSH sessions' login shell
// to dir/bin/login-shell, if their content changed. An error that is only ErrInUse leaves the
// files there in place.
func Install(dir, arch string) error {
	shell := install(filepath.Join(dir, "bin", "login-shell"), []byte(loginShell))
	if shell != nil && !errors.Is(shell, ErrInUse) {
		return shell
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
	agent := install(filepath.Join(dir, "bin", "studio-agent"), data)
	if agent != nil && !errors.Is(agent, ErrInUse) {
		return agent
	}
	return errors.Join(shell, agent)
}

// install writes an executable to dst, unless it already holds data.
func install(dst string, data []byte) error {
	removeAside(dst)
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
	err := rename(tmp, dst)
	if err == nil {
		return nil
	}
	_ = os.Remove(tmp)
	if _, serr := os.Stat(dst); serr != nil {
		return err
	}
	// Windows won't replace a file another process has open, and a running sandbox's VM holds
	// the agent open through its mount of the guest directory. Moving it aside for the new one
	// isn't safe either: msb's Windows file sharing knows files by path, so the VM would see the
	// new file under the old one's inode, next to the old pages it cached, and every
	// studio-agent it starts would run a mix of both. The sandboxes keep the old agent.
	return fmt.Errorf("%w: replacing %s: %v", ErrInUse, filepath.Base(dst), err)
}

// removeAside removes the files Studio 0.25 and 0.26 moved aside, once no sandbox holds them.
func removeAside(dst string) {
	aside, _ := filepath.Glob(dst + ".old-*")
	for _, f := range aside {
		_ = os.Remove(f)
	}
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
