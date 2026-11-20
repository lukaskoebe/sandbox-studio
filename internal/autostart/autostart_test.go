package autostart

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newService never looks at the real home directory: every path is under a temp dir.
func newService(t *testing.T, goos string) *Service {
	t.Helper()
	home := t.TempDir()
	return &Service{GOOS: goos, Home: home, ConfigHome: filepath.Join(home, ".config"), Exe: "/opt/Sandbox Studio/studio"}
}

func TestLinux(t *testing.T) {
	s := newService(t, "linux")
	// A failing reload, as without a systemd user manager, doesn't fail the change.
	reloads := 0
	s.Reload = func() error { reloads++; return errors.New("no user manager") }
	st, err := s.Status()
	if err != nil || st.Enabled || !st.Supported {
		t.Fatalf("initial: %+v %v", st, err)
	}
	if st, err = s.Enable(); err != nil || !st.Enabled || st.Stale {
		t.Fatalf("enable: %+v %v", st, err)
	}
	unit := filepath.Join(s.Home, ".config", "systemd", "user", "sandbox-studio.service")
	data, err := os.ReadFile(unit)
	if err != nil {
		t.Fatal(err)
	}
	// The start limit keeps a Studio that fails at every start from restarting without end.
	for _, want := range []string{`ExecStart="/opt/Sandbox Studio/studio"`, "KillMode=process", "WantedBy=default.target", "StartLimitIntervalSec=5min", "StartLimitBurst=10"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("unit lacks %q:\n%s", want, data)
		}
	}
	link := filepath.Join(s.Home, ".config", "systemd", "user", "default.target.wants", "sandbox-studio.service")
	// This simulates Linux on the host filesystem, so the symlink spelling follows the
	// host's path separators (including Windows when this test runs there).
	if target, err := os.Readlink(link); err != nil || target != filepath.Join("..", "sandbox-studio.service") {
		t.Fatalf("wants link: %q %v", target, err)
	}
	// Enabling twice is fine.
	if _, err := s.Enable(); err != nil {
		t.Fatal(err)
	}

	s.Exe = "/usr/local/bin/studio"
	if st, _ = s.Status(); !st.Stale {
		t.Fatalf("a moved binary is not reported: %+v", st)
	}
	if st, err = s.Disable(); err != nil || st.Enabled {
		t.Fatalf("disable: %+v %v", st, err)
	}
	for _, p := range []string{unit, link} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Errorf("%s still exists", p)
		}
	}
	if _, err := s.Disable(); err != nil {
		t.Fatalf("disabling twice: %v", err)
	}
	if reloads != 4 {
		t.Fatalf("systemd reloaded %d times, want once per change (4)", reloads)
	}
}

func TestMac(t *testing.T) {
	s := newService(t, "darwin")
	s.Exe = "/Applications/A&B/studio"
	st, err := s.Enable()
	if err != nil || !st.Enabled {
		t.Fatalf("enable: %+v %v", st, err)
	}
	plist := filepath.Join(s.Home, "Library", "LaunchAgents", "io.github.lukaskoebe.sandbox-studio.plist")
	if st.Path != plist {
		t.Fatalf("path %s", st.Path)
	}
	data, _ := os.ReadFile(plist)
	for _, want := range []string{"<string>/Applications/A&amp;B/studio</string>", "<key>RunAtLoad</key>", "<key>AbandonProcessGroup</key>"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("plist lacks %q:\n%s", want, data)
		}
	}
	if st, err = s.Disable(); err != nil || st.Enabled {
		t.Fatalf("disable: %+v %v", st, err)
	}
}

type fakeRunKey map[string]string

func (f fakeRunKey) Get(name string) (string, bool, error) { v, ok := f[name]; return v, ok, nil }
func (f fakeRunKey) Set(name, value string) error          { f[name] = value; return nil }
func (f fakeRunKey) Delete(name string) error              { delete(f, name); return nil }

func TestWindows(t *testing.T) {
	key := fakeRunKey{}
	s := &Service{GOOS: "windows", Exe: `C:\Users\u\Sandbox Studio\studio.exe`, Run: key}
	st, err := s.Enable()
	if err != nil || !st.Enabled || key["Sandbox Studio"] != `"C:\Users\u\Sandbox Studio\studio.exe" -no-browser` {
		t.Fatalf("enable: %+v %v %v", st, err, key)
	}
	if st, err = s.Disable(); err != nil || st.Enabled || len(key) != 0 {
		t.Fatalf("disable: %+v %v", st, err)
	}
}

