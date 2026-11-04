package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

type fakeVMs struct {
	all                []string
	listErr, removeErr error
	stopped, removed   []string
	checkpointsRemoved []string
}

func (f *fakeVMs) names(context.Context) ([]string, error) { return f.all, f.listErr }
func (f *fakeVMs) stop(_ context.Context, name string) error {
	f.stopped = append(f.stopped, name)
	return nil
}
func (f *fakeVMs) remove(_ context.Context, name string) error {
	if f.removeErr != nil {
		return f.removeErr
	}
	f.removed = append(f.removed, name)
	return nil
}
func (f *fakeVMs) removeCheckpoints(_ context.Context, ids []string) error {
	f.checkpointsRemoved = ids
	return nil
}

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newInstall is an installation in a temporary directory with a sandbox and a VM of Studio's.
func newInstall(t *testing.T) (*uninstaller, *fakeVMs, *bool) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	vms := &fakeVMs{all: []string{"ss-abc-g1", "ss-prewarm-x"}}
	entry := false
	u := &uninstaller{
		exe: filepath.Join(root, "bin", "studio"), data: filepath.Join(root, "data"), msbHome: filepath.Join(root, "msb"),
		vms: vms, sandboxes: []string{"dev"}, ids: []string{"abc"},
		lockMSB:    func() (func() error, error) { return func() error { return nil }, nil },
		entries:    []loginEntry{{"the login entry", func() error { entry = true; return nil }}},
		running:    func() bool { return false },
		stopStudio: func(context.Context) error { return nil },
		closeApps:  func() {},
	}
	touch(t, u.exe)
	touch(t, filepath.Join(root, "bin", "studio-app"))
	touch(t, filepath.Join(root, "bin", "unrelated"))
	touch(t, filepath.Join(u.data, "studio.db"))
	touch(t, filepath.Join(u.msbHome, "db", "msb.db"))
	return u, vms, &entry
}

func TestUninstallRemovesEverythingOnceConfirmed(t *testing.T) {
	u, vms, entry := newInstall(t)
	var out bytes.Buffer
	if code := u.confirmAndRun(context.Background(), strings.NewReader("uninstall\n"), &out, false); code != 0 {
		t.Fatalf("code %d: %s", code, &out)
	}
	for _, want := range []string{u.exe, "studio-app", u.data, u.msbHome, "1 sandbox ", "dev", "2 VMs of this Studio's go (ss-abc-g1, ss-prewarm-x)", "the login entry", "isn't pushed"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the prompt doesn't say %q:\n%s", want, &out)
		}
	}
	for _, gone := range []string{u.exe, filepath.Join(filepath.Dir(u.exe), "studio-app"), u.data, u.msbHome} {
		if exists(gone) {
			t.Errorf("%s is still there", gone)
		}
	}
	if !exists(filepath.Join(filepath.Dir(u.exe), "unrelated")) {
		t.Error("a file that isn't Studio's went too")
	}
	if !slices.Equal(vms.removed, []string{"ss-abc-g1", "ss-prewarm-x"}) || !*entry {
		t.Errorf("removed %v, entry %v", vms.removed, *entry)
	}
}

func TestUninstallRemovesNothingWithoutTheWord(t *testing.T) {
	for _, answer := range []string{"", "\n", "y\n", "yes\n", "UNINSTALL\n"} {
		u, vms, entry := newInstall(t)
		stopped := false
		u.running = func() bool { return true }
		u.stopStudio = func(context.Context) error { stopped = true; return nil }
		var out bytes.Buffer
		if code := u.confirmAndRun(context.Background(), strings.NewReader(answer), &out, false); code == 0 {
			t.Fatalf("%q: code 0", answer)
		}
		if !exists(u.exe) || !exists(u.data) || !exists(u.msbHome) || len(vms.removed) > 0 || *entry || stopped {
			t.Fatalf("%q changed something: %s", answer, &out)
		}
		if !strings.Contains(out.String(), "Nothing was removed") {
			t.Errorf("%q: %s", answer, &out)
		}
	}
}

func TestUninstallRefusesWithoutATerminalOrYes(t *testing.T) {
	var out bytes.Buffer
	if code := runUninstall(context.Background(), strings.NewReader("uninstall\n"), &out, "127.0.0.1:1", false, nil); code != 2 {
		t.Fatalf("code %d: %s", code, &out)
	}
}

