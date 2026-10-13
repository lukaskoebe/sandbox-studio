//go:build !windows

package gitreview

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

// noAgentHint is in the error when Studio has no agent to reach.
const noAgentHint = "SSH_AUTH_SOCK"

// listenAgent listens on a temporary unix socket, as a user's ssh-agent does.
func listenAgent(t *testing.T) (net.Listener, string) {
	t.Helper()
	// Unix socket paths are short (104 bytes on macOS); the test's TMPDIR may be long.
	dir, err := os.MkdirTemp("", "sa")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "agent.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skipf("no unix sockets: %v", err)
	}
	return ln, sock
}

// noAgent leaves Studio without an agent: SSH_AUTH_SOCK is unset.
func noAgent(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")
	os.Unsetenv("SSH_AUTH_SOCK")
}