// With the tray launcher next to studio.exe, the entry starts the tray, and an entry that
// starts studio.exe itself is stale.
func TestWindowsTray(t *testing.T) {
	key := fakeRunKey{"Sandbox Studio": `"C:\Users\u\Sandbox Studio\studio.exe" -no-browser`}
	s := &Service{GOOS: "windows", Exe: `C:\Users\u\Sandbox Studio\studio.exe`, Tray: `C:\Users\u\Sandbox Studio\studiow.exe`, Run: key}
	if st, err := s.Status(); err != nil || !st.Stale || st.Command != s.Tray {
		t.Fatalf("status: %+v %v", st, err)
	}
	st, err := s.Enable()
	if err != nil || st.Stale || key["Sandbox Studio"] != `"C:\Users\u\Sandbox Studio\studiow.exe" -no-browser` {
		t.Fatalf("enable: %+v %v %v", st, err, key)
	}
	dir := t.TempDir()
	if TrayNextTo(filepath.Join(dir, "studio.exe")) != "" {
		t.Fatal("a tray where there is none")
	}
	if err := os.WriteFile(filepath.Join(dir, "studiow.exe"), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := TrayNextTo(filepath.Join(dir, "studio.exe")); got != filepath.Join(dir, "studiow.exe") {
		t.Fatalf("tray = %q", got)
	}
}

func TestRefusesGoRun(t *testing.T) {
	s := newService(t, "linux")
	s.Exe = filepath.Join(os.TempDir(), "go-build123", "b001", "exe", "studio")
	if _, err := s.Enable(); err == nil {
		t.Fatal("a go run binary was accepted")
	}
	if _, err := (&Service{GOOS: "plan9", Exe: "/bin/studio"}).Enable(); err != ErrUnsupported {
		t.Fatalf("plan9: %v", err)
	}
}

func TestLinuxRefresh(t *testing.T) {
	s := newService(t, "linux")
	reloads := 0
	s.Reload = func() error { reloads++; return nil }
	unit := filepath.Join(s.Home, ".config", "systemd", "user", "sandbox-studio.service")
	if done, err := s.Refresh(); done || err != nil {
		t.Fatalf("refresh without a unit: %v %v", done, err)
	}
	// The unit v0.27.0 wrote, without the start limit.
	old := `# Written by Sandbox Studio (Settings > Start at login).
[Unit]
Description=Sandbox Studio
After=network-online.target

[Service]
ExecStart=%s
Restart=on-failure
RestartSec=5
KillMode=process

[Install]
WantedBy=default.target
`
	write := func(content string) {
		t.Helper()
		if err := writeFile(unit, content); err != nil {
			t.Fatal(err)
		}
	}
	read := func() string {
		t.Helper()
		data, err := os.ReadFile(unit)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}

	// Another binary's unit, as a dev build finds the installed Studio's, stays.
	other := strings.Replace(old, "%s", `"/usr/local/bin/studio"`, 1)
	write(other)
	if done, err := s.Refresh(); done || err != nil || read() != other {
		t.Fatalf("refresh of another binary's unit: %v %v\n%s", done, err, read())
	}
	// So does one edited by hand.
	edited := strings.Replace(old, "%s", `"/opt/Sandbox Studio/studio"`, 1)
	edited = strings.TrimPrefix(edited, "# Written by Sandbox Studio (Settings > Start at login).\n")
	write(edited)
	if done, err := s.Refresh(); done || err != nil || read() != edited {
		t.Fatalf("refresh of an edited unit: %v %v\n%s", done, err, read())
	}

	write(strings.Replace(old, "%s", `"/opt/Sandbox Studio/studio"`, 1))
	if st, _ := s.Status(); !st.Stale {
		t.Fatalf("an older unit isn't stale: %+v", st)
	}
	if done, err := s.Refresh(); !done || err != nil || !strings.Contains(read(), "StartLimitBurst=10") {
		t.Fatalf("refresh of an older unit: %v %v\n%s", done, err, read())
	}
	if st, _ := s.Status(); st.Stale {
		t.Fatalf("the refreshed unit is stale: %+v", st)
	}
	if done, err := s.Refresh(); done || err != nil {
		t.Fatalf("refresh of a current unit: %v %v", done, err)
	}
	if reloads != 1 {
		t.Fatalf("systemd reloaded %d times, want 1", reloads)
	}
}