// Other VMs in microsandbox's home keep it, whether or not they look like Studio's, as those of
// another data directory do. Only this Studio's VMs and checkpoints go.
func TestUninstallLeavesOthersSandboxes(t *testing.T) {
	u, vms, _ := newInstall(t)
	vms.all = append(vms.all, "mine", "ss-other-g1")
	var out bytes.Buffer
	if code := u.confirmAndRun(context.Background(), nil, &out, true); code != 0 {
		t.Fatalf("code %d: %s", code, &out)
	}
	if !exists(u.msbHome) || !strings.Contains(out.String(), "2 VMs that aren't this Studio's (mine, ss-other-g1)") {
		t.Fatalf("msb gone or not said: %s", &out)
	}
	if slices.Contains(vms.removed, "mine") || slices.Contains(vms.removed, "ss-other-g1") || !slices.Equal(vms.checkpointsRemoved, u.ids) || exists(u.data) {
		t.Fatalf("removed %v, checkpoints %v", vms.removed, vms.checkpointsRemoved)
	}
}

func TestUninstallKeepingTheData(t *testing.T) {
	u, vms, _ := newInstall(t)
	u.keepData = true
	var out bytes.Buffer
	if code := u.confirmAndRun(context.Background(), nil, &out, true); code != 0 {
		t.Fatalf("code %d: %s", code, &out)
	}
	if exists(u.exe) || !exists(u.data) || !exists(u.msbHome) || len(vms.removed) > 0 || vms.checkpointsRemoved != nil || len(vms.stopped) != 2 {
		t.Fatalf("removed %v, stopped %v: %s", vms.removed, vms.stopped, &out)
	}
}

func TestUninstallKeepingMicrosandbox(t *testing.T) {
	u, vms, _ := newInstall(t)
	u.keepMSB = true
	var out bytes.Buffer
	if code := u.confirmAndRun(context.Background(), nil, &out, true); code != 0 {
		t.Fatalf("code %d: %s", code, &out)
	}
	if !exists(u.msbHome) || exists(u.data) || len(vms.removed) != 2 || !slices.Equal(vms.checkpointsRemoved, u.ids) {
		t.Fatalf("removed %v: %s", vms.removed, &out)
	}
}

// A Studio that won't stop leaves everything as it was.
func TestUninstallChangesNothingWhenStudioWontStop(t *testing.T) {
	u, vms, entry := newInstall(t)
	u.running = func() bool { return true }
	u.stopStudio = func(context.Context) error { return os.ErrDeadlineExceeded }
	var out bytes.Buffer
	if code := u.confirmAndRun(context.Background(), nil, &out, true); code != 1 {
		t.Fatalf("code %d: %s", code, &out)
	}
	if !exists(u.exe) || !exists(u.data) || len(vms.removed) > 0 || *entry {
		t.Fatalf("changed something: %s", &out)
	}
}

// Another Studio with other data has microsandbox's lock, and its VMs are in use: nothing goes.
func TestUninstallChangesNothingWhileAnotherStudioUsesMicrosandbox(t *testing.T) {
	u, vms, entry := newInstall(t)
	u.lockMSB = func() (func() error, error) { return nil, store.ErrLocked }
	var out bytes.Buffer
	if code := u.confirmAndRun(context.Background(), nil, &out, true); code != 1 {
		t.Fatalf("code %d: %s", code, &out)
	}
	if !exists(u.exe) || !exists(u.data) || !exists(u.msbHome) || len(vms.removed) > 0 || *entry {
		t.Fatalf("changed something: %s", &out)
	}
	if !strings.Contains(out.String(), "another Studio") || !strings.Contains(out.String(), "nothing was removed") {
		t.Errorf("not said: %s", &out)
	}
}

