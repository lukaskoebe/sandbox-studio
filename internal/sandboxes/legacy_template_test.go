package sandboxes

import (
	"bytes"
	"encoding/binary"
	"errors"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/resources"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/templateimage"
	"github.com/lukaskoebe/sandbox-studio/internal/templatespec"
)

// legacyTemplate is a ready template built with exporter version 1. Its toolchains live in
// /home/agent and it has no home skeleton, so its sandboxes keep their home on the root layer.
func legacyTemplate(t *testing.T, f *checkpointFixture, res resources.Resources) store.Template {
	t.Helper()
	return readyTemplateForManager(t, f, canonicalTemplateSpec(t, res), "linux/"+goruntime.GOARCH, templateimage.LegacyExporterVersion)
}

// createFromTemplate makes a sandbox from template with a runtime that records the spec it
// is created with. Tests install the transfer fake afterwards for the copies that follow.
func createFromTemplate(t *testing.T, f *checkpointFixture, template store.Template, name string) (View, *templateRecordingRuntime) {
	t.Helper()
	f.runtime.guestBoots = true
	rt := &templateRecordingRuntime{SandboxRuntime: f.runtime}
	f.manager.Runtime, f.manager.Templates = rt, perTemplateResolver{}
	view, err := f.manager.CreateFromTemplate(f.ctx, f.env.ID, template.ID, name)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.manager.Wait(f.ctx, view.ID); err != nil {
		t.Fatal(err)
	}
	return view, rt
}

// callsWithPrefix counts the runtime calls that start with prefix.
func callsWithPrefix(f *checkpointFixture, prefix string) int {
	f.runtime.mu.Lock()
	defer f.runtime.mu.Unlock()
	n := 0
	for _, call := range f.runtime.calls {
		if strings.HasPrefix(call, prefix) {
			n++
		}
	}
	return n
}

// exportFileWithoutHome is an export of a sandbox without a home disk: format 2 with an
// empty home section, then the workspace archive.
func exportFileWithoutHome(t *testing.T, m ExportManifest, archive string) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := WriteExportHeader(&buf, m); err != nil {
		t.Fatal(err)
	}
	buf.Write(homeChunks(""))
	buf.WriteString(archive)
	return buf.Bytes()
}

func TestCreateFromLegacyTemplateHasNoHomeDisk(t *testing.T) {
	f := newCheckpointFixture(t)
	view, rt := createFromTemplate(t, f, legacyTemplate(t, f, rebaseResources), "legacy-box")
	if rt.spec.HomeMiB != 0 || rt.spec.Image == nil || view.TemplateID == "" {
		t.Fatalf("legacy sandbox spec home = %d, image = %v", rt.spec.HomeMiB, rt.spec.Image)
	}

	view, rt = createFromTemplate(t, f, rebaseTemplate(t, f, rebaseResources), "current-box")
	if rt.spec.HomeMiB != HomeMiB || view.TemplateID == "" {
		t.Fatalf("current sandbox spec home = %d, want %d", rt.spec.HomeMiB, HomeMiB)
	}
}

func TestForkOfLegacySandboxHasNoHomeDisk(t *testing.T) {
	f := newCheckpointFixture(t)
	source, _ := createFromTemplate(t, f, legacyTemplate(t, f, rebaseResources), "legacy-box")
	tf := newTransferFake(f)
	tf.guestBoots = true
	tf.setWorkspace(VMName(source.Sandbox), "files")
	tf.setHome(VMName(source.Sandbox), "root home")

	fork, err := f.manager.Fork(f.ctx, f.env.ID, source.ID, "legacy-fork")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.manager.Wait(f.ctx, fork.ID); err != nil {
		t.Fatal(err)
	}
	vm := VMName(fork.Sandbox)
	if tf.specs[vm].HomeMiB != 0 || tf.workspace(vm) != "files" || tf.home(vm) != "" || !tf.booted[vm] {
		t.Fatalf("fork home disk = %d, workspace %q, home %q, booted %v", tf.specs[vm].HomeMiB, tf.workspace(vm), tf.home(vm), tf.booted[vm])
	}
	if callsWithPrefix(f, "export-home:") != 0 {
		t.Fatal("the root home of a legacy sandbox was copied into a fork without a home disk")
	}
}

