// Package runtime wraps the microsandbox Go SDK. Nothing else in Studio imports the SDK,
// so upgrades and API churn stay contained here.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"net"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/templateimage"
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
	MaxMemoryMiB uint32
	WorkspaceMiB uint32
	DockerMiB    uint32
	// Image selects a private, environment-scoped image source for internal
	// builder VMs. It is host-side SDK configuration and is never serialized.
	Image  *ImageSource `json:"-"`
	Egress Egress
}

// ImageSource contains host-side credentials and a private immutable image
// reference. Its fields must never be serialized into guest or job data.
type ImageSource struct {
	Reference string `json:"-"`
	Username  string `json:"-"`
	Password  string `json:"-"`
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
type Runtime struct {
	opts Options

	runMu      sync.Mutex
	runSlot    *runTask
	runBackend runBackend
	runLimits  runLimits
}

// guestShutdownExecTimeout allows the guest's three sequential 20-second service-stop
// windows, its 5-second margin, and time for msb to start and return the command.
const guestShutdownExecTimeout = 70 * time.Second

// Ensure installs the msb runtime if needed. It is idempotent.
func Ensure(ctx context.Context) error {
	_, err := msb.EnsureRuntime(ctx, msb.RuntimeConfig{}, msb.InstallOptions{})
	return err
}

// New returns a runtime with the given options.
func New(opts Options) *Runtime { return &Runtime{opts: opts} }

// BaseReference returns the configured OCI image reference used for new VMs.
func (r *Runtime) BaseReference() string {
	if r == nil {
		return ""
	}
	return r.opts.Image
}

// TargetPlatform returns the supported Linux host platform, or an empty string
// when this host architecture is not supported by the runtime.
func (r *Runtime) TargetPlatform() string {
	if r == nil {
		return ""
	}
	switch goruntime.GOARCH {
	case "amd64", "arm64":
		return "linux/" + goruntime.GOARCH
	default:
		return ""
	}
}

// Create creates and boots a detached sandbox VM. agentSocket is the host Unix socket
// that the guest agent reaches over vsock.
func (r *Runtime) Create(ctx context.Context, name string, spec Spec, agentSocket string, labels map[string]string) error {
	imageOptions, err := privateImageSourceOptions(spec.Image)
	if err != nil {
		return fmt.Errorf("create sandbox: %w", err)
	}
	maxMemoryMiB := spec.MaxMemoryMiB
	if maxMemoryMiB == 0 {
		maxMemoryMiB = spec.MemoryMiB
	}
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
	options := []msb.SandboxOption{
		msb.WithImage(r.opts.Image),
		msb.WithCPUs(spec.CPUs),
		msb.WithMemory(spec.MemoryMiB),
		msb.WithMaxMemory(maxMemoryMiB),
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
	}
	options = append(options, imageOptions...)
	sb, err := msb.CreateSandbox(ctx, name, options...)
	if err != nil {
		return fmt.Errorf("create sandbox: %w", err)
	}
	return errors.Join(boot(ctx, sb), sb.Detach(ctx))
}

// privateImageSourceOptions validates and translates private registry
// credentials into SDK host options. It never puts credentials in guest env,
// vsock, or an error message.
func privateImageSourceOptions(source *ImageSource) ([]msb.SandboxOption, error) {
	if source == nil {
		return nil, nil
	}
	if err := validatePrivateImageSource(*source); err != nil {
		return nil, err
	}
	return []msb.SandboxOption{
		msb.WithImage(source.Reference),
		msb.WithRegistryAuth(msb.RegistryAuth{Username: source.Username, Password: source.Password}),
		msb.WithRegistryInsecure(),
		msb.WithPullPolicy(msb.PullPolicyIfMissing),
	}, nil
}

func validatePrivateImageSource(source ImageSource) error {
	if source.Password == "" {
		return errors.New("private image source credentials are required")
	}
	if !validImageSourceID(source.Username) {
		return errors.New("private image source has invalid environment scope")
	}
	if strings.TrimSpace(source.Reference) != source.Reference || strings.ContainsAny(source.Reference, "\\?# \t\r\n") {
		return errors.New("private image reference is not canonical")
	}
	authority, imagePath, ok := strings.Cut(source.Reference, "/")
	if !ok {
		return errors.New("private image reference is not canonical")
	}
	host, port, err := net.SplitHostPort(authority)
	if err != nil {
		return errors.New("private image reference is not a loopback registry address")
	}
	address := net.ParseIP(host)
	if address == nil || address.To4() == nil || !address.IsLoopback() || host != address.String() {
		return errors.New("private image reference is not a canonical IPv4 loopback address")
	}
	portNumber, err := strconv.ParseUint(port, 10, 16)
	if err != nil || portNumber == 0 || strconv.FormatUint(portNumber, 10) != port {
		return errors.New("private image reference has an invalid registry port")
	}

	parts := strings.Split(imagePath, "/")
	if len(parts) != 3 || parts[0] != "studio" || !validImageSourceID(parts[1]) || parts[1] != source.Username {
		return errors.New("private image reference has invalid environment scope")
	}
	repository, digest, ok := strings.Cut(parts[2], "@")
	if !ok || (repository != "base" && !validImageSourceID(repository)) || !templateimage.ValidDigest(digest) {
		return errors.New("private image reference must be a digest-pinned Studio image")
	}
	return nil
}

func validImageSourceID(id string) bool {
	if len(id) != 13 {
		return false
	}
	for i := 0; i < len(id); i++ {
		if (id[i] < 'a' || id[i] > 'z') && (id[i] < '2' || id[i] > '7') {
			return false
		}
	}
	return true
}

// Start boots a stopped sandbox in detached mode.
func (r *Runtime) Start(ctx context.Context, name string) error {
	sb, err := msb.StartSandboxDetached(ctx, name)
	if err != nil {
		return err
	}
	return errors.Join(boot(ctx, sb), sb.Detach(ctx))
}

// agentPath is the agent's path in the guest, under Options.GuestDir.
const agentPath = "/opt/studio/bin/studio-agent"

// boot starts the guest's services. Sandboxes have no init system, so that microsandbox
// can freeze all of their processes; see guest.Boot.
func boot(ctx context.Context, sb *msb.Sandbox) error {
	out, err := sb.Exec(ctx, agentPath, []string{"boot"}, msb.WithExecUser("root"), msb.WithExecTimeout(30*time.Second))
	if err != nil {
		return fmt.Errorf("boot guest: %w", err)
	}
	if !out.Success() {
		return fmt.Errorf("boot guest: exit code %d: %s", out.ExitCode(), strings.TrimSpace(out.Stderr()))
	}
	return nil
}

// Stop shuts a sandbox down gracefully: the guest stops Docker and its containers before
// the VM goes down.
func (r *Runtime) Stop(ctx context.Context, name string) error {
	return r.stop(ctx, name, true)
}

func (r *Runtime) stop(ctx context.Context, name string, graceful bool) error {
	h, err := msb.GetSandbox(ctx, name)
	if err != nil {
		if msb.IsKind(err, msb.ErrSandboxNotFound) {
			return nil
		}
		return err
	}
	s := Status(h.Status())
	if s == StatusStopped || s == StatusCrashed || s == StatusCreated {
		return nil
	}
	var shutdownErr error
	if graceful && s == StatusRunning {
		// Best effort: report a graceful shutdown failure, but still stop the VM below.
		shutdownErr = shutdownGuest(ctx, h)
	}
	stopErr := h.Stop(ctx, msb.WithStopTimeout(30*time.Second))
	return errors.Join(shutdownErr, stopErr)
}

func shutdownGuest(ctx context.Context, h *msb.SandboxHandle) error {
	sb, err := h.Connect(ctx)
	if err != nil {
		return fmt.Errorf("connect for graceful guest shutdown: %w", err)
	}
	if sb == nil {
		return errors.New("connect for graceful guest shutdown: SDK returned an empty sandbox")
	}
	out, execErr := sb.Exec(ctx, agentPath, []string{"shutdown"},
		msb.WithExecUser("root"), msb.WithExecTimeout(guestShutdownExecTimeout))
	detachErr := sb.Detach(ctx)
	if execErr != nil {
		execErr = fmt.Errorf("run graceful guest shutdown: %w", execErr)
	} else if out == nil {
		execErr = errors.New("run graceful guest shutdown: SDK returned no command result")
	} else if !out.Success() {
		execErr = fmt.Errorf("run graceful guest shutdown: exit code %d: %s", out.ExitCode(), strings.TrimSpace(out.Stderr()))
	}
	if detachErr != nil {
		detachErr = fmt.Errorf("detach after graceful guest shutdown: %w", detachErr)
	}
	return errors.Join(execErr, detachErr)
}

// Remove stops and deletes a sandbox and its owned disks. Missing sandboxes are fine.
func (r *Runtime) Remove(ctx context.Context, name string) error {
	if err := r.stop(ctx, name, false); err != nil {
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
