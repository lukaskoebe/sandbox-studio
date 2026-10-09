package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"

	msb "github.com/superradcompany/microsandbox/sdk/go"

	"github.com/lukaskoebe/sandbox-studio/internal/templateimage"
)

const baseTestDigest = "sha256:" + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type fakeTemplateBaseEngine struct {
	requests        []prewarmRequest
	inspection      inspectedBaseImage
	inspectRef      string
	inspectCalls    int
	lookupCalls     int
	inspectErr      error
	createErr       error
	persistOnError  bool
	collision       *fakePrewarm
	records         map[string]*fakePrewarm
	removeErr       error
	removeCalls     []string
	onInspect       func()
	onCreate        func(*fakePrewarm)
	cleanupErr      error
	cleanupDeadline bool
	allVMs          []*fakePrewarm
}

func newFakeTemplateBaseEngine() *fakeTemplateBaseEngine {
	return &fakeTemplateBaseEngine{
		inspection: validBaseInspection(),
		records:    make(map[string]*fakePrewarm),
	}
}

func (f *fakeTemplateBaseEngine) createPrewarm(_ context.Context, request prewarmRequest) (prewarmVM, error) {
	f.requests = append(f.requests, request)
	if f.collision != nil {
		f.collision.vmName = request.Name
		f.records[request.Name] = f.collision
		return nil, f.createErr
	}
	vm := &fakePrewarm{
		vmName: request.Name,
		vmID:   "fake-vm-id",
		state:  "running",
		labelValues: map[string]string{
			prewarmKindLabel:  prewarmKindValue,
			prewarmTokenLabel: request.Token,
		},
		engine: f,
	}
	f.allVMs = append(f.allVMs, vm)
	if f.onCreate != nil {
		f.onCreate(vm)
	}
	if f.createErr == nil || f.persistOnError {
		f.records[request.Name] = vm
	}
	if f.createErr != nil {
		return nil, f.createErr
	}
	return vm, nil
}

func (f *fakeTemplateBaseEngine) inspectImage(ctx context.Context, reference string) (inspectedBaseImage, error) {
	f.inspectCalls++
	f.inspectRef = reference
	if f.onInspect != nil {
		f.onInspect()
	}
	if f.inspectErr != nil {
		return inspectedBaseImage{}, f.inspectErr
	}
	if err := ctx.Err(); err != nil {
		return inspectedBaseImage{}, err
	}
	return f.inspection, nil
}

func (f *fakeTemplateBaseEngine) lookupSandbox(_ context.Context, name string) (prewarmRecord, error) {
	f.lookupCalls++
	record, ok := f.records[name]
	if !ok {
		return nil, errPrewarmNotFound
	}
	return record, nil
}

func (f *fakeTemplateBaseEngine) removeSandbox(ctx context.Context, name string) error {
	f.removeCalls = append(f.removeCalls, name)
	f.cleanupErr = ctx.Err()
	_, f.cleanupDeadline = ctx.Deadline()
	if f.removeErr != nil {
		return f.removeErr
	}
	delete(f.records, name)
	return nil
}

type fakePrewarm struct {
	vmName          string
	vmID            string
	state           string
	labelValues     map[string]string
	labelsErr       error
	stopErr         error
	closeErr        error
	detachErr       error
	stopCalls       int
	closeCalls      int
	detachCalls     int
	cleanupErr      error
	cleanupDeadline bool
	engine          *fakeTemplateBaseEngine
}

func (f *fakePrewarm) id() string     { return f.vmID }
func (f *fakePrewarm) name() string   { return f.vmName }
func (f *fakePrewarm) status() string { return f.state }
func (f *fakePrewarm) labels() (map[string]string, error) {
	if f.labelsErr != nil {
		return nil, f.labelsErr
	}
	labels := make(map[string]string, len(f.labelValues))
	for key, value := range f.labelValues {
		labels[key] = value
	}
	return labels, nil
}
func (f *fakePrewarm) stop(ctx context.Context) error {
	f.stopCalls++
	f.cleanupErr = ctx.Err()
	_, f.cleanupDeadline = ctx.Deadline()
	if f.stopErr != nil {
		return f.stopErr
	}
	f.state = "stopped"
	return nil
}
func (f *fakePrewarm) close() error {
	f.closeCalls++
	return f.closeErr
}
func (f *fakePrewarm) detach(context.Context) error {
	f.detachCalls++
	return f.detachErr
}

