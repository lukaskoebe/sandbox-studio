//go:build linux

package guestcapture

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

const (
	// DefaultMaxFreeze is how long the watchdog holds the freeze when the
	// caller does not ask for longer.
	DefaultMaxFreeze  = 60 * time.Second
	readyWaitDuration = 8 * time.Second
	ackWaitSlack      = 5 * time.Second
	controlMaxBytes   = 4096
	controlPollSlice  = 100 * time.Millisecond
	freezeIoctl       = 0xC0045877
	thawIoctl         = 0xC0045878
)

type watchdogEvent struct {
	Event  string `json:"event"`
	Reason string `json:"reason,omitempty"`
	Thawed bool   `json:"thawed,omitempty"`
	Error  string `json:"error,omitempty"`
}

type processWatchdog struct {
	cmd       *exec.Cmd
	control   net.Conn
	root      *os.File
	maxFreeze time.Duration

	waitProcess   func() error
	emergencyThaw func(*os.File) error

	mu       sync.Mutex
	buffer   []byte
	ready    bool
	released bool
	stopOnce sync.Once
	waitOnce sync.Once
	waitErr  error
}

// checkMaxFreeze resolves a requested freeze limit: zero means DefaultMaxFreeze.
func checkMaxFreeze(maxFreeze time.Duration) (time.Duration, error) {
	if maxFreeze == 0 {
		return DefaultMaxFreeze, nil
	}
	if maxFreeze < 0 || maxFreeze > agentproto.ExportMaxFreeze {
		return 0, fmt.Errorf("freeze limit %s is outside (0, %s]", maxFreeze, agentproto.ExportMaxFreeze)
	}
	return maxFreeze, nil
}

func startWatchdog(root, lock *os.File, maxFreeze time.Duration) (watchdog, error) {
	maxFreeze, err := checkMaxFreeze(maxFreeze)
	if err != nil {
		return nil, err
	}
	if root == nil {
		return nil, errors.New("nil pinned filesystem root")
	}
	if lock == nil {
		return nil, errors.New("nil capture lock file")
	}
	if _, err := root.Stat(); err != nil {
		return nil, fmt.Errorf("stat pinned filesystem root: %w", err)
	}
	if _, err := lock.Stat(); err != nil {
		return nil, fmt.Errorf("stat capture lock file: %w", err)
	}
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("create freeze watchdog control socket: %w", err)
	}
	parentFile := os.NewFile(uintptr(pair[0]), "capture-watchdog-parent")
	childControl := os.NewFile(uintptr(pair[1]), "capture-watchdog-child")
	parentControl, err := net.FileConn(parentFile)
	_ = parentFile.Close()
	if err != nil {
		_ = childControl.Close()
		return nil, fmt.Errorf("make freeze watchdog control socket pollable: %w", err)
	}
	started := false
	defer func() {
		if !started {
			_ = parentControl.Close()
			_ = childControl.Close()
		}
	}()

	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate studio-agent executable: %w", err)
	}
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open null device for freeze watchdog: %w", err)
	}
	defer devNull.Close()
	cmd := exec.Command(executable, "__capture-watchdog", maxFreeze.String())
	cmd.Dir = "/"
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C"}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = devNull, devNull, devNull
	cmd.ExtraFiles = []*os.File{root, childControl, lock}
	// exec gives the helper only standard streams plus the pinned root, control
	// socket, and same-open-file-description capture lock as fd 3, 4, and 5.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("exec freeze watchdog: %w", err)
	}
	started = true
	_ = childControl.Close()
	return &processWatchdog{cmd: cmd, control: parentControl, root: root, maxFreeze: maxFreeze}, nil
}

func (w *processWatchdog) waitReady(ctx context.Context) error {
	w.mu.Lock()
	if w.ready {
		w.mu.Unlock()
		return errors.New("freeze watchdog ready event was already consumed")
	}
	w.mu.Unlock()
	event, err := w.readEvent(ctx, time.Now().Add(readyWaitDuration))
	if err != nil {
		return fmt.Errorf("wait for frozen filesystem: %w", err)
	}
	if event.Event != "ready" || event.Reason != "" || event.Thawed || event.Error != "" {
		return fmt.Errorf("unexpected freeze watchdog readiness event: %+v", event)
	}
	w.mu.Lock()
	if len(w.buffer) != 0 {
		w.mu.Unlock()
		return errors.New("freeze watchdog sent unsolicited readiness data")
	}
	w.ready = true
	w.mu.Unlock()
	return nil
}

