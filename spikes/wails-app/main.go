// Command wails-app is the spike of a native Studio window (docs/design/feedback-v0.8.md, "A
// native app with Wails 3"): a Wails v3 window that loads the running Studio's own URL instead
// of embedding the UI, and a tray with Open Studio and Quit.
//
// It answers the spike's questions: whether the webview resolves *.localhost (previews),
// whether links that open a new window can go to the system browser, and whether the login
// cookie outlives a restart. With -selftest it loads Studio in a hidden window, prints what it
// found as JSON and exits.
//
// It is a module of its own, so Studio's build needs neither cgo nor GTK. On Linux it builds
// with GTK3 and WebKitGTK 4.1 (-tags gtk3) until Wails v3.1 takes GTK4 by default.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// The page's messages to the app, through the webview's message handler. Only Studio's own
// origin may send them.
const (
	msgOpen     = "studio:open "     // + a URL for the system browser
	msgSelfTest = "studio:selftest " // + the self-test's JSON
)

// shim runs in every page Studio loads: links and window.open calls that would make a new
// window go to the system browser instead, as WebKitGTK and WebView2 open none on their own.
const shim = `(() => {
  if (window.__studioShim) return
  window.__studioShim = true
  const post = (m) => (window.webkit?.messageHandlers?.external ?? window.chrome?.webview)?.postMessage(m)
  const open = (u) => post(%q + new URL(u, location.href).href)
  window.open = (u) => { if (u) open(u); return null }
  document.addEventListener("click", (e) => {
    const a = e.target instanceof Element && e.target.closest("a[href]")
    if (!a || e.defaultPrevented) return
    const external = new URL(a.href).origin !== location.origin
    if (a.target === "_blank" || external) { e.preventDefault(); open(a.href) }
  }, true)
})()`

// selfTest reports what the page sees: the address it ended on, whether the API takes its
// session, and whether a *.localhost name reaches Studio.
const selfTest = `(async () => {
  const post = (m) => (window.webkit?.messageHandlers?.external ?? window.chrome?.webview)?.postMessage(m)
  // The page trades a login link's token for its cookie after it loads.
  await new Promise((r) => setTimeout(r, 3000))
  const out = { href: location.href, userAgent: navigator.userAgent }
  try {
    const r = await fetch("/api/environments", { credentials: "include" })
    out.session = r.status
  } catch (e) { out.session = String(e) }
  try {
    await fetch("http://wails-probe.localhost:" + location.port + "/", { mode: "no-cors" })
    out.localhost = "resolves"
  } catch (e) { out.localhost = String(e) }
  // The shim sends this to the app, which in a self-test records it instead of opening it.
  out.shim = String(window.__studioShim) + " " + String(window.open).slice(0, 60)
  try { window.open("/selftest-popup") } catch (e) { out.openError = String(e) }
  // Messages can arrive out of order; give the open its head start.
  await new Promise((r) => setTimeout(r, 500))
  post(%q + JSON.stringify(out))
})()`

func main() {
	addr := flag.String("addr", "127.0.0.1:7878", "the running Studio's address")
	studio := flag.String("studio", "studio", "the studio binary, for a one-time login link")
	start := flag.String("url", "", "the page to open; default a login link from -studio, else Studio's address")
	test := flag.Bool("selftest", false, "load Studio in a hidden window, print what it found and exit")
	flag.Parse()

	base := "http://" + *addr
	page := *start
	if page == "" {
		page = loginURL(*studio, *addr, base)
	}

	var app *application.App
	var win *application.WebviewWindow
	done := make(chan map[string]any, 1)
	opened := "" // what the page asked the browser to open, in a self-test
	app = application.New(application.Options{
		Name:        "Sandbox Studio",
		Description: "Sandbox Studio in a window of its own",
		Mac:         application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: *test},
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
			if from == nil || !sameOrigin(from.Origin, base) {
				log.Printf("ignored a message from %v", from)
				return
			}
			switch {
			case strings.HasPrefix(msg, msgOpen):
				target := strings.TrimPrefix(msg, msgOpen)
				if *test {
					log.Printf("self-test: the page asked to open %s", target)
					opened = target
					return
				}
				if u, err := url.Parse(target); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
					log.Printf("opening %s in the browser", target)
					_ = app.Browser.OpenURL(target)
				}
			case strings.HasPrefix(msg, msgSelfTest):
				var out map[string]any
				_ = json.Unmarshal([]byte(strings.TrimPrefix(msg, msgSelfTest)), &out)
				out["opened"] = opened
				select {
				case done <- out:
				default:
				}
			}
		},
	})

	// ExecJS waits for Wails' own runtime, which a page from Studio never loads, so the shim goes
	// in as the window's JS: WebKitGTK runs it after each load. WebView2 runs a window's JS only
	// for HTML windows, so on Windows Studio's UI has to carry the shim itself.
	js := fmt.Sprintf(shim, msgOpen)
	if *test {
		js += ";" + fmt.Sprintf(selfTest, msgSelfTest)
	}
	win = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:   "studio",
		Title:  "Sandbox Studio",
		Width:  1280,
		Height: 840,
		URL:    page,
		Hidden: *test,
		JS:     js,
	})
	if *test {
		go func() {
			var out map[string]any
			select {
			case out = <-done:
			case <-time.After(30 * time.Second):
				out = map[string]any{"error": "no answer from the page in 30s"}
			}
			b, _ := json.MarshalIndent(out, "", "  ")
			fmt.Println(string(b))
			app.Quit()
		}()
	} else {
		// Closing the window hides it; the tray brings it back or quits.
		win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
			win.Hide()
			e.Cancel()
		})
		tray := app.SystemTray.New()
		tray.SetLabel("Studio")
		menu := app.NewMenu()
		menu.Add("Open Studio").OnClick(func(*application.Context) { win.Show().Focus() })
		menu.Add("Open in browser").OnClick(func(*application.Context) {
			_ = app.Browser.OpenURL(loginURL(*studio, *addr, base))
		})
		menu.AddSeparator()
		menu.Add("Quit").OnClick(func(*application.Context) { app.Quit() })
		tray.SetMenu(menu)
	}

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

// loginURL asks studio for a one-time login link, or answers base when it can't.
func loginURL(studio, addr, base string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, studio, "-addr", addr, "login-url")
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		log.Printf("no login link from %s: %v", studio, err)
		return base
	}
	return strings.TrimSpace(string(out))
}

func sameOrigin(origin, base string) bool {
	o, err1 := url.Parse(origin)
	b, err2 := url.Parse(base)
	return err1 == nil && err2 == nil && o.Scheme == b.Scheme && o.Host == b.Host
}
