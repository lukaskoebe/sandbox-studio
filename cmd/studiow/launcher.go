// Command studiow is Studio's Windows tray launcher. Built with -H windowsgui, it has no
// console window: it starts studio.exe from its own folder without one, shows a tray icon
// with Open Studio, Logs, Start at login and Quit, and opens Studio in the browser.
//
// It never runs the server itself. studio.exe stays the one binary that serves, updates and
// rolls back; studiow only watches it over HTTP, so an update that restarts studio.exe
// needs nothing from it, and Quit asks Studio to shut down (`studio quit`).
package main

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

// The states the tray shows.
type state int

const (
	stateStarting state = iota
	stateRunning
	stateStopped
	stateStopping
)

// startWait is how long a start may take before the tray gives up waiting: a first start
// migrates the database and pulls nothing, so a minute is plenty, but a slow disk isn't rare.
const startWait = 3 * time.Minute

// launcher starts, opens and stops one Studio. Its functions are the host's; tests replace
// them.
type launcher struct {
	addr string
	// serving reports whether Studio answers on addr.
	serving func(ctx context.Context) bool
	// start starts the server without a console window. exited is closed with the exit
	// error when that process ends.
	start func() (exited <-chan error, err error)
	// studio runs a studio.exe command, such as login-url, and returns its output.
	studio func(ctx context.Context, args ...string) (string, error)
	open   func(url string) error
	poll   time.Duration
}

// ensure starts Studio unless it serves already, and waits until it does.
func (l *launcher) ensure(ctx context.Context) error {
	if l.serving(ctx) {
		return nil
	}
	exited, err := l.start()
	if err != nil {
		return fmt.Errorf("start studio.exe: %w", err)
	}
	deadline := time.NewTimer(startWait)
	defer deadline.Stop()
	tick := time.NewTicker(l.poll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-exited:
			// A second Studio exits at once when one already runs; it may serve by now.
			if l.serving(ctx) {
				return nil
			}
			if err == nil {
				err = errors.New("it exited")
			}
			return fmt.Errorf("Studio stopped while starting (%v); its log says why", err)
		case <-deadline.C:
			return fmt.Errorf("Studio didn't answer on %s within %s", l.addr, startWait)
		case <-tick.C:
			if l.serving(ctx) {
				return nil
			}
		}
	}
}

// openStudio starts Studio if it must and opens it with a fresh login link.
func (l *launcher) openStudio(ctx context.Context) error {
	if err := l.ensure(ctx); err != nil {
		return err
	}
	link, err := l.studio(ctx, "-addr", l.addr, "login-url")
	if err != nil {
		return fmt.Errorf("get a login link: %w", err)
	}
	link = strings.TrimSpace(link)
	if !strings.HasPrefix(link, "http://") {
		return fmt.Errorf("studio login-url printed %q", link)
	}
	return l.open(link)
}

// quit asks Studio to shut down and waits until it has.
func (l *launcher) quit(ctx context.Context) error {
	if !l.serving(ctx) {
		return nil
	}
	out, err := l.studio(ctx, "-addr", l.addr, "quit")
	if err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(out+" "+err.Error()))
	}
	return nil
}

// watch reports Studio's state whenever it changes, until ctx ends.
func (l *launcher) watch(ctx context.Context, report func(state)) {
	last := state(-1)
	tick := time.NewTicker(l.poll)
	defer tick.Stop()
	for {
		s := stateStopped
		if l.serving(ctx) {
			s = stateRunning
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

// httpServing reports whether a Studio answers its auth status on addr.
func httpServing(addr string) func(ctx context.Context) bool {
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
