package gateway

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strconv"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/dnsproxy"
)

// This computer. A sandbox reaches the computer Studio runs on as dnsproxy.HostName, and only on
// the ports its environment opened to sandboxes in Network settings (HostPorts). The gateway
// dials those on loopback, so a service listening on 127.0.0.1 or 0.0.0.0 answers. Nothing else
// changes: the policy still decides each connection, a security level never allows one (it is
// not a public host), and Studio's own ports are never dialed, listed or not.

// HostPortError says why a connection to a port of this computer was refused.
type HostPortError struct{ Port int }

func (e HostPortError) Error() string {
	return fmt.Sprintf("port %d of this computer is not open to sandboxes: open it in Settings → Network → This computer", e.Port)
}

// dialHost connects to port of this computer for environment envID.
func (g *Gateway) dialHost(ctx context.Context, envID, network string, port int) (net.Conn, error) {
	if slices.Contains(g.reservedPorts(), uint16(port)) {
		return nil, fmt.Errorf("refusing to connect to port %d of this computer: Studio's own port", port)
	}
	var open []int
	if g.HostPorts != nil {
		var err error
		if open, err = g.HostPorts(ctx, envID); err != nil {
			return nil, fmt.Errorf("host ports: %w", err)
		}
	}
	if !slices.Contains(open, port) {
		return nil, HostPortError{port}
	}
	d := &net.Dialer{Timeout: 15 * time.Second}
	c, err := d.DialContext(ctx, network, net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		// A service that listens on IPv6 loopback only.
		if c6, err6 := d.DialContext(ctx, network, net.JoinHostPort("::1", strconv.Itoa(port))); err6 == nil {
			return c6, nil
		}
		return nil, err
	}
	return c, nil
}

// isHost reports whether host names this computer.
func isHost(host string) bool { return host == dnsproxy.HostName }
