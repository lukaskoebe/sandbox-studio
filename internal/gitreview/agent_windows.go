package gitreview

import (
	"context"
	"net"
	"os"
	"strings"

	"github.com/Microsoft/go-winio"
)

// defaultAgentPipe is the named pipe of Windows' OpenSSH agent service, which password
// managers with an SSH agent serve too.
const defaultAgentPipe = `\\.\pipe\openssh-ssh-agent`

func agentAddress() (string, error) {
	if p := os.Getenv("SSH_AUTH_SOCK"); strings.HasPrefix(p, `\\.\pipe\`) {
		return p, nil
	}
	return defaultAgentPipe, nil
}

func dialAgentAddress(ctx context.Context, addr string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, addr)
}
