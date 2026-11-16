package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnfinishedLayout(t *testing.T) {
	dir := t.TempDir()
	if why, err := unfinishedLayout(dir); why != "" || err != nil {
		t.Errorf("no layout = %q, %v", why, err)
	}
	for body, unfinished := range map[string]bool{
		`{"schema":"microsandbox.protected-layout/1","phase":"ready"}`:   false,
		`{"schema":"microsandbox.protected-layout/1","phase":"copying"}`: true,
		`not json`: true,
	} {
		if err := os.WriteFile(filepath.Join(dir, layoutFile), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		why, err := unfinishedLayout(dir)
		if err != nil || (why != "") != unfinished {
			t.Errorf("%s = %q, %v", body, why, err)
		}
	}
}

func TestUpgradeErrorSaysToRestart(t *testing.T) {
	msg := "create checkpoint: runtime error: upgrade required: sandbox 'ss-x-g1' is not running with protected sandbox state. Update the SDK/CLI and runtime, then stop and restart this sandbox before taking snapshots, forking, growing disks, or compacting disks. Exec and stop remain available."
	err := upgradeError(errors.New(msg))
	if !errors.Is(err, ErrRestartToUpgrade) || !strings.Contains(err.Error(), "ss-x-g1") {
		t.Errorf("upgradeError = %v", err)
	}
	other := errors.New("create checkpoint: disk full")
	if upgradeError(other) != other || upgradeError(nil) != nil {
		t.Error("upgradeError changed another error")
	}
}

func TestMoveErrorNamesTheMoveForMsbsLayoutRefusals(t *testing.T) {
	for _, msg := range []string{
		"macOS legacy storage requires explicit maintenance recovery: stop legacy VMs across all microsandbox homes",
		"migration/recovery required: legacy journal has no configured root binding",
	} {
		if err := moveError(errors.New(msg)); !errors.Is(err, ErrMoveDisks) || !strings.Contains(err.Error(), msg) {
			t.Errorf("moveError(%q) = %v, want ErrMoveDisks keeping msb's words", msg, err)
		}
	}
	other := errors.New("no such sandbox")
	if err := moveError(other); err != other {
		t.Errorf("moveError(other) = %v, want it unchanged", err)
	}
	for _, status := range []string{"maintenance_required", "recovery_required"} {
		if !NeedsMove(status) {
			t.Errorf("NeedsMove(%q) = false", status)
		}
	}
	for _, status := range []string{"ready", "eligible", "in_progress", "unavailable"} {
		if NeedsMove(status) {
			t.Errorf("NeedsMove(%q) = true", status)
		}
	}
}

func TestWriterOf(t *testing.T) {
	for msg, want := range map[string]string{
		"legacy storage migration on macOS cannot exclude a writer in sandbox 'ss-xmeh-g1'; stop that legacy runtime": "ss-xmeh-g1",
		"cannot exclude a writer in sandbox 'unterminated":                                                            "",
		"migration/recovery required": "",
	} {
		if got := writerOf(msg); got != want {
			t.Errorf("writerOf(%q) = %q, want %q", msg, got, want)
		}
	}
}
