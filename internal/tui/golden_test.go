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

func TestGoldenApprovals(t *testing.T) {
	m := sampleApp()
	sampleApprovals(m)
	golden(t, "approvals", press(m, "a"))
}

func TestGoldenApprovalsGroup(t *testing.T) {
	m := sampleApp()
	sampleApprovals(m)
	golden(t, "approvals_group", press(m, "a", "j"))
}

func TestGoldenApprovalsNone(t *testing.T) {
	m := sampleApp()
	m.approvals = nil
	golden(t, "approvals_none", press(m, "a"))
}

func TestGoldenApprovalNetwork(t *testing.T) {
	m := sampleApp()
	sampleApprovals(m)
	golden(t, "approvals_network", press(m, "a", "j", "j"))
}

func TestGoldenApprovalsHelp(t *testing.T) {
	m := sampleApp()
	sampleApprovals(m)
	golden(t, "approvals_help", press(m, "a", "?"))
}
