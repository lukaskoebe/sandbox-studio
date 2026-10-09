package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/lukaskoebe/sandbox-studio/internal/templateimage"
)

const (
	developmentBaseReference = "sandbox-studio-base:dev"
	prewarmNamePrefix        = "ss-prewarm-"
	prewarmKindLabel         = "studio.kind"
	prewarmTokenLabel        = "studio.prewarm-token"
	prewarmKindValue         = "template-prewarm"
	prewarmCleanupTimeout    = 12 * time.Second
	prewarmStopTimeout       = 5 * time.Second
)

var (
	errPrewarmNotFound = errors.New("template base prewarm VM not found")
	repoComponentRE    = regexp.MustCompile(`^[a-z0-9]+(?:(?:[._]|__|[-]+)[a-z0-9]+)*$`)
)

type prewarmRequest struct {
	Image          string
	Name           string
	Token          string
	PullPolicy     string
	CPUs           uint8
	MemoryMiB      uint32
	MaxMemoryMiB   uint32
	MaxCPUs        uint8
	DefaultEgress  string
	DefaultIngress string
}

// These narrow interfaces keep template-base preparation testable without a live VM.
// The request intentionally has no fields for mounts, proxy, vsock, or guest environment.
type templateBaseEngine interface {
	createPrewarm(context.Context, prewarmRequest) (prewarmVM, error)
	inspectImage(context.Context, string) (inspectedBaseImage, error)
	lookupSandbox(context.Context, string) (prewarmRecord, error)
	removeSandbox(context.Context, string) error
}

type prewarmVM interface {
	id() string
	stop(context.Context) error
	close() error
	detach(context.Context) error
}

type prewarmRecord interface {
	name() string
	id() string
	status() string
	labels() (map[string]string, error)
	stop(context.Context) error
}

type inspectedBaseImage struct {
	ManifestDigest string
	OS             string
	Architecture   string
	LayerCount     uint
	Config         *templateimage.Config
	Layers         []inspectedBaseLayer
}

type inspectedBaseLayer struct {
	DiffID         string
	BlobDigest     string
	MediaType      string
	CompressedSize *int64
}

// PrepareTemplateBase prewarms and inspects only the configured base image. It never
// accepts browser or caller-supplied OCI metadata.
func (r *Runtime) PrepareTemplateBase(ctx context.Context) (templateimage.Base, error) {
	if r == nil {
		return templateimage.Base{}, errors.New("prepare template base: nil runtime")
	}
	return prepareTemplateBase(ctx, r.opts.Image, sdkTemplateBaseEngine{})
}

func prepareTemplateBase(ctx context.Context, reference string, engine templateBaseEngine) (templateimage.Base, error) {
	if ctx == nil {
		return templateimage.Base{}, errors.New("prepare template base: nil context")
	}
	if err := ctx.Err(); err != nil {
		return templateimage.Base{}, fmt.Errorf("prepare template base: %w", err)
	}
	if engine == nil {
		return templateimage.Base{}, errors.New("prepare template base: nil runtime engine")
	}
	pullPolicy, err := basePullPolicy(reference)
	if err != nil {
		return templateimage.Base{}, err
	}
	name, token, err := newPrewarmIdentity()
	if err != nil {
		return templateimage.Base{}, fmt.Errorf("prepare template base: generate prewarm identity: %w", err)
	}
	request := prewarmRequest{
		Image: reference, Name: name, Token: token, PullPolicy: pullPolicy,
		CPUs: 1, MemoryMiB: 512, MaxMemoryMiB: 512, MaxCPUs: 1,
		DefaultEgress: "deny", DefaultIngress: "deny",
	}

	vm, createErr := engine.createPrewarm(ctx, request)
	if createErr != nil || vm == nil {
		if createErr == nil {
			createErr = errors.New("SDK returned no prewarm VM")
		}
		cleanupErr := withPrewarmCleanupContext(ctx, func(cleanupCtx context.Context) error {
			if vm != nil {
				return cleanupCreatedPrewarm(cleanupCtx, engine, vm, name, token)
			}
			return cleanupFailedPrewarm(cleanupCtx, engine, name, token)
		})
		return templateimage.Base{}, errors.Join(fmt.Errorf("prewarm configured base image: %w", createErr), cleanupErr)
	}

	image, inspectErr := engine.inspectImage(ctx, reference)
	var base templateimage.Base
	if inspectErr == nil {
		base, inspectErr = templateBaseFromInspection(reference, image)
	} else {
		inspectErr = fmt.Errorf("inspect configured base image: %w", inspectErr)
	}
	cleanupErr := withPrewarmCleanupContext(ctx, func(cleanupCtx context.Context) error {
		return cleanupCreatedPrewarm(cleanupCtx, engine, vm, name, token)
	})
	if inspectErr != nil || cleanupErr != nil {
		return templateimage.Base{}, errors.Join(inspectErr, cleanupErr)
	}
	return base, nil
}

