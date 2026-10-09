// Command rebase runs fork and rebase end to end on real VMs with a private Store,
// registry and fake sealer. Egress points at 127.0.0.1:9, so guests have no network.
//
//	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o "$dir/studio-agent" ./cmd/studio-agent
//	go run ./spikes/rebase -agent "$dir/studio-agent" -scratch "$dir"
//
// It builds two templates, creates a sandbox and writes a workspace fixture, forks it
// while running, rebases the source while running and the fork while stopped, and
// compares a manifest of /workspace each time. Everything it creates is removed.
package main

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentchan"
	"github.com/lukaskoebe/sandbox-studio/internal/ca"
	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/paths"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/templatebuild"
	"github.com/lukaskoebe/sandbox-studio/internal/templateregistry"
)

const baseImage = "sandbox-studio-base:dev"

func main() {
	agent := flag.String("agent", "", "absolute path to a Linux amd64 studio-agent binary")
	scratch := flag.String("scratch", "", "existing parent directory for private state")
	flag.Parse()
	if !filepath.IsAbs(*agent) || *scratch == "" {
		fmt.Fprintln(os.Stderr, "usage: rebase -agent /abs/studio-agent -scratch DIR")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	if err := run(ctx, *agent, *scratch); err != nil {
		fmt.Fprintln(os.Stderr, "FAIL:", err)
		os.Exit(1)
	}
	fmt.Println("PASS")
}

func run(ctx context.Context, agentPath, scratch string) (retErr error) {
	root, err := os.MkdirTemp(scratch, "ss-rebase-")
	if err != nil {
		return err
	}
	p := paths.Paths{Data: filepath.Join(root, "state")}
	if err := p.Ensure(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(p.Guest(), "bin"), 0o755); err != nil {
		return err
	}
	if err := copyFile(agentPath, filepath.Join(p.Guest(), "bin", "studio-agent")); err != nil {
		return err
	}
	sealer, err := newFakeSealer()
	if err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	st, err := store.Open(ctx, p.DB())
	if err != nil {
		return err
	}
	env, err := st.CreateEnvironment(ctx, "rebase-probe")
	if err != nil {
		return err
	}
	rt := runtime.New(runtime.Options{Image: baseImage, GuestDir: p.Guest()})
	before, err := rt.Statuses(ctx, sandboxes.VMPrefix)
	if err != nil {
		return err
	}

	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	registry, err := templateregistry.Open(ctx, st, filepath.Join(root, "registry"), ln.Addr().String(), sealer)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: registry.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go srv.Serve(ln)

	hub := agentchan.NewHub(log)
	mgr := &sandboxes.Manager{
		Store: st, Runtime: rt, Templates: registry, Hub: hub, Egress: &deadEgress{},
		CA: &ca.Authority{Store: st, Sealer: sealer}, Secrets: noSecrets{}, Paths: p, Log: log,
	}
	hub.OnConnect = mgr.Configure
	worker, err := templatebuild.New(templatebuild.Options{
		Store: st, Runtime: rt, Guests: mgr, Registry: registry, Bus: &events.Bus{}, Log: log,
	})
	if err != nil {
		return err
	}
	workerCtx, stopWorker := context.WithCancel(ctx)
	workerDone := make(chan struct{})
	go func() { worker.Run(workerCtx); close(workerDone) }()

	var created []string
	var templates []string
	defer func() {
		cctx, ccancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Minute)
		defer ccancel()
		for _, id := range created {
			if err := mgr.Delete(cctx, env.ID, id); err != nil && !errors.Is(err, store.ErrNotFound) {
				retErr = errors.Join(retErr, fmt.Errorf("delete %s: %w", id, err))
			}
		}
		for _, id := range templates {
			if err := registry.Delete(cctx, env.ID, id); err != nil {
				retErr = errors.Join(retErr, fmt.Errorf("delete template %s: %w", id, err))
			}
		}
		stopWorker()
		<-workerDone
		srv.Close()
		registry.Close()
		st.Close()
		after, err := rt.Statuses(cctx, sandboxes.VMPrefix)
		if err != nil {
			retErr = errors.Join(retErr, err)
		}
		for name := range after {
			if _, ok := before[name]; !ok {
				retErr = errors.Join(retErr, fmt.Errorf("VM %s is left over", name))
			}
		}
		if retErr == nil {
			retErr = os.RemoveAll(root)
		} else {
			fmt.Fprintln(os.Stderr, "state kept at", root)
		}
	}()

	// Builds only log that base preparation failed; this surfaces the cause.
	if _, err := rt.PrepareTemplateBase(ctx); err != nil {
		return fmt.Errorf("prepare base: %w", err)
	}
	tplA, err := build(ctx, worker, st, env.ID, "A", "cpus: 1\n  memory: 512MiB\n  max_memory: 1024MiB\n  workspace: 1024MiB\n  docker: 1024MiB")
	if err != nil {
		return err
	}
	templates = append(templates, tplA)
	tplB, err := build(ctx, worker, st, env.ID, "B", "cpus: 2\n  memory: 640MiB\n  max_memory: 1024MiB\n  workspace: 2048MiB\n  docker: 1024MiB")
	if err != nil {
		return err
	}
	templates = append(templates, tplB)
	fmt.Println("ok   built templates", tplA, tplB)

	src, err := mgr.CreateFromTemplate(ctx, env.ID, tplA, "source")
	if err != nil {
		return err
	}
	created = append(created, src.ID)
	if err := ready(ctx, mgr, env.ID, src.ID); err != nil {
		return err
	}
	if _, err := sh(ctx, rt, st, src.Sandbox, "root", fixture); err != nil {
		return fmt.Errorf("write fixture: %w", err)
	}
	want, err := sh(ctx, rt, st, src.Sandbox, "root", manifest)
	if err != nil {
		return err
	}
	fmt.Printf("ok   fixture written (%d manifest lines)\n", strings.Count(want, "\n"))

	start := time.Now()
	fork, err := mgr.Fork(ctx, env.ID, src.ID, "fork")
	if fork.ID != "" {
		created = append(created, fork.ID)
	}
	if err != nil {
		return fmt.Errorf("fork: %w", err)
	}
	if fork.TemplateID != tplA || fork.Generation != 1 || fork.ID == src.ID {
		return fmt.Errorf("fork view: %+v", fork.Sandbox)
	}
	if err := ready(ctx, mgr, env.ID, fork.ID); err != nil {
		return err
	}
	if err := check(ctx, rt, st, fork.Sandbox, want, "A"); err != nil {
		return fmt.Errorf("fork: %w", err)
	}
	fmt.Printf("ok   fork of a running sandbox matches (%s)\n", time.Since(start).Round(time.Millisecond))
	if _, err := sh(ctx, rt, st, fork.Sandbox, "root", "echo fork-only > /workspace/fork-only"); err != nil {
		return err
	}
	if out, err := sh(ctx, rt, st, src.Sandbox, "root", "test ! -e /workspace/fork-only && echo independent"); err != nil || out != "independent\n" {
		return fmt.Errorf("fork is not independent: %q %v", out, err)
	}
	if _, err := sh(ctx, rt, st, fork.Sandbox, "root", "rm /workspace/fork-only"); err != nil {
		return err
	}
	if _, err := mgr.Stop(ctx, env.ID, fork.ID); err != nil {
		return err
	}

	start = time.Now()
	rebased, err := mgr.Rebase(ctx, env.ID, src.ID, tplB)
	if err != nil {
		return fmt.Errorf("rebase running: %w", err)
	}
	if rebased.TemplateID != tplB || rebased.Generation != 2 || rebased.CPUs != 2 || rebased.MemoryMiB != 640 || rebased.WorkspaceMiB != 2048 {
		return fmt.Errorf("rebased view: %+v", rebased.Sandbox)
	}
	if rebased.Status != runtime.StatusRunning {
		return fmt.Errorf("rebased running sandbox is %s", rebased.Status)
	}
	if err := ready(ctx, mgr, env.ID, src.ID); err != nil {
		return err
	}
	if err := check(ctx, rt, st, rebased.Sandbox, want, "B"); err != nil {
		return fmt.Errorf("rebase running: %w", err)
	}
	if err := gone(ctx, rt, sandboxes.VMName(src.Sandbox)); err != nil {
		return err
	}
	fmt.Printf("ok   rebase of a running sandbox onto B matches, g1 removed (%s)\n", time.Since(start).Round(time.Millisecond))
	if _, err := mgr.Stop(ctx, env.ID, src.ID); err != nil {
		return err
	}

	start = time.Now()
	rebased, err = mgr.Rebase(ctx, env.ID, fork.ID, tplB)
	if err != nil {
		return fmt.Errorf("rebase stopped: %w", err)
	}
	if rebased.Status != runtime.StatusStopped || rebased.Generation != 2 {
		return fmt.Errorf("rebased stopped sandbox is %s at g%d", rebased.Status, rebased.Generation)
	}
	took := time.Since(start)
	if _, err := mgr.Start(ctx, env.ID, fork.ID); err != nil {
		return err
	}
	if err := ready(ctx, mgr, env.ID, fork.ID); err != nil {
		return err
	}
	if err := check(ctx, rt, st, rebased.Sandbox, want, "B"); err != nil {
		return fmt.Errorf("rebase stopped: %w", err)
	}
	if err := gone(ctx, rt, sandboxes.VMName(fork.Sandbox)); err != nil {
		return err
	}
	fmt.Printf("ok   rebase of a stopped sandbox stays stopped and matches after start (%s)\n", took.Round(time.Millisecond))
	return nil
}