func TestRebaseLegacySandboxOntoCurrentTemplateCopiesRootHome(t *testing.T) {
	f := newCheckpointFixture(t)
	source, _ := createFromTemplate(t, f, legacyTemplate(t, f, rebaseResources), "legacy-box")
	tf := newTransferFake(f)
	tf.guestBoots = true
	tf.setWorkspace(VMName(source.Sandbox), "files")
	tf.setHome(VMName(source.Sandbox), "root home")
	current := rebaseTemplate(t, f, rebaseResources)

	view, err := f.manager.Rebase(f.ctx, f.env.ID, source.ID, current.ID)
	if err != nil {
		t.Fatal(err)
	}
	target := VMName(view.Sandbox)
	if view.TemplateID != current.ID || tf.specs[target].HomeMiB != HomeMiB || tf.workspace(target) != "files" ||
		tf.home(target) != "root home" || !tf.booted[target] {
		t.Fatalf("rebased home disk = %d, workspace %q, home %q, booted %v", tf.specs[target].HomeMiB, tf.workspace(target), tf.home(target), tf.booted[target])
	}
}

func TestRebaseLegacySandboxOntoLegacyTemplateKeepsItsShape(t *testing.T) {
	f := newCheckpointFixture(t)
	source, _ := createFromTemplate(t, f, legacyTemplate(t, f, rebaseResources), "legacy-box")
	tf := newTransferFake(f)
	tf.guestBoots = true
	tf.setWorkspace(VMName(source.Sandbox), "files")
	tf.setHome(VMName(source.Sandbox), "root home")
	other := legacyTemplate(t, f, resources.Resources{CPUs: 1, MemoryMiB: 1024, MaxMemoryMiB: 1024, WorkspaceMiB: 4096, DockerMiB: 2048})

	view, err := f.manager.Rebase(f.ctx, f.env.ID, source.ID, other.ID)
	if err != nil {
		t.Fatal(err)
	}
	target := VMName(view.Sandbox)
	if tf.specs[target].HomeMiB != 0 || tf.workspace(target) != "files" || tf.home(target) != "" {
		t.Fatalf("rebased home disk = %d, workspace %q, home %q", tf.specs[target].HomeMiB, tf.workspace(target), tf.home(target))
	}
}

