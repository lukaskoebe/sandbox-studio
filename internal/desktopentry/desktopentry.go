// Package desktopentry puts the Studio app in a Linux desktop's launcher. A launcher pins and
// shows only what has a desktop entry, and takes a window for that entry's app when the window's
// app id is the entry's file name.
package desktopentry

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ID names the app to the desktop: it is the window's app id and the entry's file name, and the
// identifier of the macOS bundle.
const ID = "dev.sandbox-studio.app"

// Files are where the entry and its icon go.
type Files struct {
	Entry, Icon string
}

// Locate finds them under $XDG_DATA_HOME, else ~/.local/share.
func Locate() (Files, error) {
	data := os.Getenv("XDG_DATA_HOME")
	if !filepath.IsAbs(data) { // The specification says to ignore a relative one.
		home, err := os.UserHomeDir()
		if err != nil {
			return Files{}, err
		}
		data = filepath.Join(home, ".local", "share")
	}
	return Files{
		Entry: filepath.Join(data, "applications", ID+".desktop"),
		Icon:  filepath.Join(data, "icons", "hicolor", "512x512", "apps", ID+".png"),
	}, nil
}

// Install writes the entry that starts the program exe, and its icon, where they differ from
// what is there: a window pinned earlier goes on working when the program has moved.
func (f Files) Install(exe string, icon []byte) error {
	entry, err := f.entry(exe)
	if err != nil {
		return err
	}
	// The icon first, for an entry that shows as soon as it is there.
	return errors.Join(writeNew(f.Icon, icon), writeNew(f.Entry, entry))
}

// Installed reports whether any of it is there.
func (f Files) Installed() bool {
	for _, p := range []string{f.Entry, f.Icon} {
		if _, err := os.Lstat(p); err == nil {
			return true
		}
	}
	return false
}

// Remove removes the entry and its icon.
func (f Files) Remove() error {
	var errs []error
	for _, p := range []string{f.Entry, f.Icon} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// The icon is a path rather than a theme's name: a theme needs its index, which a NixOS or
// minimal desktop may not have where it looks.
func (f Files) entry(exe string) ([]byte, error) {
	if strings.ContainsAny(exe+f.Icon, "\n\r") {
		return nil, fmt.Errorf("a desktop entry can't name a path with a line break: %q", exe)
	}
	var b bytes.Buffer
	b.WriteString("[Desktop Entry]\nType=Application\nName=Sandbox Studio\n")
	b.WriteString("Comment=Sandbox Studio in a window of its own\n")
	fmt.Fprintf(&b, "Exec=%s\nIcon=%s\n", execValue(exe), strings.ReplaceAll(f.Icon, `\`, `\\`))
	b.WriteString("Terminal=false\nCategories=Development;\nSingleMainWindow=true\n")
	fmt.Fprintf(&b, "StartupWMClass=%s\n", ID)
	return b.Bytes(), nil
}

// execValue is the path as the entry's Exec key takes it: quoted, with four characters escaped
// inside the quotes, then a value's own escapes, then % as a field code would read it.
func execValue(path string) string {
	if strings.ContainsAny(path, " \t\"'\\><~|&;$*?#()`%=") {
		path = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`", `$`, `\$`).Replace(path) + `"`
		path = strings.ReplaceAll(path, `\`, `\\`)
	}
	return strings.ReplaceAll(path, "%", "%%")
}

// writeNew replaces the file at p with data, unless that is what it holds.
func writeNew(p string, data []byte) error {
	if old, err := os.ReadFile(p); err == nil && bytes.Equal(old, data) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".studio-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(data)
	if err := errors.Join(err, tmp.Chmod(0o644), tmp.Close()); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}
