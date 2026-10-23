package browser

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// A note the user pins on the live view names the element under it, so that the message
// reads the same to an agent that can't see the screenshot. ElementAt asks the guest for
// that element with agentproto.BrowserElementAt, which runs the guest's own fixed script
// through agent-browser's eval (internal/guest/browser_element.go). The other ways in were
// worse:
//   - agent-browser 0.39's allow-listed commands have no hit test, and a snapshot would
//     renumber the refs the agents' calls and the policy checks rely on; eval leaves them be.
//   - The live view's stream takes only input and pacing, not CDP, and opening CDP to the
//     browser would put the one thing the guest keeps out of reach within the host's.
//
// The script only reads the page, never a form field's value, and the host sends it only the
// point, so the lookup gives the host no new way to run a script; agents can't reach it, as
// they never reach the browser VM. The page itself can lie to the script, as it can to the
// screenshot, so what comes back is sanitized and treated as the page's text, not Studio's.

// Errors of a lookup.
var (
	// ErrBusy is a lookup that would have waited for another command, such as an agent's.
	ErrBusy      = errors.New("the browser is busy with another command")
	ErrBadPoint  = errors.New("a point is two numbers from 0 to 100000, in CSS pixels")
	ErrNoElement = errors.New("there is no element at that point")
)

const (
	// elementWait is how long a lookup waits for the browser to finish another command,
	// and elementTimeout how long it may take: the user waits for the note to name it.
	elementWait    = 1500 * time.Millisecond
	elementTimeout = 3 * time.Second
	// maxElementPoint bounds a point, in CSS pixels, as the guest does.
	maxElementPoint = 100_000
)

// Element is the element under a point of a page.
type Element struct {
	Tag      string `json:"tag" doc:"The element's tag name, such as button"`
	Role     string `json:"role,omitempty" doc:"Its ARIA role, explicit or implied by the tag"`
	Name     string `json:"name,omitempty" doc:"Its accessible name: aria-labelledby, aria-label, a field's label, placeholder or title, an image's alt, or the text of a button, link or heading"`
	ID       string `json:"id,omitempty" doc:"Its id attribute"`
	Selector string `json:"selector,omitempty" doc:"A short CSS path to it: up to an element with a unique ID, or five steps up. >>> steps out of a shadow root to its host"`
	Text     string `json:"text,omitempty" doc:"Up to 300 characters of the text it shows, without the contents of form fields; empty for a field"`
	// A form field (input, textarea, select or editable content) never has its value here.
	Field       bool       `json:"field,omitempty" doc:"It is a form field; its value is never read"`
	Type        string     `json:"type,omitempty" doc:"An input's type attribute"`
	FieldName   string     `json:"fieldName,omitempty" doc:"A field's name attribute"`
	Placeholder string     `json:"placeholder,omitempty" doc:"A field's placeholder"`
	Href        string     `json:"href,omitempty" doc:"Where a link goes"`
	Box         ElementBox `json:"box" doc:"Its border box in CSS pixels of the viewport"`
	// Description names the element in one line of a message to an agent (Describe).
	Description string `json:"description" doc:"The element in one line, as a note's message names it"`
}

// ElementBox is an element's border box in CSS pixels of the page's viewport.
type ElementBox struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// ElementAt describes the element at (x, y), in CSS pixels of the viewport, of a session of
// the teammate's browser, for a note the user pins there. It neither waits long for another
// command (ErrBusy) nor counts as using the browser, and it doesn't touch the session's refs.
func (s *Service) ElementAt(ctx context.Context, envID, teammateID, session string, x, y float64) (Element, error) {
	if !validPoint(x) || !validPoint(y) {
		return Element{}, ErrBadPoint
	}
	if _, err := s.Store.Teammate(ctx, envID, teammateID); err != nil {
		return Element{}, err
	}
	st := s.state(envID, teammateID)
	s.mu.Lock()
	pg := st.pageFor(session)
	hidden := pg != nil && pg.visible
	s.mu.Unlock()
	switch {
	case pg == nil:
		return Element{}, ErrNoSession
	case hidden:
		// A credential shows on the page, so neither its picture nor its text leaves it.
		return Element{}, ErrHidden
	}
	t, err := s.streamTarget(ctx, envID, teammateID, session)
	if err != nil {
		return Element{}, err
	}
	wait := time.NewTimer(elementWait)
	defer wait.Stop()
	select {
	case st.run <- struct{}{}:
	case <-wait.C:
		return Element{}, ErrBusy
	case <-ctx.Done():
		return Element{}, ctx.Err()
	}
	defer func() { <-st.run }()
	s.mu.Lock()
	current := s.streamCurrentLocked(st, pg, envID, t.vm, t.session) && st.pageFor(session) == pg
	hidden = pg.visible
	secrets := pg.secrets
	s.mu.Unlock()
	switch {
	case !current:
		return Element{}, ErrNotLive
	case hidden:
		return Element{}, ErrHidden
	}
	ctx, cancel := context.WithTimeout(ctx, elementTimeout)
	defer cancel()
	data, _, err := s.exec(ctx, st, pg, agentproto.BrowserCommand{
		Args:      []string{agentproto.BrowserElementAt, formatPoint(x), formatPoint(y)},
		TimeoutMS: int(elementTimeout / time.Millisecond),
	})
	if err != nil {
		return Element{}, err
	}
	// eval answers {"result": …, "origin": …}; the result is null where no element is.
	var reply struct {
		Result *Element `json:"result"`
	}
	if err := json.Unmarshal(data, &reply); err != nil {
		return Element{}, errors.New("the browser's reply was not understood")
	}
	if reply.Result == nil {
		return Element{}, ErrNoElement
	}
	el := reply.Result.sanitized(secrets)
	if el.Tag == "" {
		return Element{}, errors.New("the browser's reply was not understood")
	}
	return el, nil
}

