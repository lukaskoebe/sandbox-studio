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

func TestGoldenComputerDetails(t *testing.T) {
	golden(t, "computer_details", withDetailData(openComputer(sampleApp(), "sb2", tabDetails)))
}

func TestGoldenDeleteAsks(t *testing.T) {
	golden(t, "delete_asks", press(openComputer(sampleApp(), "sb1", tabDetails), "d"))
}

// A thread at 80 columns with the request of its computer as a card above the composer (A2).
func TestGoldenThread(t *testing.T) {
	golden(t, "thread", threadWithCard(sampleApp()))
}

// The sidebar as a drawer over a thread at 80 columns, on the thread's row (A5).
func TestGoldenDrawer(t *testing.T) {
	m := sampleMorning(sampleApp())
	golden(t, "drawer", onSession(t, m, "sb1", "Fix the login form"))
}

// A teammate's home: its computer, tasks, what it knows, and the composer of a new thread.
func TestGoldenMate(t *testing.T) {
	m := withSessions(sampleApp())
	m.open(place{kind: placeMate, mate: "t1"})
	m = settle(m)
	m.mates = mateState{mate: "t1", loaded: true}
	golden(t, "mate", m)
}

// A computer on its own on the Terminal tab, the fake terminal's screen in it (A3), and the
// same terminal in full screen.
func TestGoldenComputerTerminal(t *testing.T) {
	m := withSessions(sampleApp())
	m.approvals = nil // no banner over the screen
	m = liveTerminal(t, m, "sb2", "codex")
	golden(t, "computer_terminal", m)
	m.pane.focus, m.zoom = true, true // full screen while the terminal has the keys
	golden(t, "zoom_terminal", m)
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
