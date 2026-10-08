// Package paths resolves where Studio keeps its state on each OS.
package paths

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

// Paths are the directories Studio uses. All of them are created on demand.
type Paths struct {
	Data string // catalog database, guest files, run sockets
}

// Default returns the per-OS default locations, honoring SANDBOX_STUDIO_HOME.
func Default() (Paths, error) {
	if dir := os.Getenv("SANDBOX_STUDIO_HOME"); dir != "" {
		return Paths{Data: dir}, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	switch runtime.GOOS {
	case "darwin":
		return Paths{Data: filepath.Join(home, "Library", "Application Support", "Sandbox Studio")}, nil
	case "windows":
		local := os.Getenv("LOCALAPPDATA")
		if local == "" {
			return Paths{}, errors.New("LOCALAPPDATA is not set")
		}
		return Paths{Data: filepath.Join(local, "Sandbox Studio")}, nil
	default:
		if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
			return Paths{Data: filepath.Join(xdg, "sandbox-studio")}, nil
		}
		return Paths{Data: filepath.Join(home, ".local", "share", "sandbox-studio")}, nil
	}
}

// DB is the SQLite catalog.
func (p Paths) DB() string { return filepath.Join(p.Data, "studio.db") }

// Guest is the directory mounted read-only at /opt/studio in every sandbox.
func (p Paths) Guest() string { return filepath.Join(p.Data, "guest") }

// AgentSocket is the host end of a sandbox's agent channel. Unix socket paths are
// limited to about 104 bytes, so callers must keep sandbox IDs short.
func (p Paths) AgentSocket(sandboxID string) string {
	return filepath.Join(p.Data, "run", sandboxID+".sock")
}

// Ensure creates the directories.
func (p Paths) Ensure() error {
	for _, d := range []string{p.Data, p.Guest(), filepath.Join(p.Data, "run")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	return nil
}
