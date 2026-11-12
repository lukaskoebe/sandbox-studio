//go:build linux

package guest

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// CommandLink is the `studio` command in the sandbox (docs/agent-cli.md): a symbolic link to
// the agent binary, which runs as the command when it is called by that name.
const CommandLink = "/usr/local/bin/studio"

// linkCommand points link at self. A link of an older agent's path is moved to this one; a
// file of the user's own, or a link to something else, stays as it is.
func linkCommand(link, self string) error {
	target, err := os.Readlink(link)
	switch {
	case err == nil && target == self:
		return nil
	case err == nil && filepath.Base(target) != "studio-agent":
		return nil
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		// Not a link: the user's own studio.
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return err
	}
	tmp := link + ".studio-new"
	_ = os.Remove(tmp)
	if err := os.Symlink(self, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, link); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