func validBaseInspection() inspectedBaseImage {
	size1, size2 := int64(4096), int64(8192)
	return inspectedBaseImage{
		ManifestDigest: baseTestDigest,
		OS:             "linux",
		Architecture:   "amd64",
		LayerCount:     2,
		Config: &templateimage.Config{
			Env: []string{"PATH=/usr/bin"}, Cmd: []string{"/bin/sh"},
			Entrypoint: []string{"/entry"}, WorkingDir: "/work", User: "studio",
			StopSignal: "SIGTERM", Labels: map[string]string{"base": "approved"},
		},
		Layers: []inspectedBaseLayer{
			{DiffID: "sha256:" + strings.Repeat("b", 64), BlobDigest: "sha256:" + strings.Repeat("c", 64), MediaType: templateimage.MediaLayer, CompressedSize: &size1},
			{DiffID: "sha256:" + strings.Repeat("d", 64), BlobDigest: "sha256:" + strings.Repeat("e", 64), MediaType: "application/vnd.oci.image.layer.v1.tar+zstd", CompressedSize: &size2},
		},
	}
}

func TestBasePullPolicyRejectsMutableOrNonCanonicalReferences(t *testing.T) {
	valid := []string{
		developmentBaseReference,
		"ghcr.io/lukaskoebe/sandbox-studio-base@" + baseTestDigest,
		"127.0.0.1:7880/v2/studio/aaaa/bbbbbbbbbbbbb@" + baseTestDigest,
		"[::1]:7880/studio/base@" + baseTestDigest,
		"localhost:5000/team/base@" + baseTestDigest,
	}
	for _, reference := range valid {
		if _, err := basePullPolicy(reference); err != nil {
			t.Errorf("basePullPolicy(%q): %v", reference, err)
		}
	}
	invalid := []string{
		"",
		"sandbox-studio-base:latest",
		"ghcr.io/team/base:latest",
		"ghcr.io/team/base:latest@" + baseTestDigest,
		"ghcr.io/team/base@sha256:" + strings.Repeat("A", 64),
		"ghcr.io/team/base@sha512:" + strings.Repeat("a", 128),
		"sha256:" + strings.Repeat("a", 64),
		"https://ghcr.io/team/base@" + baseTestDigest,
		"user@ghcr.io/team/base@" + baseTestDigest,
		"ghcr.io/team/base?x=1@" + baseTestDigest,
		"ghcr.io/team/base#fragment@" + baseTestDigest,
		"ghcr.io/team/base\\child@" + baseTestDigest,
		"ghcr.io/team/Base@" + baseTestDigest,
		"ghcr.io/team/base @" + baseTestDigest,
		"127.0.0.1:7880@" + baseTestDigest,
		"registry:abc/team/base@" + baseTestDigest,
		"registry:/team/base@" + baseTestDigest,
	}
	for _, reference := range invalid {
		if _, err := basePullPolicy(reference); err == nil {
			t.Errorf("basePullPolicy(%q) accepted invalid reference", reference)
		}
	}
}

