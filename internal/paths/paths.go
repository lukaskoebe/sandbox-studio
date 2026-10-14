// Package paths resolves where Studio keeps its state on each OS.
package paths

import (
	"crypto/sha256"
	"encoding/hex"
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
// limited to about 104 bytes, so callers must keep sandbox IDs short. On Windows it is a
// named pipe, which is all msb routes vsock to there; its name carries a hash of the data
// directory, so two Studios on one machine don't share pipes.
func (p Paths) AgentSocket(sandboxID string) string {
	if runtime.GOOS == "windows" {
		sum := sha256.Sum256([]byte(filepath.Clean(p.Data)))
		return `\\.\pipe\sandbox-studio-` + hex.EncodeToString(sum[:6]) + "-" + sandboxID
	}
	return filepath.Join(p.Data, "run", sandboxID+".sock")
}

// Imports holds uploaded workspace archives of sandbox imports until they finish. The
// host never extracts them.
func (p Paths) Imports() string { return filepath.Join(p.Data, "imports") }

// Ensure creates the directories. Guest is shared into every sandbox, where the harness
// runs studio-agent as the unprivileged agent user, so it must be traversable there; the
// data directory around it keeps it private on the host.
func (p Paths) Ensure() error {
	for _, d := range []string{p.Data, p.Guest(), filepath.Join(p.Data, "run"), p.Imports()} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	// MkdirAll leaves existing directories alone, and installs before this fix made it 0700.
	return os.Chmod(p.Guest(), 0o755)
}
