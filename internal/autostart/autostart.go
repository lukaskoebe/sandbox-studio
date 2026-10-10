// Package autostart starts Studio when the user logs in. It writes one user-scope entry
// per OS and never needs administrator rights:
//
//   - Linux: a systemd user unit, enabled through a default.target.wants link
//   - macOS: a LaunchAgent plist with RunAtLoad
//   - Windows: a value under HKCU\Software\Microsoft\Windows\CurrentVersion\Run
//
// The entries take effect at the next login; nothing is started right away.
package autostart

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Name identifies Studio's entry in each OS's autostart mechanism.
const (
	unitName  = "sandbox-studio.service"
	agentName = "io.github.lukaskoebe.sandbox-studio"
	runValue  = "Sandbox Studio"
)

// Status describes the autostart entry.
type Status struct {
	Supported bool   `json:"supported"`
	Enabled   bool   `json:"enabled"`
	Path      string `json:"path,omitempty" doc:"The file or registry value that starts Studio"`
	Command   string `json:"command,omitempty" doc:"The Studio binary the entry starts"`
	// Stale means the entry starts something other than this binary, e.g. after an update
	// moved it. Enabling again rewrites it.
	Stale bool `json:"stale"`
}

// RunKey is the Windows per-user Run key.
type RunKey interface {
	Get(name string) (value string, ok bool, err error)
	Set(name, value string) error
	Delete(name string) error
}

// Service manages the entry for one user.
type Service struct {
	GOOS       string
	Home       string // the user's home directory (macOS LaunchAgents)
	ConfigHome string // XDG_CONFIG_HOME or ~/.config (Linux)
	Exe        string // the Studio binary to start
	Run        RunKey // Windows only
}

// Default returns the service for the current user and binary.
func Default() (*Service, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	cfg := os.Getenv("XDG_CONFIG_HOME")
	if cfg == "" {
		cfg = filepath.Join(home, ".config")
	}
	return &Service{GOOS: runtime.GOOS, Home: home, ConfigHome: cfg, Exe: exe, Run: hostRunKey()}, nil
}

// ErrUnsupported is returned on platforms without an autostart mechanism.
var ErrUnsupported = errors.New("autostart is not supported on this platform")

// Status reports whether the entry exists and starts this binary.
func (s *Service) Status() (Status, error) {
	switch s.GOOS {
	case "linux", "darwin":
		path, want := s.file()
		got, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			return Status{Supported: true, Path: path}, nil
		}
		if err != nil {
			return Status{Supported: true, Path: path}, err
		}
		st := Status{Supported: true, Enabled: true, Path: path, Command: s.Exe, Stale: string(got) != want}
		if s.GOOS == "linux" {
			if _, err := os.Lstat(s.wantsLink()); err != nil {
				st.Enabled = false // the unit exists but is not enabled
			}
		}
		return st, nil
	case "windows":
		if s.Run == nil {
			return Status{}, ErrUnsupported
		}
		path := `HKCU\Software\Microsoft\Windows\CurrentVersion\Run\` + runValue
		v, ok, err := s.Run.Get(runValue)
		if err != nil || !ok {
			return Status{Supported: true, Path: path}, err
		}
		return Status{Supported: true, Enabled: true, Path: path, Command: s.Exe, Stale: v != quoteWindows(s.Exe)}, nil
	}
	return Status{}, ErrUnsupported
}

// Enable writes the entry, replacing an existing one.
func (s *Service) Enable() (Status, error) {
	if err := s.checkExe(); err != nil {
		return Status{}, err
	}
	switch s.GOOS {
	case "linux", "darwin":
		path, content := s.file()
		if err := writeFile(path, content); err != nil {
			return Status{}, err
		}
		if s.GOOS == "linux" {
			link := s.wantsLink()
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				return Status{}, err
			}
			if err := os.Remove(link); err != nil && !errors.Is(err, os.ErrNotExist) {
				return Status{}, err
			}
			if err := os.Symlink(filepath.Join("..", unitName), link); err != nil {
				return Status{}, err
			}
		}
	case "windows":
		if s.Run == nil {
			return Status{}, ErrUnsupported
		}
		if err := s.Run.Set(runValue, quoteWindows(s.Exe)); err != nil {
			return Status{}, err
		}
	default:
		return Status{}, ErrUnsupported
	}
	return s.Status()
}

// Disable removes the entry. A missing entry is fine.
func (s *Service) Disable() (Status, error) {
	switch s.GOOS {
	case "linux", "darwin":
		path, _ := s.file()
		var paths []string
		if s.GOOS == "linux" {
			paths = append(paths, s.wantsLink())
		}
		for _, p := range append(paths, path) {
			if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
				return Status{}, err
			}
		}
	case "windows":
		if s.Run == nil {
			return Status{}, ErrUnsupported
		}
		if err := s.Run.Delete(runValue); err != nil {
			return Status{}, err
		}
	default:
		return Status{}, ErrUnsupported
	}
	return s.Status()
}

// checkExe refuses binaries that won't exist at the next login, such as `go run` builds.
func (s *Service) checkExe() error {
	if !isAbs(s.GOOS, s.Exe) {
		return errors.New("the Studio binary's path is unknown")
	}
	if strings.Contains(filepath.ToSlash(s.Exe), "/go-build") {
		return errors.New("this Studio runs from a temporary `go run` build; start an installed studio binary to enable autostart")
	}
	return nil
}

// file returns the entry's path and content on Linux and macOS.
func (s *Service) file() (path, content string) {
	if s.GOOS == "darwin" {
		return filepath.Join(s.Home, "Library", "LaunchAgents", agentName+".plist"), launchAgent(s.Exe, filepath.Join(s.Home, "Library", "Logs", "Sandbox Studio.log"))
	}
	return filepath.Join(s.ConfigHome, "systemd", "user", unitName), systemdUnit(s.Exe)
}

func (s *Service) wantsLink() string {
	return filepath.Join(s.ConfigHome, "systemd", "user", "default.target.wants", unitName)
}

// systemdUnit starts Studio with the user session. KillMode=process keeps the sandbox VMs,
// which are detached children of msb, running when Studio stops, as they do without systemd.
func systemdUnit(exe string) string {
	return fmt.Sprintf(`# Written by Sandbox Studio (Settings > Start at login).
[Unit]
Description=Sandbox Studio
After=network-online.target

[Service]
ExecStart=%s
Restart=on-failure
RestartSec=5
KillMode=process

[Install]
WantedBy=default.target
`, quoteSystemd(exe))
}

// launchAgent starts Studio at login. AbandonProcessGroup keeps the sandbox VMs running
// when Studio exits.
func launchAgent(exe, log string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<!-- Written by Sandbox Studio (Settings > Start at login). -->
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>AbandonProcessGroup</key>
	<true/>
	<key>ProcessType</key>
	<string>Interactive</string>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`, agentName, xmlEscape(exe), xmlEscape(log), xmlEscape(log))
}

// quoteSystemd quotes a path for ExecStart: systemd expands % specifiers and splits on
// spaces unless the word is quoted.
func quoteSystemd(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%", "$", "$$")
	return `"` + r.Replace(s) + `"`
}

// isAbs is filepath.IsAbs for goos, so that every OS's rules are testable anywhere.
func isAbs(goos, p string) bool {
	if goos == "windows" {
		return len(p) > 2 && p[1] == ':' && (p[2] == '\\' || p[2] == '/') || strings.HasPrefix(p, `\\`)
	}
	return strings.HasPrefix(p, "/")
}

func quoteWindows(s string) string { return `"` + s + `"` }

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;").Replace(s)
}

func writeFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
