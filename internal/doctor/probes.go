package doctor

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/diskspace"
)

// HostProbes returns the probes for this host. The msb and image probes need the
// microsandbox SDK and are left for the caller to set.
func HostProbes() Probes {
	p := Probes{
		GOOS:          runtime.GOOS,
		GOARCH:        runtime.GOARCH,
		Run:           run,
		Stat:          os.Stat,
		ImagePullable: (&Registry{Client: &http.Client{Timeout: 10 * time.Second}}).Pullable,
		Writable:      writable,
		DiskFree:      diskspace.Free,
		PortFree:      portFree,
	}
	hostVirtualization(&p)
	return p
}

// runTimeout bounds each program the doctor runs.
const runTimeout = 30 * time.Second

func run(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, runTimeout)
	defer cancel()
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

func writable(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".doctor-*")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

func portFree(addr string) error {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return l.Close()
}
