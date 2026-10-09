//go:build linux

package guest

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestReadSupervisorPIDRequiresCompleteRecord(t *testing.T) {
	pid, ready, err := readSupervisorPID(bytes.NewReader([]byte("123")))
	if err != nil || ready {
		t.Fatalf("partial PID record = (%d, %v, %v); want pending", pid, ready, err)
	}

	pid, ready, err = readSupervisorPID(bytes.NewReader([]byte("123\n")))
	if err != nil || !ready || pid != 123 {
		t.Fatalf("complete PID record = (%d, %v, %v); want (123, true, nil)", pid, ready, err)
	}
}

func lockedSupervisorFile(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "supervisor.lock")
	owner, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(owner.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		owner.Close()
		t.Fatal(err)
	}
	reader, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		owner.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		reader.Close()
		owner.Close()
	})
	return owner, reader
}

func TestShutdownSupervisorWaitsForPIDPublication(t *testing.T) {
	owner, reader := lockedSupervisorFile(t)
	publishErr := make(chan error, 1)
	go func() {
		time.Sleep(150 * time.Millisecond)
		_, err := owner.WriteAt([]byte("4321\n"), 0)
		publishErr <- err
	}()

	var signaledPID int
	err := shutdownSupervisor(reader, time.Second, func(pid int) error {
		signaledPID = pid
		return syscall.Flock(int(owner.Fd()), syscall.LOCK_UN)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-publishErr; err != nil {
		t.Fatal(err)
	}
	if signaledPID != 4321 {
		t.Fatalf("signaled PID = %d; want 4321", signaledPID)
	}
}

func TestShutdownSupervisorReturnsWhenLockReleasesWithoutPID(t *testing.T) {
	owner, reader := lockedSupervisorFile(t)
	releaseErr := make(chan error, 1)
	go func() {
		time.Sleep(150 * time.Millisecond)
		releaseErr <- syscall.Flock(int(owner.Fd()), syscall.LOCK_UN)
	}()

	signaled := false
	err := shutdownSupervisor(reader, time.Second, func(int) error {
		signaled = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-releaseErr; err != nil {
		t.Fatal(err)
	}
	if signaled {
		t.Fatal("signal callback ran without a published PID")
	}
}

func TestServiceRestartsAfterExit(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "starts")
	s := service{name: "flaky", path: "/bin/sh", args: []string{"-c", "echo x >> " + marker}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.run(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), io.Discard, 5*time.Millisecond)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, _ := os.ReadFile(marker)
		if strings.Count(string(b), "x") >= 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("service started %d times; want at least 3", strings.Count(string(b), "x"))
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
}

func TestSuperviseAllStopsInReverseOrder(t *testing.T) {
	dir := t.TempDir()
	order := filepath.Join(dir, "order")
	svc := func(name string) service {
		// Each service records its name when it is asked to stop.
		script := "trap 'echo " + name + " >> " + order + "; exit 0' TERM; while :; do sleep 0.05; done"
		return service{name: name, path: "/bin/sh", args: []string{"-c", script}}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- superviseAll(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), filepath.Join(dir, "logs"),
			[]service{svc("first"), svc("second"), svc("third")}, 5*time.Millisecond)
	}()
	time.Sleep(300 * time.Millisecond) // let the shells install their traps
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("superviseAll did not stop")
	}
	b, err := os.ReadFile(order)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Fields(string(b)); strings.Join(got, ",") != "third,second,first" {
		t.Fatalf("stop order = %v; want third, second, first", got)
	}
}

func TestCappedLogStartsOver(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "log"))
	if err != nil {
		t.Fatal(err)
	}
	l := &cappedLog{f: f, max: 10}
	io.WriteString(l, "12345678")
	io.WriteString(l, "abcd")
	l.Close()
	b, _ := os.ReadFile(f.Name())
	if string(b) != "abcd" {
		t.Fatalf("log = %q; want only the write after the cap", b)
	}
}
