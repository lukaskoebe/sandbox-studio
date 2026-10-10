package autostart

import (
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
	for _, want := range []string{`ExecStart="/opt/Sandbox Studio/studio"`, "KillMode=process", "WantedBy=default.target"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("unit lacks %q:\n%s", want, data)
		}
	}
	link := filepath.Join(s.Home, ".config", "systemd", "user", "default.target.wants", "sandbox-studio.service")
	if target, err := os.Readlink(link); err != nil || target != "../sandbox-studio.service" {
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
	if err != nil || !st.Enabled || key["Sandbox Studio"] != `"C:\Users\u\Sandbox Studio\studio.exe"` {
		t.Fatalf("enable: %+v %v %v", st, err, key)
	}
	if st, err = s.Disable(); err != nil || st.Enabled || len(key) != 0 {
		t.Fatalf("disable: %+v %v", st, err)
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
