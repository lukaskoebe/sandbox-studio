package browser

import (
	"context"
	"net/url"

	"github.com/lukaskoebe/sandbox-studio/internal/policy"
)

// Site scope (C1): what the policy asks of a browser about the pages it has open. The policy
// decides the requests of a browser VM one at a time; the broker says which page each one
// belongs to, and which sites the user approved for the browser session.

// TopLevel returns the site of the page the teammate's browser drove last. Its requests are
// attributed to that page: exactly while one page is open, and to the one used last otherwise.
// It names the page for the approval; it never allows a request on its own.
func (s *Service) TopLevel(_ context.Context, envID, teammateID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.states[teammateID]
	if st == nil || st.env != envID {
		return ""
	}
	var last *page
	for _, pg := range st.pages() {
		if Origin(pg.url) != "" && (last == nil || pg.used.After(last.used)) {
			last = pg
		}
	}
	if last == nil {
		return ""
	}
	u, err := url.Parse(last.url)
	if err != nil {
		return ""
	}
	return policy.Site(u.Hostname())
}

// pages lists the persistent profile and the ephemeral sessions. The caller holds s.mu.
func (st *state) pages() []*page {
	out := []*page{&st.page}
	for _, pg := range st.eph {
		out = append(out, pg)
	}
	return out
}
