# Wails 3 window spike

A Wails v3.0.0 window around the running Studio, as planned in
[feedback-v0.8.md](../../docs/design/feedback-v0.8.md#a-native-app-with-wails-3). It loads
Studio's own URL instead of embedding the UI, and has a tray with Open Studio, Open in
browser and Quit. It is a module of its own, so Studio's build needs neither cgo nor GTK.

```sh
go build -tags gtk3 -o /tmp/wails-app .           # Linux: GTK3 and WebKitGTK 4.1
GOOS=windows go build -ldflags -H=windowsgui .    # Windows: no cgo
/tmp/wails-app -addr 127.0.0.1:7878               # a window on the running Studio
/tmp/wails-app -addr 127.0.0.1:7911 -selftest -studio bin/studio-e2e   # prints JSON, exits
```

Without `-url` it opens a fresh one-time link from `studio login-url`. `-selftest` loads
Studio in a hidden window and prints the page's address, whether the API takes its session,
whether `wails-probe.localhost` reaches Studio, and what `window.open` asked the app to open
(in a self-test the app records it instead of opening the browser).