func basePullPolicy(reference string) (string, error) {
	if reference == developmentBaseReference {
		return string(msb.PullPolicyNever), nil
	}
	if strings.TrimSpace(reference) != reference || strings.ContainsAny(reference, "\\?#\r\n\t ") {
		return "", errors.New("template base image must be a canonical OCI repository reference")
	}
	if strings.Count(reference, "@") != 1 {
		return "", errors.New("template base image must be digest-pinned with @sha256:<64 lowercase hex>")
	}
	repository, digest, _ := strings.Cut(reference, "@")
	if !templateimage.ValidDigest(digest) || !validOCIRepository(repository) {
		return "", errors.New("template base image must be a valid repository pinned by a lowercase sha256 digest")
	}
	return string(msb.PullPolicyIfMissing), nil
}

func validOCIRepository(repository string) bool {
	if repository == "" || len(repository) > 255 || strings.ToLower(repository) != repository {
		return false
	}
	parts := strings.Split(repository, "/")
	if len(parts) == 0 {
		return false
	}
	if looksLikeRegistryAuthority(parts[0]) {
		if !validRegistryAuthority(parts[0]) || len(parts) == 1 {
			return false
		}
		parts = parts[1:]
	}
	for _, part := range parts {
		if !repoComponentRE.MatchString(part) {
			return false
		}
	}
	return true
}

func looksLikeRegistryAuthority(first string) bool {
	return first == "localhost" || strings.ContainsAny(first, ".:") || strings.HasPrefix(first, "[")
}

func validRegistryAuthority(authority string) bool {
	var host, port string
	hasPort := false
	if strings.HasPrefix(authority, "[") {
		end := strings.IndexByte(authority, ']')
		if end < 0 {
			return false
		}
		host = authority[1:end]
		ip := net.ParseIP(host)
		if ip == nil || ip.To4() != nil {
			return false
		}
		rest := authority[end+1:]
		if rest != "" {
			if !strings.HasPrefix(rest, ":") {
				return false
			}
			hasPort, port = true, rest[1:]
		}
	} else {
		if strings.Count(authority, ":") > 1 {
			return false
		}
		if index := strings.LastIndexByte(authority, ':'); index >= 0 {
			hasPort, host, port = true, authority[:index], authority[index+1:]
		} else {
			host = authority
		}
		if ip := net.ParseIP(host); ip != nil {
			if ip.To4() == nil {
				return false
			}
		} else if !validDNSHost(host) {
			return false
		}
	}
	if !hasPort {
		return true
	}
	if port == "" {
		return false
	}
	for i := 0; i < len(port); i++ {
		if port[i] < '0' || port[i] > '9' {
			return false
		}
	}
	n, err := strconv.Atoi(port)
	return err == nil && n > 0 && n <= 65535
}

func validDNSHost(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
				return false
			}
		}
	}
	return true
}

func newPrewarmIdentity() (string, string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", "", err
	}
	token := hex.EncodeToString(random[:])
	return prewarmNamePrefix + token[:24], token, nil
}

