package browser

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

func validNavigationURL(raw string) bool {
	if raw == "" || utf8.RuneCountInString(raw) > 4000 || !utf8.ValidString(raw) {
		return false
	}
	for _, r := range raw {
		if unicode.IsControl(r) {
			return false
		}
	}
	u, err := url.Parse(raw)
	return err == nil && u.IsAbs() &&
		(strings.EqualFold(u.Scheme, "http") || strings.EqualFold(u.Scheme, "https")) &&
		u.Host != "" && u.Hostname() != "" && u.User == nil
}

func returnedURL(data json.RawMessage) string {
	var result struct {
		URL string `json:"url"`
	}
	if json.Unmarshal(data, &result) != nil {
		return ""
	}
	return result.URL
}

func (s *Service) currentBrowserURL(ctx context.Context, st *state, pg *page) (string, error) {
	data, _, err := s.exec(ctx, st, pg, agentBrowserGetURL)
	if err != nil {
		return "", err
	}
	return returnedURL(data), nil
}

// Navigate runs a user-requested page open in a session ("" for the persistent profile) of
// an already-running browser while the user has taken that session over. It shares the
// command slot with agent calls, Stop, and handback.
func (s *Service) Navigate(ctx context.Context, envID, teammateID, session, rawURL string) (BrowserStatus, error) {
	if _, err := s.Store.Teammate(ctx, envID, teammateID); err != nil {
		return BrowserStatus{}, err
	}
	if !validNavigationURL(rawURL) {
		return BrowserStatus{}, ErrBadURL
	}
	return s.drive(ctx, envID, teammateID, session, []string{"open", rawURL}, "open", rawURL)
}

// Moves the user can make in a session's history, as the browser's toolbar does.
var historyMoves = map[string]bool{"back": true, "forward": true, "reload": true}

// Go goes back, forward or reloads the page in a session the user has taken over, like
// Navigate.
func (s *Service) Go(ctx context.Context, envID, teammateID, session, to string) (BrowserStatus, error) {
	if _, err := s.Store.Teammate(ctx, envID, teammateID); err != nil {
		return BrowserStatus{}, err
	}
	if !historyMoves[to] {
		return BrowserStatus{}, ErrBadMove
	}
	return s.drive(ctx, envID, teammateID, session, []string{to}, to, "")
}

// drive runs one of the user's commands in a session they have taken over, records it as
// action, and returns the status with the page it lands on. fallbackURL is logged when the
// browser cannot say where it is.
func (s *Service) drive(ctx context.Context, envID, teammateID, session string, args []string, action, fallbackURL string) (BrowserStatus, error) {
	st := s.state(envID, teammateID)
	s.mu.Lock()
	pg := st.pageFor(session)
	takingOver := pg != nil && pg.takeover
	s.mu.Unlock()
	if pg == nil {
		return BrowserStatus{}, ErrNoSession
	}
	if !takingOver {
		return BrowserStatus{}, ErrNoTakeover
	}
	if _, vm, err := s.VMs.BrowserStatus(ctx, envID, teammateID); err != nil || vm != runtime.StatusRunning {
		return BrowserStatus{}, ErrNotLive
	}
	select {
	case st.run <- struct{}{}:
	case <-ctx.Done():
		return BrowserStatus{}, ctx.Err()
	}
	defer func() { <-st.run }()
	if err := ctx.Err(); err != nil {
		return BrowserStatus{}, err
	}

	sb, vm, err := s.VMs.BrowserStatus(ctx, envID, teammateID)
	if err != nil || vm != runtime.StatusRunning || sb.ID == "" {
		return BrowserStatus{}, ErrNotLive
	}
	s.mu.Lock()
	if st.vm != "" && st.vm != sb.ID {
		st.reset()
	}
	if st.pageFor(session) != pg {
		s.mu.Unlock()
		return BrowserStatus{}, ErrNoSession
	}
	if !pg.takeover {
		s.mu.Unlock()
		return BrowserStatus{}, ErrNoTakeover
	}
	st.vm = sb.ID
	if st.session == "" {
		st.session = store.NewID()
	}
	st.lastUsed, pg.used = s.now(), s.now()
	// A command may change the page before it reports a failure, so refs are invalid as soon
	// as it starts. Keep the old URL out of Status until it can be confirmed.
	pg.url, pg.refs, pg.credRefs = "", nil, nil
	s.mu.Unlock()

	data, _, runErr := s.exec(ctx, st, pg, agentproto.BrowserCommand{Args: args})
	if runErr != nil {
		actualURL, _ := s.currentBrowserURL(ctx, st, pg)
		s.mu.Lock()
		if actualURL != "" {
			pg.observedURL(actualURL)
		} else {
			pg.url = ""
		}
		st.lastUsed = s.now()
		s.mu.Unlock()
		logURL := actualURL
		if logURL == "" {
			logURL = fallbackURL
		}
		s.record(ctx, st, pg, entry{actor: "user", action: action, url: logURL, outcome: "failed", detail: runErr.Error()})
		return BrowserStatus{}, runErr
	}

	actualURL := returnedURL(data)
	if actualURL == "" {
		actualURL, _ = s.currentBrowserURL(ctx, st, pg)
	}
	s.mu.Lock()
	if actualURL != "" {
		pg.navigated(actualURL)
	} else {
		pg.url, pg.refs, pg.credRefs, pg.visible = "", nil, nil, false
	}
	st.lastUsed, pg.used = s.now(), s.now()
	status := st.statusLocked(string(runtime.StatusRunning))
	s.mu.Unlock()
	logURL := actualURL
	if logURL == "" {
		logURL = fallbackURL
	}
	s.record(ctx, st, pg, entry{actor: "user", action: action, url: logURL, outcome: "ok"})
	return status, nil
}

// CloseSession ends an ephemeral session for the user: its browser closes and its temporary
// profile is deleted. An agent still using it gets an error naming the closed session.
func (s *Service) CloseSession(ctx context.Context, envID, teammateID, session string) error {
	if _, err := s.Store.Teammate(ctx, envID, teammateID); err != nil {
		return err
	}
	if !IsEphemeral(session) {
		return ErrNoSession
	}
	st := s.state(envID, teammateID)
	s.mu.Lock()
	pg := st.eph[session]
	s.mu.Unlock()
	if pg == nil {
		return ErrNoSession
	}
	// The user's own takeover of it ends with it: waiting agent calls are released and fail
	// on the closed session.
	s.endTakeover(pg)
	select {
	case st.run <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-st.run }()
	s.mu.Lock()
	vm, current := st.vm, st.eph[pg.id] == pg
	s.mu.Unlock()
	if !current {
		return nil
	}
	var err error
	if vm != "" {
		_, err = s.Runner.Browser(ctx, vm, agentproto.BrowserCommand{Args: []string{"close"}, Ephemeral: pg.slot, TimeoutMS: 20000})
	}
	if err != nil {
		// The slot is wiped again before its next use.
		s.log().Warn("closing an ephemeral browser session failed", "teammate", st.teammate, "session", pg.id, "err", err)
	}
	s.mu.Lock()
	s.dropLocked(st, pg)
	s.mu.Unlock()
	s.record(ctx, st, pg, entry{actor: "user", sandbox: pg.owner.ID, action: "close", outcome: "ok", detail: "closed by the user"})
	return nil
}
