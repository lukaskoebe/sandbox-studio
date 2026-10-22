package main

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/autostart"
	"github.com/lukaskoebe/sandbox-studio/internal/paths"
	"golang.org/x/sys/windows"
)

//go:embed studio.ico
var studioIcon []byte

const (
	createNewProcessGroup  = 0x00000200
	createNoWindow         = 0x08000000
	createBreakawayFromJob = 0x01000000
)

// The menu's items.
const (
	itemOpen = iota + 1
	itemLogs
	itemAutostart
	itemQuit
)

func init() {
	// The tray's window and message loop must stay on one thread.
	runtime.LockOSThread()
}

func main() {
	addr := flag.String("addr", "127.0.0.1:7878", "the address Studio listens on")
	noBrowser := flag.Bool("no-browser", false, "don't open Studio in the browser on start, as at login")
	flag.Parse()

	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		messageBox("Sandbox Studio can't tell where it is installed: "+err.Error(), true)
		os.Exit(1)
	}
	studio := filepath.Join(filepath.Dir(exe), "studio.exe")
	if _, err := os.Stat(studio); err != nil {
		messageBox("studio.exe isn't next to studiow.exe in "+filepath.Dir(exe)+". Unpack the whole release into one folder.", true)
		os.Exit(1)
	}
	// One tray per user: a second start opens Studio and leaves.
	name, _ := windows.UTF16PtrFromString(`Local\SandboxStudioTray`)
	mutex, err := windows.CreateMutex(nil, false, name)
	l := &launcher{
		addr:    *addr,
		serving: httpServing(*addr),
		start:   func() (<-chan error, error) { return startServer(studio, *addr) },
		studio:  func(ctx context.Context, args ...string) (string, error) { return runStudio(ctx, studio, args...) },
		open:    openBrowser,
		poll:    time.Second,
	}
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		ctx, cancel := context.WithTimeout(context.Background(), startWait)
		defer cancel()
		if err := l.openStudio(ctx); err != nil {
			messageBox(err.Error(), true)
		}
		return
	}
	if mutex != 0 {
		defer func() { _ = windows.CloseHandle(mutex) }()
	}

	t, err := newTray(studioIcon, tip(stateStarting, *addr))
	if err != nil {
		messageBox("Sandbox Studio can't add its icon to the taskbar: "+err.Error(), true)
		os.Exit(1)
	}
	app := &trayApp{l: l, t: t, studio: studio, addr: *addr}
	app.ctx, app.cancel = context.WithCancel(context.Background())
	t.menu, t.choose, t.activate = app.menu, app.choose, func() { go app.openStudio() }

	go app.watch()
	go func() {
		if *noBrowser {
			app.ensure()
		} else {
			app.openStudio()
		}
	}()
	t.run()
	app.cancel()
}

// trayApp is the tray's state: what Studio does, and the action running, if any.
type trayApp struct {
	l      *launcher
	t      *tray
	studio string
	addr   string
	ctx    context.Context
	cancel context.CancelFunc

	mu    sync.Mutex
	state state
	busy  bool // a start or a quit runs
}

func (a *trayApp) set(s state) {
	a.mu.Lock()
	a.state = s
	a.mu.Unlock()
	a.t.setTip(tip(s, a.addr))
}

// watch follows Studio's state, except while a start or a quit decides it.
func (a *trayApp) watch() {
	a.l.watch(a.ctx, func(s state) {
		a.mu.Lock()
		busy := a.busy
		a.mu.Unlock()
		if !busy {
			a.set(s)
		}
	})
}

// action runs one start or quit at a time; it reports false if another runs.
func (a *trayApp) action(during state, f func() error) bool {
	a.mu.Lock()
	if a.busy {
		a.mu.Unlock()
		return false
	}
	a.busy = true
	a.mu.Unlock()
	if !a.l.serving(a.ctx) || during == stateStopping {
		a.set(during)
	}
	err := f()
	a.mu.Lock()
	a.busy = false
	a.mu.Unlock()
	s := stateStopped
	if a.l.serving(a.ctx) {
		s = stateRunning
	}
	a.set(s)
	if err != nil && a.ctx.Err() == nil {
		a.t.balloon("Sandbox Studio", err.Error(), true)
	}
	return true
}

func (a *trayApp) ensure() {
	a.action(stateStarting, func() error { return a.l.ensure(a.ctx) })
}

