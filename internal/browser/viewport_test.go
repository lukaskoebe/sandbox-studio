package browser

import (
	"errors"
	"math"
	"slices"
	"strconv"
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
	if _, err := f.svc.Resize(f.ctx, f.env, f.ada.ID, "", 1024, 640, 0); err != nil {
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
		got, err := f.svc.Resize(f.ctx, f.env, f.ada.ID, "", c.w, c.h, c.scale)
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
	if got, err := f.svc.Resize(f.ctx, f.env, f.ada.ID, "", 1024, 640, 2); err != nil || got != 1 {
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
		if _, err := f.svc.Resize(f.ctx, f.env, f.ada.ID, "", c.w, c.h, c.scale); !errors.Is(err, ErrBadViewport) {
			t.Errorf("%+v: %v", c, err)
		}
	}
	if _, err := f.svc.Resize(f.ctx, f.env, f.ada.ID, "e-nothere", 1024, 640, 0); !errors.Is(err, ErrNoSession) {
		t.Errorf("a missing session: %v", err)
	}
	if n := len(f.fb.allCommands()); n != sent {
		t.Errorf("%d more commands reached the guest", n-sent)
	}
}