func (w *processWatchdog) releaseAndWait(ctx context.Context) error {
	w.mu.Lock()
	if !w.ready {
		w.mu.Unlock()
		return errors.New("freeze watchdog was not ready before release")
	}
	if w.released {
		w.mu.Unlock()
		return errors.New("freeze watchdog release was already requested")
	}
	w.released = true
	w.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := w.writeCommand('R'); err != nil {
		return fmt.Errorf("send normal release to freeze watchdog: %w", err)
	}
	event, err := w.readEvent(ctx, time.Now().Add(w.maxFreeze+ackWaitSlack))
	if err != nil {
		return fmt.Errorf("wait for verified thaw: %w", err)
	}
	if event.Event != "thawed" || event.Reason != "release" || !event.Thawed || event.Error != "" {
		return fmt.Errorf("freeze watchdog did not confirm normal thaw: %+v", event)
	}
	if len(w.buffer) != 0 {
		return errors.New("freeze watchdog sent trailing data after thaw acknowledgement")
	}
	if err := w.wait(); err != nil {
		return fmt.Errorf("freeze watchdog exited unsuccessfully after thaw: %w", err)
	}
	return nil
}

func (w *processWatchdog) writeCommand(command byte) error {
	if w.control == nil {
		return errors.New("freeze watchdog control socket is closed")
	}
	if err := w.control.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		return err
	}
	for {
		n, err := w.control.Write([]byte{command})
		if err != nil {
			return err
		}
		if n != 1 {
			return io.ErrShortWrite
		}
		return nil
	}
}

func (w *processWatchdog) readEvent(ctx context.Context, deadline time.Time) (watchdogEvent, error) {
	for {
		if i := bytes.IndexByte(w.buffer, '\n'); i >= 0 {
			if i > controlMaxBytes {
				return watchdogEvent{}, errors.New("freeze watchdog event exceeds size limit")
			}
			line := append([]byte(nil), w.buffer[:i]...)
			w.buffer = append(w.buffer[:0], w.buffer[i+1:]...)
			return decodeWatchdogEvent(line)
		}
		if len(w.buffer) > controlMaxBytes {
			return watchdogEvent{}, errors.New("freeze watchdog event exceeds size limit")
		}
		if err := ctx.Err(); err != nil {
			return watchdogEvent{}, err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return watchdogEvent{}, errors.New("timed out waiting for freeze watchdog event")
		}
		readDeadline := deadline
		shortDeadline := time.Now().Add(controlPollSlice)
		if shortDeadline.Before(readDeadline) {
			readDeadline = shortDeadline
		}
		if err := w.control.SetReadDeadline(readDeadline); err != nil {
			return watchdogEvent{}, fmt.Errorf("set freeze watchdog read deadline: %w", err)
		}
		var chunk [1024]byte
		n, readErr := w.control.Read(chunk[:])
		if n > 0 {
			w.buffer = append(w.buffer, chunk[:n]...)
			continue
		}
		if readErr != nil {
			if timeoutErr, ok := readErr.(net.Error); ok && timeoutErr.Timeout() {
				continue
			}
			return watchdogEvent{}, fmt.Errorf("read freeze watchdog event: %w", readErr)
		}
		return watchdogEvent{}, io.EOF
	}
}

func decodeWatchdogEvent(data []byte) (watchdogEvent, error) {
	var event watchdogEvent
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return event, fmt.Errorf("decode freeze watchdog event: %w", err)
	}
	if token != json.Delim('{') {
		return event, errors.New("freeze watchdog event is not a JSON object")
	}
	seen := make(map[string]struct{}, 4)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return event, fmt.Errorf("decode freeze watchdog event key: %w", err)
		}
		key, ok := token.(string)
		if !ok {
			return event, errors.New("freeze watchdog event key is not a string")
		}
		if _, exists := seen[key]; exists {
			return event, fmt.Errorf("freeze watchdog event repeats field %q", key)
		}
		seen[key] = struct{}{}
		switch key {
		case "event":
			err = decoder.Decode(&event.Event)
		case "reason":
			err = decoder.Decode(&event.Reason)
		case "thawed":
			err = decoder.Decode(&event.Thawed)
		case "error":
			err = decoder.Decode(&event.Error)
		default:
			return event, fmt.Errorf("unknown freeze watchdog event field %q", key)
		}
		if err != nil {
			return event, fmt.Errorf("decode freeze watchdog event field %q: %w", key, err)
		}
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim('}') {
		if err == nil {
			err = errors.New("missing closing brace")
		}
		return event, fmt.Errorf("decode freeze watchdog event end: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return event, errors.New("freeze watchdog event contains multiple JSON values")
		}
		return event, fmt.Errorf("decode freeze watchdog event trailer: %w", err)
	}
	if event.Event == "" {
		return event, errors.New("freeze watchdog event has no event type")
	}
	return event, nil
}

func (w *processWatchdog) requestStop() {
	w.stopOnce.Do(func() {
		if w.control != nil {
			_ = w.control.Close()
		}
		if w.cmd != nil && w.cmd.Process != nil {
			_ = w.cmd.Process.Signal(syscall.SIGTERM)
		}
	})
}

