package browser

import (
	"errors"
	"math"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// A note's lookup asks the guest for the element at the point and nothing else: it neither
// renumbers the session's refs nor logs an action.
func TestElementAtNamesTheElementWithoutTouchingRefs(t *testing.T) {
	f := newFixture(t)
	f.mustCall(f.sbAda, agentproto.MethodBrowserSnapshot, `{}`)
	st := f.svc.state(f.env, f.ada.ID)
	f.svc.mu.Lock()
	refs := len(st.refs)
	session := st.session
	f.svc.mu.Unlock()
	actions, err := f.st.BrowserActions(f.ctx, f.env, f.ada.ID, session, 50)
	if err != nil {
		t.Fatal(err)
	}
	f.fb.mu.Lock()
	f.fb.element = map[string]any{"tag": "button", "role": "button", "name": "Sign in", "id": "signin", "selector": "#signin",
		"text": "Sign in now", "field": false, "box": map[string]float64{"x": 40, "y": 286, "width": 160, "height": 40}}
	f.fb.mu.Unlock()

	el, err := f.svc.ElementAt(f.ctx, f.env, f.ada.ID, "", 120.25, 306)
	if err != nil {
		t.Fatal(err)
	}
	if want := `button "Sign in" (#signin), text "Sign in now"`; el.Description != want {
		t.Errorf("description = %q, want %q", el.Description, want)
	}
	if el.Box != (ElementBox{40, 286, 160, 40}) || el.Tag != "button" || el.ID != "signin" {
		t.Errorf("element = %+v", el)
	}
	cmds := f.fb.allCommands()
	last := cmds[len(cmds)-1]
	if !slices.Equal(last.Args, []string{agentproto.BrowserElementAt, "120.3", "306"}) || last.TimeoutMS != 3000 || last.Ephemeral != 0 || last.Screenshot {
		t.Errorf("command = %+v", last)
	}
	f.svc.mu.Lock()
	after := len(st.refs)
	f.svc.mu.Unlock()
	if after != refs || refs == 0 {
		t.Errorf("refs went from %d to %d", refs, after)
	}
	if again, err := f.st.BrowserActions(f.ctx, f.env, f.ada.ID, session, 50); err != nil || len(again) != len(actions) {
		t.Errorf("the lookup was logged: %d actions, then %d (%v)", len(actions), len(again), err)
	}
}

// What the page returns is the page's: a field's text goes, control characters go, the
// session's credentials are scrubbed, strings are capped and a box of no sense is dropped.
func TestElementAtSanitizesWhatThePageSays(t *testing.T) {
	f := newFixture(t)
	if err := f.svc.Start(f.ctx, f.env, f.ada.ID); err != nil {
		t.Fatal(err)
	}
	const secret = "hunter2-from-the-vault"
	f.svc.mu.Lock()
	f.svc.stateLocked(f.env, f.ada.ID).secrets = []string{secret}
	f.svc.mu.Unlock()
	f.fb.mu.Lock()
	f.fb.element = map[string]any{"tag": "input", "role": "textbox", "name": "Password\x07 for " + secret, "id": "pw", "selector": "#pw",
		"text": secret, "field": true, "type": "password", "fieldName": "pw", "placeholder": "",
		"box": map[string]float64{"x": 1e300, "y": 8, "width": 158, "height": 21}}
	f.fb.mu.Unlock()
	el, err := f.svc.ElementAt(f.ctx, f.env, f.ada.ID, "", 400, 18)
	if err != nil {
		t.Fatal(err)
	}
	if el.Text != "" || el.Name != "Password for "+RedactedValue || el.Box != (ElementBox{}) {
		t.Errorf("element = %+v", el)
	}
	if want := `textbox "Password for [redacted]" (#pw), a field of type password named "pw"`; el.Description != want {
		t.Errorf("description = %q, want %q", el.Description, want)
	}

	f.fb.mu.Lock()
	f.fb.element = map[string]any{"tag": "p", "role": "paragraph", "selector": "main > p:nth-of-type(2)", "text": strings.Repeat("Ünïcode \"quoted\" ", 40),
		"box": map[string]float64{"x": 8, "y": 115, "width": 784, "height": 112}}
	f.fb.mu.Unlock()
	el, err = f.svc.ElementAt(f.ctx, f.env, f.ada.ID, "", 100, 150)
	if err != nil {
		t.Fatal(err)
	}
	if n := utf8.RuneCountInString(el.Text); n != 300 || !strings.HasSuffix(el.Text, "…") {
		t.Errorf("text has %d runes: %q", n, el.Text)
	}
	if !strings.HasPrefix(el.Description, `paragraph (main > p:nth-of-type(2)), text "Ünïcode \"quoted\" Ünïcode`) {
		t.Errorf("description = %q", el.Description)
	}
}

func TestElementAtRefusals(t *testing.T) {
	f := newFixture(t)
	for _, p := range [][2]float64{{-1, 5}, {5, math.NaN()}, {5, math.Inf(1)}, {200_000, 5}} {
		if _, err := f.svc.ElementAt(f.ctx, f.env, f.ada.ID, "", p[0], p[1]); !errors.Is(err, ErrBadPoint) {
			t.Errorf("%v: %v", p, err)
		}
	}
	if _, err := f.svc.ElementAt(f.ctx, f.env, f.ada.ID, "", 5, 5); !errors.Is(err, ErrNotLive) {
		t.Errorf("a browser that never started: %v", err)
	}
	if err := f.svc.Start(f.ctx, f.env, f.ada.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ElementAt(f.ctx, f.env, f.ada.ID, "", 5, 5); !errors.Is(err, ErrNoElement) {
		t.Errorf("no element at the point: %v", err)
	}
	if _, err := f.svc.ElementAt(f.ctx, f.env, f.ada.ID, "eph-0000000f", 5, 5); !errors.Is(err, ErrNoSession) {
		t.Errorf("a session that doesn't exist: %v", err)
	}

	// A credential that shows on the page keeps its text in, like its picture.
	st := f.svc.state(f.env, f.ada.ID)
	f.svc.mu.Lock()
	st.visible = true
	f.svc.mu.Unlock()
	before := len(f.fb.allCommands())
	if _, err := f.svc.ElementAt(f.ctx, f.env, f.ada.ID, "", 5, 5); !errors.Is(err, ErrHidden) {
		t.Errorf("a page showing a credential: %v", err)
	}
	if len(f.fb.allCommands()) != before {
		t.Error("the page showing a credential was asked")
	}
	f.svc.mu.Lock()
	st.visible = false
	f.svc.mu.Unlock()

	// The lookup doesn't wait for long behind another command, such as an agent's.
	f.fb.mu.Lock()
	f.fb.blockArgs = "get url"
	f.fb.blockStarted, f.fb.blockRelease = make(chan struct{}), make(chan struct{})
	started, release := f.fb.blockStarted, f.fb.blockRelease
	f.fb.mu.Unlock()
	done := make(chan error, 1)
	go func() { done <- f.svc.Start(f.ctx, f.env, f.ada.ID) }()
	<-started
	begun := time.Now()
	if _, err := f.svc.ElementAt(f.ctx, f.env, f.ada.ID, "", 5, 5); !errors.Is(err, ErrBusy) {
		t.Errorf("a busy browser: %v", err)
	}
	if waited := time.Since(begun); waited < elementWait || waited > elementWait+time.Second {
		t.Errorf("waited %v for a busy browser", waited)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	f.vms.crash(f.ada.ID)
	if _, err := f.svc.ElementAt(f.ctx, f.env, f.ada.ID, "", 5, 5); !errors.Is(err, ErrNotLive) {
		t.Errorf("a browser that crashed: %v", err)
	}
}

func TestElementDescribe(t *testing.T) {
	for _, c := range []struct {
		el   Element
		want string
	}{
		{Element{Tag: "button", Role: "button", Name: "Save", ID: "save-btn", Selector: "#save-btn", Text: "Save changes"},
			`button "Save" (#save-btn), text "Save changes"`},
		{Element{Tag: "input", Role: "textbox", Name: "Email", Selector: "#email", Field: true, Type: "email", FieldName: "email", Placeholder: "you@example.com"},
			`textbox "Email" (#email), a field of type email named "email" with placeholder "you@example.com"`},
		{Element{Tag: "textarea", Role: "textbox", Name: "Message", Selector: "form > textarea", Field: true, Placeholder: "Message"},
			`textbox "Message" (form > textarea), a text area`},
		{Element{Tag: "select", Role: "combobox", Selector: "#country", Field: true, FieldName: "country"},
			`combobox (#country), a drop-down named "country"`},
		{Element{Tag: "div", Selector: "#editor", Field: true},
			`div (#editor), an editable area`},
		{Element{Tag: "a", Role: "link", Name: "Docs", Selector: "nav > a:nth-of-type(3)", Text: "Docs", Href: "https://example.com/docs"},
			`link "Docs" (nav > a:nth-of-type(3)), links to https://example.com/docs`},
		{Element{Tag: "span", ID: ":r1:", Text: `say "hi"`},
			`span (id ":r1:"), text "say \"hi\""`},
		{Element{Tag: "body", Selector: "body"}, `body`},
		{Element{}, `element`},
	} {
		if got := c.el.Describe(); got != c.want {
			t.Errorf("Describe(%+v) = %q, want %q", c.el, got, c.want)
		}
	}
}
