package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/paths"
)

// appStartWait is how long `studio app` watches the app start: one that can't load its
// libraries or open a window exits well within it.
const appStartWait = 3 * time.Second

// appBinary returns the Studio app (cmd/studio-app) installed next to exe, or "" without one:
// studio-app beside it, or on macOS first the one in Sandbox Studio.app beside it, as only
// a bundle gets the system's notifications there.
func appBinary(exe, goos string) string {
	dir := filepath.Dir(exe)
	name := "studio-app"
	if goos == "windows" {
		name += ".exe"
	}
	candidates := []string{filepath.Join(dir, name)}
	if goos == "darwin" {
		candidates = append([]string{filepath.Join(dir, "Sandbox Studio.app", "Contents", "MacOS", name)}, candidates...)
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.Mode().IsRegular() {
			return c
		}
	}
	return ""
}

// runApp opens Studio in its app, which starts the server if it must. Without the app, or
// when it can't start, such as on a machine without WebKitGTK, it opens Studio in the
// browser instead: the app is only a window around the server.
func runApp(ctx context.Context, w io.Writer, addr string) int {
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		fmt.Fprintln(w, err)
		return 1
	}
	app := appBinary(exe, goruntime.GOOS)
	if app == "" {
		fmt.Fprintln(w, "The Studio app isn't installed next to studio, so Studio opens in the browser.")
		return openInBrowser(ctx, w, addr)
	}
	if _, err := browserCommand(goruntime.GOOS, "", os.Getenv); errors.Is(err, errNoDisplay) {
		return openInBrowser(ctx, w, addr) // a window needs a display as much as the browser does
	}
	if err := startApp(app, exe, addr); err != nil {
		_, statErr := os.Stat("/etc/NIXOS")
		fmt.Fprintf(w, "The Studio app didn't start: %v\n%sStudio opens in the browser instead.\n", err, appLibHint(err, statErr == nil))
		return openInBrowser(ctx, w, addr)
	}
	return 0
}

// nixAppLibraries are the Nix packages with the libraries the Linux app links, for
// programs.nix-ld.libraries. Their own dependencies come with them.
const nixAppLibraries = "pkgs.gtk3 pkgs.webkitgtk_4_1 pkgs.libsoup_3 pkgs.glib pkgs.gdk-pixbuf pkgs.xorg.libX11"

// appLibHint says how to install the libraries of an app the loader couldn't start, as a
// line, or "" for another failure. On NixOS, a prebuilt binary finds libraries only through
// nix-ld.
func appLibHint(err error, nixos bool) string {
	if !strings.Contains(err.Error(), "cannot open shared object file") {
		return ""
	}
	if nixos {
		return "On NixOS, the app is a prebuilt binary that needs programs.nix-ld with programs.nix-ld.libraries = [ " + nixAppLibraries + " ]. Then run studio app again.\n"
	}
	return "The app needs GTK 3 and WebKitGTK 4.1: install them (Debian and Ubuntu: libwebkit2gtk-4.1-0, Fedora: webkit2gtk4.1).\n"
}

// startApp starts the app on its own and waits appStartWait for it to fail. A second app
// exits at once with success, after showing the window of the one already open.
func startApp(app, studio, addr string) error {
	log, logPath := appLog()
	cmd := exec.Command(app, "-addr", addr, "-studio", studio)
	detach(cmd)
	if log != nil {
		defer log.Close() // the app has its own copy
		cmd.Stdout, cmd.Stderr = log, log
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case err := <-exited:
		if err == nil {
			return nil
		}
		if tail := logTail(logPath); tail != "" {
			return fmt.Errorf("%w: %s", err, tail)
		}
		return err
	case <-time.After(appStartWait):
		return cmd.Process.Release()
	}
}

// appLog is logs/studio-app.log in the data directory, appended to, or nil when it can't be
// opened.
func appLog() (*os.File, string) {
	p, err := paths.Default()
	if err != nil {
		return nil, ""
	}
	dir := filepath.Join(p.Data, "logs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, ""
	}
	path := filepath.Join(dir, "studio-app.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, ""
	}
	return f, path
}

// logTail is the last line of the app's log, which says why it stopped.
func logTail(path string) string {
	if path == "" {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// openInBrowser opens a running Studio in the browser with a fresh login link.
func openInBrowser(ctx context.Context, w io.Writer, addr string) int {
	link, err := requestLoginURL(ctx, addr)
	if err != nil {
		fmt.Fprintf(w, "Studio doesn't answer on %s (%v). Run studio to start it; it opens the browser itself.\n", addr, err)
		return 1
	}
	if err := openBrowser(link); err != nil {
		if errors.Is(err, errNoDisplay) {
			fmt.Fprintln(w, "There's no display to open a browser on. Open this link where there is one:")
		} else {
			fmt.Fprintf(w, "The browser didn't open (%v). Open this link:\n", err)
		}
		fmt.Fprintln(w, link)
	}
	return 0
}