func validPoint(v float64) bool {
	return !math.IsNaN(v) && v >= 0 && v <= maxElementPoint
}

func formatPoint(v float64) string { return strconv.FormatFloat(math.Round(v*10)/10, 'f', -1, 64) }

// sanitized bounds what the page returned: each string to its length, without control
// characters and with the session's credentials scrubbed, a field without text, and a box of
// finite numbers.
func (e Element) sanitized(secrets []string) Element {
	clean := func(s string, n int) string {
		s = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) || r == utf8.RuneError {
				return ' '
			}
			return r
		}, s)
		s = strings.Join(strings.Fields(Scrub(s, secrets)), " ")
		if utf8.RuneCountInString(s) > n {
			s = string([]rune(s)[:n-1]) + "…"
		}
		return s
	}
	out := Element{
		Tag: clean(e.Tag, 40), Role: clean(e.Role, 40), Name: clean(e.Name, 200), ID: clean(e.ID, 100),
		Selector: clean(e.Selector, 200), Text: clean(e.Text, 300), Field: e.Field, Type: clean(e.Type, 20),
		FieldName: clean(e.FieldName, 100), Placeholder: clean(e.Placeholder, 100), Href: clean(e.Href, 300),
	}
	if out.Field {
		out.Text = ""
	}
	box := e.Box
	if finite(box.X, box.Y, box.Width, box.Height) && box.Width >= 0 && box.Height >= 0 {
		out.Box = box
	}
	out.Description = out.Describe()
	return out
}

func finite(vs ...float64) bool {
	for _, v := range vs {
		if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > 10*maxElementPoint {
			return false
		}
	}
	return true
}

// Describe names the element in one line that reads without the screenshot: its role (or
// tag) and accessible name, its selector, what kind of field it is, where a link goes, and
// the text it shows, such as
//
//	button "Save" (#save-btn), text "Save changes"
//	textbox "Email" (#email), a field of type email named "email" with placeholder "you@example.com"
//
// Quoted values are Go-quoted, so a quote in the page's text can't end them.
func (e Element) Describe() string {
	var b strings.Builder
	b.WriteString(cmp.Or(e.Role, e.Tag, "element"))
	if e.Name != "" {
		b.WriteString(" " + strconv.Quote(e.Name))
	}
	if e.Selector != "" && e.Selector != e.Tag {
		fmt.Fprintf(&b, " (%s)", e.Selector)
	} else if e.ID != "" {
		fmt.Fprintf(&b, " (id %s)", strconv.Quote(e.ID))
	}
	if e.Field {
		kind := "an editable area"
		switch {
		case e.Type != "":
			kind = "a field of type " + e.Type
		case e.Tag == "textarea":
			kind = "a text area"
		case e.Tag == "select":
			kind = "a drop-down"
		}
		b.WriteString(", " + kind)
		if e.FieldName != "" {
			b.WriteString(" named " + strconv.Quote(e.FieldName))
		}
		if e.Placeholder != "" && e.Placeholder != e.Name {
			b.WriteString(" with placeholder " + strconv.Quote(e.Placeholder))
		}
	}
	if e.Href != "" {
		b.WriteString(", links to " + e.Href)
	}
	if e.Text != "" && e.Text != e.Name {
		b.WriteString(", text " + strconv.Quote(e.Text))
	}
	return b.String()
}
