package sandboxes

import (
	"context"
	"encoding/json"
	"errors"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/resources"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/templateimage"
	"github.com/lukaskoebe/sandbox-studio/internal/templateregistry"
	"github.com/lukaskoebe/sandbox-studio/internal/templatespec"
)

type templateResolverFake struct {
	reference templateregistry.Reference
	err       error
	calls     int
	onResolve func(string, string)
}

func (f *templateResolverFake) Resolve(_ context.Context, envID, templateID string) (templateregistry.Reference, error) {
	f.calls++
	if f.onResolve != nil {
		f.onResolve(envID, templateID)
	}
	return f.reference, f.err
}

type templateRecordingRuntime struct {
	SandboxRuntime
	name             string
	spec             runtime.Spec
	labels           map[string]string
	createErr        error
	removeOwnedErr   error
	removeOwnedCalls []runtime.OwnedVM
}

func (r *templateRecordingRuntime) Create(ctx context.Context, name string, spec runtime.Spec, socket string, labels map[string]string) error {
	r.name, r.spec, r.labels = name, spec, cloneStringMap(labels)
	if err := r.SandboxRuntime.Create(ctx, name, spec, socket, labels); err != nil {
		return err
	}
	return r.createErr
}

func (r *templateRecordingRuntime) RemoveOwned(ctx context.Context, owned runtime.OwnedVM) error {
	r.removeOwnedCalls = append(r.removeOwnedCalls, owned)
	if r.removeOwnedErr != nil {
		return r.removeOwnedErr
	}
	return r.SandboxRuntime.Remove(ctx, owned.Name)
}

func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func readyTemplateForManager(t *testing.T, f *checkpointFixture, canonical []byte, platform, exporterVersion string) store.Template {
	t.Helper()
	baseDigest := "sha256:" + strings.Repeat("a", 64)
	cacheKey, err := templateimage.CacheKey(canonical, baseDigest, platform, exporterVersion)
	if err != nil {
		t.Fatalf("CacheKey: %v", err)
	}
	template, err := f.store.CreateTemplate(f.ctx, store.Template{
		EnvironmentID: f.env.ID, CacheKey: cacheKey, Spec: string(canonical),
		BaseRef: "sandbox-studio-base:test", BaseDigest: baseDigest,
		Platform: platform, ExporterVersion: exporterVersion,
	})
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	artifacts := []store.TemplateArtifact{
		{Role: "manifest", Digest: "sha256:" + strings.Repeat("b", 64), MediaType: templateimage.MediaManifest, Size: 1},
		{Role: "config", Digest: "sha256:" + strings.Repeat("c", 64), MediaType: templateimage.MediaConfig, Size: 1},
		{Role: "layer", Digest: "sha256:" + strings.Repeat("d", 64), MediaType: templateimage.MediaLayer, Size: 1},
	}
	if err := f.store.ReadyTemplate(f.ctx, f.env.ID, template.ID, artifacts); err != nil {
		t.Fatalf("ReadyTemplate: %v", err)
	}
	template, err = f.store.Template(f.ctx, f.env.ID, template.ID)
	if err != nil {
		t.Fatalf("read ready template: %v", err)
	}
	return template
}

func canonicalTemplateSpec(t *testing.T, res resources.Resources) []byte {
	t.Helper()
	encoded, err := templatespec.CanonicalJSON(templatespec.Spec{Resources: res, Tools: []templatespec.ToolPin{}, Apt: []string{}})
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	return encoded
}

func templateReference(envID, templateID string) templateregistry.Reference {
	return templateregistry.Reference{
		Image:    "127.0.0.1:7880/studio/" + envID + "/" + templateID + "@sha256:" + strings.Repeat("e", 64),
		Username: envID, Password: "registry-password-test-only",
	}
}