// Without knowing which VMs are whose, or with the catalog that says so at stake, nothing goes.
func TestUninstallRemovesNothingWhenItCantTellWhichVMsAreItsOwn(t *testing.T) {
	for name, breakIt := range map[string]func(*uninstaller, *fakeVMs){
		"list":    func(_ *uninstaller, vms *fakeVMs) { vms.listErr = os.ErrPermission },
		"catalog": func(u *uninstaller, _ *fakeVMs) { u.catalogErr = os.ErrPermission },
	} {
		u, vms, entry := newInstall(t)
		breakIt(u, vms)
		var out bytes.Buffer
		if code := u.confirmAndRun(context.Background(), nil, &out, true); code != 1 {
			t.Fatalf("%s: code %d: %s", name, code, &out)
		}
		if !exists(u.exe) || !exists(u.data) || !exists(u.msbHome) || len(vms.removed) > 0 || *entry {
			t.Fatalf("%s changed something: %s", name, &out)
		}
		if !strings.Contains(out.String(), "Nothing was removed") || !strings.Contains(out.String(), "-keep-data") {
			t.Errorf("%s: %s", name, &out)
		}

		// -keep-data touches no VM, so it can still go on.
		u, vms, _ = newInstall(t)
		breakIt(u, vms)
		u.keepData = true
		out.Reset()
		if code := u.confirmAndRun(context.Background(), nil, &out, true); code != 0 {
			t.Fatalf("%s with -keep-data: code %d: %s", name, code, &out)
		}
		if exists(u.exe) || !exists(u.data) || len(vms.stopped) > 0 {
			t.Fatalf("%s with -keep-data: stopped %v: %s", name, vms.stopped, &out)
		}
	}
}

// A VM that can't be removed keeps the data, whose catalog says it is Studio's, and the program
// that can try again.
func TestUninstallStopsAtAVMItCantRemove(t *testing.T) {
	u, vms, _ := newInstall(t)
	vms.removeErr = os.ErrPermission
	var out bytes.Buffer
	if code := u.confirmAndRun(context.Background(), nil, &out, true); code != 1 {
		t.Fatalf("code %d: %s", code, &out)
	}
	if !exists(u.exe) || !exists(u.data) || !exists(u.msbHome) {
		t.Fatalf("went on after the failure: %s", &out)
	}
	if !strings.Contains(out.String(), "run studio uninstall again") {
		t.Errorf("not said: %s", &out)
	}

	vms.removeErr = nil
	out.Reset()
	if code := u.confirmAndRun(context.Background(), nil, &out, true); code != 0 {
		t.Fatalf("again: code %d: %s", code, &out)
	}
	if exists(u.exe) || exists(u.data) || exists(u.msbHome) {
		t.Fatalf("again left something: %s", &out)
	}
}

// An entry that can't be removed, like an SSH config a tool owns, keeps only the program.
func TestUninstallKeepsTheProgramWhenAnEntryFails(t *testing.T) {
	u, vms, _ := newInstall(t)
	u.entries = []loginEntry{{"the login entry", func() error { return os.ErrPermission }}}
	var out bytes.Buffer
	if code := u.confirmAndRun(context.Background(), nil, &out, true); code != 1 {
		t.Fatalf("code %d: %s", code, &out)
	}
	if !exists(u.exe) || exists(u.data) || exists(u.msbHome) || len(vms.removed) != 2 {
		t.Fatalf("removed %v: %s", vms.removed, &out)
	}
}

// The locations come from the environment, and a wrong one mustn't cost the user their files.
func TestUninstallWontRemoveTheWrongDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	touch(t, filepath.Join(home, "studio.db"))
	touch(t, filepath.Join(home, "plain", "photo.jpg"))
	for _, dir := range []string{"/", home, filepath.Dir(home), filepath.Join(home, "plain")} {
		if err := removableDir(dir, "studio.db", "photo.txt"); err == nil {
			t.Errorf("%s may be removed", dir)
		}
	}
	touch(t, filepath.Join(home, "data", "studio.db"))
	if err := removableDir(filepath.Join(home, "data"), "studio.db"); err != nil {
		t.Error(err)
	}
}

func TestRemoveTreeTakesReadOnlyDirectories(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "x")
	touch(t, filepath.Join(dir, "ro", "f"))
	if err := os.Chmod(filepath.Join(dir, "ro"), 0o500); err != nil {
		t.Fatal(err)
	}
	if err := removeTree(dir); err != nil || exists(dir) {
		t.Fatalf("%v", err)
	}
	if err := removeTree(dir); err != nil {
		t.Fatalf("a missing path: %v", err)
	}
}
