package browser

import (
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// A live view fits the page with set viewport, without having taken over and without logging
// an action; a size out of bounds or a session that isn't there is refused before the guest.
func TestResizeSetsTheViewport(t *testing.T) {
	f := newFixture(t)
	f.mustCall(f.sbAda, agentproto.MethodBrowserSnapshot, `{}`)
	st := f.svc.state(f.env, f.ada.ID)
	f.svc.mu.Lock()
	session := st.session
	f.svc.mu.Unlock()
	actions, err := f.st.BrowserActions(f.ctx, f.env, f.ada.ID, session, 50)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Resize(f.ctx, f.env, f.ada.ID, "", 1024, 640, 0, false); err != nil {
		t.Fatal(err)
	}
	cmds := f.fb.allCommands()
	last := cmds[len(cmds)-1]
	if !slices.Equal(last.Args, []string{"set", "viewport", "1024", "640"}) || last.Ephemeral != 0 || last.Screenshot {
		t.Errorf("command = %+v", last)
	}
	if again, err := f.st.BrowserActions(f.ctx, f.env, f.ada.ID, session, 50); err != nil || len(again) != len(actions) {
		t.Errorf("the resize was logged: %d actions, then %d (%v)", len(actions), len(again), err)
	}

	// A screen's scale goes along, within a 4K frame and in hundredths; 1x says none.
	for _, c := range []struct {
		w, h  int
		scale float64
		want  []string
	}{
		{1024, 640, 2, []string{"set", "viewport", "1024", "640", "2"}},
		{1600, 1000, 1.25, []string{"set", "viewport", "1600", "1000", "1.25"}},
		{1366, 700, 1.333333, []string{"set", "viewport", "1366", "700", "1.33"}},
		{2400, 1200, 2, []string{"set", "viewport", "2400", "1200", "1.6"}},
		{3840, 2160, 2, []string{"set", "viewport", "3840", "2160"}},
		{1024, 640, 1, []string{"set", "viewport", "1024", "640"}},
	} {
		got, err := f.svc.Resize(f.ctx, f.env, f.ada.ID, "", c.w, c.h, c.scale, false)
		if err != nil {
			t.Fatal(err)
		}
		if want := c.want[len(c.want)-1]; len(c.want) == 5 && strconv.FormatFloat(got, 'f', -1, 64) != want || len(c.want) == 4 && got != 1 {
			t.Errorf("%dx%d at %v got the scale %v", c.w, c.h, c.scale, got)
		}
		cmds := f.fb.allCommands()
		if got := cmds[len(cmds)-1].Args; !slices.Equal(got, c.want) {
			t.Errorf("%dx%d at %v: %q, want %q", c.w, c.h, c.scale, got, c.want)
		}
	}

	// A guest agent from before scales gets the size alone.
	f.fb.mu.Lock()
	f.fb.unscaled = true
	f.fb.mu.Unlock()
	if got, err := f.svc.Resize(f.ctx, f.env, f.ada.ID, "", 1024, 640, 2, false); err != nil || got != 1 {
		t.Fatalf("an older guest: %v, %v", got, err)
	}
	cmds = f.fb.allCommands()
	if got := cmds[len(cmds)-1].Args; !slices.Equal(got, []string{"set", "viewport", "1024", "640"}) {
		t.Errorf("an older guest got %q", got)
	}

	sent := len(cmds)
	for _, c := range []struct {
		w, h  int
		scale float64
	}{{319, 640, 1}, {3841, 640, 1}, {1024, 199, 1}, {1024, 2161, 1}, {1024, 640, 0.5}, {1024, 640, 4.5}, {1024, 640, math.NaN()}} {
		if _, err := f.svc.Resize(f.ctx, f.env, f.ada.ID, "", c.w, c.h, c.scale, false); !errors.Is(err, ErrBadViewport) {
			t.Errorf("%+v: %v", c, err)
		}
	}
	if _, err := f.svc.Resize(f.ctx, f.env, f.ada.ID, "e-nothere", 1024, 640, 0, false); !errors.Is(err, ErrNoSession) {
		t.Errorf("a missing session: %v", err)
	}
	if n := len(f.fb.allCommands()); n != sent {
		t.Errorf("%d more commands reached the guest", n-sent)
	}
}

// An agent sets the page size by name or as WIDTHxHEIGHT, and the session keeps it: a live
// view's fit is refused, and the status says who chose it, until the user lets it go or picks
// another; default lets it go too. A size it can't take reaches no guest.
func TestAgentKeepsAViewport(t *testing.T) {
	f := newFixture(t)
	last := func() []string {
		t.Helper()
		cmds := f.fb.allCommands()
		return cmds[len(cmds)-1].Args
	}
	kept := func() *Viewport {
		t.Helper()
		st, err := f.svc.Status(f.ctx, f.env, f.ada.ID)
		if err != nil {
			t.Fatal(err)
		}
		return st.Viewport
	}
	res := f.mustCall(f.sbAda, agentproto.MethodBrowserViewport, `{"size":"phone"}`)
	if text, _ := res.(string); !strings.Contains(text, "390×844") || !strings.Contains(text, "studio browser viewport default") {
		t.Errorf("result = %q", text)
	}
	if got := last(); !slices.Equal(got, []string{"set", "viewport", "390", "844"}) {
		t.Errorf("phone = %q", got)
	}
	if got := kept(); got == nil || *got != (Viewport{Width: 390, Height: 844, By: f.sbAda.Name}) {
		t.Errorf("kept = %+v", got)
	}
	if _, err := f.svc.Resize(f.ctx, f.env, f.ada.ID, "", 1024, 640, 0, false); !errors.Is(err, ErrKept) {
		t.Errorf("a fit over the agent's size: %v", err)
	}

	// The user's choice replaces it, at their screen's scale within a 4K frame.
	if _, err := f.svc.Resize(f.ctx, f.env, f.ada.ID, "", 820, 1180, 2, true); err != nil {
		t.Fatal(err)
	}
	if got := kept(); got == nil || *got != (Viewport{Width: 820, Height: 1180, Scale: 1.83}) {
		t.Errorf("the user's = %+v", got)
	}
	if err := f.svc.Unkeep(f.ctx, f.env, f.ada.ID, ""); err != nil {
		t.Fatal(err)
	}
	if got := last(); !slices.Equal(got, []string{"set", "viewport", "1280", "720"}) || kept() != nil {
		t.Errorf("let go: %q, kept %+v", got, kept())
	}
	if _, err := f.svc.Resize(f.ctx, f.env, f.ada.ID, "", 1024, 640, 0, false); err != nil {
		t.Errorf("a fit once let go: %v", err)
	}

	f.mustCall(f.sbAda, agentproto.MethodBrowserViewport, `{"size":"375x667","scale":3}`)
	if got := last(); !slices.Equal(got, []string{"set", "viewport", "375", "667", "3"}) {
		t.Errorf("375x667 at 3 = %q", got)
	}
	f.mustCall(f.sbAda, agentproto.MethodBrowserViewport, `{"size":"default"}`)
	if got := last(); !slices.Equal(got, []string{"set", "viewport", "1280", "720"}) || kept() != nil {
		t.Errorf("default: %q, kept %+v", got, kept())
	}

	sent := len(f.fb.allCommands())
	for _, p := range []string{`{"size":"watch"}`, `{"size":"100x100"}`, `{"size":"5000x900"}`, `{"size":"phone","scale":4}`, `{"size":"phone","scale":0.5}`} {
		if _, err := f.call(f.sbAda, agentproto.MethodBrowserViewport, p); err == nil {
			t.Errorf("%s was taken", p)
		}
	}
	for _, c := range f.fb.allCommands()[sent:] {
		if len(c.Args) > 0 && c.Args[0] == "set" {
			t.Errorf("a refused size reached the guest: %q", c.Args)
		}
	}
}
