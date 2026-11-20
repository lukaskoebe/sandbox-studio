package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
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

// Viewport is a page size chosen for a session, which it keeps until it is let go or the browser
// restarts: a live view that fits the page leaves it, and agents' screenshots have it.
type Viewport struct {
	Width  int     `json:"width"`
	Height int     `json:"height"`
	Scale  float64 `json:"scale,omitempty" doc:"Device pixels per CSS pixel; omitted is 1"`
	By     string  `json:"by,omitempty" doc:"Name of the agent sandbox that chose it; empty when the user did"`
}

// ErrKept refuses to fit a page over the size chosen for it.
var ErrKept = errors.New("the page keeps a size chosen for it; let it go first")

// Resize sets the page size of a session of the teammate's browser, so the page fits the
// user's live view of it at one CSS pixel to one. scale is the device pixels per CSS pixel of
// the user's screen, 0 for 1: the page gets as many, within a 4K frame, so its frames are as
// sharp as the screen shows them. It returns the scale the page got, which a guest agent from
// before scales leaves at 1. The user needn't have taken over: it changes how the page is laid
// out, not what is on it. It is not logged and doesn't count as using the browser.
//
// With keep, the session keeps the size, as the user chose it: a fit is refused with ErrKept
// until Unkeep. Without it, a size chosen for the session is refused the same way.
func (s *Service) Resize(ctx context.Context, envID, teammateID, session string, width, height int, scale float64, keep bool) (float64, error) {
	if scale == 0 {
		scale = 1
	}
	if width < MinViewportWidth || width > MaxViewportWidth || height < MinViewportHeight || height > MaxViewportHeight ||
		!(scale >= 1 && scale <= MaxViewportScale) {
		return 0, ErrBadViewport
	}
	var got float64
	err := s.onPage(ctx, envID, teammateID, session, func(ctx context.Context, st *state, pg *page) error {
		s.mu.Lock()
		kept := pg.size != nil
		s.mu.Unlock()
		if kept && !keep {
			return ErrKept
		}
		var err error
		if got, err = s.setViewport(ctx, st, pg, width, height, scale); err != nil {
			return err
		}
		if keep {
			s.keepSize(envID, pg, &Viewport{Width: width, Height: height, Scale: scaleOrNone(got)})
		}
		return nil
	})
	return got, err
}

// Unkeep lets go of the size chosen for a session: the page goes back to agent-browser's own
// size, 1280×720 at 1x, which a live view that fits it then changes.
func (s *Service) Unkeep(ctx context.Context, envID, teammateID, session string) error {
	return s.onPage(ctx, envID, teammateID, session, func(ctx context.Context, st *state, pg *page) error {
		if _, err := s.setViewport(ctx, st, pg, DefaultViewportWidth, DefaultViewportHeight, 1); err != nil {
			return err
		}
		s.keepSize(envID, pg, nil)
		return nil
	})
}

// agent-browser's own page size, which a browser starts with.
const DefaultViewportWidth, DefaultViewportHeight = 1280, 720

// keepSize records the size a session keeps, nil for none, and tells the views.
func (s *Service) keepSize(envID string, pg *page, v *Viewport) {
	s.mu.Lock()
	pg.size = v
	s.mu.Unlock()
	s.notify(envID)
}

func copyOf(v *Viewport) *Viewport {
	if v == nil {
		return nil
	}
	c := *v
	return &c
}

func scaleOrNone(scale float64) float64 {
	if scale == 1 {
		return 0
	}
	return scale
}

// onPage runs f on a running session of the teammate's browser once no other command runs.
func (s *Service) onPage(ctx context.Context, envID, teammateID, session string, f func(context.Context, *state, *page) error) error {
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
	return f(ctx, st, pg)
}