func templateBaseFromInspection(reference string, image inspectedBaseImage) (templateimage.Base, error) {
	if image.Config == nil {
		return templateimage.Base{}, errors.New("configured base image has no OCI config")
	}
	if !templateimage.ValidDigest(image.ManifestDigest) {
		return templateimage.Base{}, errors.New("configured base image has an invalid manifest digest")
	}
	if image.OS != "linux" || (image.Architecture != "amd64" && image.Architecture != "arm64") {
		return templateimage.Base{}, errors.New("configured base image must be linux/amd64 or linux/arm64")
	}
	if len(image.Layers) == 0 || len(image.Layers) >= 256 || image.LayerCount != uint(len(image.Layers)) {
		return templateimage.Base{}, errors.New("configured base image has incomplete or unsupported layer metadata")
	}

	base := templateimage.Base{
		Reference: reference, Digest: image.ManifestDigest,
		OS: image.OS, Architecture: image.Architecture,
		// msb 0.7.7 does not expose the OCI platform variant.
		Variant: "", Config: cloneTemplateConfig(*image.Config),
		Layers: make([]templateimage.BaseLayer, 0, len(image.Layers)),
	}
	for _, layer := range image.Layers {
		if !templateimage.ValidDigest(layer.BlobDigest) || !templateimage.ValidDigest(layer.DiffID) ||
			layer.CompressedSize == nil || *layer.CompressedSize <= 0 || !supportedBaseLayerMediaType(layer.MediaType) {
			return templateimage.Base{}, errors.New("configured base image has malformed OCI layer metadata")
		}
		base.Layers = append(base.Layers, templateimage.BaseLayer{
			Descriptor: templateimage.Descriptor{
				MediaType: layer.MediaType, Digest: layer.BlobDigest, Size: *layer.CompressedSize,
			},
			DiffID: layer.DiffID,
		})
	}
	return base, nil
}

func cloneTemplateConfig(config templateimage.Config) templateimage.Config {
	config.Env = append([]string(nil), config.Env...)
	config.Cmd = append([]string(nil), config.Cmd...)
	config.Entrypoint = append([]string(nil), config.Entrypoint...)
	if config.Labels != nil {
		labels := make(map[string]string, len(config.Labels))
		for key, value := range config.Labels {
			labels[key] = value
		}
		config.Labels = labels
	}
	return config
}

func supportedBaseLayerMediaType(mediaType string) bool {
	switch mediaType {
	case templateimage.MediaLayer,
		"application/vnd.oci.image.layer.v1.tar",
		"application/vnd.oci.image.layer.v1.tar+zstd",
		"application/vnd.docker.image.rootfs.diff.tar.gzip",
		"application/vnd.docker.image.rootfs.diff.tar":
		return true
	default:
		return false
	}
}

func withPrewarmCleanupContext(ctx context.Context, cleanup func(context.Context) error) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), prewarmCleanupTimeout)
	defer cancel()
	return cleanup(cleanupCtx)
}

func cleanupCreatedPrewarm(ctx context.Context, engine templateBaseEngine, vm prewarmVM, name, token string) error {
	stopErr := vm.stop(ctx)
	record, lookupErr := engine.lookupSandbox(ctx, name)
	if errors.Is(lookupErr, errPrewarmNotFound) {
		return vm.close()
	}
	if lookupErr != nil {
		detachErr := vm.detach(ctx)
		return prewarmCleanupError(name, errors.Join(stopErr, fmt.Errorf("verify ownership: %w", lookupErr), detachErr))
	}
	owned, ownershipErr := prewarmRecordOwned(record, name, token, vm.id())
	if ownershipErr != nil {
		detachErr := vm.detach(ctx)
		return prewarmCleanupError(name, errors.Join(stopErr, fmt.Errorf("verify ownership labels: %w", ownershipErr), detachErr))
	}
	if !owned {
		detachErr := vm.detach(ctx)
		return prewarmCleanupError(name, errors.Join(stopErr, errors.New("sandbox identity or ownership labels changed; left for recovery"), detachErr))
	}
	if !prewarmTerminal(record.status()) {
		detachErr := vm.detach(ctx)
		return prewarmCleanupError(name, errors.Join(stopErr, fmt.Errorf("VM remains in state %q; left for recovery", record.status()), detachErr))
	}
	closeErr := vm.close()
	removeErr := engine.removeSandbox(ctx, name)
	return prewarmCleanupError(name, errors.Join(closeErr, removeErr))
}

