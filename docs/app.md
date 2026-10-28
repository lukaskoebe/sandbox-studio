# The Studio app

`studio app` opens Studio in a window of its own, with a tray icon and the system's
notifications. The app is `studio-app` (`studio-app.exe` on Windows), a [Wails
v3](https://v3.wails.io) program in [`cmd/studio-app`](../cmd/studio-app) that each release
archive carries next to `studio`. The browser keeps working beside it and instead of it: the
server is still the product, and the app is a window and a tray around it.

## What it does

- **It never serves anything.** It asks `GET /api/auth/status` whether a Studio answers on
  `-addr` (default `127.0.0.1:7878`), and starts `studio -no-browser` in the background when
  none does: detached, logging to `logs/studio.log` in the data directory, outside the app's
  process group, so Studio keeps running when the app quits.
- **It loads Studio's own address**, not an embedded copy of the UI, so cookies, the event
  stream and `*.localhost` previews behave as in a browser. It opens a fresh one-time link
  from `studio login-url` on every start, as WebKitGTK keeps no cookies across restarts.
- **The tray** has Open Studio, Open in browser (a fresh login link in the system browser),
  Close the app and Quit Studio (`studio quit`, then the app). Closing the window hides it on
  macOS and Windows, where the tray or the Dock brings it back; on Linux, whose desktops may
  show no tray, it quits the app.
- **One app per user**: a second start brings the open window to the front.

The process logic (state, start, login links, quit) is `internal/launcher`, shared with the
Windows tray launcher `studiow.exe`.

## The page and the app

Studio's UI knows when it runs in the app ([`web/src/lib/desktop.ts`](../web/src/lib/desktop.ts)):
the webview has a message handler, `window.webkit.messageHandlers.external` (WebKit) or
`window.chrome.webview` (WebView2). The page posts plain strings to it:

| Message | Does |
|---|---|
| `studio:open <url>` | opens an http(s) address in the system browser |
| `studio:notify {"id","title","body"}` | shows a system notification; clicking it shows the window |
| `studio:selftest <json>` | the self-test's result (below) |

The app takes them only from Studio's own origin (`OriginInfo.Origin`), as
`OriginInfo.IsMainFrame` was false for the main frame. The webview opens no new windows, so
the page sends `window.open`, links with a target and links to other origins (OAuth sign-ins,
previews) through `studio:open`, and keeps links within Studio in the window.

The app says what it does for the page in the login link, `?studio-app=notify` when the
system shows notifications (macOS shows them only for an app the user allowed) or
`?studio-app=1`. The page keeps it in `sessionStorage` and drops it from the address. Without
`notify` the page keeps the browser's notifications.

## `studio app`

`studio app` starts `studio-app` from next to the `studio` binary (on macOS also from
`Sandbox Studio.app/Contents/MacOS` beside it) and waits three seconds for it to fail. It
opens Studio in the browser instead when there is no app, no display (neither `DISPLAY` nor
`WAYLAND_DISPLAY` on Linux; GTK without one waits rather than fails), or the app exits with
an error, such as a missing WebKitGTK; its last log line, from `logs/studio-app.log`, says
why. When the loader couldn't find a library, it also names the packages to install, or on
NixOS (`/etc/NIXOS`) the ones for `programs.nix-ld.libraries` (`nixAppLibraries` in
`cmd/studio/app.go`, one for each library `studio-app` links). An exit with status 0 is a second app that showed the first one's window.

## Building

The app is a Go module of its own, with `replace github.com/lukaskoebe/sandbox-studio =>
../..`, so `studio`'s build needs neither cgo, GTK nor WebKit, and a headless machine runs
`studio` without them. The root module's `./...` doesn't include it.

```sh
make app                                      # bin/studio-app
cd cmd/studio-app && go test -tags gtk3 ./... # Linux: GTK3 and WebKitGTK 4.1 headers
GOOS=windows CGO_ENABLED=0 go build -ldflags -H=windowsgui ./cmd/studio-app
```

On Linux it builds with `-tags gtk3` against WebKitGTK 4.1 until Wails v3.1 takes GTK4 by
default; `libgtk-3-dev` and `libwebkit2gtk-4.1-dev` on Debian and Ubuntu. On Windows it
builds without cgo and uses WebView2, which Windows 10 and 11 ship. The `app` job in CI
vets, tests and builds it on all three systems, and the release workflow builds it into each
archive, signs it on macOS and Windows, and the updater replaces it with `studio`.

`studio-app -selftest -addr <addr> -studio <studio>` loads Studio in a hidden window and
prints, as JSON, the address the page ended on, whether the API took its session, whether a
`*.localhost` name reaches Studio, what the app was asked to open for `window.open` (it
records that instead of opening it) and the `studio-app` value the page kept. Run it against
an e2e Studio with its own `SANDBOX_STUDIO_HOME`.

## Not done yet

- WebView2's messages and macOS are built in CI but untried by hand. On macOS the app is a
  bare binary, not a bundle, so its notifications may be refused; the page keeps its own then.
- The autostart entry still starts `studio` (or `studiow.exe` on Windows), not the app.
