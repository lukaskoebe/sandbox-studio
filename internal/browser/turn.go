package browser

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// maxTurnItems caps the pages and downloads a userTurn remembers.
const maxTurnItems = 10

// userTurn is what Studio saw of the user's takeover of a session: when it started, the
// pages the browser was on and the downloads the user sent to a sandbox. Clicks and typing
// reach the page as raw input and are not kept, as they may be a password.
type userTurn struct {
	since     time.Time
	pages     []string // in order, without repeats; the first is where the agent left off
	more      int      // pages past maxTurnItems
	downloads []string
}

// saw notes that the browser is on url. A nil turn (no takeover) ignores it.
func (t *userTurn) saw(url string) {
	if t == nil || url == "" {
		return
	}
	for _, p := range t.pages {
		if p == url {
			return
		}
	}
	if len(t.pages) < maxTurnItems {
		t.pages = append(t.pages, url)
	} else {
		t.more++
	}
}

// note tells the agent, in its next browser call, that the user drove and what Studio saw:
// the pages past the one the agent left, downloads sent, and where the page is now.
func (t *userTurn) note(now time.Time, at string) string {
	if t == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Note: the user took over this browser for %s and handed it back.", turnLength(now.Sub(t.since)))
	if len(t.pages) > 1 {
		b.WriteString(" While they drove, it was on " + strings.Join(t.pages[1:], ", "))
		if t.more > 0 {
			fmt.Fprintf(&b, " and %d more", t.more)
		}
		b.WriteString(".")
	} else if t.more == 0 {
		b.WriteString(" The browser stayed on the page you left.")
	}
	if len(t.downloads) > 0 {
		b.WriteString(" They sent downloads: " + strings.Join(t.downloads, "; ") + ".")
	}
	if at != "" {
		b.WriteString(" The page is now " + at + ".")
	}
	b.WriteString(" What they clicked or typed is not recorded; earlier refs are stale, so take a browser_snapshot before you act on the page.")
	return b.String()
}

func turnLength(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "less than a minute"
	case d < 2*time.Minute:
		return "a minute"
	case d < time.Hour:
		return fmt.Sprintf("%d minutes", int(d/time.Minute))
	default:
		return fmt.Sprintf("%.1f hours", d.Hours())
	}
}

// withHandback puts the note of a user's turn in front of a call's result or error, which is
// where the agent reads it.
func withHandback(res any, err error, note string) (any, error) {
	if note == "" {
		return res, err
	}
	if err != nil {
		return nil, errors.New(note + "\n\n" + err.Error())
	}
	switch r := res.(type) {
	case string:
		return note + "\n\n" + r, nil
	case Image:
		r.Text = note + "\n\n" + r.Text
		return r, nil
	}
	return res, nil
}
