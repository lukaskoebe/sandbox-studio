//go:build linux

package guest

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