func (w *processWatchdog) wait() error {
	w.waitOnce.Do(func() {
		if w.waitProcess != nil {
			w.waitErr = w.waitProcess()
		} else if w.cmd == nil {
			w.waitErr = errors.New("freeze watchdog process is unavailable")
		} else {
			w.waitErr = w.cmd.Wait()
		}
		if w.waitErr != nil && w.root != nil {
			thaw := w.emergencyThaw
			if thaw == nil {
				thaw = emergencyThaw
			}
			if thawErr := thaw(w.root); thawErr != nil {
				w.waitErr = errors.Join(w.waitErr, fmt.Errorf("emergency thaw after watchdog exit: %w", thawErr))
			}
		}
		if w.control != nil {
			_ = w.control.Close()
		}
	})
	return w.waitErr
}

// emergencyThaw is called only after the self-exec helper has conclusively
// exited with an error. It is never used while a helper could still freeze or
// thaw the filesystem. EINVAL means no freeze remains to release.
func emergencyThaw(root *os.File) error {
	if root == nil {
		return errors.New("missing pinned filesystem root for emergency thaw")
	}
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(root.Fd()), uintptr(thawIoctl), 0)
	if errno == 0 || errors.Is(errno, unix.EINVAL) {
		return nil
	}
	return errno
}

// RunWatchdog is the self-exec entry point for studio-agent __capture-watchdog
// <max-freeze>. The launcher maps the pinned filesystem directory to fd 3, a duplex Unix
// socket to fd 4, and the inherited capture lock to fd 5. The helper does no
// logging or filesystem writes and holds fd 5 until process exit.
func RunWatchdog(maxFreezeArg string) error {
	maxFreeze, err := time.ParseDuration(maxFreezeArg)
	if err != nil {
		return fmt.Errorf("parse freeze limit: %w", err)
	}
	if maxFreeze, err = checkMaxFreeze(maxFreeze); err != nil {
		return err
	}
	pid, err := unix.Getsid(0)
	if err != nil {
		return fmt.Errorf("inspect watchdog session: %w", err)
	}
	if pid != unix.Getpid() {
		if _, err := unix.Setsid(); err != nil {
			return fmt.Errorf("create watchdog session: %w", err)
		}
	}
	if err := validateWatchdogFDs(3, 4, 5); err != nil {
		return err
	}
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(signals)
	runtime := &unixWatchdogRuntime{rootFD: 3, controlFD: 4, signals: signals}
	return runWatchdogMachine(runtime, time.Now, maxFreeze)
}

func validateWatchdogFDs(rootFD, controlFD, lockFD int) error {
	var rootStat unix.Stat_t
	if err := unix.Fstat(rootFD, &rootStat); err != nil {
		return fmt.Errorf("validate watchdog root fd: %w", err)
	}
	if rootStat.Mode&unix.S_IFMT != unix.S_IFDIR {
		return errors.New("watchdog root fd is not a directory")
	}
	var fs unix.Statfs_t
	if err := unix.Fstatfs(rootFD, &fs); err != nil {
		return fmt.Errorf("validate watchdog filesystem: %w", err)
	}
	if uint64(fs.Type) != uint64(unix.EXT4_SUPER_MAGIC) {
		return errors.New("watchdog root fd is not on ext4")
	}
	typeValue, err := unix.GetsockoptInt(controlFD, unix.SOL_SOCKET, unix.SO_TYPE)
	if err != nil {
		return fmt.Errorf("validate watchdog control fd: %w", err)
	}
	if typeValue != unix.SOCK_STREAM {
		return errors.New("watchdog control fd is not a stream socket")
	}
	if err := validateWatchdogLockFD(lockFD); err != nil {
		return err
	}
	return nil
}

// validateWatchdogLockFD checks the inherited descriptor without acquiring or
// releasing its flock. Keeping fd 5 open keeps the parent's open-file-description
// lock alive after an abrupt agent death.
func validateWatchdogLockFD(lockFD int) error {
	var lockStat unix.Stat_t
	if err := unix.Fstat(lockFD, &lockStat); err != nil {
		return fmt.Errorf("validate watchdog capture lock fd: %w", err)
	}
	if lockStat.Mode&unix.S_IFMT != unix.S_IFREG || lockStat.Nlink != 1 || lockStat.Uid != uint32(os.Geteuid()) || lockStat.Mode&0o077 != 0 {
		return errors.New("watchdog capture lock is not a private regular file")
	}
	var lockFS unix.Statfs_t
	if err := unix.Fstatfs(lockFD, &lockFS); err != nil {
		return fmt.Errorf("inspect watchdog capture lock filesystem: %w", err)
	}
	if uint64(lockFS.Type) != uint64(unix.TMPFS_MAGIC) {
		return errors.New("watchdog capture lock is not on tmpfs")
	}
	shm, err := openVerifiedShm()
	if err != nil {
		return fmt.Errorf("verify watchdog capture lock tmpfs: %w", err)
	}
	defer shm.Close()
	var shmStat unix.Stat_t
	if err := unix.Fstat(int(shm.Fd()), &shmStat); err != nil {
		return fmt.Errorf("stat verified /dev/shm directory: %w", err)
	}
	if lockStat.Dev != shmStat.Dev {
		return errors.New("watchdog capture lock is not on the verified /dev/shm filesystem")
	}
	return nil
}