// fixture covers directories, owners, modes, setuid, a hardlink, symlinks (one
// pointing outside), a read-only directory, explicit mtimes and a 3 MB random file.
const fixture = `set -eu
cd /workspace
mkdir -p proj/sub ro
printf 'hello\n' > proj/a.txt
ln proj/a.txt proj/hard.txt
ln -s a.txt proj/link
ln -s ../../etc/passwd proj/outside
printf '#!/bin/sh\necho run-ok\n' > proj/run.sh
head -c 3000000 /dev/urandom > proj/sub/big.bin
cp /bin/true proj/suid
: > empty
: > ro/f
chown -R agent:agent proj
chmod 0755 proj/run.sh
chmod 0640 proj/sub/big.bin
chmod 4755 proj/suid
touch -d '2002-01-01 00:00:00' proj/a.txt
touch -h -d '2001-02-03 04:05:06' proj/link
chmod 0555 ro
`

// manifest lists every entry with type, mode, owner, link count, mtime and link
// target, plus a checksum of each regular file.
const manifest = `set -eu
cd /workspace
find . -path ./lost+found -prune -o -printf '%p|%y|%m|%U|%G|%n|%T@|%l\n' | sort
find . -path ./lost+found -prune -o -type f -print0 | sort -z | xargs -0 sha256sum
`

