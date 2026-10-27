package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// The page's messages to the app (web/src/lib/desktop.ts), through the webview's message
// handler.
const (
	msgOpen     = "studio:open "     // + an http(s) URL for the system browser
	msgNotify   = "studio:notify "   // + a notice as JSON
	msgSelfTest = "studio:selftest " // + the self-test's JSON
)

// notice is a notification the page asks the system to show.
type notice struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

// bridge takes the page's messages. Only Studio's own origin may send them: a page the window
// was led to elsewhere gets nothing from the app.
type bridge struct {
	base     string
	open     func(url string) error
	notify   func(notice) error
	selfTest func(map[string]any) // set in a self-test
	opened   string               // what the page asked to open, in a self-test
}

func (b *bridge) handle(msg, origin string) error {
	if !sameOrigin(origin, b.base) {
		return fmt.Errorf("ignored a message from %q", origin)
	}
	switch {
	case strings.HasPrefix(msg, msgOpen):
		target := strings.TrimPrefix(msg, msgOpen)
		u, err := url.Parse(target)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("the page asked to open %q, which isn't a web address", target)
		}
		return b.open(u.String())
	case strings.HasPrefix(msg, msgNotify):
		var n notice
		if err := json.Unmarshal([]byte(strings.TrimPrefix(msg, msgNotify)), &n); err != nil {
			return fmt.Errorf("a notification: %w", err)
		}
		if n.Title == "" {
			return errors.New("a notification without a title")
		}
		if n.ID == "" {
			n.ID = n.Title
		}
		return b.notify(n)
	case strings.HasPrefix(msg, msgSelfTest) && b.selfTest != nil:
		var out map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(msg, msgSelfTest)), &out); err != nil {
			return err
		}
		b.selfTest(out)
		return nil
	}
	return nil // Wails' own messages, from pages it serves; Studio's pages send none.
}

func sameOrigin(origin, base string) bool {
	o, err1 := url.Parse(origin)
	b, err2 := url.Parse(base)
	return err1 == nil && err2 == nil && o.Scheme == b.Scheme && o.Host == b.Host
}
