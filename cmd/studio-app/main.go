// Command studio-app is Studio in a window of its own (docs/app.md): a Wails v3 window that
// loads the running Studio's own address, a tray, and the system's notifications.
//
// It never serves anything itself. It starts the studio binary next to it when no Studio
// answers, opens a fresh one-time login link in its window, and leaves the server running
// when it quits, so the browser keeps working beside it and instead of it. Studio's UI knows
// when it runs here (web/src/lib/desktop.ts): it sends what would open a new window, and its
// notifications, through the webview's message handler, and the app takes them only from
// Studio's own origin.
//
// It is a module of its own, so Studio's build needs neither GTK nor WebKit, and a headless
// machine runs studio without them. On Linux it builds with GTK3 and WebKitGTK 4.1
// (-tags gtk3) until Wails v3.1 takes GTK4 by default.
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"sync/atomic"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/launcher"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

// optionalNotifier is the system's notifications where the system may refuse them: macOS
// takes them only from an app bundle with an identifier, and studio-app is a bare binary
// there (docs/app.md). A notifier that doesn't start leaves the app running without it, and
// the page keeps its own notifications. Nothing calls it then: on macOS that would crash.
type optionalNotifier struct {
	*notifications.NotificationService
	on atomic.Bool // it started
}

func (n *optionalNotifier) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	if err := n.NotificationService.ServiceStartup(ctx, options); err != nil {
		log.Printf("notifications: %v; the page shows its own", err)
		return nil
	}
	n.on.Store(true)
	return nil
}

//go:embed icon.png
var icon []byte

// version is set by the release build, as studio's is.
var version = "dev"

func main() {
	addr := flag.String("addr", "127.0.0.1:7878", "the address Studio listens on")
	studioFlag := flag.String("studio", "", "the studio binary; default the one next to this app")
	test := flag.Bool("selftest", false, "load Studio in a hidden window, print what the page found as JSON and exit")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("studio-app " + version)
		return
	}

	// GTK without a display doesn't fail, it waits: say so and leave, so that `studio app`
	// opens the browser instead.
	if goruntime.GOOS == "linux" && os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		log.Fatal("no graphical display (neither DISPLAY nor WAYLAND_DISPLAY is set)")
	}
	studio, err := studioBinary(*studioFlag)
	if err != nil {
		log.Fatal(err)
	}
	base := "http://" + *addr

	var app *application.App
	var win *application.WebviewWindow
	notifier := &optionalNotifier{NotificationService: notifications.New()}
	l := &launcher.Launcher{
		Addr:    *addr,
		Serving: launcher.HTTPServing(*addr),
		Start:   func() (<-chan error, error) { return launcher.StartServer(studio, *addr) },
		Studio: func(ctx context.Context, args ...string) (string, error) {
			return launcher.RunStudio(ctx, studio, args...)
		},
		Poll: 500 * time.Millisecond,
	}
	notify := false // whether the system shows the page's notifications, see appLink
	l.Open = func(link string) error {
		link, err := appLink(link, notify)
		if err != nil {
			return err
		}
		application.InvokeAsync(func() {
			win.SetURL(link)
			win.Show().Focus()
		})
		return nil
	}
	b := &bridge{
		base: base,
		open: func(url string) error { return app.Browser.OpenURL(url) },
		notify: func(n notice) error {
			if !notifier.on.Load() {
				return errors.New("the system takes no notifications from the app")
			}
			return notifier.SendNotification(notifications.NotificationOptions{ID: n.ID, Title: n.Title, Body: n.Body})
		},
	}
	selfTest := make(chan map[string]any, 1)
	if *test {
		b.open = func(url string) error { b.opened = url; return nil }
		b.selfTest = func(out map[string]any) {
			select {
			case selfTest <- out:
			default:
			}
		}
	}

	app = application.New(application.Options{
		Name:        "Sandbox Studio",
		Description: "Sandbox Studio in a window of its own",
		Icon:        icon,
		Services:    []application.Service{application.NewService(notifier)},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: *test,
		},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "dev.sandbox-studio.app",
			// A second start shows the window that is already open.
			OnSecondInstanceLaunch: func(application.SecondInstanceData) {
				if win != nil {
					win.Show().Focus()
				}
			},
		},
		RawMessageHandler: func(_ application.Window, msg string, from *application.OriginInfo) {
			origin := ""
			if from != nil {
				origin = from.Origin
			}
			if err := b.handle(msg, origin); err != nil {
				log.Print(err)
			}
		},
	})
	// Clicking a notification brings the window to the front, on the page it was on.
	notifier.OnNotificationResponse(func(notifications.NotificationResult) {
		application.InvokeAsync(func() { win.Show().Focus() })
	})

	js := ""
	if *test {
		js = fmt.Sprintf(selfTestJS, msgSelfTest)
	}
	win = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:   "studio",
		Title:  "Sandbox Studio",
		Width:  1280,
		Height: 840,
		HTML:   page("Starting Studio…", ""),
		Hidden: *test,
		JS:     js,
	})

	// Studio may take a while to start; the window says so meanwhile, and why if it can't.
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		go func() {
			// macOS shows notifications only for an app the user allowed, and a bare binary
			// may get none; then the page keeps its own. The self-test asks no one: the
			// question can wait for an answer.
			if notifier.on.Load() && !*test {
				ok, err := notifier.RequestNotificationAuthorization()
				notify = ok && err == nil
				if err != nil {
					log.Printf("notifications: %v", err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), launcher.StartWait)
			defer cancel()
			if err := l.OpenStudio(ctx); err != nil {
				log.Printf("open Studio: %v", err)
				application.InvokeAsync(func() {
					win.SetHTML(page("Studio didn't start", err.Error()+"\n\nRun studio in a terminal to see why, or studio doctor."))
				})
			}
		}()
	})

	if *test {
		go func() {
			var out map[string]any
			select {
			case out = <-selfTest:
			case <-time.After(60 * time.Second):
				out = map[string]any{"error": "no answer from the page in 60s"}
			}
			out["opened"] = b.opened
			out["notifications"] = notifier.on.Load()
			j, _ := json.MarshalIndent(out, "", "  ")
			fmt.Println(string(j))
			app.Quit()
		}()
	} else {
		// Closing the window hides it where a tray or the Dock brings it back. Linux desktops
		// may show no tray, so there it quits the app; Studio keeps running either way.
		if goruntime.GOOS != "linux" {
			win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
				win.Hide()
				e.Cancel()
			})
		}
		tray := app.SystemTray.New()
		if goruntime.GOOS == "darwin" {
			tray.SetTemplateIcon(icon)
		} else {
			tray.SetIcon(icon)
		}
		tray.SetTooltip("Sandbox Studio")
		menu := app.NewMenu()
		menu.Add("Open Studio").OnClick(func(*application.Context) { win.Show().Focus() })
		menu.Add("Open in browser").OnClick(func(*application.Context) {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), launcher.StartWait)
				defer cancel()
				link, err := l.LoginURL(ctx)
				if err != nil {
					log.Printf("open in browser: %v", err)
					link = base
				}
				_ = app.Browser.OpenURL(link)
			}()
		})
		menu.AddSeparator()
		menu.Add("Close the app").OnClick(func(*application.Context) { app.Quit() })
		menu.Add("Quit Studio").OnClick(func(*application.Context) {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				if err := l.Quit(ctx); err != nil {
					log.Printf("quit Studio: %v", err)
				}
				app.Quit()
			}()
		})
		tray.SetMenu(menu)
	}

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