func TestPrepareTemplateBasePrewarmOptionsAndMetadata(t *testing.T) {
	tests := []struct {
		name       string
		reference  string
		wantPolicy string
	}{
		{name: "local development", reference: developmentBaseReference, wantPolicy: "never"},
		{name: "digest pinned release", reference: "ghcr.io/lukaskoebe/base@" + baseTestDigest, wantPolicy: "if-missing"},
		{name: "explicit registry host and port", reference: "127.0.0.1:7880/v2/studio/aaaa/bbbbbbbbbbbbb@" + baseTestDigest, wantPolicy: "if-missing"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine := newFakeTemplateBaseEngine()
			base, err := prepareTemplateBase(context.Background(), test.reference, engine)
			if err != nil {
				t.Fatal(err)
			}
			if len(engine.requests) != 1 {
				t.Fatalf("prewarm create calls = %d, want 1", len(engine.requests))
			}
			request := engine.requests[0]
			if request.Image != test.reference || engine.inspectRef != test.reference || base.Reference != test.reference {
				t.Fatalf("configured reference was not used consistently: request=%q inspect=%q base=%q", request.Image, engine.inspectRef, base.Reference)
			}
			if request.PullPolicy != test.wantPolicy || request.CPUs != 1 || request.MaxCPUs != 1 ||
				request.MemoryMiB != 512 || request.MaxMemoryMiB != 512 ||
				request.DefaultEgress != "deny" || request.DefaultIngress != "deny" {
				t.Fatalf("unsafe or unexpected prewarm options: %+v", request)
			}
			if !strings.HasPrefix(request.Name, prewarmNamePrefix) || len(request.Name) != len(prewarmNamePrefix)+24 ||
				len(request.Name) > 40 || len(request.Token) != 32 ||
				strings.Trim(request.Token, "0123456789abcdef") != "" {
				t.Fatalf("prewarm identity is not random lowercase hex: name=%q token=%q", request.Name, request.Token)
			}
			if engine.records[request.Name] != nil || len(engine.removeCalls) != 1 || engine.removeCalls[0] != request.Name {
				t.Fatalf("owned prewarm VM was not stopped and removed: records=%v removed=%v", engine.records, engine.removeCalls)
			}
			if len(engine.allVMs) != 1 || engine.allVMs[0].stopCalls != 1 || engine.allVMs[0].closeCalls != 1 {
				t.Fatalf("owned VM lifecycle calls = %+v", engine.allVMs)
			}
			if base.Digest != baseTestDigest || base.OS != "linux" || base.Architecture != "amd64" || base.Variant != "" {
				t.Fatalf("unexpected base identity/platform: %+v", base)
			}
			if len(base.Layers) != 2 || base.Layers[0].Size != 4096 || base.Layers[1].Size != 8192 ||
				base.Layers[1].DiffID != engine.inspection.Layers[1].DiffID {
				t.Fatalf("layer metadata was not mapped from compressed OCI metadata: %+v", base.Layers)
			}
			if base.Config.User != "studio" || base.Config.WorkingDir != "/work" || base.Config.Labels["base"] != "approved" {
				t.Fatalf("config subset not mapped: %+v", base.Config)
			}
		})
	}
}

func TestPrepareTemplateBaseAlreadyCanceledMakesNoCalls(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	engine := newFakeTemplateBaseEngine()

	if _, err := prepareTemplateBase(ctx, developmentBaseReference, engine); !errors.Is(err, context.Canceled) {
		t.Fatalf("prepareTemplateBase error = %v, want context.Canceled", err)
	}
	if len(engine.requests) != 0 || engine.inspectCalls != 0 || engine.lookupCalls != 0 || len(engine.removeCalls) != 0 {
		t.Fatalf("canceled context caused engine calls: creates=%d inspects=%d lookups=%d removals=%d",
			len(engine.requests), engine.inspectCalls, engine.lookupCalls, len(engine.removeCalls))
	}
}

func TestPrepareTemplateBaseRejectsMutableReferenceBeforeRuntimeCalls(t *testing.T) {
	engine := newFakeTemplateBaseEngine()
	if _, err := prepareTemplateBase(context.Background(), "ghcr.io/team/base:latest", engine); err == nil {
		t.Fatal("mutable image reference was accepted")
	}
	if len(engine.requests) != 0 || engine.inspectRef != "" {
		t.Fatalf("invalid reference caused SDK operations: requests=%v inspected=%q", engine.requests, engine.inspectRef)
	}
}

func TestTemplateBaseMetadataValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*inspectedBaseImage)
	}{
		{name: "missing config", mutate: func(image *inspectedBaseImage) { image.Config = nil }},
		{name: "bad manifest digest", mutate: func(image *inspectedBaseImage) { image.ManifestDigest = "sha256:ABC" }},
		{name: "unsupported platform", mutate: func(image *inspectedBaseImage) { image.Architecture = "386" }},
		{name: "empty layers", mutate: func(image *inspectedBaseImage) { image.Layers = nil; image.LayerCount = 0 }},
		{name: "mismatched layer count", mutate: func(image *inspectedBaseImage) { image.LayerCount++ }},
		{name: "missing compressed size", mutate: func(image *inspectedBaseImage) { image.Layers[0].CompressedSize = nil }},
		{name: "unsupported media type", mutate: func(image *inspectedBaseImage) { image.Layers[0].MediaType = "application/octet-stream" }},
		{name: "bad layer digest", mutate: func(image *inspectedBaseImage) { image.Layers[0].BlobDigest = "sha256:bad" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			image := validBaseInspection()
			test.mutate(&image)
			if _, err := templateBaseFromInspection(developmentBaseReference, image); err == nil {
				t.Fatal("malformed image inspection was accepted")
			}
		})
	}
}