type watchdogRuntime interface {
	freeze() error
	thaw() error
	send(watchdogEvent) error
	waitUntil(time.Time) (string, error)
}

func runWatchdogMachine(runtime watchdogRuntime, now func() time.Time, maxFreeze time.Duration) error {
	// FIFREEZE itself is a synchronous kernel call. The maxFreeze lease starts
	// only after it returns successfully; the parent has an independent bounded
	// ready wait and signals/closes this helper on cancellation. If the kernel
	// never returns from FIFREEZE or FITHAW, userspace cannot promise recovery or
	// safely kill this helper while the filesystem's state is unknown.
	if err := runtime.freeze(); err != nil {
		var thawErr error
		// EINTR leaves the ioctl outcome uncertain. A conservative thaw is safe
		// here; EINVAL means the freeze did not take effect. No success ack is sent.
		if errors.Is(err, unix.EINTR) {
			thawErr = runtime.thaw()
		}
		eventErr := runtime.send(watchdogEvent{Event: "error", Reason: "freeze", Error: err.Error()})
		return errors.Join(err, thawErr, eventErr)
	}
	deadline := now().Add(maxFreeze)
	if err := runtime.send(watchdogEvent{Event: "ready"}); err != nil {
		thawErr := runtime.thaw()
		return errors.Join(fmt.Errorf("send watchdog ready event: %w", err), thawErr)
	}
	reason, waitErr := runtime.waitUntil(deadline)
	if reason == "" {
		reason = "error"
	}
	thawErr := runtime.thaw()
	if waitErr != nil || thawErr != nil {
		combined := errors.Join(waitErr, thawErr)
		eventErr := runtime.send(watchdogEvent{Event: "error", Reason: reason, Error: combined.Error()})
		return errors.Join(combined, eventErr)
	}
	if reason == "error" {
		err := errors.New("watchdog ended without a release reason")
		return errors.Join(err, runtime.send(watchdogEvent{Event: "error", Reason: reason, Thawed: true, Error: err.Error()}))
	}
	return runtime.send(watchdogEvent{Event: "thawed", Reason: reason, Thawed: true})
}

type unixWatchdogRuntime struct {
	rootFD    int
	controlFD int
	signals   <-chan os.Signal
}

func (r *unixWatchdogRuntime) freeze() error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(r.rootFD), uintptr(freezeIoctl), 0)
	if errno != 0 {
		return errno
	}
	return nil
}

func (r *unixWatchdogRuntime) thaw() error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(r.rootFD), uintptr(thawIoctl), 0)
	if errno != 0 {
		return errno
	}
	return nil
}

func (r *unixWatchdogRuntime) send(event watchdogEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeControl(r.controlFD, data)
}

func (r *unixWatchdogRuntime) waitUntil(deadline time.Time) (string, error) {
	for {
		select {
		case <-r.signals:
			return "signal", nil
		default:
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return "timeout", nil
		}
		millis := int((min(remaining, controlPollSlice) + time.Millisecond - 1) / time.Millisecond)
		if millis < 1 {
			millis = 1
		}
		pollfd := []unix.PollFd{{Fd: int32(r.controlFD), Events: unix.POLLIN | unix.POLLHUP | unix.POLLERR}}
		_, err := unix.Poll(pollfd, millis)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return "error", fmt.Errorf("poll watchdog control socket: %w", err)
		}
		if pollfd[0].Revents&unix.POLLNVAL != 0 {
			return "error", errors.New("watchdog control socket is invalid")
		}
		if pollfd[0].Revents&(unix.POLLIN|unix.POLLHUP|unix.POLLERR) == 0 {
			continue
		}
		var command [1]byte
		n, readErr := unix.Read(r.controlFD, command[:])
		if errors.Is(readErr, unix.EINTR) || errors.Is(readErr, unix.EAGAIN) {
			continue
		}
		if readErr != nil {
			return "error", fmt.Errorf("read watchdog control command: %w", readErr)
		}
		if n == 0 {
			return "owner-gone", nil
		}
		switch command[0] {
		case 'R':
			return "release", nil
		case 'C':
			return "cancel", nil
		default:
			return "invalid-command", nil
		}
	}
}

func writeControl(fd int, data []byte) error {
	for len(data) != 0 {
		n, err := unix.Write(fd, data)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