func TestRebaseSandboxWithHomeDiskOntoLegacyTemplateIsRefused(t *testing.T) {
	f := newCheckpointFixture(t)
	tf := newTransferFake(f)
	tf.guestBoots = true
	tf.setStatus(VMName(f.sandbox), runtime.StatusRunning)
	tf.setHome(VMName(f.sandbox), "opencode sessions")
	legacy := legacyTemplate(t, f, rebaseResources)

	if _, err := f.manager.Rebase(f.ctx, f.env.ID, f.sandbox.ID, legacy.ID); !errors.Is(err, ErrTemplateWithoutHome) {
		t.Fatalf("Rebase error = %v, want ErrTemplateWithoutHome", err)
	}
	after, err := f.store.Sandbox(f.ctx, f.env.ID, f.sandbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	ops, err := f.store.Rebases(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Generation != f.sandbox.Generation || after.TemplateID != f.sandbox.TemplateID || len(ops) != 0 ||
		callsWithPrefix(f, "create-transfer:") != 0 || tf.home(VMName(f.sandbox)) != "opencode sessions" {
		t.Fatalf("refused rebase changed the sandbox: generation %d, template %q, rebases %d", after.Generation, after.TemplateID, len(ops))
	}
}

func TestExportOfLegacySandboxHasEmptyHomeSection(t *testing.T) {
	f := newCheckpointFixture(t)
	source, _ := createFromTemplate(t, f, legacyTemplate(t, f, rebaseResources), "legacy-box")
	tf := newTransferFake(f)
	tf.guestBoots = true
	tf.setWorkspace(VMName(source.Sandbox), "files")
	tf.setHome(VMName(source.Sandbox), "root home")

	file, manifest := exportToBuffer(t, f, source.ID)
	if manifest.Format != ExportFormat {
		t.Fatalf("format = %d, want %d", manifest.Format, ExportFormat)
	}
	headerEnd := exportHeaderPrefix + int(binary.BigEndian.Uint32(file[len(ExportMagic):]))
	if !bytes.Equal(file[headerEnd:headerEnd+4], []byte{0, 0, 0, 0}) || string(file[headerEnd+4:]) != "files" {
		t.Fatalf("home section and workspace = %q", file[headerEnd:])
	}
	if callsWithPrefix(f, "export-home:") != 0 {
		t.Fatal("the root home of a legacy sandbox was exported")
	}
}

func TestImportOfExportWithoutHomeTakesHomeDiskFromTemplate(t *testing.T) {
	f, tf, builds := newImportFixture(t)
	legacy := legacyTemplate(t, f, rebaseResources)
	current := rebaseTemplate(t, f, rebaseResources)
	spec, err := templatespec.ParseCanonicalJSON([]byte(legacy.Spec))
	if err != nil {
		t.Fatal(err)
	}
	source, err := templatespec.YAML(spec)
	if err != nil {
		t.Fatal(err)
	}
	m := ExportManifest{Format: ExportFormat, Name: "fresh", TemplateSpecYAML: new(string), Resources: rebaseResources}
	*m.TemplateSpecYAML = string(source)

	for _, tc := range []struct {
		name     string
		template store.Template
		job      string
		home     uint32
	}{
		{name: "legacy", template: legacy, job: "job-legacy", home: 0},
		{name: "current", template: current, job: "job-current", home: HomeMiB},
	} {
		builds.set(store.BuildJob{ID: tc.job, Status: store.BuildReady, TemplateID: tc.template.ID})
		imp, err := f.manager.Import(f.ctx, f.env.ID, bytes.NewReader(exportFileWithoutHome(t, m, "archive")), -1)
		if err != nil {
			t.Fatal(err)
		}
		done := waitImport(t, f, imp.ID)
		if done.State != store.ImportReady {
			t.Fatalf("%s import = %+v", tc.name, done)
		}
		sb, err := f.store.Sandbox(f.ctx, f.env.ID, done.SandboxID)
		if err != nil {
			t.Fatal(err)
		}
		vm := VMName(sb)
		if tf.specs[vm].HomeMiB != tc.home || tf.workspace(vm) != "archive" || tf.home(vm) != "" {
			t.Fatalf("%s import home disk = %d, workspace %q, home %q", tc.name, tf.specs[vm].HomeMiB, tf.workspace(vm), tf.home(vm))
		}
	}
}

func TestImportWithHomeIntoLegacyTemplateFails(t *testing.T) {
	f, _, builds := newImportFixture(t)
	legacy := legacyTemplate(t, f, rebaseResources)
	spec, err := templatespec.ParseCanonicalJSON([]byte(legacy.Spec))
	if err != nil {
		t.Fatal(err)
	}
	source, err := templatespec.YAML(spec)
	if err != nil {
		t.Fatal(err)
	}
	m := ExportManifest{Format: ExportFormat, Name: "fresh", TemplateSpecYAML: new(string), Resources: rebaseResources}
	*m.TemplateSpecYAML = string(source)
	builds.set(store.BuildJob{ID: "job-1", Status: store.BuildReady, TemplateID: legacy.ID})

	imp, err := f.manager.Import(f.ctx, f.env.ID, bytes.NewReader(exportFile(t, m, "archive")), -1)
	if err != nil {
		t.Fatal(err)
	}
	done := waitImport(t, f, imp.ID)
	if done.State != store.ImportFailed || !strings.Contains(done.Error, "home directory") {
		t.Fatalf("import = %+v", done)
	}
	if all, err := f.store.AllSandboxes(f.ctx); err != nil || len(all) != 1 {
		t.Fatalf("sandboxes after the failed import = %d, %v; want only the fixture's", len(all), err)
	}
}
