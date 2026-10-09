//go:build linux

// template-base is a disposable live qualification for the configured base image and
// the guest configuration ACK gate. It uses a private catalog and one uniquely named VM.
package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base32"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/lukaskoebe/sandbox-studio/internal/agentchan"
	"github.com/lukaskoebe/sandbox-studio/internal/paths"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/templateimage"
)

const (
	baseImage        = "sandbox-studio-base:dev"
	cleanupTimeout   = 25 * time.Second
	readyTimeout     = 90 * time.Second
	dummyPlaceholder = "studio-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	loginShellCheck  = `test "$TOKEN" = "studio-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`
)

func main() {
	agent := flag.String("agent", "", "absolute path to the compiled Linux studio-agent binary")
	flag.Parse()
	if *agent == "" || !filepath.IsAbs(*agent) {
		fmt.Fprintln(os.Stderr, "-agent must name an absolute path to the compiled Linux studio-agent binary")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := qualify(ctx, *agent); err != nil {
		fmt.Fprintln(os.Stderr, "FAIL template-base qualification:", err)
		os.Exit(1)
	}
}

func qualify(ctx context.Context, agentPath string) (err error) {
	root, err := os.MkdirTemp("/tmp", "ss-tb-")
	if err != nil {
		return fmt.Errorf("create private qualification directory: %w", err)
	}
	removeRoot := true
	var st *store.Store
	var hub *agentchan.Hub
	var egress *probeEgress
	var rt *runtime.Runtime
	var sb store.Sandbox
	var runtimeCreateAttempted bool
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()

		var cleanupErr error
		if sb.ID != "" {
			if hub != nil {
				hub.Close(sb.ID)
			}
			if runtimeCreateAttempted {
				cleanupErr = errors.Join(cleanupErr, removeOwnedVM(cleanupCtx, rt, sb))
			}
			if egress != nil {
				egress.Detach(sb.ID)
			}
			if cleanupErr == nil && st != nil {
				cleanupErr = errors.Join(cleanupErr, st.DeleteSandbox(cleanupCtx, sb.EnvironmentID, sb.ID))
			}
		}
		if st != nil {
			cleanupErr = errors.Join(cleanupErr, st.Close())
		}
		if cleanupErr != nil {
			removeRoot = false
			err = errors.Join(err, fmt.Errorf("bounded cleanup: %w", cleanupErr))
		}
		if removeRoot {
			if removeErr := os.RemoveAll(root); removeErr != nil {
				removeRoot = false
				err = errors.Join(err, fmt.Errorf("remove private qualification directory: %w", removeErr))
			}
		}
		if !removeRoot {
			fmt.Fprintln(os.Stderr, "qualification data retained for recovery:", root)
		} else if err == nil {
			fmt.Println("PASS owned VM cleanup and private-data removal")
		}
	}()

	suffix, err := randomBase32(5)
	if err != nil {
		return fmt.Errorf("generate private names: %w", err)
	}
	privatePaths := paths.Paths{Data: filepath.Join(root, "state")}
	if err := privatePaths.Ensure(); err != nil {
		return fmt.Errorf("create private state paths: %w", err)
	}
	if err := installAgent(agentPath, filepath.Join(privatePaths.Guest(), "bin", "studio-agent")); err != nil {
		return err
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	rt = runtime.New(runtime.Options{Image: baseImage})
	prewarmBefore, err := rt.Statuses(ctx, "ss-prewarm-")
	if err != nil {
		return fmt.Errorf("snapshot prewarm VMs before preparation: %w", err)
	}
	base, prepareErr := rt.PrepareTemplateBase(ctx)
	prewarmAfter, statusErr := rt.Statuses(ctx, "ss-prewarm-")
	var lingering []string
	for name, state := range prewarmAfter {
		if _, existed := prewarmBefore[name]; !existed {
			lingering = append(lingering, fmt.Sprintf("%s (%s)", name, state))
		}
	}
	sort.Strings(lingering)
	var prewarmErr error
	if statusErr != nil {
		prewarmErr = fmt.Errorf("check prewarm cleanup: %w", statusErr)
	} else if len(lingering) != 0 {
		prewarmErr = fmt.Errorf("new prewarm VMs remain after preparation: %s", strings.Join(lingering, ", "))
	}
	if prepareErr != nil || prewarmErr != nil {
		var wrappedPrepareErr error
		if prepareErr != nil {
			wrappedPrepareErr = fmt.Errorf("prepare configured base: %w", prepareErr)
		}
		return errors.Join(wrappedPrepareErr, prewarmErr)
	}
	if !templateimage.ValidDigest(base.Digest) || base.OS != "linux" || base.Architecture != "amd64" || len(base.Layers) == 0 {
		return fmt.Errorf("configured base inspection is incomplete: digest-valid=%t platform=%s/%s layers=%d",
			templateimage.ValidDigest(base.Digest), base.OS, base.Architecture, len(base.Layers))
	}
	fmt.Printf("PASS configured base: digest=%s platform=%s/%s layers=%d; no new prewarm VMs remain\n",
		base.Digest, base.OS, base.Architecture, len(base.Layers))

	st, err = store.Open(ctx, privatePaths.DB())
	if err != nil {
		return fmt.Errorf("open private catalog: %w", err)
	}
	env, err := st.CreateEnvironment(ctx, "template-base-probe-"+suffix)
	if err != nil {
		return fmt.Errorf("create private environment: %w", err)
	}
	caPEM, err := newProbeCACertificate()
	if err != nil {
		return fmt.Errorf("create in-memory probe CA: %w", err)
	}
	hub = agentchan.NewHub(logger)
	rt = runtime.New(runtime.Options{Image: baseImage, GuestDir: privatePaths.Guest()})
	manager := &sandboxes.Manager{
		Store: st, Runtime: rt, Hub: hub, CA: probeCA{cert: caPEM},
		Secrets: probeSecrets{env: map[string]string{"TOKEN": dummyPlaceholder}},
		Paths:   privatePaths, Log: logger,
	}

	// Record the unique identity before creating the VM. This keeps cleanup scoped to
	// this one name even if SDK creation or guest boot returns an error.
	sb, err = st.CreateSandbox(ctx, store.Sandbox{
		EnvironmentID: env.ID,
		Name:          "template-base-probe-" + suffix,
		CPUs:          1,
		MemoryMiB:     512,
		MaxMemoryMiB:  1024,
		WorkspaceMiB:  1024,
		DockerMiB:     1024,
	})
	if err != nil {
		return fmt.Errorf("create private sandbox catalog row: %w", err)
	}
	egress = &probeEgress{}
	network, err := egress.Attach(sb)
	if err != nil {
		return fmt.Errorf("prepare fail-closed probe egress: %w", err)
	}
	vmName := sandboxes.VMName(sb)
	if err := hub.Listen(sb.ID, privatePaths.AgentSocket(sb.ID)); err != nil {
		return fmt.Errorf("listen for probe guest agent: %w", err)
	}
	runtimeCreateAttempted = true
	labels := map[string]string{
		"studio.sandbox-id":     sb.ID,
		"studio.environment-id": env.ID,
		"studio.sandbox-name":   sb.Name,
	}
	spec := runtime.Spec{
		CPUs: 1, MemoryMiB: 512, MaxMemoryMiB: 1024,
		WorkspaceMiB: 1024, DockerMiB: 1024, Egress: network,
	}
	if err := rt.Create(ctx, vmName, spec, privatePaths.AgentSocket(sb.ID), labels); err != nil {
		return fmt.Errorf("create probe VM: %w", err)
	}
	if err := manager.WaitReady(ctx, sb.ID, readyTimeout); err != nil {
		return fmt.Errorf("wait for probe guest agent: %w", err)
	}
	if err := manager.ConfigureGuest(ctx, env.ID, sb.ID); err != nil {
		return fmt.Errorf("wait for guest configuration ACK: %w", err)
	}
	fmt.Println("PASS guest agent connected and acknowledged CA plus placeholder configuration")

	if err := inspectAndCheckGuest(ctx, vmName, sb, caPEM); err != nil {
		return err
	}
	fmt.Println("PASS SDK memory limits, guest CA file, environment file, and login-shell placeholder")
	return nil
}

func installAgent(source, destination string) error {
	info, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("stat -agent binary: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return errors.New("-agent must be an executable regular file")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return fmt.Errorf("create private guest binary directory: %w", err)
	}
	in, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open -agent binary: %w", err)
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return fmt.Errorf("create private guest agent copy: %w", err)
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return fmt.Errorf("copy -agent binary into private guest directory: %w", err)
	}
	return nil
}

func inspectAndCheckGuest(ctx context.Context, vmName string, sb store.Sandbox, caPEM []byte) (err error) {
	handle, err := msb.GetSandbox(ctx, vmName)
	if err != nil {
		return fmt.Errorf("inspect created SDK VM: %w", err)
	}
	if handle == nil {
		return errors.New("inspect created SDK VM: SDK returned no sandbox handle")
	}
	config, err := handle.Config()
	if err != nil {
		return fmt.Errorf("read SDK VM config: %w", err)
	}
	if config == nil {
		return errors.New("read SDK VM config: SDK returned no config")
	}
	if config.MemoryMiB != 512 || config.MaxMemoryMiB != 1024 || !ownsVM(config.Labels, sb) {
		return fmt.Errorf("SDK VM config mismatch: memory=%d maxMemory=%d ownership=%t",
			config.MemoryMiB, config.MaxMemoryMiB, ownsVM(config.Labels, sb))
	}

	vm, err := handle.Connect(ctx)
	if err != nil {
		return fmt.Errorf("connect for read-only guest checks: %w", err)
	}
	if vm == nil {
		return errors.New("connect for read-only guest checks: SDK returned no live sandbox")
	}
	defer func() {
		detachCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		err = errors.Join(err, vm.Detach(detachCtx))
	}()

	caOut, err := vm.Exec(ctx, "/bin/cat", []string{"/etc/sandbox-studio/ca.crt"}, msb.WithExecTimeout(10*time.Second))
	if err != nil {
		return fmt.Errorf("read guest CA file: %w", err)
	}
	if caOut == nil {
		return errors.New("read guest CA file: SDK returned no command output")
	}
	if !caOut.Success() || !bytes.Equal(caOut.StdoutBytes(), caPEM) {
		return fmt.Errorf("guest CA file does not match the in-memory probe CA (exit=%d)", caOut.ExitCode())
	}

	envOut, err := vm.Exec(ctx, "/bin/cat", []string{"/etc/sandbox-studio/env"}, msb.WithExecTimeout(10*time.Second))
	if err != nil {
		return fmt.Errorf("read guest environment file: %w", err)
	}
	if envOut == nil {
		return errors.New("read guest environment file: SDK returned no command output")
	}
	wantEnv := []byte("TOKEN=" + dummyPlaceholder + "\n")
	if !envOut.Success() || !bytes.Equal(envOut.StdoutBytes(), wantEnv) {
		return fmt.Errorf("guest environment file does not contain the expected probe placeholder (exit=%d)", envOut.ExitCode())
	}

	loginOut, err := vm.Exec(ctx, "/bin/bash", []string{"--login", "-c", loginShellCheck}, msb.WithExecTimeout(15*time.Second))
	if err != nil {
		return fmt.Errorf("run login-shell placeholder check: %w", err)
	}
	if loginOut == nil {
		return errors.New("run login-shell placeholder check: SDK returned no command output")
	}
	if !loginOut.Success() {
		return fmt.Errorf("login shell did not export the probe placeholder (exit=%d)", loginOut.ExitCode())
	}
	return nil
}

func removeOwnedVM(ctx context.Context, rt *runtime.Runtime, sb store.Sandbox) error {
	name := sandboxes.VMName(sb)
	status, err := rt.Status(ctx, name)
	if err != nil {
		return fmt.Errorf("inspect owned VM before cleanup: %w", err)
	}
	if status == runtime.StatusAbsent {
		return nil
	}
	handle, err := msb.GetSandbox(ctx, name)
	if err != nil {
		return fmt.Errorf("read VM ownership before cleanup: %w", err)
	}
	if handle == nil {
		return fmt.Errorf("read VM ownership before cleanup: SDK returned no handle for %q", name)
	}
	config, err := handle.Config()
	if err != nil {
		return fmt.Errorf("read VM config before cleanup: %w", err)
	}
	if config == nil {
		return fmt.Errorf("read VM config before cleanup: SDK returned no config for %q", name)
	}
	if handle.Name() != name || !ownsVM(config.Labels, sb) {
		return fmt.Errorf("refusing cleanup because VM %q ownership labels do not match the private catalog row", name)
	}
	if err := rt.Remove(ctx, name); err != nil {
		return fmt.Errorf("remove owned VM %q: %w", name, err)
	}
	status, err = rt.Status(ctx, name)
	if err != nil {
		return fmt.Errorf("verify owned VM removal: %w", err)
	}
	if status != runtime.StatusAbsent {
		return fmt.Errorf("owned VM %q remains in state %q after cleanup", name, status)
	}
	return nil
}

func ownsVM(labels map[string]string, sb store.Sandbox) bool {
	return labels["studio.sandbox-id"] == sb.ID &&
		labels["studio.environment-id"] == sb.EnvironmentID &&
		labels["studio.sandbox-name"] == sb.Name
}

func newProbeCACertificate() ([]byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "Sandbox Studio disposable template probe CA"},
		NotBefore:    now.Add(-time.Minute), NotAfter: now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true, IsCA: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
}

func randomBase32(n int) (string, error) {
	data := make([]byte, n)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(data)), nil
}

type probeCA struct{ cert []byte }

func (c probeCA) CertPEM(context.Context, string) ([]byte, error) {
	return append([]byte(nil), c.cert...), nil
}

type probeSecrets struct{ env map[string]string }

func (s probeSecrets) Env(context.Context, string) (map[string]string, error) {
	copy := make(map[string]string, len(s.env))
	for name, value := range s.env {
		copy[name] = value
	}
	return copy, nil
}

type probeEgress struct {
	passwordEnv string
	password    string
	sandboxID   string
}

func (e *probeEgress) Attach(sb store.Sandbox) (runtime.Egress, error) {
	if e.passwordEnv != "" {
		return runtime.Egress{}, errors.New("probe egress was already attached")
	}
	password, err := randomBase32(20)
	if err != nil {
		return runtime.Egress{}, err
	}
	passwordEnv := "SS_TEMPLATE_PROBE_GW_" + strings.ToUpper(sb.ID)
	if _, exists := os.LookupEnv(passwordEnv); exists {
		return runtime.Egress{}, errors.New("unique probe gateway environment variable already exists")
	}
	if err := os.Setenv(passwordEnv, password); err != nil {
		return runtime.Egress{}, fmt.Errorf("set host-only probe gateway credential: %w", err)
	}
	e.passwordEnv, e.password, e.sandboxID = passwordEnv, password, sb.ID
	return runtime.Egress{
		Nameserver:  "127.0.0.1:9",
		Proxy:       "127.0.0.1:9",
		User:        "template-base-probe",
		PasswordEnv: passwordEnv,
	}, nil
}

func (e *probeEgress) Detach(sandboxID string) {
	if sandboxID == e.sandboxID && e.passwordEnv != "" && os.Getenv(e.passwordEnv) == e.password {
		_ = os.Unsetenv(e.passwordEnv)
	}
}
