package sandboxes

import (
	"bytes"
	"errors"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/resources"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/templatespec"
)

// A sandbox's home disk has the size it was created with, 4 GiB unless the request or the
// template names another, and keeps it through a fork and a rebase.
func TestHomeDiskSize(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		f := newCheckpointFixture(t)
		rt := &templateRecordingRuntime{SandboxRuntime: f.runtime}
		f.manager.Runtime = rt
		f.runtime.guestBoots = true
		view, err := f.manager.Create(f.ctx, f.env.ID, CreateRequest{Name: "big-home", HomeMiB: 8192})
		if err != nil {
			t.Fatal(err)
		}
		if err := f.manager.Wait(f.ctx, view.ID); err != nil {
			t.Fatal(err)
		}
		if rt.spec.HomeMiB != 8192 || view.HomeMiB != 8192 {
			t.Fatalf("spec home = %d, view home = %d", rt.spec.HomeMiB, view.HomeMiB)
		}
		if _, err := f.manager.Create(f.ctx, f.env.ID, CreateRequest{Name: "tiny-home", HomeMiB: 100}); !errors.Is(err, resources.ErrInvalid) {
			t.Fatalf("a 100 MiB home: err = %v", err)
		}
	})

	t.Run("template", func(t *testing.T) {
		f := newCheckpointFixture(t)
		res := rebaseResources
		res.HomeMiB = 12288
		view, rt := createFromTemplate(t, f, rebaseTemplate(t, f, res), "from-template")
		if rt.spec.HomeMiB != 12288 || view.HomeMiB != 12288 {
			t.Fatalf("spec home = %d, view home = %d", rt.spec.HomeMiB, view.HomeMiB)
		}
	})

	t.Run("fork", func(t *testing.T) {
		f := newCheckpointFixture(t)
		tf := newTransferFake(f)
		tf.guestBoots = true
		setHomeMiB(t, f, 6144)
		tf.setHome(VMName(f.sandbox), "dotfiles")
		answered, err := f.manager.Fork(f.ctx, f.env.ID, f.sandbox.ID, "copy")
		if err != nil {
			t.Fatal(err)
		}
		if err := f.manager.Wait(f.ctx, answered.ID); err != nil {
			t.Fatal(err)
		}
		fork, err := f.store.Sandbox(f.ctx, f.env.ID, answered.ID)
		if err != nil {
			t.Fatal(err)
		}
		if fork.HomeMiB != 6144 || tf.specs[VMName(fork)].HomeMiB != 6144 || tf.home(VMName(fork)) != "dotfiles" {
			t.Fatalf("fork home = %d, spec %d, %q", fork.HomeMiB, tf.specs[VMName(fork)].HomeMiB, tf.home(VMName(fork)))
		}
	})

	t.Run("rebase", func(t *testing.T) {
		f := newCheckpointFixture(t)
		tf := newTransferFake(f)
		tf.guestBoots = true
		setHomeMiB(t, f, 6144)
		// A template that names no home size keeps the sandbox's...
		view, err := rebaseNow(f, f.sandbox.ID, rebaseTemplate(t, f, rebaseResources).ID)
		if err != nil {
			t.Fatal(err)
		}
		if view.HomeMiB != 6144 || tf.specs[VMName(view.Sandbox)].HomeMiB != 6144 {
			t.Fatalf("rebased home = %d, spec %d", view.HomeMiB, tf.specs[VMName(view.Sandbox)].HomeMiB)
		}
		// ...and one that names it gives its own.
		res := rebaseResources
		res.HomeMiB = 2048
		if view, err = rebaseNow(f, f.sandbox.ID, rebaseTemplate(t, f, res).ID); err != nil {
			t.Fatal(err)
		}
		if view.HomeMiB != 2048 || tf.specs[VMName(view.Sandbox)].HomeMiB != 2048 {
			t.Fatalf("rebased home = %d, spec %d", view.HomeMiB, tf.specs[VMName(view.Sandbox)].HomeMiB)
		}
	})
}