func TestCreateFromTemplateUsesPinnedResourcesAndHostOnlyImageCredentials(t *testing.T) {
	f := newCheckpointFixture(t)
	wantResources := resources.Resources{CPUs: 3, MemoryMiB: 3072, MaxMemoryMiB: 4096, WorkspaceMiB: 8192, DockerMiB: 12288}
	template := readyTemplateForManager(t, f, canonicalTemplateSpec(t, wantResources), "linux/"+goruntime.GOARCH, templateimage.ExporterVersion)
	resolver := &templateResolverFake{reference: templateReference(f.env.ID, template.ID)}
	resolver.onResolve = func(envID, templateID string) {
		rows, err := f.store.Sandboxes(f.ctx, envID)
		if err != nil {
			t.Errorf("list sandboxes during resolve: %v", err)
			return
		}
		for _, row := range rows {
			if row.Name == "from-template" && row.TemplateID == templateID {
				return
			}
		}
		t.Error("registry Resolve ran before the sandbox pinned its template")
	}
	rt := &templateRecordingRuntime{SandboxRuntime: f.runtime}
	f.manager.Runtime, f.manager.Templates = rt, resolver

	view, err := f.manager.CreateFromTemplate(f.ctx, f.env.ID, template.ID, "from-template")
	if err != nil {
		t.Fatalf("CreateFromTemplate: %v", err)
	}
	if view.TemplateID != template.ID || view.ID == f.sandbox.ID || resolver.calls != 1 {
		t.Fatalf("created view=%+v resolver calls=%d", view, resolver.calls)
	}
	if got := (resources.Resources{CPUs: int64(view.CPUs), MemoryMiB: int64(view.MemoryMiB), MaxMemoryMiB: int64(view.MaxMemoryMiB), WorkspaceMiB: int64(view.WorkspaceMiB), DockerMiB: int64(view.DockerMiB)}); got != wantResources {
		t.Fatalf("persisted resources = %+v, want %+v", got, wantResources)
	}
	if rt.spec.CPUs != uint8(wantResources.CPUs) || rt.spec.MemoryMiB != uint32(wantResources.MemoryMiB) ||
		rt.spec.MaxMemoryMiB != uint32(wantResources.MaxMemoryMiB) || rt.spec.WorkspaceMiB != uint32(wantResources.WorkspaceMiB) ||
		rt.spec.DockerMiB != uint32(wantResources.DockerMiB) || rt.spec.Image == nil {
		t.Fatalf("runtime spec = %+v", rt.spec)
	}
	ref := resolver.reference
	if rt.spec.Image.Reference != ref.Image || rt.spec.Image.Username != f.env.ID || rt.spec.Image.Password != ref.Password {
		t.Fatalf("runtime received wrong private image source: %+v", rt.spec.Image)
	}
	if rt.labels["studio.sandbox-id"] != view.ID || rt.labels["studio.environment-id"] != f.env.ID ||
		rt.labels["studio.sandbox-name"] != "from-template" || rt.labels["studio.template-id"] != template.ID {
		t.Fatalf("runtime labels = %+v", rt.labels)
	}
	serialized, err := json.Marshal(rt.spec)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(serialized), ref.Password) || strings.Contains(string(serialized), ref.Image) {
		t.Fatalf("private image fields serialized in runtime spec: %s", serialized)
	}
}

func TestCreateFromTemplateRejectsInvalidMetadataBeforeSideEffects(t *testing.T) {
	f := newCheckpointFixture(t)
	resourcesSpec := canonicalTemplateSpec(t, resources.Defaults())
	validPlatform := "linux/" + goruntime.GOARCH
	invalidPlatform := "linux/amd64"
	if goruntime.GOARCH == "amd64" {
		invalidPlatform = "linux/arm64"
	}
	valid := readyTemplateForManager(t, f, resourcesSpec, validPlatform, templateimage.ExporterVersion)
	corrupt := readyTemplateForManager(t, f, []byte(`{"formatVersion":1,"unknown":true}`), validPlatform, templateimage.ExporterVersion)
	wrongPlatform := readyTemplateForManager(t, f, resourcesSpec, invalidPlatform, templateimage.ExporterVersion)
	oldExporter := readyTemplateForManager(t, f, resourcesSpec, validPlatform, "0")
	resolver := &templateResolverFake{reference: templateReference(f.env.ID, valid.ID)}
	rt := &templateRecordingRuntime{SandboxRuntime: f.runtime}
	egress := &resourceCountingEgress{}
	f.manager.Runtime, f.manager.Templates, f.manager.Egress = rt, resolver, egress
	before, err := f.store.AllSandboxes(f.ctx)
	if err != nil {
		t.Fatal(err)
	}

	for _, template := range []store.Template{corrupt, wrongPlatform, oldExporter} {
		if _, err := f.manager.CreateFromTemplate(f.ctx, f.env.ID, template.ID, "invalid-"+template.ID); err == nil {
			t.Errorf("CreateFromTemplate(%s) unexpectedly succeeded", template.ID)
		}
	}
	after, err := f.store.AllSandboxes(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) || resolver.calls != 0 || rt.name != "" || egress.attachCalls != 0 {
		t.Fatalf("invalid templates had side effects: rows %d -> %d, resolve=%d, runtime=%q, egress=%d", len(before), len(after), resolver.calls, rt.name, egress.attachCalls)
	}
}

func TestCreateFromTemplateSanitizesProviderErrorsAndCleansUnallocatedPin(t *testing.T) {
	f := newCheckpointFixture(t)
	template := readyTemplateForManager(t, f, canonicalTemplateSpec(t, resources.Defaults()), "linux/"+goruntime.GOARCH, templateimage.ExporterVersion)
	secret := "registry-password-test-only"
	image := templateReference(f.env.ID, template.ID)
	resolver := &templateResolverFake{err: errors.New("provider auth failed for " + secret + " at " + image.Image)}
	rt := &templateRecordingRuntime{SandboxRuntime: f.runtime}
	f.manager.Runtime, f.manager.Templates = rt, resolver
	before, _ := f.store.AllSandboxes(f.ctx)

	_, err := f.manager.CreateFromTemplate(f.ctx, f.env.ID, template.ID, "provider-error")
	if err == nil || strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), image.Image) {
		t.Fatalf("provider error was not safely reported: %v", err)
	}
	after, listErr := f.store.AllSandboxes(f.ctx)
	if listErr != nil || len(after) != len(before) || rt.name != "" {
		t.Fatalf("unallocated template attempt left state: rows=%d listErr=%v runtime=%q", len(after), listErr, rt.name)
	}
}

