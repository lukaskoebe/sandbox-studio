package gitreview

import (
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
)

// noAgentHint is in the error when Studio has no agent to reach.
const noAgentHint = "could not connect"

// listenAgent listens on a named pipe of its own, as Windows' OpenSSH agent does.
func listenAgent(t *testing.T) (net.Listener, string) {
	t.Helper()
	addr := fmt.Sprintf(`\\.\pipe\studio-test-agent-%d-%d`, os.Getpid(), time.Now().UnixNano())
	ln, err := winio.ListenPipe(addr, nil)
	if err != nil {
		t.Fatal(err)
	}
	return ln, addr
}

// noAgent leaves Studio without an agent. An unset SSH_AUTH_SOCK would mean OpenSSH's own
// pipe, which may be the developer's agent, so it names a pipe nobody serves.
func noAgent(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", fmt.Sprintf(`\\.\pipe\studio-test-no-agent-%d`, time.Now().UnixNano()))
}
