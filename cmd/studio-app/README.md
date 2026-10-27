# studio-app

Studio in a window of its own: a Wails v3 window on the running Studio, a tray and the
system's notifications. `studio app` starts it. See [the Studio app](../../docs/app.md).

It is a module of its own, so `studio` builds without cgo, GTK or WebKit.

```sh
make app                                                   # bin/studio-app, from the repository root
go test -tags gtk3 ./...                                   # Linux: GTK3 and WebKitGTK 4.1
GOOS=windows CGO_ENABLED=0 go build -ldflags -H=windowsgui .
studio-app -addr 127.0.0.1:7911 -selftest -studio ../../bin/studio-e2e   # prints JSON, exits
```