func TestCreateFromTemplateRejectsBaseReferenceFallback(t *testing.T) {
	f := newCheckpointFixture(t)
	template := readyTemplateForManager(t, f, canonicalTemplateSpec(t, resources.Defaults()), "linux/"+goruntime.GOARCH, templateimage.ExporterVersion)
	ref := templateReference(f.env.ID, template.ID)
	ref.Image = strings.Replace(ref.Image, "/"+template.ID+"@", "/base@", 1)
	rt := &templateRecordingRuntime{SandboxRuntime: f.runtime}
	f.manager.Runtime = rt
	f.manager.Templates = &templateResolverFake{reference: ref}
	before, _ := f.store.AllSandboxes(f.ctx)

	if _, err := f.manager.CreateFromTemplate(f.ctx, f.env.ID, template.ID, "no-base-fallback"); err == nil {
		t.Fatal("CreateFromTemplate accepted the mutable Studio base reference")
	}
	after, listErr := f.store.AllSandboxes(f.ctx)
	if listErr != nil || len(after) != len(before) || rt.name != "" {
		t.Fatalf("rejected base reference left state: rows=%d listErr=%v runtime=%q", len(after), listErr, rt.name)
	}
}

func TestCreateFromTemplateRetainsPinWhenOwnedCleanupFails(t *testing.T) {
	f := newCheckpointFixture(t)
	template := readyTemplateForManager(t, f, canonicalTemplateSpec(t, resources.Defaults()), "linux/"+goruntime.GOARCH, templateimage.ExporterVersion)
	ref := templateReference(f.env.ID, template.ID)
	rt := &templateRecordingRuntime{
		SandboxRuntime: f.runtime,
		createErr:      errors.New("pull failed for " + ref.Image + " with " + ref.Password),
		removeOwnedErr: errors.New("cleanup could not verify VM identity"),
	}
	f.manager.Runtime = rt
	f.manager.Templates = &templateResolverFake{reference: ref}

	_, err := f.manager.CreateFromTemplate(f.ctx, f.env.ID, template.ID, "cleanup-needed")
	if err == nil || strings.Contains(err.Error(), ref.Image) || strings.Contains(err.Error(), ref.Password) {
		t.Fatalf("boot error was not safely reported: %v", err)
	}
	rows, listErr := f.store.Sandboxes(f.ctx, f.env.ID)
	if listErr != nil {
		t.Fatal(listErr)
	}
	var retained store.Sandbox
	for _, row := range rows {
		if row.Name == "cleanup-needed" {
			retained = row
		}
	}
	if retained.ID == "" || retained.TemplateID != template.ID {
		t.Fatalf("failed cleanup did not retain the public template pin: %+v", retained)
	}
	if len(rt.removeOwnedCalls) != 1 {
		t.Fatalf("RemoveOwned calls = %d, want one", len(rt.removeOwnedCalls))
	}
	owned := rt.removeOwnedCalls[0]
	if owned.Name != VMName(retained) || owned.Labels["studio.sandbox-id"] != retained.ID ||
		owned.Labels["studio.environment-id"] != f.env.ID || owned.Labels["studio.sandbox-name"] != retained.Name ||
		owned.Labels["studio.template-id"] != template.ID {
		t.Fatalf("cleanup ownership = %+v", owned)
	}
}

func TestCreateFromTemplateDropsPinOnlyAfterOwnedCleanupConfirmsAbsence(t *testing.T) {
	f := newCheckpointFixture(t)
	template := readyTemplateForManager(t, f, canonicalTemplateSpec(t, resources.Defaults()), "linux/"+goruntime.GOARCH, templateimage.ExporterVersion)
	rt := &templateRecordingRuntime{SandboxRuntime: f.runtime, createErr: errors.New("boot failed")}
	f.manager.Runtime = rt
	f.manager.Templates = &templateResolverFake{reference: templateReference(f.env.ID, template.ID)}

	if _, err := f.manager.CreateFromTemplate(f.ctx, f.env.ID, template.ID, "cleanup-complete"); err == nil {
		t.Fatal("CreateFromTemplate succeeded despite a boot failure")
	}
	rows, err := f.store.Sandboxes(f.ctx, f.env.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Name == "cleanup-complete" {
			t.Fatalf("catalog row remained after verified VM removal: %+v", row)
		}
	}
	if len(rt.removeOwnedCalls) != 1 {
		t.Fatalf("RemoveOwned calls = %d, want one", len(rt.removeOwnedCalls))
	}
}
