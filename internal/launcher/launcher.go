// Package launcher starts, opens and stops the Studio server for the programs around it: the
// Windows tray (cmd/studiow) and the Studio app (cmd/studio-app).
//
// Neither runs the server itself. studio stays the one binary that serves, updates and rolls
// back; they only watch it over HTTP, so an update that restarts studio needs nothing from
// them, and Quit asks Studio to shut down (`studio quit`).
package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// State is what the server does, as a tray shows it.
type State int

const (
	Starting State = iota
	Running
	Stopped
	Stopping
)

// StartWait is how long a start may take before the tray gives up waiting: a first start
// migrates the database and pulls nothing, so a minute is plenty, but a slow disk isn't rare.
const StartWait = 3 * time.Minute

// Launcher starts, opens and stops one Studio. Its functions are the host's; tests replace
// them.
type Launcher struct {
	Addr string
	// Serving reports whether Studio answers on Addr.
	Serving func(ctx context.Context) bool
	// Start starts the server without a console window. exited gets the exit error when that
	// process ends.
	Start func() (exited <-chan error, err error)
	// Studio runs a studio command, such as login-url, and returns its output.
	Studio func(ctx context.Context, args ...string) (string, error)
	// Open shows a page: in the browser, or in the app's window.
	Open func(url string) error
	Poll time.Duration
}

// Ensure starts Studio unless it serves already, and waits until it does.
func (l *Launcher) Ensure(ctx context.Context) error {
	if l.Serving(ctx) {
		return nil
	}
	exited, err := l.Start()
	if err != nil {
		return fmt.Errorf("start studio: %w", err)
	}
	deadline := time.NewTimer(StartWait)
	defer deadline.Stop()
	tick := time.NewTicker(l.Poll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-exited:
			// A second Studio exits at once when one already runs; it may serve by now.
			if l.Serving(ctx) {
				return nil
			}
			if err == nil {
				err = errors.New("it exited")
			}
			return fmt.Errorf("Studio stopped while starting (%v); its log says why", err)
		case <-deadline.C:
			return fmt.Errorf("Studio didn't answer on %s within %s", l.Addr, StartWait)
		case <-tick.C:
			if l.Serving(ctx) {
				return nil
			}
		}
	}
}

// OpenStudio starts Studio if it must and opens it with a fresh login link.
func (l *Launcher) OpenStudio(ctx context.Context) error {
	link, err := l.LoginURL(ctx)
	if err != nil {
		return err
	}
	return l.Open(link)
}

// LoginURL starts Studio if it must and returns a fresh one-time login link.
func (l *Launcher) LoginURL(ctx context.Context) (string, error) {
	if err := l.Ensure(ctx); err != nil {
		return "", err
	}
	link, err := l.Studio(ctx, "-addr", l.Addr, "login-url")
	if err != nil {
		return "", fmt.Errorf("get a login link: %w", err)
	}
	link = strings.TrimSpace(link)
	if !strings.HasPrefix(link, "http://") {
		return "", fmt.Errorf("studio login-url printed %q", link)
	}
	return link, nil
}

// Quit asks Studio to shut down and waits until it has.
func (l *Launcher) Quit(ctx context.Context) error {
	if !l.Serving(ctx) {
		return nil
	}
	out, err := l.Studio(ctx, "-addr", l.Addr, "quit")
	if err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(out+" "+err.Error()))
	}
	return nil
}

// Watch reports Studio's state whenever it changes, until ctx ends.
func (l *Launcher) Watch(ctx context.Context, report func(State)) {
	last := State(-1)
	tick := time.NewTicker(l.Poll)
	defer tick.Stop()
	for {
		s := Stopped
		if l.Serving(ctx) {
			s = Running
		}
		if s != last {
			report(s)
			last = s
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// HTTPServing reports whether a Studio answers its auth status on addr.
func HTTPServing(addr string) func(ctx context.Context) bool {
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 2 * time.Second}
	return func(ctx context.Context) bool {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/api/auth/status", nil)
		if err != nil {
			return false
		}
		resp, err := client.Do(req)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		var body struct {
			Authenticated *bool `json:"authenticated"`
		}
		return resp.StatusCode == http.StatusOK && json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&body) == nil && body.Authenticated != nil
	}
}
