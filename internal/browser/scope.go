package browser

import (
	"context"
	"net/url"
	"slices"

	"github.com/lukaskoebe/sandbox-studio/internal/policy"
)

// Site scope: what the policy asks of a browser about the pages it has open. The policy
// decides the requests of a browser VM one at a time; the broker says which page each one
// belongs to, and which sites and hosts the user approved for the browser session.

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
		if pg.site() != "" && (last == nil || pg.used.After(last.used)) {
			last = pg
		}
	}
	if last == nil {
		return ""
	}
	return last.site()
}

// Allows reports whether what the user approved for the teammate's browser session covers r to
// host. A host pattern allowed for the session covers its hosts. A navigation is covered by its
// own site. A subresource is covered when every web page the browser has open is on an approved
// site: which page loaded it is not known, so none may be one the user has not approved. Nothing
// is covered before the user approves a site or a host.
func (s *Service) Allows(_ context.Context, envID, teammateID, host string, r policy.HTTPRequest) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.states[teammateID]
	if st == nil || st.env != envID {
		return false
	}
	for _, p := range st.hosts {
		if policy.Covers(p, host) {
			return true
		}
	}
	if len(st.sites) == 0 {
		return false
	}
	if r.Navigation {
		return r.Site != "" && slices.Contains(st.sites, r.Site)
	}
	open := 0
	for _, pg := range st.pages() {
		site := pg.site()
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

// ApproveHost allows the hosts a pattern covers in the teammate's browser until the session
// ends, whichever page asks for them.
func (s *Service) ApproveHost(_ context.Context, envID, teammateID, pattern string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.stateLocked(envID, teammateID)
	if !slices.Contains(st.hosts, pattern) {
		st.hosts = append(st.hosts, pattern)
	}
}

// Navigating records that the teammate's browser is loading a page of site: the policy allowed a
// navigation there. The page's address is only known once the command that moved it returns, or
// never when a link or a script moved it, so until then its requests belong to the page used last,
// on site. Without this they would belong to no page, and none would be covered by the site.
func (s *Service) Navigating(_ context.Context, envID, teammateID, site string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.stateLocked(envID, teammateID)
	var last *page
	for _, pg := range st.pages() {
		if last == nil || pg.used.After(last.used) {
			last = pg
		}
	}
	last.loading = site
}

// site is the site of the page: the one an allowed navigation is loading, else its address's.
func (pg *page) site() string {
	if pg.loading != "" {
		return pg.loading
	}
	return pageSite(pg.url)
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
