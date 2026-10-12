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
	// An open may change the page before it reports a failure, so refs are invalid as soon
	// as the command starts. Keep the old URL out of Status until it can be confirmed.
	pg.url, pg.refs, pg.credRefs = "", nil, nil
	s.mu.Unlock()

	data, _, navErr := s.exec(ctx, st, pg, agentproto.BrowserCommand{Args: []string{"open", rawURL}})
	if navErr != nil {
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
			logURL = rawURL
		}
		s.record(ctx, st, pg, entry{actor: "user", action: "open", url: logURL, outcome: "failed", detail: navErr.Error()})
		return BrowserStatus{}, navErr
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
		logURL = rawURL
	}
	s.record(ctx, st, pg, entry{actor: "user", action: "open", url: logURL, outcome: "ok"})
	return status, nil
}
