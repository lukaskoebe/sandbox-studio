package sandboxes

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/resources"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/templatespec"
)

func exportToBuffer(t *testing.T, f *checkpointFixture, id string) ([]byte, ExportManifest) {
	t.Helper()
	var buf bytes.Buffer
	var got ExportManifest
	err := f.manager.Export(f.ctx, f.env.ID, id, func(m ExportManifest) (io.Writer, error) {
		got = m
		return &buf, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), got
}

func TestExportWritesHeaderAndWorkspace(t *testing.T) {
	for _, running := range []bool{true, false} {
		t.Run(fmt.Sprint("running=", running), func(t *testing.T) {
			f := newCheckpointFixture(t)
			tf := newTransferFake(f)
			source := VMName(f.sandbox)
			if running {
				tf.setStatus(source, runtime.StatusRunning)
			}
			tf.setWorkspace(source, "workspace-archive")

			file, _ := exportToBuffer(t, f, f.sandbox.ID)
			r := bytes.NewReader(file)
			manifest, err := ReadExportHeader(r)
			if err != nil {
				t.Fatal(err)
			}
			rest, _ := io.ReadAll(r)
			if string(rest) != "workspace-archive" {
				t.Fatalf("archive = %q", rest)
			}
			want := resources.Resources{CPUs: 1, MemoryMiB: 512, MaxMemoryMiB: 512, WorkspaceMiB: 1024, DockerMiB: 1024}
			if manifest.Name != f.sandbox.Name || manifest.Resources != want || manifest.TemplateSpecYAML != nil || manifest.WorkspaceBytes != 0 {
				t.Fatalf("manifest = %+v", manifest)
			}
			wantStatus := runtime.StatusStopped
			if running {
				wantStatus = runtime.StatusRunning
			}
			if status, _ := tf.Status(f.ctx, source); status != wantStatus {
				t.Fatalf("source is %s, want %s", status, wantStatus)
			}
			if running != (tf.countCall("start-transfer:"+source) == 0) {
				t.Fatalf("start-transfer calls = %d", tf.countCall("start-transfer:"+source))
			}
		})
	}
}

func TestExportIncludesTemplateSpecThatReparses(t *testing.T) {
	f := newCheckpointFixture(t)
	tf := newTransferFake(f)
	template := rebaseTemplate(t, f, rebaseResources)
	sb, err := f.store.CreateSandbox(f.ctx, store.Sandbox{EnvironmentID: f.env.ID, TemplateID: template.ID, Name: "templated",
		CPUs: 2, MemoryMiB: 1024, MaxMemoryMiB: 2048, WorkspaceMiB: 4096, DockerMiB: 2048})
	if err != nil {
		t.Fatal(err)
	}
	tf.setStatus(VMName(sb), runtime.StatusRunning)
	_, manifest := exportToBuffer(t, f, sb.ID)
	if manifest.TemplateSpecYAML == nil {
		t.Fatal("no template spec")
	}
	spec, err := templatespec.ParseYAML([]byte(*manifest.TemplateSpecYAML))
	if err != nil {
		t.Fatal(err)
	}
	canonical, _ := templatespec.CanonicalJSON(spec)
	if string(canonical) != template.Spec || manifest.Resources != rebaseResources {
		t.Fatalf("spec %s, want %s; resources %+v", canonical, template.Spec, manifest.Resources)
	}
}

func TestExportRefusesBusySandboxAndStopsAfterFailure(t *testing.T) {
	f := newCheckpointFixture(t)
	tf := newTransferFake(f)
	unlock, err := f.manager.tryMutation(f.sandbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	opened := false
	open := func(ExportManifest) (io.Writer, error) { opened = true; return io.Discard, nil }
	if err := f.manager.Export(f.ctx, f.env.ID, f.sandbox.ID, open); !errors.Is(err, ErrBusy) || opened {
		t.Fatalf("busy export: err = %v, opened %v", err, opened)
	}
	unlock()

	tf.fail("export:"+VMName(f.sandbox), errors.New("injected"))
	if err := f.manager.Export(f.ctx, f.env.ID, f.sandbox.ID, open); err == nil {
		t.Fatal("failed export succeeded")
	}
	if status, _ := tf.Status(f.ctx, VMName(f.sandbox)); status != runtime.StatusStopped {
		t.Fatalf("source is %s after a failed export", status)
	}
}

// --- import -----------------------------------------------------------------------

type fakeBuilds struct {
	mu        sync.Mutex
	job       store.BuildJob
	submitted []string
}

func (b *fakeBuilds) Submit(_ context.Context, envID, source string) (store.BuildJob, bool, error) {
	if _, err := templatespec.ParseYAML([]byte(source)); err != nil {
		return store.BuildJob{}, false, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.submitted = append(b.submitted, source)
	job := b.job
	job.EnvironmentID = envID
	return job, true, nil
}

func (b *fakeBuilds) Job(context.Context, string, string) (store.BuildJob, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.job, nil
}

func (b *fakeBuilds) set(job store.BuildJob) {
	b.mu.Lock()
	b.job = job
	b.mu.Unlock()
}

func newImportFixture(t *testing.T) (*checkpointFixture, *transferFake, *fakeBuilds) {
	t.Helper()
	f := newCheckpointFixture(t)
	tf := newTransferFake(f)
	builds := &fakeBuilds{}
	f.manager.Builds = builds
	f.manager.importPoll = time.Millisecond
	f.manager.diskFree = func(string) (uint64, error) { return 1 << 50, nil }
	return f, tf, builds
}

func exportFile(t *testing.T, m ExportManifest, archive string) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := WriteExportHeader(&buf, m); err != nil {
		t.Fatal(err)
	}
	buf.WriteString(archive)
	return buf.Bytes()
}

func plainManifest(name string) ExportManifest {
	return ExportManifest{Format: ExportFormat, Name: name,
		Resources: resources.Resources{CPUs: 1, MemoryMiB: 512, MaxMemoryMiB: 512, WorkspaceMiB: 1024, DockerMiB: 1024}}
}

// waitImport waits until an import is finished and its goroutine has cleaned up.
func waitImport(t *testing.T, f *checkpointFixture, id string) store.SandboxImport {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		imp, err := f.manager.SandboxImport(f.ctx, f.env.ID, id)
		if err != nil {
			t.Fatal(err)
		}
		f.manager.imports.mu.Lock()
		_, running := f.manager.imports.cancels[id]
		f.manager.imports.mu.Unlock()
		if (imp.State == store.ImportReady || imp.State == store.ImportFailed) && !running {
			return imp
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("import did not finish")
	return store.SandboxImport{}
}

func stagedFiles(t *testing.T, f *checkpointFixture) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(f.paths.Imports())
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestImportWithoutTemplateRecreatesSandbox(t *testing.T) {
	f, tf, builds := newImportFixture(t)
	file := exportFile(t, plainManifest("checkpoint-box"), "workspace-archive")

	imp, err := f.manager.Import(f.ctx, f.env.ID, bytes.NewReader(file), int64(len(file)))
	if err != nil {
		t.Fatal(err)
	}
	if imp.State != store.ImportBuilding {
		t.Fatalf("state = %s", imp.State)
	}
	done := waitImport(t, f, imp.ID)
	if done.State != store.ImportReady || done.SandboxID == "" {
		t.Fatalf("import = %+v", done)
	}
	sb, err := f.store.Sandbox(f.ctx, f.env.ID, done.SandboxID)
	if err != nil {
		t.Fatal(err)
	}
	// The exported name is taken by the fixture sandbox, so the import gets a suffix.
	if sb.Name != "checkpoint-box-2" || done.Name != sb.Name || sb.TemplateID != "" || sb.CPUs != 1 || sb.WorkspaceMiB != 1024 {
		t.Fatalf("sandbox = %+v", sb)
	}
	vm := VMName(sb)
	if tf.workspace(vm) != "workspace-archive" || !tf.booted[vm] || tf.specs[vm].Image != nil {
		t.Fatalf("workspace %q, booted %v, image %+v", tf.workspace(vm), tf.booted[vm], tf.specs[vm].Image)
	}
	if len(builds.submitted) != 0 || len(stagedFiles(t, f)) != 0 {
		t.Fatalf("builds %d, staged files %v", len(builds.submitted), stagedFiles(t, f))
	}
}

func TestImportWithCachedTemplate(t *testing.T) {
	f, tf, builds := newImportFixture(t)
	template := rebaseTemplate(t, f, rebaseResources)
	builds.set(store.BuildJob{ID: "job-1", Status: store.BuildQueued})
	spec, _ := templatespec.ParseCanonicalJSON([]byte(template.Spec))
	source, err := templatespec.YAML(spec)
	if err != nil {
		t.Fatal(err)
	}
	m := ExportManifest{Format: ExportFormat, Name: "fresh", TemplateSpecYAML: new(string), Resources: rebaseResources}
	*m.TemplateSpecYAML = string(source)
	file := exportFile(t, m, "archive")

	imp, err := f.manager.Import(f.ctx, f.env.ID, bytes.NewReader(file), -1)
	if err != nil {
		t.Fatal(err)
	}
	// The worker reports the cache hit by completing the job with the cached template.
	builds.set(store.BuildJob{ID: "job-1", Status: store.BuildReady, TemplateID: template.ID})
	done := waitImport(t, f, imp.ID)
	if done.State != store.ImportReady || done.BuildJobID != "job-1" {
		t.Fatalf("import = %+v", done)
	}
	sb, err := f.store.Sandbox(f.ctx, f.env.ID, done.SandboxID)
	if err != nil {
		t.Fatal(err)
	}
	vm := VMName(sb)
	if sb.Name != "fresh" || sb.TemplateID != template.ID || sb.MaxMemoryMiB != 2048 || tf.workspace(vm) != "archive" || tf.specs[vm].Image == nil {
		t.Fatalf("sandbox = %+v, workspace %q", sb, tf.workspace(vm))
	}
	if len(builds.submitted) != 1 || builds.submitted[0] != string(source) {
		t.Fatalf("submitted = %q", builds.submitted)
	}
}

func TestImportRejectsMalformedFiles(t *testing.T) {
	valid := exportFile(t, plainManifest("box"), "archive")
	header := func(body string) []byte {
		out := []byte(ExportMagic)
		out = binary.BigEndian.AppendUint32(out, uint32(len(body)))
		return append(append(out, body...), "archive"...)
	}
	manifestJSON := func(edit func(map[string]any)) string {
		var fields map[string]any
		raw, _ := json.Marshal(plainManifest("box"))
		_ = json.Unmarshal(raw, &fields)
		edit(fields)
		out, _ := json.Marshal(fields)
		return string(out)
	}
	oversize := []byte(ExportMagic)
	oversize = binary.BigEndian.AppendUint32(oversize, maxManifestBytes+1)
	badSpec := "resources:\n  cpus: 0\n"
	for name, file := range map[string][]byte{
		"empty":           nil,
		"bad magic":       append([]byte("sandbox-studio-export v2\n"), valid[len(ExportMagic):]...),
		"oversize length": oversize,
		"zero length":     header(""),
		"truncated":       valid[:exportHeaderPrefix+5],
		"not json":        header("{nope"),
		"trailing data":   header(manifestJSON(func(map[string]any) {}) + " {}"),
		"unknown field":   header(manifestJSON(func(m map[string]any) { m["secrets"] = map[string]string{"A": "b"} })),
		"wrong format":    header(manifestJSON(func(m map[string]any) { m["format"] = 2 })),
		"missing format":  header(manifestJSON(func(m map[string]any) { delete(m, "format") })),
		"bad name":        header(manifestJSON(func(m map[string]any) { m["name"] = "../etc" })),
		"bad resources":   header(manifestJSON(func(m map[string]any) { m["resources"].(map[string]any)["cpus"] = 0 })),
		"bad spec":        header(manifestJSON(func(m map[string]any) { m["templateSpecYAML"] = badSpec })),
		"spec mismatch":   header(manifestJSON(func(m map[string]any) { m["templateSpecYAML"] = "tools: {}\n" })),
		"negative bytes":  header(manifestJSON(func(m map[string]any) { m["workspaceBytes"] = -1 })),
		"length mismatch": header(manifestJSON(func(m map[string]any) { m["workspaceBytes"] = 3 })),
		"missing archive": valid[:len(valid)-len("archive")],
	} {
		t.Run(name, func(t *testing.T) {
			f, _, _ := newImportFixture(t)
			_, err := f.manager.Import(f.ctx, f.env.ID, bytes.NewReader(file), int64(len(file)))
			if !errors.Is(err, ErrInvalidExport) {
				t.Fatalf("err = %v, want ErrInvalidExport", err)
			}
			all, _ := f.store.AllSandboxes(f.ctx)
			if len(all) != 1 || len(stagedFiles(t, f)) != 0 {
				t.Fatalf("sandboxes %d, staged %v", len(all), stagedFiles(t, f))
			}
		})
	}
}

func TestStoreArchiveStopsAtLimit(t *testing.T) {
	f, _, _ := newImportFixture(t)
	blob := f.manager.importBlob("oversize")
	if err := storeArchive(blob, strings.NewReader("0123456789"), 9, 0); !errors.Is(err, ErrWorkspaceTooLarge) {
		t.Fatalf("err = %v, want ErrWorkspaceTooLarge", err)
	}
	if info, err := os.Stat(blob); err != nil || info.Size() > 10 {
		t.Fatalf("stat = %v, %v", info, err)
	}
	os.Remove(blob)
	if err := storeArchive(blob, strings.NewReader("0123456789"), 10, 0); err != nil {
		t.Fatal(err)
	}
}

func TestImportRejectsFullDisk(t *testing.T) {
	f, _, _ := newImportFixture(t)
	f.manager.diskFree = func(string) (uint64, error) { return 1 << 30, nil }
	file := exportFile(t, plainManifest("box"), "archive")
	if _, err := f.manager.Import(f.ctx, f.env.ID, bytes.NewReader(file), int64(len(file))); !errors.Is(err, ErrInsufficientDisk) {
		t.Fatalf("err = %v, want ErrInsufficientDisk", err)
	}
	active, _ := f.store.ActiveSandboxImports(f.ctx)
	if len(active) != 0 || len(stagedFiles(t, f)) != 0 {
		t.Fatalf("active %+v, staged %v", active, stagedFiles(t, f))
	}
}

func TestImportFailureRemovesHalfMadeSandbox(t *testing.T) {
	for _, step := range []string{"import", "boot"} {
		t.Run(step, func(t *testing.T) {
			f, tf, _ := newImportFixture(t)
			// The new sandbox's VM name is unknown up front, so fail every VM at this step.
			failing := &failAllTransfer{transferFake: tf, step: step}
			f.manager.Runtime = failing
			file := exportFile(t, plainManifest("box"), "archive")
			imp, err := f.manager.Import(f.ctx, f.env.ID, bytes.NewReader(file), int64(len(file)))
			if err != nil {
				t.Fatal(err)
			}
			done := waitImport(t, f, imp.ID)
			if done.State != store.ImportFailed || done.Error == "" || strings.Contains(done.Error, "/secret") {
				t.Fatalf("import = %+v", done)
			}
			all, _ := f.store.AllSandboxes(f.ctx)
			if len(all) != 1 || done.SandboxID != "" {
				t.Fatalf("sandboxes = %+v, import sandbox %q", all, done.SandboxID)
			}
			for name := range tf.specs {
				if tf.hasVM(name) {
					t.Fatalf("VM %s remains", name)
				}
			}
			if len(stagedFiles(t, f)) != 0 {
				t.Fatalf("staged files remain: %v", stagedFiles(t, f))
			}
		})
	}
}

type failAllTransfer struct {
	*transferFake
	step string
}

func (f *failAllTransfer) ImportWorkspace(ctx context.Context, owned runtime.OwnedVM, r io.Reader) error {
	if f.step == "import" {
		return errors.New("injected failure at /secret/host/path")
	}
	return f.transferFake.ImportWorkspace(ctx, owned, r)
}

func (f *failAllTransfer) Boot(ctx context.Context, name string) error {
	if f.step == "boot" {
		return errors.New("injected")
	}
	return f.transferFake.Boot(ctx, name)
}

func TestImportCancelRemovesSandbox(t *testing.T) {
	f, _, builds := newImportFixture(t)
	template := rebaseTemplate(t, f, rebaseResources)
	builds.set(store.BuildJob{ID: "job-1", Status: store.BuildQueued})
	spec, _ := templatespec.ParseCanonicalJSON([]byte(template.Spec))
	source, _ := templatespec.YAML(spec)
	m := ExportManifest{Format: ExportFormat, Name: "fresh", TemplateSpecYAML: new(string), Resources: rebaseResources}
	*m.TemplateSpecYAML = string(source)
	file := exportFile(t, m, "archive")
	imp, err := f.manager.Import(f.ctx, f.env.ID, bytes.NewReader(file), int64(len(file)))
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := f.manager.CancelImport(f.ctx, f.env.ID, imp.ID)
	if err != nil || cancelled.State != store.ImportFailed {
		t.Fatalf("cancel = %+v, %v", cancelled, err)
	}
	done := waitImport(t, f, imp.ID)
	if done.Error != "import was cancelled" {
		t.Fatalf("import = %+v", done)
	}
	if _, err := f.manager.CancelImport(f.ctx, f.env.ID, imp.ID); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("second cancel: err = %v", err)
	}
	all, _ := f.store.AllSandboxes(f.ctx)
	if len(all) != 1 || len(stagedFiles(t, f)) != 0 {
		t.Fatalf("sandboxes %d, staged %v", len(all), stagedFiles(t, f))
	}
}

func TestReconcileFailsInterruptedImports(t *testing.T) {
	f, tf, _ := newImportFixture(t)
	uploading, err := f.store.CreateSandboxImport(f.ctx, f.env.ID, "first")
	if err != nil {
		t.Fatal(err)
	}
	creating, err := f.store.CreateSandboxImport(f.ctx, f.env.ID, "second")
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{store.ImportBuilding, store.ImportCreating} {
		from := map[string]string{store.ImportBuilding: store.ImportUploading, store.ImportCreating: store.ImportBuilding}[state]
		if err := f.store.AdvanceSandboxImport(f.ctx, f.env.ID, creating.ID, from, state, ""); err != nil {
			t.Fatal(err)
		}
	}
	sb, err := f.store.CreateImportedSandbox(f.ctx, creating.ID, store.Sandbox{EnvironmentID: f.env.ID, Name: "second",
		CPUs: 1, MemoryMiB: 512, WorkspaceMiB: 1024, DockerMiB: 1024})
	if err != nil {
		t.Fatal(err)
	}
	tf.setStatus(VMName(sb), runtime.StatusRunning)
	if err := os.WriteFile(f.manager.importBlob(uploading.ID), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := f.manager.Reconcile(f.ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{uploading.ID, creating.ID} {
		imp, err := f.store.SandboxImport(f.ctx, f.env.ID, id)
		if err != nil || imp.State != store.ImportFailed || imp.Error != "Studio restarted during this import" {
			t.Fatalf("import = %+v, %v", imp, err)
		}
	}
	if _, err := f.store.Sandbox(f.ctx, f.env.ID, sb.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("half-made sandbox remains: %v", err)
	}
	if tf.hasVM(VMName(sb)) || len(stagedFiles(t, f)) != 0 {
		t.Fatalf("VM remains %v, staged %v", tf.hasVM(VMName(sb)), stagedFiles(t, f))
	}
}

func TestImportNameSuffix(t *testing.T) {
	long := strings.Repeat("a", 39) + "b"
	for _, tc := range []struct {
		name string
		try  int
		want string
	}{
		{"box", 1, "box"},
		{"box", 2, "box-2"},
		{long, 12, strings.Repeat("a", 37) + "-12"},
		{"ab-" + strings.Repeat("c", 37), 3, "ab-" + strings.Repeat("c", 35) + "-3"},
		{strings.Repeat("a", 37) + "-cc", 2, strings.Repeat("a", 37) + "-2"},
	} {
		got := importName(tc.name, tc.try)
		if got != tc.want || runtime.ValidName(got) != nil {
			t.Errorf("importName(%q, %d) = %q, want %q", tc.name, tc.try, got, tc.want)
		}
	}
}