// setViewport sets pg's page size with set viewport, at a scale within a 4K frame, and returns
// the scale the page got.
func (s *Service) setViewport(ctx context.Context, st *state, pg *page, width, height int, scale float64) (float64, error) {
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
	err := set(strconv.FormatFloat(scale, 'f', -1, 64))
	if err != nil && strings.Contains(err.Error(), olderGuestViewport) {
		// A guest from before scales takes the size alone, and its frames stay at 1x.
		return 1, set()
	}
	return scale, err
}

// ViewportSizes are the sizes `studio browser viewport` takes by name, as the user's live view
// offers them: a phone's, a tablet's, a laptop's and a desktop's screen, upright for the first two.
var ViewportSizes = map[string][2]int{
	"phone":   {390, 844},
	"tablet":  {820, 1180},
	"laptop":  {1440, 900},
	"desktop": {1920, 1080},
}

// maxAgentScale is the most device pixels per CSS pixel an agent asks for: a phone's 3x.
const maxAgentScale = 3

var sizePattern = regexp.MustCompile(`^([0-9]{3,4})x([0-9]{3,4})$`)

// viewport is browser_viewport: the agent sets the session's page size, for screenshots of a
// phone's or a laptop's layout. The session keeps it, as when the user chooses one, so the
// user's live view shows it too rather than fitting over it; default lets it go.
func (c *call) viewport(ctx context.Context, params json.RawMessage) (any, error) {
	var in struct {
		Size  string  `json:"size"`
		Scale float64 `json:"scale"`
	}
	if err := decode(params, &in); err != nil {
		return nil, err
	}
	name := strings.ToLower(strings.TrimSpace(in.Size))
	w, h := DefaultViewportWidth, DefaultViewportHeight
	if wh, ok := ViewportSizes[name]; ok {
		w, h = wh[0], wh[1]
	} else if m := sizePattern.FindStringSubmatch(name); m != nil {
		w, _ = strconv.Atoi(m[1])
		h, _ = strconv.Atoi(m[2])
		if w < MinViewportWidth || w > MaxViewportWidth || h < MinViewportHeight || h > MaxViewportHeight {
			return nil, refuse("a size is %d to %d pixels wide and %d to %d tall", MinViewportWidth, MaxViewportWidth, MinViewportHeight, MaxViewportHeight)
		}
	} else if name != "default" {
		return nil, refuse("give phone, tablet, laptop, desktop, default, or a size such as 390x844")
	}
	scale := in.Scale
	if scale == 0 {
		scale = 1
	}
	if !(scale >= 1 && scale <= maxAgentScale) {
		return nil, refuse("--scale is 1 to %d", maxAgentScale)
	}
	c.s.mu.Lock()
	paused := c.pg.takeover
	c.s.mu.Unlock()
	if paused {
		return nil, ErrPaused
	}
	got, err := c.s.setViewport(ctx, c.st, c.pg, w, h, scale)
	if err != nil {
		return nil, userError{err}
	}
	env := c.sb.EnvironmentID
	if name == "default" {
		c.s.keepSize(env, c.pg, nil)
		return fmt.Sprintf("the page is %d×%d again, agent-browser's own size; the user's live view may fit it to their screen again", w, h), nil
	}
	c.s.keepSize(env, c.pg, &Viewport{Width: w, Height: h, Scale: scaleOrNone(got), By: c.sb.Name})
	at := ""
	if got != 1 {
		at = fmt.Sprintf(" at %vx", got)
	} else if scale > 1 {
		at = " at 1x: this browser takes no scale"
	}
	return fmt.Sprintf("the page is %d×%d now%s, until the browser restarts or a size is set again: pages lay out for that size, "+
		"`studio browser screenshot` shows it so, and so does the user's view of your browser. The browser still says it is a desktop Chrome. "+
		"`studio browser viewport default` goes back to %d×%d", w, h, at, DefaultViewportWidth, DefaultViewportHeight), nil
}

// olderGuestViewport is how a guest agent from before scales refuses a scale.
const olderGuestViewport = "only set viewport, with a width and a height, is allowed"
