package browser

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// The page sizes a live view may fit the browser to, in CSS pixels. The largest is the screen
// stealth mode's Chromium reports (internal/guest), so a page never outgrows its screen.
const (
	MinViewportWidth, MinViewportHeight = 320, 200
	MaxViewportWidth, MaxViewportHeight = 3840, 2160
)

// MaxViewportScale is the most device pixels per CSS pixel a live view asks for.
const MaxViewportScale = 4

var ErrBadViewport = errors.New("a viewport is 320 to 3840 pixels wide and 200 to 2160 tall, at a scale of 1 to 4")

// viewportScale is the device scale factor a page of width by height gets for a screen of
// scale: no more than the largest page at 1x has pixels, so that a frame is never bigger than
// that page's, and in hundredths, rounded down, as the guest takes it.
func viewportScale(width, height int, scale float64) float64 {
	s := min(scale, float64(MaxViewportWidth)/float64(width), float64(MaxViewportHeight)/float64(height))
	return max(1, math.Floor(s*100)/100)
}

const (
	// viewportWait is how long a resize waits for another command to finish; the live view
	// asks again when its frames still have the old size.
	viewportWait    = 5 * time.Second
	viewportTimeout = 5 * time.Second
)

// Resize sets the page size of a session of the teammate's browser, so the page fits the
// user's live view of it at one CSS pixel to one. scale is the device pixels per CSS pixel of
// the user's screen, 0 for 1: the page gets as many, within a 4K frame, so its frames are as
// sharp as the screen shows them. It returns the scale the page got, which a guest agent from
// before scales leaves at 1. The user needn't have taken over: it changes how the page is laid
// out, not what is on it. It is not logged and doesn't count as using the browser.
func (s *Service) Resize(ctx context.Context, envID, teammateID, session string, width, height int, scale float64) (float64, error) {
	if scale == 0 {
		scale = 1
	}
	if width < MinViewportWidth || width > MaxViewportWidth || height < MinViewportHeight || height > MaxViewportHeight ||
		!(scale >= 1 && scale <= MaxViewportScale) {
		return 0, ErrBadViewport
	}
	if _, err := s.Store.Teammate(ctx, envID, teammateID); err != nil {
		return 0, err
	}
	t, err := s.streamTarget(ctx, envID, teammateID, session)
	if err != nil {
		return 0, err
	}
	st, pg := t.st, t.pg
	wait := time.NewTimer(viewportWait)
	defer wait.Stop()
	select {
	case st.run <- struct{}{}:
	case <-wait.C:
		return 0, ErrBusy
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	defer func() { <-st.run }()
	s.mu.Lock()
	current := s.streamCurrentLocked(st, pg, envID, t.vm, t.session)
	s.mu.Unlock()
	if !current {
		return 0, ErrNotLive
	}
	ctx, cancel := context.WithTimeout(ctx, viewportTimeout)
	defer cancel()
	set := func(args ...string) error {
		args = append([]string{"set", "viewport", strconv.Itoa(width), strconv.Itoa(height)}, args...)
		_, _, err := s.exec(ctx, st, pg, agentproto.BrowserCommand{Args: args, TimeoutMS: int(viewportTimeout / time.Millisecond)})
		return err
	}
	// Without a scale, set viewport sets 1x: a page that was at 2x goes back.
	scale = viewportScale(width, height, scale)
	if scale == 1 {
		return 1, set()
	}
	err = set(strconv.FormatFloat(scale, 'f', -1, 64))
	if err != nil && strings.Contains(err.Error(), olderGuestViewport) {
		// A guest from before scales takes the size alone, and its frames stay at 1x.
		return 1, set()
	}
	return scale, err
}

// olderGuestViewport is how a guest agent from before scales refuses a scale.
const olderGuestViewport = "only set viewport, with a width and a height, is allowed"