func cleanupFailedPrewarm(ctx context.Context, engine templateBaseEngine, name, token string) error {
	record, err := engine.lookupSandbox(ctx, name)
	if errors.Is(err, errPrewarmNotFound) {
		return nil
	}
	if err != nil {
		return prewarmCleanupError(name, fmt.Errorf("check for a partially created VM: %w", err))
	}
	owned, ownershipErr := prewarmRecordOwned(record, name, token, "")
	if ownershipErr != nil {
		return prewarmCleanupError(name, fmt.Errorf("verify ownership labels: %w", ownershipErr))
	}
	if !owned {
		// A colliding generated name is not ours. Never stop or delete it.
		return nil
	}
	return cleanupOwnedPrewarmRecord(ctx, engine, record, name, token)
}

func cleanupOwnedPrewarmRecord(ctx context.Context, engine templateBaseEngine, record prewarmRecord, name, token string) error {
	stopErr := record.stop(ctx)
	current, err := engine.lookupSandbox(ctx, name)
	if errors.Is(err, errPrewarmNotFound) {
		return nil
	}
	if err != nil {
		return prewarmCleanupError(name, errors.Join(stopErr, fmt.Errorf("verify stopped state: %w", err)))
	}
	owned, ownershipErr := prewarmRecordOwned(current, name, token, record.id())
	if ownershipErr != nil {
		return prewarmCleanupError(name, errors.Join(stopErr, fmt.Errorf("verify ownership labels: %w", ownershipErr)))
	}
	if !owned {
		return prewarmCleanupError(name, errors.Join(stopErr, errors.New("sandbox identity or ownership labels changed; left for recovery")))
	}
	if !prewarmTerminal(current.status()) {
		return prewarmCleanupError(name, errors.Join(stopErr, fmt.Errorf("VM remains in state %q; left for recovery", current.status())))
	}
	return prewarmCleanupError(name, engine.removeSandbox(ctx, name))
}

func prewarmRecordOwned(record prewarmRecord, name, token, id string) (bool, error) {
	if record == nil || record.name() != name || (id != "" && record.id() != id) {
		return false, nil
	}
	labels, err := record.labels()
	if err != nil {
		return false, err
	}
	return labels[prewarmKindLabel] == prewarmKindValue && labels[prewarmTokenLabel] == token, nil
}

func prewarmTerminal(status string) bool {
	return status == "created" || status == "stopped" || status == "crashed"
}

func prewarmCleanupError(name string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("cleanup template base prewarm VM %q: %w", name, err)
}

type sdkTemplateBaseEngine struct{}

func (sdkTemplateBaseEngine) createPrewarm(ctx context.Context, request prewarmRequest) (prewarmVM, error) {
	progress, results := msb.CreateSandboxWithProgress(ctx, request.Name, prewarmSandboxOptions(request)...)
	_ = progress // Progress is best-effort; the result channel is authoritative on cancellation.
	result, ok := <-results
	if !ok {
		return nil, errors.New("microsandbox returned no prewarm creation result")
	}
	if result.Sandbox != nil {
		return sdkPrewarmVM{sandbox: result.Sandbox}, result.Err
	}
	if result.Err != nil {
		return nil, result.Err
	}
	return nil, errors.New("microsandbox returned an empty prewarm result")
}

func prewarmSandboxOptions(request prewarmRequest) []msb.SandboxOption {
	labels := map[string]string{
		prewarmKindLabel:  prewarmKindValue,
		prewarmTokenLabel: request.Token,
	}
	network := &msb.NetworkConfig{
		DefaultEgress:  msb.PolicyAction(request.DefaultEgress),
		DefaultIngress: msb.PolicyAction(request.DefaultIngress),
	}
	return []msb.SandboxOption{
		msb.WithImage(request.Image),
		msb.WithPullPolicy(msb.PullPolicy(request.PullPolicy)),
		msb.WithCPUs(request.CPUs),
		msb.WithMaxCPUs(request.MaxCPUs),
		msb.WithMemory(request.MemoryMiB),
		msb.WithMaxMemory(request.MaxMemoryMiB),
		msb.WithNetwork(network),
		msb.WithLabels(labels),
		msb.WithDetached(),
	}
}