func TestPrepareTemplateBaseUsesIndependentBoundedCleanupContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	engine := newFakeTemplateBaseEngine()
	engine.onInspect = cancel
	engine.removeErr = errors.New("remove failed")
	_, err := prepareTemplateBase(ctx, developmentBaseReference, engine)
	if err == nil {
		t.Fatal("expected inspect cancellation and cleanup failure")
	}
	if len(engine.requests) != 1 {
		t.Fatalf("create calls = %d, want 1", len(engine.requests))
	}
	name := engine.requests[0].Name
	if !strings.Contains(err.Error(), name) || !strings.Contains(err.Error(), "remove failed") {
		t.Fatalf("cleanup failure does not identify recoverable VM %q: %v", name, err)
	}
	vm := engine.records[name]
	if vm == nil || vm.cleanupErr != nil || !vm.cleanupDeadline || engine.cleanupErr != nil || !engine.cleanupDeadline {
		t.Fatalf("cleanup did not use bounded independent context: vm=%+v engineErr=%v deadline=%v", vm, engine.cleanupErr, engine.cleanupDeadline)
	}
}

func TestPrepareTemplateBaseCreateFailureCleansOnlyMatchingOwnership(t *testing.T) {
	t.Run("partial owned creation", func(t *testing.T) {
		engine := newFakeTemplateBaseEngine()
		engine.createErr = errors.New("creation canceled")
		engine.persistOnError = true
		_, err := prepareTemplateBase(context.Background(), developmentBaseReference, engine)
		if err == nil || !strings.Contains(err.Error(), "creation canceled") {
			t.Fatalf("creation error not returned: %v", err)
		}
		if len(engine.removeCalls) != 1 || len(engine.records) != 0 {
			t.Fatalf("partially created owned VM not cleaned: removed=%v records=%v", engine.removeCalls, engine.records)
		}
	})

	t.Run("same-name collision is preserved", func(t *testing.T) {
		engine := newFakeTemplateBaseEngine()
		engine.createErr = errors.New("name already exists")
		engine.collision = &fakePrewarm{
			vmID: "unrelated", state: "running",
			labelValues: map[string]string{prewarmKindLabel: "other", prewarmTokenLabel: "other-token"},
			engine:      engine,
		}
		_, err := prepareTemplateBase(context.Background(), developmentBaseReference, engine)
		if err == nil || len(engine.removeCalls) != 0 || engine.collision.stopCalls != 0 {
			t.Fatalf("unowned name collision was modified: err=%v removed=%v stopCalls=%d", err, engine.removeCalls, engine.collision.stopCalls)
		}
	})

	t.Run("ambiguous ownership is reported", func(t *testing.T) {
		engine := newFakeTemplateBaseEngine()
		engine.createErr = errors.New("creation canceled")
		engine.persistOnError = true
		engine.onCreate = func(vm *fakePrewarm) {
			vm.labelsErr = errors.New("config unavailable")
		}
		_, err := prepareTemplateBase(context.Background(), developmentBaseReference, engine)
		if err == nil || !strings.Contains(err.Error(), engine.requests[0].Name) ||
			!strings.Contains(err.Error(), "config unavailable") {
			t.Fatalf("ownership failure was not surfaced with recovery name: %v", err)
		}
		if engine.allVMs[0].stopCalls != 0 || len(engine.removeCalls) != 0 {
			t.Fatalf("VM with unverifiable ownership was modified: stops=%d removed=%v", engine.allVMs[0].stopCalls, engine.removeCalls)
		}
	})
}

func TestPrepareTemplateBaseLeavesUnstoppedOwnedVMForRecovery(t *testing.T) {
	engine := newFakeTemplateBaseEngine()
	if err := prepareValidBaseWithStopError(engine); err == nil {
		t.Fatal("expected cleanup failure when SDK did not confirm stop")
	}
	if len(engine.requests) != 1 {
		t.Fatalf("prewarm requests = %d, want 1", len(engine.requests))
	}
	name := engine.requests[0].Name
	vm := engine.records[name]
	if vm == nil || vm.state != "running" || vm.detachCalls != 1 || len(engine.removeCalls) != 0 {
		t.Fatalf("active owned VM was not preserved for recovery: vm=%+v removed=%v", vm, engine.removeCalls)
	}
}

