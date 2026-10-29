package browser

import (
	"errors"
	"slices"
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
	if err := f.svc.Resize(f.ctx, f.env, f.ada.ID, "", 1024, 640); err != nil {
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

	sent := len(cmds)
	for _, size := range [][2]int{{319, 640}, {1921, 640}, {1024, 199}, {1024, 1081}} {
		if err := f.svc.Resize(f.ctx, f.env, f.ada.ID, "", size[0], size[1]); !errors.Is(err, ErrBadViewport) {
			t.Errorf("%v: %v", size, err)
		}
	}
	if err := f.svc.Resize(f.ctx, f.env, f.ada.ID, "e-nothere", 1024, 640); !errors.Is(err, ErrNoSession) {
		t.Errorf("a missing session: %v", err)
	}
	if n := len(f.fb.allCommands()); n != sent {
		t.Errorf("%d more commands reached the guest", n-sent)
	}
}
