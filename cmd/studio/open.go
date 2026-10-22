package main

import (
	"errors"
	"os"
	"os/exec"
	goruntime "runtime"
)

// errNoDisplay is a Linux session without a graphical display, such as over SSH.
var errNoDisplay = errors.New("no graphical display")

// browserCommand is the command that opens url in the default browser on goos.
func browserCommand(goos, url string, getenv func(string) string) (*exec.Cmd, error) {
	switch goos {
	case "windows":
		// Unlike `cmd /c start`, it passes the link on whole: no shell parses its & or #.
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url), nil
	case "darwin":
		return exec.Command("open", url), nil
	default:
		if getenv("DISPLAY") == "" && getenv("WAYLAND_DISPLAY") == "" {
			return nil, errNoDisplay
		}
		return exec.Command("xdg-open", url), nil
	}
}

// openBrowser opens url in the default browser without waiting for it.
func openBrowser(url string) error {
	cmd, err := browserCommand(goruntime.GOOS, url, os.Getenv)
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