func (sdkTemplateBaseEngine) inspectImage(ctx context.Context, reference string) (inspectedBaseImage, error) {
	detail, err := msb.Image.Inspect(ctx, reference)
	if err != nil {
		return inspectedBaseImage{}, err
	}
	if detail == nil || detail.ImageHandle == nil {
		return inspectedBaseImage{}, errors.New("microsandbox returned empty image details")
	}
	var config *templateimage.Config
	if detail.Config != nil {
		config = &templateimage.Config{
			Env:        append([]string(nil), detail.Config.Env...),
			Cmd:        append([]string(nil), detail.Config.Cmd...),
			Entrypoint: append([]string(nil), detail.Config.Entrypoint...),
			WorkingDir: detail.Config.WorkingDir,
			User:       detail.Config.User,
			StopSignal: detail.Config.StopSignal,
		}
		if detail.Config.Labels != nil {
			config.Labels = make(map[string]string, len(detail.Config.Labels))
			for key, value := range detail.Config.Labels {
				config.Labels[key] = value
			}
		}
	}
	layers := make([]inspectedBaseLayer, 0, len(detail.Layers))
	for _, layer := range detail.Layers {
		var size *int64
		if layer.CompressedSizeBytes != nil {
			value := *layer.CompressedSizeBytes
			size = &value
		}
		layers = append(layers, inspectedBaseLayer{
			DiffID: layer.DiffID, BlobDigest: layer.BlobDigest,
			MediaType: layer.MediaType, CompressedSize: size,
		})
	}
	return inspectedBaseImage{
		ManifestDigest: detail.ManifestDigest(),
		OS:             detail.OS(), Architecture: detail.Architecture(),
		LayerCount: detail.LayerCount(), Config: config, Layers: layers,
	}, nil
}

func (sdkTemplateBaseEngine) lookupSandbox(ctx context.Context, name string) (prewarmRecord, error) {
	handle, err := msb.GetSandbox(ctx, name)
	if err != nil {
		if msb.IsKind(err, msb.ErrSandboxNotFound) {
			return nil, errPrewarmNotFound
		}
		return nil, err
	}
	return sdkPrewarmRecord{handle: handle}, nil
}

func (sdkTemplateBaseEngine) removeSandbox(ctx context.Context, name string) error {
	err := msb.RemoveSandbox(ctx, name)
	if err != nil && msb.IsKind(err, msb.ErrSandboxNotFound) {
		return nil
	}
	return err
}

type sdkPrewarmVM struct{ sandbox *msb.Sandbox }

func (s sdkPrewarmVM) id() string { return s.sandbox.ID() }
func (s sdkPrewarmVM) stop(ctx context.Context) error {
	return s.sandbox.StopWithTimeout(ctx, prewarmStopTimeout)
}
func (s sdkPrewarmVM) close() error                     { return s.sandbox.Close() }
func (s sdkPrewarmVM) detach(ctx context.Context) error { return s.sandbox.Detach(ctx) }

type sdkPrewarmRecord struct{ handle *msb.SandboxHandle }

func (s sdkPrewarmRecord) name() string   { return s.handle.Name() }
func (s sdkPrewarmRecord) id() string     { return s.handle.ID() }
func (s sdkPrewarmRecord) status() string { return string(s.handle.Status()) }
func (s sdkPrewarmRecord) labels() (map[string]string, error) {
	config, err := s.handle.Config()
	if err != nil {
		return nil, err
	}
	labels := make(map[string]string, len(config.Labels))
	for key, value := range config.Labels {
		labels[key] = value
	}
	return labels, nil
}
func (s sdkPrewarmRecord) stop(ctx context.Context) error {
	return s.handle.StopWithTimeout(ctx, prewarmStopTimeout)
}
