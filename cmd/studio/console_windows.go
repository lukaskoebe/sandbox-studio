package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// prepareConsole turns off QuickEdit in Studio's console window: with it, a click in the
// window starts a selection that blocks every write to the console, and so the server's next
// log line, until Esc. It also turns on escape sequences for the colored, clickable banner.
func prepareConsole() {
	in := windows.Handle(os.Stdin.Fd())
	var mode uint32
	if windows.GetConsoleMode(in, &mode) == nil {
		_ = windows.SetConsoleMode(in, (mode&^windows.ENABLE_QUICK_EDIT_MODE)|windows.ENABLE_EXTENDED_FLAGS)
	}
	errh := windows.Handle(os.Stderr.Fd())
	if windows.GetConsoleMode(errh, &mode) == nil {
		_ = windows.SetConsoleMode(errh, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
	}
}
