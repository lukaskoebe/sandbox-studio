// Command export-import runs sandbox export and import end to end on real VMs with a
// private Store, data directory, registry and fake sealer. Egress points at 127.0.0.1:9,
// so guests have no network.
//
//	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o "$dir/studio-agent" ./cmd/studio-agent
//	go run ./spikes/export-import -agent "$dir/studio-agent" -scratch "$dir"
//
// It builds a template, creates a template-backed sandbox and a base-image sandbox and
// writes a workspace fixture into both. It exports the first while running and imports
// it into a second environment (a stand-in for another host), exports the second while
// stopped and imports it into its own environment (taking a suffixed name), and compares
// a manifest of /workspace each time. Everything it creates is removed.
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
		fmt.Fprintln(os.Stderr, "usage: export-import -agent /abs/studio-agent -scratch DIR")
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

type created struct{ env, id string }

func run(ctx context.Context, agentPath, scratch string) (retErr error) {
	root, err := os.MkdirTemp(scratch, "ss-export-")
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
	envA, err := st.CreateEnvironment(ctx, "spike-export-a")
	if err != nil {
		return err
	}
	envB, err := st.CreateEnvironment(ctx, "spike-export-b")
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
	mgr.Builds = worker
	workerCtx, stopWorker := context.WithCancel(ctx)
	workerDone := make(chan struct{})
	go func() { worker.Run(workerCtx); close(workerDone) }()

	var sandboxesMade []created
	var templates []created
	defer func() {
		cctx, ccancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Minute)
		defer ccancel()
		for _, c := range sandboxesMade {
			if err := mgr.Delete(cctx, c.env, c.id); err != nil && !errors.Is(err, store.ErrNotFound) {
				retErr = errors.Join(retErr, fmt.Errorf("delete %s: %w", c.id, err))
			}
		}
		for _, c := range templates {
			if err := registry.Delete(cctx, c.env, c.id); err != nil {
				retErr = errors.Join(retErr, fmt.Errorf("delete template %s: %w", c.id, err))
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
		if entries, _ := os.ReadDir(p.Imports()); len(entries) != 0 {
			retErr = errors.Join(retErr, fmt.Errorf("%d staged import files are left over", len(entries)))
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
	tpl, err := build(ctx, worker, st, envA.ID)
	if err != nil {
		return err
	}
	templates = append(templates, created{envA.ID, tpl})
	fmt.Println("ok   built template", tpl)

	// 1. A template-backed sandbox exported while running, imported into another environment.
	src, err := mgr.CreateFromTemplate(ctx, envA.ID, tpl, "spike-src")
	if err != nil {
		return err
	}
	sandboxesMade = append(sandboxesMade, created{envA.ID, src.ID})
	want, err := writeFixture(ctx, mgr, rt, st, src.Sandbox)
	if err != nil {
		return err
	}
	file := filepath.Join(root, "spike-src"+sandboxes.ExportExtension)
	start := time.Now()
	if err := exportTo(ctx, mgr, envA.ID, src.ID, file); err != nil {
		return fmt.Errorf("export running: %w", err)
	}
	fmt.Printf("ok   exported a running sandbox (%s)\n", time.Since(start).Round(time.Millisecond))
	start = time.Now()
	imported, err := importFrom(ctx, mgr, envB.ID, file, &sandboxesMade)
	if err != nil {
		return fmt.Errorf("import into another environment: %w", err)
	}
	if imported.Name != "spike-src" || imported.TemplateID == "" || imported.TemplateID == tpl || imported.CPUs != 1 || imported.MaxMemoryMiB != 1024 {
		return fmt.Errorf("imported view: %+v", imported)
	}
	templates = append(templates, created{envB.ID, imported.TemplateID})
	if err := check(ctx, mgr, rt, st, imported, want, "proof"); err != nil {
		return fmt.Errorf("import into another environment: %w", err)
	}
	fmt.Printf("ok   import into a second environment rebuilt the template and matches (%s)\n", time.Since(start).Round(time.Millisecond))

	// 2. A base-image sandbox exported while stopped, imported next to itself.
	plain, err := mgr.Create(ctx, envA.ID, sandboxes.CreateRequest{Name: "spike-plain"})
	if err != nil {
		return err
	}
	sandboxesMade = append(sandboxesMade, created{envA.ID, plain.ID})
	want, err = writeFixture(ctx, mgr, rt, st, plain.Sandbox)
	if err != nil {
		return err
	}
	if _, err := mgr.Stop(ctx, envA.ID, plain.ID); err != nil {
		return err
	}
	file = filepath.Join(root, "spike-plain"+sandboxes.ExportExtension)
	if err := exportTo(ctx, mgr, envA.ID, plain.ID, file); err != nil {
		return fmt.Errorf("export stopped: %w", err)
	}
	if view, err := mgr.Get(ctx, envA.ID, plain.ID); err != nil || view.Status != runtime.StatusStopped {
		return fmt.Errorf("exported stopped sandbox is %s (%v)", view.Status, err)
	}
	imported, err = importFrom(ctx, mgr, envA.ID, file, &sandboxesMade)
	if err != nil {
		return fmt.Errorf("import next to the source: %w", err)
	}
	if imported.Name != "spike-plain-2" || imported.TemplateID != "" {
		return fmt.Errorf("imported view: %+v", imported)
	}
	if err := check(ctx, mgr, rt, st, imported, want, ""); err != nil {
		return fmt.Errorf("import next to the source: %w", err)
	}
	fmt.Println("ok   export of a stopped sandbox keeps it stopped; import takes a suffixed name and matches")
	return nil
}

func writeFixture(ctx context.Context, mgr *sandboxes.Manager, rt *runtime.Runtime, st *store.Store, sb store.Sandbox) (string, error) {
	if err := ready(ctx, mgr, sb.EnvironmentID, sb.ID); err != nil {
		return "", err
	}
	if _, err := sh(ctx, rt, st, sb, "root", fixture); err != nil {
		return "", fmt.Errorf("write fixture: %w", err)
	}
	return sh(ctx, rt, st, sb, "root", manifest)
}

func exportTo(ctx context.Context, mgr *sandboxes.Manager, envID, id, file string) error {
	f, err := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	err = mgr.Export(ctx, envID, id, func(sandboxes.ExportManifest) (io.Writer, error) { return f, nil })
	return errors.Join(err, f.Close())
}

func importFrom(ctx context.Context, mgr *sandboxes.Manager, envID, file string, made *[]created) (store.Sandbox, error) {
	f, err := os.Open(file)
	if err != nil {
		return store.Sandbox{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return store.Sandbox{}, err
	}
	imp, err := mgr.Import(ctx, envID, f, info.Size())
	if err != nil {
		return store.Sandbox{}, err
	}
	for imp.State != store.ImportReady {
		if imp.State == store.ImportFailed {
			return store.Sandbox{}, fmt.Errorf("import failed: %s", imp.Error)
		}
		select {
		case <-ctx.Done():
			return store.Sandbox{}, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
		if imp, err = mgr.SandboxImport(ctx, envID, imp.ID); err != nil {
			return store.Sandbox{}, err
		}
		if imp.SandboxID != "" && (len(*made) == 0 || (*made)[len(*made)-1].id != imp.SandboxID) {
			*made = append(*made, created{envID, imp.SandboxID})
		}
	}
	view, err := mgr.Get(ctx, envID, imp.SandboxID)
	return view.Sandbox, err
}

func check(ctx context.Context, mgr *sandboxes.Manager, rt *runtime.Runtime, st *store.Store, sb store.Sandbox, want, proof string) error {
	if err := ready(ctx, mgr, sb.EnvironmentID, sb.ID); err != nil {
		return err
	}
	got, err := sh(ctx, rt, st, sb, "root", manifest)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("manifest differs:\nwant:\n%s\ngot:\n%s", want, got)
	}
	script, wantOut := "/workspace/proj/run.sh", "run-ok\n"
	if proof != "" {
		script += " && cat /home/agent/template-proof"
		wantOut += proof + "\n"
	}
	out, err := sh(ctx, rt, st, sb, "agent", script)
	if err != nil {
		return err
	}
	if out != wantOut {
		return fmt.Errorf("guest check output %q", out)
	}
	return nil
}

func build(ctx context.Context, w *templatebuild.Worker, st *store.Store, envID string) (string, error) {
	source := "resources:\n  cpus: 1\n  memory: 512MiB\n  max_memory: 1024MiB\n  workspace: 1024MiB\n  docker: 1024MiB\n" +
		"apt: []\ntools: {}\nsetup: |\n  echo proof > /home/agent/template-proof\n"
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
			return "", fmt.Errorf("build: %s\n%s", job.Error, text)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
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
	name := "SS_EXPORT_PROBE_" + strings.ToUpper(sb.ID)
	if os.Getenv(name) == "" {
		secret := make([]byte, 16)
		if _, err := rand.Read(secret); err != nil {
			return runtime.Egress{}, err
		}
		os.Setenv(name, hex.EncodeToString(secret))
	}
	return runtime.Egress{Nameserver: "127.0.0.1:9", Proxy: "127.0.0.1:9", User: "export-probe", PasswordEnv: name}, nil
}

func (e *deadEgress) Detach(id string) { os.Unsetenv("SS_EXPORT_PROBE_" + strings.ToUpper(id)) }

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
