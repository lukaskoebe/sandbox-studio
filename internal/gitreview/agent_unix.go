//go:build !windows

package gitreview

import (
	"context"
	"errors"
	"net"
	"os"
)

func agentAddress() (string, error) {
	if p := os.Getenv("SSH_AUTH_SOCK"); p != "" {
		return p, nil
	}
	return "", errors.New("SSH_AUTH_SOCK is not set in Studio's environment")
}

func dialAgentAddress(ctx context.Context, addr string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", addr)
}
