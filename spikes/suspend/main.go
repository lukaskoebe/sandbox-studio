// Command suspend runs in-place suspend and resume end to end on a real VM with a private
// Store and fake sealer. Egress points at 127.0.0.1:9, so the guest has no network.
//
//	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o "$dir/studio-agent" ./cmd/studio-agent
//	go run ./spikes/suspend -agent "$dir/studio-agent" -scratch "$dir"
//
// It creates a sandbox, starts a counter in a tmux terminal (a stand-in for an agent TUI),
// suspends the sandbox, waits, resumes it, and checks that the same process continued
// without counting the paused time and that the terminal reattaches mid-session. It then
// stops the sandbox while suspended. Everything it creates is removed.
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
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentchan"
	"github.com/lukaskoebe/sandbox-studio/internal/ca"
	"github.com/lukaskoebe/sandbox-studio/internal/paths"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

const (
	baseImage = "sandbox-studio-base:dev"
	session   = "spike"
	pause     = 10 * time.Second
)

// counter writes its PID once and a tick per second, to a file and to the terminal.
const counter = `bash -c 'echo $$ > /tmp/spike.pid; i=0; while :; do i=$((i+1)); echo $i > /tmp/spike.count; printf "tick %d\n" $i; sleep 1; done'` + "\r"

func main() {
	agent := flag.String("agent", "", "absolute path to a Linux amd64 studio-agent binary")
	scratch := flag.String("scratch", "", "existing parent directory for private state")
	flag.Parse()
	if !filepath.IsAbs(*agent) || *scratch == "" {
		fmt.Fprintln(os.Stderr, "usage: suspend -agent /abs/studio-agent -scratch DIR")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := run(ctx, *agent, *scratch); err != nil {
		fmt.Fprintln(os.Stderr, "FAIL:", err)
		os.Exit(1)
	}
	fmt.Println("PASS")
}

func run(ctx context.Context, agentPath, scratch string) (retErr error) {
	root, err := os.MkdirTemp(scratch, "ss-suspend-")
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
	env, err := st.CreateEnvironment(ctx, "suspend-probe")
	if err != nil {
		return err
	}
	rt := runtime.New(runtime.Options{Image: baseImage, GuestDir: p.Guest()})
	before, err := rt.Statuses(ctx, sandboxes.VMPrefix)
	if err != nil {
		return err
	}
	hub := agentchan.NewHub(log)
	mgr := &sandboxes.Manager{
		Store: st, Runtime: rt, Hub: hub, Egress: &deadEgress{},
		CA: &ca.Authority{Store: st, Sealer: sealer}, Secrets: noSecrets{}, Paths: p, Log: log,
	}
	hub.OnConnect = mgr.Configure

	var created []string
	defer func() {
		cctx, ccancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Minute)
		defer ccancel()
		for _, id := range created {
			if err := mgr.Delete(cctx, env.ID, id); err != nil && !errors.Is(err, store.ErrNotFound) {
				retErr = errors.Join(retErr, fmt.Errorf("delete %s: %w", id, err))
			}
		}
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

	sb, err := mgr.Create(ctx, env.ID, sandboxes.CreateRequest{Name: "spike-suspend"})
	if sb.ID != "" {
		created = append(created, sb.ID)
	}
	if err != nil {
		return fmt.Errorf("create: %w", err)
	}
	if err := ready(ctx, mgr, env.ID, sb.ID); err != nil {
		return err
	}

	term, err := mgr.OpenTerminal(ctx, env.ID, sb.ID, session, 80, 24)
	if err != nil {
		return fmt.Errorf("open terminal: %w", err)
	}
	if _, err := term.Write([]byte(counter)); err != nil {
		return err
	}
	termDone := make(chan error, 1)
	go func() { _, err := io.Copy(io.Discard, term); termDone <- err }()
	if err := waitCount(ctx, rt, st, sb.Sandbox, 3); err != nil {
		return err
	}
	pid1, count1, err := probe(ctx, rt, st, sb.Sandbox)
	if err != nil {
		return err
	}
	fmt.Printf("ok   counter running in tmux (pid %s, count %d)\n", pid1, count1)

	paused := time.Now()
	view, err := mgr.Suspend(ctx, env.ID, sb.ID)
	if err != nil {
		return fmt.Errorf("suspend: %w", err)
	}
	if view.Status != runtime.StatusSuspended || view.Agent != nil {
		return fmt.Errorf("suspended view: status %s, agent %+v", view.Status, view.Agent)
	}
	select {
	case <-termDone:
	case <-time.After(5 * time.Second):
		return errors.New("terminal stream survived suspend")
	}
	term.Close()
	fmt.Printf("ok   suspended in %s, terminal stream ended\n", time.Since(paused).Round(time.Millisecond))
	if _, err := mgr.Fork(ctx, env.ID, sb.ID, "nope"); !errors.Is(err, sandboxes.ErrLifecycleState) {
		return fmt.Errorf("fork of a suspended sandbox: %v", err)
	}
	if _, err := mgr.Start(ctx, env.ID, sb.ID); !errors.Is(err, sandboxes.ErrLifecycleState) {
		return fmt.Errorf("start of a suspended sandbox: %v", err)
	}
	ectx, ecancel := context.WithTimeout(ctx, 20*time.Second)
	_, err = sh(ectx, rt, st, sb.Sandbox, "cat /tmp/spike.count")
	ecancel()
	if err == nil {
		return errors.New("a guest command ran while the sandbox was suspended")
	}
	fmt.Println("ok   fork, start and exec refused while suspended")

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Until(paused.Add(pause))):
	}
	view, err = mgr.Resume(ctx, env.ID, sb.ID)
	if err != nil {
		return fmt.Errorf("resume: %w", err)
	}
	if view.Status != runtime.StatusRunning {
		return fmt.Errorf("resumed sandbox is %s", view.Status)
	}
	if err := ready(ctx, mgr, env.ID, sb.ID); err != nil {
		return err
	}
	frozen := time.Since(paused)
	pid2, count2, err := probe(ctx, rt, st, sb.Sandbox)
	if err != nil {
		return err
	}
	if pid2 != pid1 {
		return fmt.Errorf("counter pid changed from %s to %s", pid1, pid2)
	}
	if count2 < count1 || float64(count2-count1) > frozen.Seconds()-pause.Seconds()/2 {
		return fmt.Errorf("count went from %d to %d over %s: the paused time was counted", count1, count2, frozen.Round(time.Second))
	}
	if err := waitCount(ctx, rt, st, sb.Sandbox, count2+2); err != nil {
		return fmt.Errorf("counter did not continue: %w", err)
	}
	fmt.Printf("ok   resumed after %s: same pid, count %d -> %d, still counting\n", frozen.Round(time.Second), count1, count2)

	sessions, err := mgr.Terminals(ctx, env.ID, sb.ID)
	if err != nil {
		return err
	}
	found := false
	for _, s := range sessions {
		found = found || s.Name == session
	}
	if !found {
		return fmt.Errorf("tmux session %q is gone after resume: %+v", session, sessions)
	}
	if err := reattach(ctx, mgr, env.ID, sb.ID); err != nil {
		return err
	}
	fmt.Println("ok   terminal reattached mid-session")

	if _, err := mgr.Suspend(ctx, env.ID, sb.ID); err != nil {
		return fmt.Errorf("second suspend: %w", err)
	}
	view, err = mgr.Stop(ctx, env.ID, sb.ID)
	if err != nil {
		return fmt.Errorf("stop while suspended: %w", err)
	}
	if view.Status != runtime.StatusStopped {
		return fmt.Errorf("stopped suspended sandbox is %s", view.Status)
	}
	fmt.Println("ok   stop of a suspended sandbox")
	return nil
}

