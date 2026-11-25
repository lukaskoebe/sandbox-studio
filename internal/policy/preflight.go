package policy

import (
	"cmp"
	"strconv"
	"sync"
	"time"
)

// preflightWindow is how long the request a preflight asked about may follow it, once the user
// answered the preflight's card with "Once". A browser sends it right after the preflight.
const preflightWindow = 30 * time.Second

// preflightGrants are the requests that a "Once" on their preflight's card lets through. The card
// shows the request the page wants to send (Preflight), but only the preflight was held on it: the
// browser sends the request itself after the preflight is answered, and without a grant it would
// raise a card of its own. A grant is for that one request, from the same sandbox, and goes once
// used or after preflightWindow.
type preflightGrants struct {
	mu   sync.Mutex
	left map[string]time.Time // until when, by preflightKey
}

func preflightKey(req Request, method string) string {
	return req.EnvironmentID + "\x00" + req.SandboxID + "\x00" + Normalize(req.Host) + "\x00" + strconv.Itoa(req.Port) +
		"\x00" + method + "\x00" + cmp.Or(req.HTTP.FullPath, req.HTTP.Path)
}

// grant notes that the preflight req was allowed once, so its request may follow.
func (g *preflightGrants) grant(req Request, now time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.left == nil {
		g.left = map[string]time.Time{}
	}
	for k, until := range g.left {
		if !now.Before(until) {
			delete(g.left, k)
		}
	}
	g.left[preflightKey(req, req.HTTP.Preflight)] = now.Add(preflightWindow)
}

// take reports whether req is the request an allowed preflight asked about, and uses the grant.
// A request that names another method in its headers never follows a preflight's grant.
func (g *preflightGrants) take(req Request, now time.Time) bool {
	if req.HTTP == nil || req.HTTP.Preflight != "" || req.HTTP.Override {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	k := preflightKey(req, req.HTTP.Method)
	until, ok := g.left[k]
	if !ok {
		return false
	}
	delete(g.left, k)
	return now.Before(until)
}
