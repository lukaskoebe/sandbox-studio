//go:build linux

package guest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Sandboxes have no init system: microsandbox's agentd stays PID 1, which is what lets it
// freeze every process for full snapshots, forks and live checkpoints. Studio runs
// `studio-agent boot` after each VM boot instead, and the supervisor it starts runs the
// services an init would.

const (
	supervisorLock = "/run/studio-agent/supervisor.lock"
	serviceLogDir  = "/var/log/studio"
	serviceLogMax  = 8 << 20
	containerdSock = "/run/containerd/containerd.sock"
	// stopTimeout leaves dockerd the time it needs to stop containers (15 s by default).
	stopTimeout = 20 * time.Second
)

// serviceEnv is the environment of every service. /usr/local/bin comes first so that
// containerd finds Studio's runc wrapper (see OCIRuntime).
var serviceEnv = []string{
	"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	"LANG=en_US.UTF-8",
}

// Boot starts the supervisor in the background and returns. It does nothing when a
// supervisor already runs, or when systemd is PID 1: sandboxes created before Studio
// dropped systemd start the agent from their own unit.
func Boot(self string) error {
	if comm, _ := os.ReadFile("/proc/1/comm"); strings.TrimSpace(string(comm)) == "systemd" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(supervisorLock), 0o755); err != nil {
		return err
	}
	lock, err := os.OpenFile(supervisorLock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil
		}
		return fmt.Errorf("lock supervisor: %w", err)
	}
	// The PID of a supervisor from an earlier boot may belong to another process now.
	if err := lock.Truncate(0); err != nil {
		return err
	}
	if err := os.MkdirAll(serviceLogDir, 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(filepath.Join(serviceLogDir, "supervisor.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	cmd := exec.Command(self, "supervise")
	cmd.Dir = "/"
	cmd.Env = serviceEnv
	cmd.Stdout, cmd.Stderr = out, out
	// The supervisor inherits the locked file as fd 3 and holds the lock for its lifetime,
	// so no second boot can slip in between this process exiting and the supervisor
	// locking.
	cmd.ExtraFiles = []*os.File{lock}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// Shutdown stops the supervisor and its services, and returns once they are gone or
// timeout passes. Studio runs it before stopping the VM: microsandbox gives the guest
// two seconds and then kills every process, which would take dockerd and its containers
// down without a clean stop.
func Shutdown(timeout time.Duration) error {
	lock, err := os.OpenFile(supervisorLock, os.O_RDWR, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer lock.Close()
	pidText, err := io.ReadAll(lock)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	for {
		err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return nil // no supervisor runs
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return fmt.Errorf("lock supervisor: %w", err)
		}
		if pidText != nil {
			pid, err := strconv.Atoi(strings.TrimSpace(string(pidText)))
			if err != nil || pid <= 1 {
				return fmt.Errorf("supervisor lock holds no PID: %q", pidText)
			}
			if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
				return fmt.Errorf("signal supervisor: %w", err)
			}
			pidText = nil
		}
		if time.Now().After(deadline) {
			return errors.New("supervisor did not stop in time")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// service is a long-running process the supervisor keeps alive.
type service struct {
	name string
	path string
	args []string
	// before runs ahead of every start; it returns early when ctx is done.
	before func(ctx context.Context)
}

// Supervise runs containerd, dockerd and the agent until ctx is done, restarting them when
// they exit, and then stops them in reverse order. lock is the supervisor lock Boot passed
// down.
func Supervise(ctx context.Context, log *slog.Logger, self string, lock *os.File) error {
	// The lock is already held through the inherited file; this fails if Supervise wasn't
	// started by Boot.
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("supervisor lock (start it with `studio-agent boot`): %w", err)
	}
	syscall.CloseOnExec(int(lock.Fd()))
	// Shutdown reads the PID from the lock file to know whom to stop.
	if err := lock.Truncate(0); err != nil {
		return err
	}
	if _, err := lock.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0); err != nil {
		return err
	}
	if err := prepareDisks(); err != nil {
		return err
	}
	// Nothing else runs the services while the lock is held, so a pid file left by an
	// unclean shutdown is stale.
	os.Remove("/var/run/docker.pid")

	services := []service{
		{name: "containerd", path: "/usr/bin/containerd"},
		{
			name:   "dockerd",
			path:   "/usr/bin/dockerd",
			args:   []string{"--containerd=" + containerdSock},
			before: func(ctx context.Context) { waitForSocket(ctx, containerdSock) },
		},
		{name: "agent", path: self, args: []string{"connect"}},
	}
	return superviseAll(ctx, log, serviceLogDir, services, time.Second)
}

func superviseAll(ctx context.Context, log *slog.Logger, logDir string, services []service, minBackoff time.Duration) error {
	type running struct {
		cancel context.CancelFunc
		done   chan struct{}
	}
	var all []running
	for _, s := range services {
		out, err := openServiceLog(logDir, s.name)
		if err != nil {
			return err
		}
		sctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer close(done)
			defer out.Close()
			s.run(sctx, log, out, minBackoff)
		}()
		all = append(all, running{cancel, done})
	}
	<-ctx.Done()
	for i := len(all) - 1; i >= 0; i-- {
		all[i].cancel()
		<-all[i].done
	}
	return nil
}

// run starts the service and restarts it with backoff until ctx is done, then stops it.
func (s service) run(ctx context.Context, log *slog.Logger, out io.Writer, minBackoff time.Duration) {
	backoff := minBackoff
	for ctx.Err() == nil {
		if s.before != nil {
			s.before(ctx)
			if ctx.Err() != nil {
				return
			}
		}
		cmd := exec.Command(s.path, s.args...)
		cmd.Dir = "/"
		cmd.Env = serviceEnv
		cmd.Stdout, cmd.Stderr = out, out
		// Services don't outlive a crashed supervisor, whose successor starts them again.
		cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
		start := time.Now()
		err := cmd.Start()
		if err == nil {
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case err = <-done:
			case <-ctx.Done():
				cmd.Process.Signal(syscall.SIGTERM)
				select {
				case <-done:
				case <-time.After(stopTimeout):
					cmd.Process.Kill()
					<-done
				}
				return
			}
		}
		if time.Since(start) > time.Minute {
			backoff = minBackoff
		}
		log.Warn("service exited; restarting", "service", s.name, "err", err, "in", backoff)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

// prepareDisks gives the owned disks, which microsandbox mounts root-owned, the ownership
// and modes the services expect.
func prepareDisks() error {
	acct, err := lookupAccount(User)
	if err != nil {
		return err
	}
	if err := os.Chown(Workdir, int(acct.uid), int(acct.gid)); err != nil {
		return err
	}
	if err := os.Chmod(Workdir, 0o755); err != nil {
		return err
	}
	for dir, mode := range map[string]os.FileMode{
		"/var/lib/docker/containerd-root": 0o711,
		"/var/lib/docker/docker-root":     0o710,
	} {
		if err := os.MkdirAll(dir, mode); err != nil {
			return err
		}
		if err := os.Chmod(dir, mode); err != nil {
			return err
		}
	}
	return nil
}

func waitForSocket(ctx context.Context, path string) {
	for ctx.Err() == nil {
		if c, err := net.DialTimeout("unix", path, time.Second); err == nil {
			c.Close()
			return
		}
		select {
		case <-time.After(200 * time.Millisecond):
		case <-ctx.Done():
		}
	}
}

func openServiceLog(dir, name string) (*cappedLog, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, name+".log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	return &cappedLog{f: f, max: serviceLogMax}, nil
}

// cappedLog is a log file that starts over when it reaches max bytes, so that a chatty
// service can't fill the root disk.
type cappedLog struct {
	mu  sync.Mutex
	f   *os.File
	n   int64
	max int64
}

func (l *cappedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.n+int64(len(p)) > l.max {
		if err := l.f.Truncate(0); err == nil {
			if _, err := l.f.Seek(0, io.SeekStart); err == nil {
				l.n = 0
			}
		}
	}
	n, err := l.f.Write(p)
	l.n += int64(n)
	return n, err
}

func (l *cappedLog) Close() error { return l.f.Close() }