// reattach opens the session again and waits for a fresh tick.
func reattach(ctx context.Context, mgr *sandboxes.Manager, envID, id string) error {
	term, err := mgr.OpenTerminal(ctx, envID, id, session, 80, 24)
	if err != nil {
		return fmt.Errorf("reattach: %w", err)
	}
	defer term.Close()
	seen := make(chan struct{})
	go func() {
		var out bytes.Buffer
		buf := make([]byte, 4096)
		for {
			n, err := term.Read(buf)
			out.Write(buf[:n])
			if strings.Contains(out.String(), "tick ") {
				close(seen)
				return
			}
			if err != nil {
				return
			}
		}
	}()
	select {
	case <-seen:
		return nil
	case <-time.After(15 * time.Second):
		return errors.New("reattached terminal shows no ticks")
	}
}

func probe(ctx context.Context, rt *runtime.Runtime, st *store.Store, sb store.Sandbox) (string, int, error) {
	out, err := sh(ctx, rt, st, sb, `pid=$(cat /tmp/spike.pid); kill -0 "$pid" && echo "$pid $(cat /tmp/spike.count)"`)
	if err != nil {
		return "", 0, err
	}
	pid, count, ok := strings.Cut(strings.TrimSpace(out), " ")
	n, err := strconv.Atoi(count)
	if !ok || err != nil {
		return "", 0, fmt.Errorf("probe output %q", out)
	}
	return pid, n, nil
}

func waitCount(ctx context.Context, rt *runtime.Runtime, st *store.Store, sb store.Sandbox, min int) error {
	deadline := time.Now().Add(30 * time.Second)
	for {
		_, n, err := probe(ctx, rt, st, sb)
		if err == nil && n >= min {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("count did not reach %d (last %d, %v)", min, n, err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func ready(ctx context.Context, mgr *sandboxes.Manager, envID, id string) error {
	if err := mgr.WaitReady(ctx, id, 60*time.Second); err != nil {
		return err
	}
	return mgr.ConfigureGuest(ctx, envID, id)
}

// sh runs a bash script as root in the sandbox's current VM and returns its stdout.
func sh(ctx context.Context, rt *runtime.Runtime, st *store.Store, sb store.Sandbox, script string) (string, error) {
	sb, err := st.Sandbox(ctx, sb.EnvironmentID, sb.ID)
	if err != nil {
		return "", err
	}
	var out lockedBuffer
	stderr := make(chan runtime.RunOutput, 64)
	result, err := rt.Run(ctx, runtime.OwnedVM{Name: sandboxes.VMName(sb), Labels: labels(sb)}, runtime.RunCommand{
		Path: "/bin/bash", Args: []string{"-c", script}, User: "root", Cwd: "/root",
		Env:     map[string]string{"HOME": "/root", "PATH": "/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"},
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
	return map[string]string{
		"studio.sandbox-id":     sb.ID,
		"studio.environment-id": sb.EnvironmentID,
		"studio.sandbox-name":   sb.Name,
	}
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
	name := "SS_SUSPEND_PROBE_" + strings.ToUpper(sb.ID)
	if os.Getenv(name) == "" {
		secret := make([]byte, 16)
		if _, err := rand.Read(secret); err != nil {
			return runtime.Egress{}, err
		}
		os.Setenv(name, hex.EncodeToString(secret))
	}
	return runtime.Egress{Nameserver: "127.0.0.1:9", Proxy: "127.0.0.1:9", User: "suspend-probe", PasswordEnv: name}, nil
}

func (e *deadEgress) Detach(id string) { os.Unsetenv("SS_SUSPEND_PROBE_" + strings.ToUpper(id)) }

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
