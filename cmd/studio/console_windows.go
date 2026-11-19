package main

import (
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"unsafe"

	"github.com/lukaskoebe/sandbox-studio/internal/autostart"
	"golang.org/x/sys/windows"
)

var (
	kernel32                  = windows.NewLazySystemDLL("kernel32.dll")
	user32                    = windows.NewLazySystemDLL("user32.dll")
	procGetConsoleWindow      = kernel32.NewProc("GetConsoleWindow")
	procGetConsoleProcessList = kernel32.NewProc("GetConsoleProcessList")
	procShowWindow            = user32.NewProc("ShowWindow")
)

const (
	swHide = 0
	swShow = 5
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

// handOffConsole is a studio.exe double-clicked in Explorer, whose console is a window of its
// own: it hides the window and starts Studio's app next to it, or else the tray launcher,
// which run Studio without one, and reports true once one started. Without either, or when
// neither starts, the window comes back and Studio runs in it as before. A studio.exe started
// from a shell shares that shell's console, and one the tray or the app starts has no window,
// so they run as they always did.
func handOffConsole(addr string) bool {
	window, _, _ := procGetConsoleWindow.Call()
	if window == 0 || consoleProcesses() != 1 {
		return false
	}
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return false
	}
	app, tray := appBinary(exe, goruntime.GOOS), autostart.TrayNextTo(exe)
	if app == "" && tray == "" {
		return false
	}
	_, _, _ = procShowWindow.Call(window, swHide)
	if app != "" && startApp(app, exe, addr) == nil {
		return true
	}
	if tray != "" {
		cmd := exec.Command(tray, "-addr", addr)
		detach(cmd)
		if cmd.Start() == nil {
			_ = cmd.Process.Release()
			return true
		}
	}
	_, _, _ = procShowWindow.Call(window, swShow)
	return false
}

// consoleProcesses is how many processes share Studio's console: 1 when it is its own.
func consoleProcesses() int {
	var ids [2]uint32
	n, _, _ := procGetConsoleProcessList.Call(uintptr(unsafe.Pointer(&ids[0])), uintptr(len(ids)))
	return int(n)
}
