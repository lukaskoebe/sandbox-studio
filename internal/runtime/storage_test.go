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
