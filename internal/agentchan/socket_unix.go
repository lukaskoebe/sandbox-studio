//go:build !windows

package agentchan

import (
	"context"
	"net"
	"os"
)

// listen serves a sandbox's agent channel on a Unix socket, replacing a stale one.
func listen(path string) (net.Listener, error) {
	_ = os.Remove(path)
	return net.Listen("unix", path)
}

// Dial connects to a sandbox's agent channel, as msb does for the guest.
func Dial(ctx context.Context, path string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", path)
}
