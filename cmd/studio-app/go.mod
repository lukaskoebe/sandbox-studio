module github.com/lukaskoebe/sandbox-studio/cmd/studio-app

go 1.26.0

require github.com/wailsapp/wails/v3 v3.0.0

require (
	git.sr.ht/~jackmordaunt/go-toast/v2 v2.0.3 // indirect
	github.com/adrg/xdg v0.5.3 // indirect
	github.com/coder/websocket v1.8.15 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	github.com/mattn/go-colorable v0.1.15 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	golang.org/x/sys v0.48.0 // indirect
)

require github.com/lukaskoebe/sandbox-studio v0.0.0

// The app shares Studio's launcher (internal/launcher) and is built from the same checkout.
replace github.com/lukaskoebe/sandbox-studio => ../..
