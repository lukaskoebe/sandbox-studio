//go:build !windows

package desktopentry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocateFollowsXDGDataHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, c := range []struct{ xdg, want string }{
		{"", filepath.Join(home, ".local", "share")},
		{"relative", filepath.Join(home, ".local", "share")},
		{"/elsewhere", "/elsewhere"},
	} {
		t.Setenv("XDG_DATA_HOME", c.xdg)
		f, err := Locate()
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(c.want, "applications", ID+".desktop"); f.Entry != want {
			t.Errorf("XDG_DATA_HOME=%q: %s, want %s", c.xdg, f.Entry, want)
		}
	}
}

func TestInstallWritesAnEntryThatMatchesTheWindow(t *testing.T) {
	dir := t.TempDir()
	f := Files{Entry: filepath.Join(dir, "a", ID+".desktop"), Icon: filepath.Join(dir, "i", ID+".png")}
	if f.Installed() {
		t.Fatal("installed before Install")
	}
	if err := f.Install("/home/me/.local/bin/studio-app", []byte("png")); err != nil {
		t.Fatal(err)
	}
	entry, err := os.ReadFile(f.Entry)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"[Desktop Entry]\n",
		"\nType=Application\n",
		"\nName=Sandbox Studio\n",
		"\nExec=/home/me/.local/bin/studio-app\n",
		"\nIcon=" + f.Icon + "\n",
		"\nStartupWMClass=" + ID + "\n",
	} {
		if !strings.Contains(string(entry), want) {
			t.Errorf("no %q in:\n%s", want, entry)
		}
	}
	if icon, _ := os.ReadFile(f.Icon); string(icon) != "png" {
		t.Errorf("icon %q", icon)
	}
	if !f.Installed() {
		t.Error("not installed after Install")
	}
	left, _ := os.ReadDir(filepath.Dir(f.Entry))
	if len(left) != 1 {
		t.Errorf("left beside the entry: %v", left)
	}

	// The same again leaves the files alone, and a program that moved updates the entry.
	old := time.Unix(1000, 0)
	if err := os.Chtimes(f.Entry, old, old); err != nil {
		t.Fatal(err)
	}
	if err := f.Install("/home/me/.local/bin/studio-app", []byte("png")); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(f.Entry); !st.ModTime().Equal(old) {
		t.Error("an unchanged entry was written again")
	}
	if err := f.Install("/opt/studio-app", []byte("png")); err != nil {
		t.Fatal(err)
	}
	if entry, _ := os.ReadFile(f.Entry); !strings.Contains(string(entry), "\nExec=/opt/studio-app\n") {
		t.Errorf("the entry didn't follow the program:\n%s", entry)
	}

	if err := f.Remove(); err != nil {
		t.Fatal(err)
	}
	if f.Installed() {
		t.Error("still there after Remove")
	}
	if err := f.Remove(); err != nil {
		t.Errorf("a second Remove: %v", err)
	}
}

func TestInstallRefusesAPathWithALineBreak(t *testing.T) {
	dir := t.TempDir()
	f := Files{Entry: filepath.Join(dir, "e"), Icon: filepath.Join(dir, "i")}
	if err := f.Install("/tmp/a\nExec=evil", nil); err == nil {
		t.Fatal("no error")
	}
	if f.Installed() {
		t.Error("wrote something")
	}
}

func TestExecValueQuotesThePath(t *testing.T) {
	for in, want := range map[string]string{
		"/usr/bin/studio-app":   "/usr/bin/studio-app",
		"/home/a b/studio-app":  `"/home/a b/studio-app"`,
		"/home/100%/studio-app": `"/home/100%%/studio-app"`,
		`/home/a"b/studio-app`:  `"/home/a\\"b/studio-app"`,
		`/home/a$b/studio-app`:  `"/home/a\\$b/studio-app"`,
		`/home/a\b/studio-app`:  `"/home/a\\\\b/studio-app"`,
		"/home/a`b/studio-app":  "\"/home/a\\\\`b/studio-app\"",
	} {
		if got := execValue(in); got != want {
			t.Errorf("execValue(%q) = %s, want %s", in, got, want)
		}
	}
}