// studioBinary is the studio binary the app starts and asks for login links: the one given,
// else the one next to the app (in a macOS bundle, also the one next to the bundle), else
// the one on PATH.
func studioBinary(given string) (string, error) {
	if given != "" {
		return given, nil
	}
	name := "studio"
	if goruntime.GOOS == "windows" {
		name = "studio.exe"
	}
	if exe, err := os.Executable(); err == nil {
		if exe, err = filepath.EvalSymlinks(exe); err == nil {
			dir := filepath.Dir(exe)
			candidates := []string{filepath.Join(dir, name)}
			if goruntime.GOOS == "darwin" {
				// Sandbox Studio.app/Contents/MacOS/studio-app, next to studio.
				candidates = append(candidates, filepath.Join(dir, "..", "..", "..", name))
			}
			for _, c := range candidates {
				if info, err := os.Stat(c); err == nil && info.Mode().IsRegular() {
					return filepath.Clean(c), nil
				}
			}
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("no %s next to the app or on PATH; unpack the whole release into one folder", name)
}

// appLink tells Studio's page that it runs in the app, and what the app does for it, in the
// studio-app query parameter of a login link: "notify" when the system shows its
// notifications, else "1". The page keeps it for the session and drops it from the address.
func appLink(link string, notify bool) (string, error) {
	u, err := url.Parse(link)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("studio-app", "1")
	if notify {
		q.Set("studio-app", "notify")
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// page is the window's own page while Studio starts, or when it can't.
func page(title, detail string) string {
	return `<!doctype html><meta charset="utf-8"><title>Sandbox Studio</title>
<style>
:root{color-scheme:light dark;font:14px/1.5 system-ui,sans-serif}
body{margin:0;display:grid;place-items:center;min-height:100vh;background:Canvas;color:CanvasText}
main{max-width:32rem;padding:2rem}
h1{font-size:1rem;font-weight:600;margin:0 0 .5rem}
p{white-space:pre-wrap;margin:0;opacity:.75;font-size:12px}
</style>
<main><h1>` + html.EscapeString(title) + `</h1><p>` + html.EscapeString(detail) + `</p></main>`
}

// selfTestJS reports what the page sees: the address it ended on, whether the API takes its
// session, whether a *.localhost name reaches Studio, and that Studio's UI routes window.open
// to the app.
const selfTestJS = `(async () => {
  if (location.protocol !== "http:") return
  const post = (m) => (window.webkit?.messageHandlers?.external ?? window.chrome?.webview)?.postMessage(m)
  // The page trades a login link's token for its cookie after it loads.
  await new Promise((r) => setTimeout(r, 3000))
  const out = { href: location.href, userAgent: navigator.userAgent, app: sessionStorage.getItem("studio.app") }
  try {
    const r = await fetch("/api/environments", { credentials: "include" })
    out.session = r.status
  } catch (e) { out.session = String(e) }
  try {
    await fetch("http://app-probe.localhost:" + location.port + "/", { mode: "no-cors" })
    out.localhost = "resolves"
  } catch (e) { out.localhost = String(e) }
  try { window.open("/selftest-popup") } catch (e) { out.openError = String(e) }
  // Messages can arrive out of order; give the open its head start.
  await new Promise((r) => setTimeout(r, 500))
  post(%q + JSON.stringify(out))
})()`
