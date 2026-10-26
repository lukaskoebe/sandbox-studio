//go:build linux

package guest

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// zshPaths are where zsh may be; the base image has it at /usr/bin/zsh.
var zshPaths = []string{"/usr/bin/zsh", "/bin/zsh"}

// zshrc is the ~/.zshrc written into a home without zsh startup files, so zsh's first-run
// wizard doesn't ask. Studio's defaults are in /etc/zsh/studio.zsh.
const zshrc = `# Your zsh settings. Sandbox Studio's defaults (history, completion, the prompt, mise) are
# in /etc/zsh/studio.zsh and run before this file.
`

// terminalShell is the shell new terminals start: zsh when Studio asked for it and the image
// has it, bash otherwise.
func (a *Agent) terminalShell() string {
	if p := a.shell.Load(); p != nil && *p == "zsh" {
		for _, z := range zshPaths {
			if _, err := os.Stat(z); err == nil {
				a.ensureZshrc()
				return z
			}
		}
	}
	return "/bin/bash"
}

// terminalCommand is the tmux command line of a terminal: the session's new windows start
// shell, set on every attach as the server may have been started by an agent session or
// before the setting changed. Windows that already run keep their shell.
func terminalCommand(name, shell string) []string {
	return []string{"-f", tmuxConfPath, "start-server",
		";", "set-option", "-g", "default-shell", shell,
		";", "new-session", "-A", "-s", name}
}

// ensureZshrc writes zshrc unless the home has a zsh startup file already.
func (a *Agent) ensureZshrc() {
	for _, f := range []string{".zshenv", ".zprofile", ".zshrc", ".zlogin"} {
		if _, err := os.Lstat(filepath.Join(a.user.home, f)); err == nil {
			return
		}
	}
	p := filepath.Join(a.user.home, ".zshrc")
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if !errors.Is(err, fs.ErrExist) {
			a.log.Warn("writing ~/.zshrc", "err", err)
		}
		return
	}
	_, err = f.WriteString(zshrc)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chown(p, int(a.user.uid), int(a.user.gid))
	}
	if err != nil {
		a.log.Warn("writing ~/.zshrc", "err", err)
	}
}
