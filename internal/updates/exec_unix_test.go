//go:build !windows

package updates

import (
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// restartHelper is the environment variable that makes the test binary a Studio restarting
// for an update: the process the in-place restart replaces.
const restartHelper = "STUDIO_TEST_RESTART_DIR"

func TestMain(m *testing.M) {
	if dir := os.Getenv(restartHelper); dir != "" {
		os.WriteFile(filepath.Join(dir, "helper.pid"), []byte(strconv.Itoa(os.Getpid())), 0o600)
		err := Restart(filepath.Join(dir, "updates"), []string{"-no-browser"}, slog.New(slog.DiscardHandler))
		os.WriteFile(filepath.Join(dir, "helper.err"), []byte(err.Error()), 0o600)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// inPlaceRecord writes a swapped install whose binaries are shell scripts.
func inPlaceRecord(t *testing.T, newBinary, oldBinary string) (string, Pending) {
	t.Helper()
	dir := t.TempDir()
	p := Pending{Dir: filepath.Join(dir, "updates"), From: "v1.0.0", To: "v1.1.0", Exe: filepath.Join(dir, "studio"), Old: filepath.Join(dir, "studio.old"),
		Backup: filepath.Join(dir, "updates", "v1.1.0", "studio-v1.0.0.db"), DB: filepath.Join(dir, "studio.db"), State: StateSwapped}
	if err := os.MkdirAll(filepath.Dir(p.Backup), 0o700); err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string]string{p.Exe: newBinary, p.Old: oldBinary, p.Backup: "old schema", p.DB: "new schema"} {
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := writePending(p.Dir, p); err != nil {
		t.Fatal(err)
	}
	return dir, p
}

// runRestart runs the test binary as a Studio that restarts into the update, and returns
// what the binary that replaced it wrote to out.
func runRestart(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), restartHelper+"="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		b, _ := os.ReadFile(filepath.Join(dir, "helper.err"))
		t.Fatalf("%v: %s %s", err, out, b)
	}
	b, err := os.ReadFile(filepath.Join(dir, "out"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

// On Unix the old process becomes the new binary: the PID, and so the terminal and Ctrl-C,
// stay, and the record says the new binary has to roll itself back.
func TestRestartReplacesTheProcess(t *testing.T) {
	dir, p := inPlaceRecord(t, "#!/bin/sh\necho \"new $$ $*\" > \"$(dirname \"$0\")/out\"\n", "#!/bin/sh\nexit 0\n")
	got := runRestart(t, dir)
	pid := read(t, filepath.Join(dir, "helper.pid"))
	if got != "new "+pid+" -no-browser" {
		t.Errorf("the new binary wrote %q, want it in process %s", got, pid)
	}
	q, _, _ := readPending(p.Dir)
	if !q.InPlace || q.State != StateSwapped {
		t.Errorf("record %+v", q)
	}
}

// A new binary that can't run puts the old one and its database back in its place.
func TestRestartInPlaceRollsBack(t *testing.T) {
	dir, p := inPlaceRecord(t, "#!/nonexistent/sh\n", "#!/bin/sh\necho \"old $$ $*\" > \"$(dirname \"$0\")/out\"\n")
	got := runRestart(t, dir)
	if pid := read(t, filepath.Join(dir, "helper.pid")); got != "old "+pid+" -no-browser" {
		t.Errorf("the old binary wrote %q, want it in process %s", got, pid)
	}
	q, _, _ := readPending(p.Dir)
	if q.State != StateRolledBack || !strings.Contains(q.Error, "starting the new binary failed") || read(t, p.DB) != "old schema" {
		t.Errorf("record %+v", q)
	}
}

// A new binary that replaced the old one in place and stops with an error before serving
// rolls back and has Studio start the old one.
func TestStoppedBeforeServing(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	_, p := inPlaceRecord(t, "new binary", "old binary")
	p.InPlace = true
	if err := writePending(p.Dir, p); err != nil {
		t.Fatal(err)
	}
	r, err := ResumeInstall(p.Dir, "v1.1.0", log)
	if err != nil || !r.Waiting() {
		t.Fatalf("%+v %v", r, err)
	}
	back, ok := r.StoppedBeforeServing(os.ErrPermission, log)
	if !ok || !back.Exit || back.Exe != p.Exe || back.Progress.Phase != PhaseFailed || !strings.Contains(back.Progress.Error, "stopped before serving") {
		t.Fatalf("%+v %v", back, ok)
	}
	if read(t, p.Exe) != "old binary" || read(t, p.DB) != "old schema" {
		t.Error("the rollback didn't restore the binary and the database")
	}

	// Once it served, an error at shutdown is no reason to roll back.
	_, p = inPlaceRecord(t, "new binary", "old binary")
	p.InPlace = true
	if err := writePending(p.Dir, p); err != nil {
		t.Fatal(err)
	}
	r, _ = ResumeInstall(p.Dir, "v1.1.0", log)
	r.Served(log)
	if _, ok := r.StoppedBeforeServing(os.ErrPermission, log); ok {
		t.Error("a served install rolled back")
	}
}
