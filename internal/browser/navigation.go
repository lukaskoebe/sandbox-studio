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

func (s *Service) currentBrowserURL(ctx context.Context, st *state) (string, error) {
	data, _, err := s.exec(ctx, st, agentBrowserGetURL)
	if err != nil {
		return "", err
	}
	return returnedURL(data), nil
}

// Navigate runs a user-requested page open in an already-running browser while takeover
// is active. It shares the command slot with agent calls, Stop, and handback.
func (s *Service) Navigate(ctx context.Context, envID, teammateID, rawURL string) (BrowserStatus, error) {
	if _, err := s.Store.Teammate(ctx, envID, teammateID); err != nil {
		return BrowserStatus{}, err
	}
	if !validNavigationURL(rawURL) {
		return BrowserStatus{}, ErrBadURL
	}
	st := s.state(envID, teammateID)
	s.mu.Lock()
	takingOver := st.takeover
	s.mu.Unlock()
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
	if !st.takeover {
		s.mu.Unlock()
		return BrowserStatus{}, ErrNoTakeover
	}
	if st.vm != "" && st.vm != sb.ID {
		st.reset()
	}
	st.vm = sb.ID
	if st.session == "" {
		st.session = store.NewID()
	}
	st.lastUsed = s.now()
	// An open may change the page before it reports a failure, so refs are invalid as soon
	// as the command starts. Keep the old URL out of Status until it can be confirmed.
	st.url, st.refs, st.credRefs = "", nil, nil
	s.mu.Unlock()

	data, _, navErr := s.exec(ctx, st, agentproto.BrowserCommand{Args: []string{"open", rawURL}})
	if navErr != nil {
		actualURL, _ := s.currentBrowserURL(ctx, st)
		s.mu.Lock()
		if actualURL != "" {
			st.observedURL(actualURL)
		} else {
			st.url = ""
		}
		st.lastUsed = s.now()
		s.mu.Unlock()
		logURL := actualURL
		if logURL == "" {
			logURL = rawURL
		}
		s.record(ctx, st, entry{actor: "user", action: "open", url: logURL, outcome: "failed", detail: navErr.Error()})
		return BrowserStatus{}, navErr
	}

	actualURL := returnedURL(data)
	if actualURL == "" {
		actualURL, _ = s.currentBrowserURL(ctx, st)
	}
	s.mu.Lock()
	if actualURL != "" {
		st.navigated(actualURL)
	} else {
		st.url, st.refs, st.credRefs, st.visible = "", nil, nil, false
	}
	st.lastUsed = s.now()
	status := BrowserStatus{TeammateID: teammateID, VM: string(runtime.StatusRunning),
		Takeover: st.takeover, SessionID: st.session, URL: st.url, Driver: st.driver.Name}
	s.mu.Unlock()
	logURL := actualURL
	if logURL == "" {
		logURL = rawURL
	}
	s.record(ctx, st, entry{actor: "user", action: "open", url: logURL, outcome: "ok"})
	return status, nil
}