func (a *trayApp) openStudio() {
	a.action(stateStarting, func() error { return a.l.openStudio(a.ctx) })
}

func (a *trayApp) menu() []menuItem {
	a.mu.Lock()
	s, busy := a.state, a.busy
	a.mu.Unlock()
	open := "Open Studio"
	if s == stateStopped {
		open = "Start Studio"
	}
	items := []menuItem{
		{ID: itemOpen, Text: open, Disabled: busy},
		{ID: itemLogs, Text: "Logs"},
	}
	if svc := a.autostart(); svc != nil {
		st, err := svc.Status()
		items = append(items, menuItem{ID: itemAutostart, Text: "Start at login", Checked: err == nil && st.Enabled && !st.Stale, Disabled: err != nil})
	}
	return append(items, menuItem{}, menuItem{ID: itemQuit, Text: "Quit", Disabled: busy})
}

func (a *trayApp) choose(id int) {
	switch id {
	case itemOpen:
		go a.openStudio()
	case itemLogs:
		go a.openLogs()
	case itemAutostart:
		go a.toggleAutostart()
	case itemQuit:
		go a.quit()
	}
}

// quit stops Studio, then the tray. Sandboxes keep running.
func (a *trayApp) quit() {
	var err error
	if !a.action(stateStopping, func() error {
		ctx, cancel := context.WithTimeout(a.ctx, 2*time.Minute)
		defer cancel()
		err = a.l.quit(ctx)
		return err
	}) {
		return
	}
	if err != nil {
		return // the balloon says why; the tray stays to try again
	}
	a.t.close()
}

func (a *trayApp) openLogs() {
	p, err := paths.Default()
	if err != nil {
		a.t.balloon("Sandbox Studio", err.Error(), true)
		return
	}
	dir := filepath.Join(p.Data, "logs")
	args := []string{dir}
	if log := filepath.Join(dir, "studio.log"); fileExists(log) {
		args = []string{"/select," + log}
	}
	// explorer.exe exits 1 even when it opened the folder.
	_ = exec.Command("explorer.exe", args...).Start()
}

// autostart is the login entry of the studio.exe next to this tray, which starts the tray.
func (a *trayApp) autostart() *autostart.Service {
	svc, err := autostart.Default()
	if err != nil {
		return nil
	}
	svc.Exe = a.studio
	svc.Tray = autostart.TrayNextTo(a.studio)
	return svc
}

func (a *trayApp) toggleAutostart() {
	svc := a.autostart()
	if svc == nil {
		return
	}
	st, err := svc.Status()
	if err == nil {
		if st.Enabled && !st.Stale {
			_, err = svc.Disable()
		} else {
			_, err = svc.Enable()
		}
	}
	if err != nil {
		a.t.balloon("Sandbox Studio", "Start at login: "+err.Error(), true)
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// startServer starts studio.exe with a console of its own that has no window: the
// programs Studio runs share that console instead of each opening a window. It runs outside
// the tray's job, so that it outlives the tray.
func startServer(studio, addr string) (<-chan error, error) {
	cmd := exec.Command(studio, "-addr", addr, "-no-browser")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow | createNewProcessGroup | createBreakawayFromJob, HideWindow: true}
	if err := cmd.Start(); err != nil {
		// A job that forbids breakaway.
		cmd = exec.Command(studio, "-addr", addr, "-no-browser")
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow | createNewProcessGroup, HideWindow: true}
		if err := cmd.Start(); err != nil {
			return nil, err
		}
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	return exited, nil
}

// runStudio runs a short studio.exe command without a window and returns its output.
func runStudio(ctx context.Context, studio string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, studio, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow, HideWindow: true}
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		msg := bytes.TrimSpace(append(out.Bytes(), errOut.Bytes()...))
		if len(msg) > 0 {
			return "", fmt.Errorf("%s", msg)
		}
		return "", err
	}
	return out.String(), nil
}

// openBrowser opens url in the default browser. Unlike `cmd /c start`, rundll32 passes the
// link on whole: no shell parses its & or #.
func openBrowser(url string) error {
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// tip is the tray icon's tooltip for a state.
func tip(s state, addr string) string {
	switch s {
	case stateStarting:
		return "Sandbox Studio is starting…"
	case stateRunning:
		return "Sandbox Studio runs on http://" + addr
	case stateStopping:
		return "Sandbox Studio is stopping…"
	}
	return "Sandbox Studio isn't running"
}
