// Package runtime wraps the microsandbox Go SDK. Nothing else in Studio imports the SDK,
// so upgrades and API churn stay contained here.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// Status is the runtime state of a sandbox VM.
type Status string

// Runtime states. StatusAbsent means no VM exists for the name.
const (
	StatusAbsent   Status = "absent"
	StatusCreated  Status = "created"
	StatusStarting Status = "starting"
	StatusRunning  Status = "running"
	StatusDraining Status = "draining"
	StatusPaused   Status = "paused"
	StatusStopped  Status = "stopped"
	StatusCrashed  Status = "crashed"
)

// Options configure every sandbox Studio creates.
type Options struct {
	Image    string // OCI reference of the base image
	GuestDir string // host directory mounted read-only at /opt/studio
}

// Spec is the per-sandbox configuration.
type Spec struct {
	CPUs         uint8
	MemoryMiB    uint32
	WorkspaceMiB uint32
	DockerMiB    uint32
	Egress       Egress
}

// Egress routes a sandbox's traffic through Studio: DNS to its own resolver, and every
// TCP connection through the SOCKS5 gateway, which asks the network rules.
type Egress struct {
	Nameserver  string // host address of the sandbox's resolver
	Proxy       string // host address of the gateway
	User        string // the gateway user name
	PasswordEnv string // the Studio environment variable holding the gateway password
}

// Runtime creates and controls sandbox VMs.
type Runtime struct{ opts Options }

// Ensure installs the msb runtime if needed. It is idempotent.
func Ensure(ctx context.Context) error {
	_, err := msb.EnsureRuntime(ctx, msb.RuntimeConfig{}, msb.InstallOptions{})
	return err
}

// New returns a runtime with the given options.
func New(opts Options) *Runtime { return &Runtime{opts: opts} }

// Create creates and boots a detached sandbox VM. agentSocket is the host Unix socket
// that the guest agent reaches over vsock.
func (r *Runtime) Create(ctx context.Context, name string, spec Spec, agentSocket string, labels map[string]string) error {
	// Only DNS to the host and TCP to public addresses leave the guest, and the TCP goes
	// through the gateway. UDP and ICMP would bypass it; QUIC falls back to TCP.
	network := &msb.NetworkConfig{
		Rules: []msb.PolicyRule{
			msb.Rule.AllowDNS(),
			{Action: msb.PolicyActionAllow, Direction: msb.PolicyDirectionEgress, Destination: "public", Protocols: []msb.PolicyProtocol{msb.PolicyProtocolTCP}},
		},
		DefaultEgress:  msb.PolicyActionDeny,
		DefaultIngress: msb.PolicyActionAllow,
		DNS:            &msb.DNSConfig{Nameservers: []string{spec.Egress.Nameserver}},
	}
	proxy := msb.SOCKS5Proxy(spec.Egress.Proxy).Credentials(spec.Egress.User, msb.SecretSourceEnv(spec.Egress.PasswordEnv))
	sb, err := msb.CreateSandbox(ctx, name,
		msb.WithImage(r.opts.Image),
		msb.WithInit(msb.Init.Auto()),
		msb.WithCPUs(spec.CPUs),
		msb.WithMemory(spec.MemoryMiB),
		msb.WithHostname(hostname(labels["studio.sandbox-name"])),
		msb.WithMounts(map[string]msb.MountConfig{
			"/workspace":      msb.Mount.Owned(msb.OwnedVolumeOptions{Kind: msb.VolumeKindDisk, SizeMiB: spec.WorkspaceMiB}),
			"/var/lib/docker": msb.Mount.Owned(msb.OwnedVolumeOptions{Kind: msb.VolumeKindDisk, SizeMiB: spec.DockerMiB}),
			"/opt/studio":     msb.Mount.Bind(r.opts.GuestDir, msb.MountOptions{Readonly: true}),
		}),
		msb.WithVsock(msb.VsockRoute{HostSocket: agentSocket, Port: agentproto.VsockPort}),
		msb.WithNetwork(network),
		msb.WithProxy(proxy),
		msb.WithLabels(labels),
		msb.WithDetached(),
	)
	if err != nil {
		return fmt.Errorf("create sandbox: %w", err)
	}
	return sb.Detach(ctx)
}

// Start boots a stopped sandbox in detached mode.
func (r *Runtime) Start(ctx context.Context, name string) error {
	sb, err := msb.StartSandboxDetached(ctx, name)
	if err != nil {
		return err
	}
	return sb.Detach(ctx)
}

// Stop shuts a sandbox down gracefully.
func (r *Runtime) Stop(ctx context.Context, name string) error {
	h, err := msb.GetSandbox(ctx, name)
	if err != nil {
		if msb.IsKind(err, msb.ErrSandboxNotFound) {
			return nil
		}
		return err
	}
	if s := Status(h.Status()); s == StatusStopped || s == StatusCrashed || s == StatusCreated {
		return nil
	}
	return h.Stop(ctx, msb.WithStopTimeout(30*time.Second))
}

// Remove stops and deletes a sandbox and its owned disks. Missing sandboxes are fine.
func (r *Runtime) Remove(ctx context.Context, name string) error {
	if err := r.Stop(ctx, name); err != nil {
		return err
	}
	err := msb.RemoveSandbox(ctx, name)
	if err != nil && msb.IsKind(err, msb.ErrSandboxNotFound) {
		return nil
	}
	return err
}

// Status returns the runtime state of name.
func (r *Runtime) Status(ctx context.Context, name string) (Status, error) {
	h, err := msb.GetSandbox(ctx, name)
	if err != nil {
		if msb.IsKind(err, msb.ErrSandboxNotFound) {
			return StatusAbsent, nil
		}
		return "", err
	}
	return Status(h.Status()), nil
}

// Statuses returns the state of every sandbox whose name starts with prefix.
func (r *Runtime) Statuses(ctx context.Context, prefix string) (map[string]Status, error) {
	out := map[string]Status{}
	var cursor *string
	for {
		opts := []msb.SandboxListOption{msb.WithListLimit(100)}
		if cursor != nil {
			opts = append(opts, msb.WithListCursor(*cursor))
		}
		page, err := msb.ListSandboxesWith(ctx, opts...)
		if err != nil {
			return nil, err
		}
		for _, h := range page.Sandboxes {
			if strings.HasPrefix(h.Name(), prefix) {
				out[h.Name()] = Status(h.Status())
			}
		}
		if page.NextCursor == nil || *page.NextCursor == "" {
			return out, nil
		}
		cursor = page.NextCursor
	}
}

// IsRunning reports whether s is a state in which the guest can be reached.
func (s Status) IsRunning() bool { return s == StatusRunning || s == StatusStarting }

// ErrInvalidName is returned for sandbox names that can't be used as hostnames.
var ErrInvalidName = errors.New("sandbox names use lowercase letters, digits and dashes (max 40)")

// ValidName checks a user-facing sandbox name.
func ValidName(name string) error {
	if name == "" || len(name) > 40 || name[0] == '-' || name[len(name)-1] == '-' {
		return ErrInvalidName
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return ErrInvalidName
		}
	}
	return nil
}

func hostname(name string) string {
	if ValidName(name) != nil {
		return "sandbox"
	}
	return name
}