func prepareValidBaseWithStopError(engine *fakeTemplateBaseEngine) error {
	engine.onCreate = func(vm *fakePrewarm) {
		vm.stopErr = errors.New("stop timed out")
	}
	_, err := prepareTemplateBase(context.Background(), developmentBaseReference, engine)
	return err
}

func TestCloneTemplateConfigDoesNotAliasSDKMetadata(t *testing.T) {
	image := validBaseInspection()
	base, err := templateBaseFromInspection(developmentBaseReference, image)
	if err != nil {
		t.Fatal(err)
	}
	base.Config.Env[0] = "changed"
	base.Config.Labels["base"] = "changed"
	if image.Config.Env[0] != "PATH=/usr/bin" || image.Config.Labels["base"] != "approved" {
		t.Fatal("base config aliases inspected SDK metadata")
	}
}

func TestPrewarmErrorIncludesGeneratedNameForRecovery(t *testing.T) {
	engine := newFakeTemplateBaseEngine()
	engine.createErr = errors.New("cancelled after partial create")
	engine.persistOnError = true
	engine.removeErr = errors.New("cleanup failed")
	_, err := prepareTemplateBase(context.Background(), developmentBaseReference, engine)
	if err == nil || len(engine.requests) != 1 {
		t.Fatalf("expected named cleanup error, got %v", err)
	}
	if !strings.Contains(err.Error(), engine.requests[0].Name) {
		t.Fatalf("error does not include recovery name %q: %v", engine.requests[0].Name, err)
	}
}

func TestBaseRepositoryValidationTreatsHostPortAsAuthority(t *testing.T) {
	if !validOCIRepository("127.0.0.1:7880/v2/studio/aaaa/bbbbbbbbbbbbb") {
		t.Fatal("valid IPv4 host:port repository was rejected")
	}
	if validOCIRepository("registry:abc/team/base") {
		t.Fatal("nonnumeric registry port was accepted")
	}
	if validOCIRepository("registry:5000") {
		t.Fatal("registry authority without repository path was accepted")
	}
}

func TestPrewarmRequestHasNoGuestCredentialOrResourceInputs(t *testing.T) {
	engine := newFakeTemplateBaseEngine()
	_, err := prepareTemplateBase(context.Background(), developmentBaseReference, engine)
	if err != nil {
		t.Fatal(err)
	}
	request := engine.requests[0]
	if request.Image != developmentBaseReference || request.Token == "" {
		t.Fatalf("unexpected prewarm request: %+v", request)
	}
	// prewarmRequest contains only image, generated ownership labels, pull policy,
	// resource limits and deny-all defaults; it cannot carry guest credentials or mounts.
	if request.DefaultEgress != "deny" || request.DefaultIngress != "deny" {
		t.Fatalf("prewarm network is not deny-all: %+v", request)
	}
	config := msb.SandboxConfig{}
	for _, option := range prewarmSandboxOptions(request) {
		option(&config)
	}
	if config.Image != developmentBaseReference || config.PullPolicy != msb.PullPolicyNever ||
		config.CPUs != 1 || config.MaxCPUs != 1 || config.MemoryMiB != 512 || config.MaxMemoryMiB != 512 {
		t.Fatalf("SDK options lost image policy or resource limits: %+v", config)
	}
	if config.Network == nil || config.Network.DefaultEgress != msb.PolicyActionDeny ||
		config.Network.DefaultIngress != msb.PolicyActionDeny || len(config.Network.Rules) != 0 {
		t.Fatalf("SDK options are not deny-all: %+v", config.Network)
	}
	if !config.Detached || len(config.Volumes) != 0 || config.Proxy != nil || len(config.Vsock) != 0 ||
		len(config.Env) != 0 || config.RegistryAuth != nil {
		t.Fatalf("SDK options contain guest mounts, networking, or credentials: %+v", config)
	}
	if config.Labels[prewarmKindLabel] != prewarmKindValue || config.Labels[prewarmTokenLabel] != request.Token {
		t.Fatalf("SDK options lost ownership labels: %+v", config.Labels)
	}
}
