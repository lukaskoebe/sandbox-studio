package agentchan

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

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
	ln, err := winio.ListenPipe(path, &winio.PipeConfig{
		SecurityDescriptor: sddl,
		InputBufferSize:    64 << 10,
		OutputBufferSize:   64 << 10,
	})
	if err != nil {
		return nil, err
	}
	return &pipeListener{Listener: ln, done: make(chan struct{})}, nil
}

// pipeListener works around a lost close in go-winio: when a client hangs up just as the
// listener closes, the accept in flight can take the close signal and fail with the client's
// error instead, and Close then waits forever. Another Close reaches the listener, so Close
// repeats until the listener is done.
type pipeListener struct {
	net.Listener
	once sync.Once
	done chan struct{}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if errors.Is(err, winio.ErrPipeListenerClosed) {
		err = net.ErrClosed
	}
	return c, err
}

func (l *pipeListener) Close() error {
	l.once.Do(func() {
		go func() {
			_ = l.Listener.Close()
			close(l.done)
		}()
	})
	retry := time.NewTicker(50 * time.Millisecond)
	defer retry.Stop()
	for {
		select {
		case <-l.done:
			return nil
		case <-retry.C:
			go func() { _ = l.Listener.Close() }() // returns once the listener is done
		}
	}
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
