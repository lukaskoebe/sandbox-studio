package agentchan

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

const pipePrefix = `\\.\pipe\`

// listen serves a sandbox's agent channel. msb routes vsock only to named pipes on
// Windows; the pipe's DACL admits only the user Studio runs as, like the private run
// directory on other systems. Other paths, as in tests, are Unix sockets.
func listen(path string) (net.Listener, error) {
	if !strings.HasPrefix(path, pipePrefix) {
		_ = os.Remove(path)
		return net.Listen("unix", path)
	}
	sddl, err := ownerOnly()
	if err != nil {
		return nil, err
	}
	return winio.ListenPipe(path, &winio.PipeConfig{
		SecurityDescriptor: sddl,
		InputBufferSize:    64 << 10,
		OutputBufferSize:   64 << 10,
	})
}

// Dial connects to a sandbox's agent channel, as msb does for the guest.
func Dial(ctx context.Context, path string) (net.Conn, error) {
	if strings.HasPrefix(path, pipePrefix) {
		return winio.DialPipeContext(ctx, path)
	}
	var d net.Dialer
	return d.DialContext(ctx, "unix", path)
}

// ownerOnly is a protected DACL granting full access to the current user alone.
func ownerOnly() (string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("agent pipe: current user: %w", err)
	}
	return "D:P(A;;GA;;;" + user.User.Sid.String() + ")", nil
}