func check(ctx context.Context, rt *runtime.Runtime, st *store.Store, sb store.Sandbox, want, template string) error {
	got, err := sh(ctx, rt, st, sb, "root", manifest)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("manifest differs:\nwant:\n%s\ngot:\n%s", want, got)
	}
	out, err := sh(ctx, rt, st, sb, "agent", "/workspace/proj/run.sh && cat /home/agent/template-proof")
	if err != nil {
		return err
	}
	if out != "run-ok\n"+template+"\n" {
		return fmt.Errorf("guest check output %q", out)
	}
	return nil
}

func gone(ctx context.Context, rt *runtime.Runtime, name string) error {
	status, err := rt.Status(ctx, name)
	if err != nil {
		return err
	}
	if status != runtime.StatusAbsent {
		return fmt.Errorf("old VM %s is %s", name, status)
	}
	return nil
}

func build(ctx context.Context, w *templatebuild.Worker, st *store.Store, envID, name, res string) (string, error) {
	source := fmt.Sprintf("resources:\n  %s\napt: []\ntools: {}\nsetup: |\n  echo %s > /home/agent/template-proof\n", res, name)
	job, _, err := w.Submit(ctx, envID, source)
	if err != nil {
		return "", err
	}
	for {
		job, err = st.BuildJob(ctx, envID, job.ID)
		if err != nil {
			return "", err
		}
		if job.Status == store.BuildReady && !job.CleanupPending {
			return job.TemplateID, nil
		}
		if job.Status == store.BuildFailed || job.Status == store.BuildCancelled {
			text, _, _ := st.BuildLog(ctx, envID, job.ID)
			return "", fmt.Errorf("build %s: %s\n%s", name, job.Error, text)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func ready(ctx context.Context, mgr *sandboxes.Manager, envID, id string) error {
	if err := mgr.WaitReady(ctx, id, 60*time.Second); err != nil {
		return err
	}
	return mgr.ConfigureGuest(ctx, envID, id)
}

// sh runs a bash script in the sandbox's current VM and returns its stdout.
func sh(ctx context.Context, rt *runtime.Runtime, st *store.Store, sb store.Sandbox, user, script string) (string, error) {
	sb, err := st.Sandbox(ctx, sb.EnvironmentID, sb.ID)
	if err != nil {
		return "", err
	}
	home := "/root"
	if user == "agent" {
		home = "/home/agent"
	}
	var out lockedBuffer
	stderr := make(chan runtime.RunOutput, 64)
	result, err := rt.Run(ctx, runtime.OwnedVM{Name: sandboxes.VMName(sb), Labels: labels(sb)}, runtime.RunCommand{
		Path: "/bin/bash", Args: []string{"-c", script}, User: user, Cwd: home,
		Env:     map[string]string{"HOME": home, "PATH": "/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"},
		Timeout: time.Minute, Stdout: &out,
	}, stderr)
	var msg strings.Builder
	for done := false; !done; {
		select {
		case chunk := <-stderr:
			msg.Write(chunk.Data)
		default:
			done = true
		}
	}
	if err != nil || !result.ExitCodeKnown || result.ExitCode != 0 {
		return "", fmt.Errorf("guest command failed (exit %d, known %t): %v: %s", result.ExitCode, result.ExitCodeKnown, err, msg.String())
	}
	return out.String(), nil
}

func labels(sb store.Sandbox) map[string]string {
	l := map[string]string{
		"studio.sandbox-id":     sb.ID,
		"studio.environment-id": sb.EnvironmentID,
		"studio.sandbox-name":   sb.Name,
	}
	if sb.TemplateID != "" {
		l["studio.template-id"] = sb.TemplateID
	}
	return l
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// deadEgress points every sandbox at a closed loopback port.
type deadEgress struct{ mu sync.Mutex }

func (e *deadEgress) Attach(sb store.Sandbox) (runtime.Egress, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	name := "SS_REBASE_PROBE_" + strings.ToUpper(sb.ID)
	if os.Getenv(name) == "" {
		secret := make([]byte, 16)
		if _, err := rand.Read(secret); err != nil {
			return runtime.Egress{}, err
		}
		os.Setenv(name, hex.EncodeToString(secret))
	}
	return runtime.Egress{Nameserver: "127.0.0.1:9", Proxy: "127.0.0.1:9", User: "rebase-probe", PasswordEnv: name}, nil
}

func (e *deadEgress) Detach(id string) { os.Unsetenv("SS_REBASE_PROBE_" + strings.ToUpper(id)) }

type noSecrets struct{}

func (noSecrets) Env(context.Context, string) (map[string]string, error) { return nil, nil }

// fakeSealer keeps its key in memory, so the OS keychain is never used.
type fakeSealer struct{ aead cipher.AEAD }

func newFakeSealer() (*fakeSealer, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &fakeSealer{aead: aead}, nil
}

func (s *fakeSealer) Seal(plaintext, aad []byte) []byte {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic(err)
	}
	return s.aead.Seal(nonce, nonce, plaintext, aad)
}

func (s *fakeSealer) Unseal(sealed, aad []byte) ([]byte, error) {
	n := s.aead.NonceSize()
	if len(sealed) < n {
		return nil, errors.New("sealed value is too short")
	}
	return s.aead.Open(nil, sealed[:n], sealed[n:], aad)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
