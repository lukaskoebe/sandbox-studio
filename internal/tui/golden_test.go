package tui

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"charm.land/lipgloss/v2"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

// golden checks what a screen shows against testdata/<name>.golden. The view is 80x24 and has
// NO_COLOR set, so the file is the text the user reads. After an intended change, run
// `go test ./internal/tui -run Golden -update` and review the diff.
func golden(t *testing.T, name string, m *app) {
	t.Helper()
	t.Setenv("NO_COLOR", "1")
	var buf bytes.Buffer
	if _, err := lipgloss.Fprint(&buf, m.View().Content); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to write it)", err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("%s does not match %s; got:\n%s", name, path, buf.String())
	}
}

func TestGoldenHome(t *testing.T) {
	golden(t, "home", sampleApp())
}

func TestGoldenHomeEmpty(t *testing.T) {
	m := sampleApp()
	m.sandboxes = nil
	m.approvals = nil
	golden(t, "home_empty", m)
}

func TestGoldenHomeLoading(t *testing.T) {
	m := sampleApp()
	m.loaded = false
	golden(t, "home_loading", m)
}

func TestGoldenFilter(t *testing.T) {
	m := press(sampleApp(), "/", "a", "p", "i")
	golden(t, "filter", m)
}

func TestGoldenDetail(t *testing.T) {
	golden(t, "detail", press(sampleApp(), "j", "enter"))
}

func TestGoldenDeleteAsks(t *testing.T) {
	golden(t, "delete_asks", press(sampleApp(), "d"))
}

func TestGoldenEnvironmentPicker(t *testing.T) {
	golden(t, "environments", press(sampleApp(), "e"))
}

func TestGoldenHelp(t *testing.T) {
	golden(t, "help", press(sampleApp(), "?"))
}

func TestGoldenInbox(t *testing.T) {
	m := sampleApp()
	sampleApprovals(m)
	golden(t, "inbox", onCard(m, 0))
}

func TestGoldenInboxGroup(t *testing.T) {
	m := sampleApp()
	sampleApprovals(m)
	golden(t, "inbox_group", onCard(m, 1))
}

func TestGoldenInboxEmpty(t *testing.T) {
	m := sampleApp()
	m.approvals = nil
	m.goHome()
	golden(t, "inbox_empty", m)
}

func TestGoldenInboxNetwork(t *testing.T) {
	m := sampleApp()
	sampleApprovals(m)
	golden(t, "inbox_network", onCard(m, 2))
}

func TestGoldenInboxPermission(t *testing.T) {
	m := sampleApp()
	m.approvals = kindApprovals(m.now)
	golden(t, "inbox_permission", onCard(m, 4))
}

func TestGoldenInboxPushAsks(t *testing.T) {
	m := sampleApp()
	sampleApprovals(m)
	golden(t, "inbox_push_asks", press(onCard(m, 0), "y"))
}

func TestGoldenInboxHelp(t *testing.T) {
	m := sampleApp()
	sampleApprovals(m)
	golden(t, "inbox_help", press(onCard(m, 0), "?"))
}
