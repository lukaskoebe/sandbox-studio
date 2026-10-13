package browser

import (
	"context"
	"net/url"
	"slices"

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
		if pageSite(pg.url) != "" && (last == nil || pg.used.After(last.used)) {
			last = pg
		}
	}
	if last == nil {
		return ""
	}
	return pageSite(last.url)
}

// Allows reports whether the sites the user approved for the teammate's browser session cover r.
// A navigation is covered by its own site. A subresource is covered when every web page the
// browser has open is on an approved site: which page loaded it is not known, so none may be
// one the user has not approved. Nothing is covered before the user approves a site.
func (s *Service) Allows(_ context.Context, envID, teammateID string, r policy.HTTPRequest) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.states[teammateID]
	if st == nil || st.env != envID || len(st.sites) == 0 {
		return false
	}
	if r.Navigation {
		return r.Site != "" && slices.Contains(st.sites, r.Site)
	}
	open := 0
	for _, pg := range st.pages() {
		site := pageSite(pg.url)
		if site == "" {
			continue
		}
		if !slices.Contains(st.sites, site) {
			return false
		}
		open++
	}
	return open > 0
}

// Approve approves a site for the teammate's browser session: its navigations and, while every
// page is on an approved site, its requests are allowed until the session ends.
func (s *Service) Approve(_ context.Context, envID, teammateID, site string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.stateLocked(envID, teammateID)
	if !slices.Contains(st.sites, site) {
		st.sites = append(st.sites, site)
	}
}

// pageSite is the site of a page's URL, or "" if the page is not a web page.
func pageSite(raw string) string {
	if Origin(raw) == "" {
		return ""
	}
	u, err := url.Parse(raw)
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
