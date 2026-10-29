package browser

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// The page sizes a live view may fit the browser to, in CSS pixels. The largest is the screen
// stealth mode's Chromium reports (internal/guest), so a page never outgrows its screen.
const (
	MinViewportWidth, MinViewportHeight = 320, 200
	MaxViewportWidth, MaxViewportHeight = 1920, 1080
)

var ErrBadViewport = errors.New("a viewport is 320 to 1920 pixels wide and 200 to 1080 tall")

const (
	// viewportWait is how long a resize waits for another command to finish; the live view
	// asks again when its frames still have the old size.
	viewportWait    = 5 * time.Second
	viewportTimeout = 5 * time.Second
)

// Resize sets the page size of a session of the teammate's browser, so the page fits the
// user's live view of it at one CSS pixel to one. The user needn't have taken over: it changes
// how the page is laid out, not what is on it. It is not logged and doesn't count as using the
// browser.
func (s *Service) Resize(ctx context.Context, envID, teammateID, session string, width, height int) error {
	if width < MinViewportWidth || width > MaxViewportWidth || height < MinViewportHeight || height > MaxViewportHeight {
		return ErrBadViewport
	}
	if _, err := s.Store.Teammate(ctx, envID, teammateID); err != nil {
		return err
	}
	t, err := s.streamTarget(ctx, envID, teammateID, session)
	if err != nil {
		return err
	}
	st, pg := t.st, t.pg
	wait := time.NewTimer(viewportWait)
	defer wait.Stop()
	select {
	case st.run <- struct{}{}:
	case <-wait.C:
		return ErrBusy
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-st.run }()
	s.mu.Lock()
	current := s.streamCurrentLocked(st, pg, envID, t.vm, t.session)
	s.mu.Unlock()
	if !current {
		return ErrNotLive
	}
	ctx, cancel := context.WithTimeout(ctx, viewportTimeout)
	defer cancel()
	_, _, err = s.exec(ctx, st, pg, agentproto.BrowserCommand{
		Args:      []string{"set", "viewport", strconv.Itoa(width), strconv.Itoa(height)},
		TimeoutMS: int(viewportTimeout / time.Millisecond),
	})
	return err
}
