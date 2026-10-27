package main

import (
	"context"
	_ "embed"
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/autostart"
	"github.com/lukaskoebe/sandbox-studio/internal/launcher"
	"github.com/lukaskoebe/sandbox-studio/internal/paths"
	"golang.org/x/sys/windows"
)

//go:embed studio.ico
var studioIcon []byte

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
	l := &launcher.Launcher{
		Addr:    *addr,
		Serving: launcher.HTTPServing(*addr),
		Start:   func() (<-chan error, error) { return launcher.StartServer(studio, *addr) },
		Studio: func(ctx context.Context, args ...string) (string, error) {
			return launcher.RunStudio(ctx, studio, args...)
		},
		Open: openBrowser,
		Poll: time.Second,
	}
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		ctx, cancel := context.WithTimeout(context.Background(), launcher.StartWait)
		defer cancel()
		if err := l.OpenStudio(ctx); err != nil {
			messageBox(err.Error(), true)
		}
		return
	}
	if mutex != 0 {
		defer func() { _ = windows.CloseHandle(mutex) }()
	}

	t, err := newTray(studioIcon, tip(launcher.Starting, *addr))
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
	l      *launcher.Launcher
	t      *tray
	studio string
	addr   string
	ctx    context.Context
	cancel context.CancelFunc

	mu    sync.Mutex
	state launcher.State
	busy  bool // a start or a quit runs
}

func (a *trayApp) set(s launcher.State) {
	a.mu.Lock()
	a.state = s
	a.mu.Unlock()
	a.t.setTip(tip(s, a.addr))
}

// watch follows Studio's state, except while a start or a quit decides it.
func (a *trayApp) watch() {
	a.l.Watch(a.ctx, func(s launcher.State) {
		a.mu.Lock()
		busy := a.busy
		a.mu.Unlock()
		if !busy {
			a.set(s)
		}
	})
}

// action runs one start or quit at a time; it reports false if another runs.
func (a *trayApp) action(during launcher.State, f func() error) bool {
	a.mu.Lock()
	if a.busy {
		a.mu.Unlock()
		return false
	}
	a.busy = true
	a.mu.Unlock()
	if !a.l.Serving(a.ctx) || during == launcher.Stopping {
		a.set(during)
	}
	err := f()
	a.mu.Lock()
	a.busy = false
	a.mu.Unlock()
	s := launcher.Stopped
	if a.l.Serving(a.ctx) {
		s = launcher.Running
	}
	a.set(s)
	if err != nil && a.ctx.Err() == nil {
		a.t.balloon("Sandbox Studio", err.Error(), true)
	}
	return true
}

func (a *trayApp) ensure() {
	a.action(launcher.Starting, func() error { return a.l.Ensure(a.ctx) })
}

func (a *trayApp) openStudio() {
	a.action(launcher.Starting, func() error { return a.l.OpenStudio(a.ctx) })
}

func (a *trayApp) menu() []menuItem {
	a.mu.Lock()
	s, busy := a.state, a.busy
	a.mu.Unlock()
	open := "Open Studio"
	if s == launcher.Stopped {
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
	if !a.action(launcher.Stopping, func() error {
		ctx, cancel := context.WithTimeout(a.ctx, 2*time.Minute)
		defer cancel()
		err = a.l.Quit(ctx)
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
func tip(s launcher.State, addr string) string {
	switch s {
	case launcher.Starting:
		return "Sandbox Studio is starting…"
	case launcher.Running:
		return "Sandbox Studio runs on http://" + addr
	case launcher.Stopping:
		return "Sandbox Studio is stopping…"
	}
	return "Sandbox Studio isn't running"
}
