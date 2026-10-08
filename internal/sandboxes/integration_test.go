//go:build integration

// Integration test: boots the real base image. Needs a microsandbox-capable host, the
// image loaded as sandbox-studio-base:dev (`make image`) and the agent built (`make agent`).
//
//	go test -tags integration -run TestSandboxLifecycle -v ./internal/sandboxes
package sandboxes

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentbin"
	"github.com/lukaskoebe/sandbox-studio/internal/agentchan"
	"github.com/lukaskoebe/sandbox-studio/internal/paths"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func TestSandboxLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	dir, err := os.MkdirTemp("", "ssit")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	p := paths.Paths{Data: dir}
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := agentbin.Install(p.Guest(), goruntime.GOARCH); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, p.DB())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := runtime.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	m := &Manager{
		Store:   st,
		Runtime: runtime.New(runtime.Options{Image: envOr("STUDIO_IMAGE", "sandbox-studio-base:dev"), GuestDir: p.Guest()}),
		Hub:     agentchan.NewHub(log),
		Paths:   p,
		Log:     log,
	}
	env, _ := st.CreateEnvironment(ctx, "it")

	t0 := time.Now()
	v, err := m.Create(ctx, env.ID, CreateRequest{Name: "it-box", MemoryMiB: 2048, WorkspaceMiB: 2048, DockerMiB: 4096})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := m.Delete(context.Background(), env.ID, v.ID); err != nil {
			t.Errorf("delete: %v", err)
		}
	}()
	t.Logf("created in %s, status %s", time.Since(t0).Round(time.Millisecond), v.Status)
	if err := m.WaitReady(ctx, v.ID, 60*time.Second); err != nil {
		t.Fatalf("agent never connected: %v", err)
	}
	t.Logf("agent connected after %s", time.Since(t0).Round(time.Millisecond))

	// A terminal: run a command and see its output.
	pty, err := m.Hub.OpenPTY(ctx, v.ID, "main", 120, 30)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(pty, "echo marker-$((40+2)); id -un; pwd\r")
	expectOutput(t, pty, "marker-42", 15*time.Second)
	fmt.Fprint(pty, "cd /workspace && (python3 -m http.server 8765 >/dev/null 2>&1 &) && echo server-up\r")
	expectOutput(t, pty, "server-up", 15*time.Second)
	pty.Close()

	sessions, err := m.Hub.Sessions(ctx, v.ID)
	if err != nil || len(sessions) == 0 || sessions[0].Name != "main" {
		t.Fatalf("sessions %+v %v", sessions, err)
	}

	// A preview: reach the guest web server through the agent channel.
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return m.Hub.DialTCP(ctx, v.ID, 8765)
	}}}
	var resp *http.Response
	for i := 0; i < 50; i++ {
		resp, err = client.Get("http://preview/")
		if err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("preview status %d", resp.StatusCode)
	}
	ports, _ := m.Hub.Ports(ctx, v.ID)
	t.Logf("listening ports: %+v", ports)

	// Docker inside the sandbox.
	pty, err = m.Hub.OpenPTY(ctx, v.ID, "docker", 120, 30)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(pty, "docker run --rm hello-world | grep -c 'Hello from Docker' | sed 's/^/docker-ok-/'\r")
	expectOutput(t, pty, "docker-ok-1", 90*time.Second)
	pty.Close()

	// Stop and start keep the workspace; the agent reconnects.
	if _, err := m.Stop(ctx, env.ID, v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(ctx, env.ID, v.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.WaitReady(ctx, v.ID, 60*time.Second); err != nil {
		t.Fatalf("agent did not reconnect after restart: %v", err)
	}
	t.Logf("restart ok")
}

func expectOutput(t *testing.T, pty *agentchan.PTY, want string, timeout time.Duration) {
	t.Helper()
	found := make(chan string, 1)
	go func() {
		var seen strings.Builder
		r := bufio.NewReader(pty)
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			seen.Write(buf[:n])
			if strings.Contains(seen.String(), want) {
				found <- ""
				return
			}
			if err != nil {
				found <- seen.String()
				return
			}
		}
	}()
	select {
	case rest := <-found:
		if rest != "" {
			t.Fatalf("terminal closed before %q; output:\n%s", want, rest)
		}
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for %q", want)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