// A home that doesn't fit the target's home disk fails the copy at that disk's size.
func TestHomeCopyStopsAtTheHomeDisk(t *testing.T) {
	f := newCheckpointFixture(t)
	tf := newTransferFake(f)
	src, dst := runtime.OwnedVM{Name: "src"}, runtime.OwnedVM{Name: "dst"}
	tf.setStatus("src", runtime.StatusRunning)
	tf.setStatus("dst", runtime.StatusRunning)
	tf.setHome("src", string(make([]byte, 1<<20+1)))
	if err := copyTrees(f.ctx, tf, src, dst, 1024, 1); !errors.Is(err, ErrHomeTooLarge) {
		t.Fatalf("err = %v, want ErrHomeTooLarge", err)
	}
	if err := copyTrees(f.ctx, tf, src, dst, 1024, 2); err != nil {
		t.Fatalf("a 2 MiB home disk: %v", err)
	}
}

// An export of a sandbox whose resources moved away from its template's, as a resize does,
// imports with the export's resources, its home disk's size among them.
func TestImportKeepsResourcesThatDifferFromTheTemplate(t *testing.T) {
	f, tf, builds := newImportFixture(t)
	template := rebaseTemplate(t, f, rebaseResources)
	builds.set(store.BuildJob{ID: "job-1", Status: store.BuildReady, TemplateID: template.ID})
	spec, _ := templatespec.ParseCanonicalJSON([]byte(template.Spec))
	source, err := templatespec.YAML(spec)
	if err != nil {
		t.Fatal(err)
	}
	resized := rebaseResources
	resized.CPUs, resized.WorkspaceMiB, resized.HomeMiB = 4, 8192, 8192
	m := ExportManifest{Format: ExportFormat, Name: "resized", TemplateSpecYAML: new(string), Resources: resized}
	*m.TemplateSpecYAML = string(source)
	file := exportFile(t, m, "archive")

	imp, err := f.manager.Import(f.ctx, f.env.ID, bytes.NewReader(file), int64(len(file)))
	if err != nil {
		t.Fatal(err)
	}
	done := waitImport(t, f, imp.ID)
	if done.State != store.ImportReady {
		t.Fatalf("import = %+v", done)
	}
	sb, err := f.store.Sandbox(f.ctx, f.env.ID, done.SandboxID)
	if err != nil {
		t.Fatal(err)
	}
	vm := VMName(sb)
	if sb.TemplateID != template.ID || sb.CPUs != 4 || sb.WorkspaceMiB != 8192 || sb.HomeMiB != 8192 || tf.specs[vm].HomeMiB != 8192 || tf.home(vm) != "home" {
		t.Fatalf("sandbox = %+v, spec %+v, home %q", sb, tf.specs[vm], tf.home(vm))
	}
}

// setHomeMiB makes the fixture's sandbox one with a home disk of mib.
func setHomeMiB(t *testing.T, f *checkpointFixture, mib int) {
	t.Helper()
	sb, err := f.store.CreateSandbox(f.ctx, store.Sandbox{EnvironmentID: f.env.ID, Name: "home-box", CPUs: 1, MemoryMiB: 512, WorkspaceMiB: 1024, DockerMiB: 1024, HomeMiB: mib})
	if err != nil {
		t.Fatal(err)
	}
	f.runtime.setStatus(VMName(sb), runtime.StatusStopped)
	f.sandbox = sb
}

// An export names the home's size where it isn't the default, and an import of it accepts a
// home archive up to that size.
func TestExportNamesABiggerHome(t *testing.T) {
	f := newCheckpointFixture(t)
	tf := newTransferFake(f)
	setHomeMiB(t, f, 8192)
	tf.setStatus(VMName(f.sandbox), runtime.StatusRunning)
	tf.setHome(VMName(f.sandbox), "dotfiles")
	_, manifest := exportToBuffer(t, f, f.sandbox.ID)
	if manifest.Resources.HomeMiB != 8192 || manifest.HomeLimit() != 8192<<20 {
		t.Fatalf("manifest resources = %+v, home limit %d", manifest.Resources, manifest.HomeLimit())
	}
}
