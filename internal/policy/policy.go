// Package policy decides what happens to the network connections of sandboxes. It matches
// rules and, when none matches, asks the user and holds the connection for the answer.
package policy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// Approval kinds of network requests. KindNetwork is a connection to a host no rule covers.
// KindRequest is a request on a connection the gateway terminates (the browser VM's HTTPS): it
// names the request, and its subject is the host and the top-level site it belongs to.
const (
	KindNetwork = "network"
	KindRequest = "network.request"
)

// IsNetworkKind reports whether an approval kind is a network request.
func IsNetworkKind(kind string) bool { return kind == KindNetwork || kind == KindRequest }

// Network decisions beyond allow, deny and dismiss (store.ActionAllow and ActionDeny, and
// dismiss). The "once" ones answer the held connections of one request, without a rule.
const (
	ActionAllowOnce = "allow_once"
	ActionDenyOnce  = "deny_once"
	// ActionAllowSite approves the top-level site of a browser navigation for the browser session.
	ActionAllowSite = "allow_site"
	// ActionAllowSession allows a host pattern in the browser for the browser session, without a
	// rule: the sandboxes' own connections to it still ask.
	ActionAllowSession = "allow_session"
)

// Errors returned by Decide and Resolve.
var (
	ErrUndecided    = errors.New("the request is waiting for approval")
	ErrDismissed    = errors.New("the request was dismissed")
	ErrDenied       = errors.New("the request was denied for this connection")
	ErrDecided      = errors.New("the request was already decided")
	ErrDoesNotCover = errors.New("the rule must cover the requested host and port")
	ErrNoTeammate   = errors.New("the sandbox has no teammate")
	ErrExpired      = errors.New("a rule must expire in the future")
	ErrNoteTooLong  = fmt.Errorf("a rule's note has at most %d characters", MaxNote)
	// ErrPerRequest is what Decide returns for a deferred connection that no rule covers: the
	// connection is not held, and its requests are decided one at a time (HTTP set).
	ErrPerRequest = errors.New("the browser's requests are decided one at a time")
	ErrNotSite    = errors.New("only a browser navigation to a new site can be allowed for its site")
	ErrNotBrowser = errors.New("only a browser's request can be allowed for the browser session")
	// ErrTracker is returned for a request of a browser VM to a host on the tracker list, while
	// the environment blocks trackers and no rule allows the host.
	ErrTracker = errors.New("the host is on the tracker list")
	// ErrChromium is returned for a request a browser VM's Chromium makes for its own services
	// (ChromiumService), while no rule allows it.
	ErrChromium = errors.New("the request is one Chromium makes for itself")
	// ErrNotRequest is returned for a method or path given for an approval of a connection.
	ErrNotRequest = errors.New("a method or path can only be allowed for a request")
)

// Request is a connection attempt from a sandbox, or a request on a connection the gateway
// terminated.
type Request struct {
	EnvironmentID string
	SandboxID     string
	SandboxName   string
	Host          string // DNS name, or an IP address when no name is known
	Port          int
	// Browser is set for connections of a browser VM: the tracker list applies to them.
	Browser bool
	// Deferred is set for a connection the gateway terminates and decides request by request
	// (a browser VM's HTTPS): with no rule for the connection it is not held.
	Deferred bool
	// Curated is set for a connection the curated list allowed (a rule with OriginCurated). If its
	// host is read-only its connection is terminated, and its requests come with RulesOnly and this:
	// a read is allowed, and a write is decided as if no entry covered the host.
	Curated bool
	// RulesOnly decides by the rules alone: nothing is asked, and ErrUndecided means no rule
	// covers the request.
	RulesOnly bool
	// Placeholder is set for a request on a terminated connection that carries the placeholder of
	// a secret the host isn't bound to. No security level allows such a request (see level.go).
	Placeholder bool
	// Allowed is set for a system sandbox (SystemHosts): the hosts it may reach without anyone
	// being asked. Other hosts are refused, after the rules had their say.
	Allowed []string
	// Preapproved is set for a template build's VM (BuildHosts): hosts it may reach on ports 80
	// and 443 without anyone being asked. Other hosts are decided as for any sandbox.
	Preapproved []string
	// HTTP is the request, for a request on a terminated connection; nil for a connection.
	HTTP *HTTPRequest
}

// HTTPRequest is a request as the gateway reads it on a terminated connection. The headers
// describe the request and never decide it: Chromium's Sec-Fetch headers tell a top-level
// navigation from a subresource, and the Referer is only shown.
type HTTPRequest struct {
	Method string `json:"method,omitempty"`
	Path   string `json:"path,omitempty" doc:"The request path, without the query"`
	// FullPath is the path that goes upstream, which rules match; Path may be clipped for showing.
	FullPath   string `json:"-"`
	Navigation bool   `json:"navigation,omitempty" doc:"A top-level navigation: the page moves to another site"`
	Site       string `json:"site,omitempty" doc:"The top-level site the request belongs to: the navigation's own, or the browser's page"`
	Initiator  string `json:"initiator,omitempty" doc:"Origin of the Referer header, as the browser sent it; shown, never trusted"`
	// Report is set for a report Chromium uploads to a site (Reporting API, Network Error
	// Logging, CSP reports), which ChromiumService denies.
	Report bool `json:"-"`
	// Override is set for a request whose headers name another method, which the upstream may
	// go by: the method here says nothing about what it does.
	Override bool `json:"-"`
}

// NetworkRequest is the payload of a network approval.
type NetworkRequest struct {
	Host        string   `json:"host"`
	Port        int      `json:"port"`
	SandboxName string   `json:"sandboxName"`
	Patterns    []string `json:"patterns" doc:"Suggested host patterns, most specific first"`
	// The request, for network.request approvals.
	HTTPRequest
	// PathPattern is the path prefix an allow for the request would name, such as /repos/*.
	PathPattern string `json:"pathPattern,omitempty"`
	// Write is set for a sandbox's write to a host of the curated list, which allows only its reads.
	// Other requests that ask are a browser's.
	Write bool `json:"write,omitempty" doc:"A sandbox's write to a host of the curated list, which allows only its reads"`
	// Tracker is set for a request of a browser denied because its host is on the tracker list.
	Tracker bool `json:"tracker,omitempty"`
	// Decided says how a request that no rule covers was decided without asking: "open" (the
	// security level Open), "curated" (the curated list), "reviewer" (the AI reviewer) or
	// "chromium" (a request Chromium makes for itself). Empty means the user decided it.
	Decided string `json:"decided,omitempty" enum:"open,curated,reviewer,chromium" doc:"How the request was decided without asking; empty when the user decided it"`
	// Reason is what the list entry or the reviewer says about the decision.
	Reason string `json:"reason,omitempty" doc:"The curated entry, or the reviewer's reason"`
}

// Browsers is what the policy asks of teammates' browsers (internal/browser).
type Browsers interface {
	// TopLevel returns the site of the page the browser of teammateID drove last, which the
	// requests of the browser are attributed to, or "" if it has no page open.
	TopLevel(ctx context.Context, envID, teammateID string) string
	// Allows reports whether what the user approved for the browser session covers r to host:
	// a host pattern that covers host, a top-level navigation to an approved site, or a
	// subresource while every page the browser has open is on an approved site.
	Allows(ctx context.Context, envID, teammateID, host string, r HTTPRequest) bool
	// Approve approves a site for the browser session of teammateID.
	Approve(ctx context.Context, envID, teammateID, site string)
	// ApproveHost allows the hosts a pattern covers for the browser session of teammateID.
	ApproveHost(ctx context.Context, envID, teammateID, pattern string)
	// Navigating says that the browser of teammateID is loading a page of site, so that the
	// page's requests belong to site before its address is known.
	Navigating(ctx context.Context, envID, teammateID, site string)
}

// Engine decides connections. Every change to rules or approvals must go through it (or
// be followed by Changed) so held connections see it.
type Engine struct {
	Store *store.Store
	Bus   *events.Bus
	Hold  time.Duration    // how long an unanswered connection is held
	Now   func() time.Time // the clock rules expire by; nil uses time.Now
	Log   *slog.Logger     // for housekeeping errors; nil uses slog.Default

	// Browsers, if set, tells which page a browser's requests belong to.
	Browsers Browsers
	// Reviewer, if set, decides the requests no rule covers at the assisted level (reviewer.go).
	Reviewer Reviewer

	mu    sync.Mutex
	rules map[string][]store.Rule  // per environment, loaded on demand
	wake  map[string]chan struct{} // closed when an environment's rules or approvals change
	hits  map[string]ruleHits      // connections rules decided, not yet written to the store
	holds map[string]*held         // the connections held right now, by approval ID

	reviewMu sync.Mutex
	reviews  map[string]*question // the reviewer's questions this session, by reviewKey
}

// held is what is holding an approval's request open: its connections, and when the last of
// them gives up.
type held struct {
	count int
	until time.Time
}

// hold notes a connection held for the approval until the time. The returned function ends that.
func (e *Engine) hold(id string, until time.Time) (release func()) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.holds == nil {
		e.holds = make(map[string]*held)
	}
	h := e.holds[id]
	if h == nil {
		h = &held{}
		e.holds[id] = h
	}
	h.count++
	if until.After(h.until) {
		h.until = until
	}
	return sync.OnceFunc(func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		if h.count--; h.count == 0 {
			delete(e.holds, id)
		}
	})
}

// HeldUntil says when the last connection held for the approval gives up. False means nothing
// holds it open, as after a timeout or a restart: an answer then only helps the next attempt.
func (e *Engine) HeldUntil(id string) (time.Time, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if h := e.holds[id]; h != nil {
		return h.until, true
	}
	return time.Time{}, false
}

// ruleHits are the connections one rule decided since the hits were last written.
type ruleHits struct {
	count int
	last  time.Time
}

// Decide returns the rule for req, asking the user and waiting up to Hold if no rule
// matches. It returns ErrUndecided when time runs out and ErrDismissed when the user
// dismissed the request without a rule. A deferred connection that no rule covers returns
// ErrPerRequest instead: its requests are asked for one at a time.
func (e *Engine) Decide(ctx context.Context, req Request) (store.Rule, error) {
	teammate, err := e.teammate(ctx, req.EnvironmentID, req.SandboxID)
	if err != nil {
		return store.Rule{}, err
	}
	r, chosen, err := e.decide(ctx, req, teammate)
	if err == nil && r.Action != store.ActionDeny {
		e.navigated(ctx, req, teammate, chosen)
	}
	return r, err
}

// navigated tells the teammate's browser where a navigation that was allowed goes, so that the
// page's requests while it loads belong to its site. If the user chose to allow it, by a rule or
// an answer other than once, the site is also approved for the browser session: the page's
// requests are then allowed (see Browsers.Allows), as if the user had chosen Allow site. A
// navigation the security level allowed approves nothing.
func (e *Engine) navigated(ctx context.Context, req Request, teammate string, chosen bool) {
	if !req.Browser || req.HTTP == nil || !req.HTTP.Navigation || req.HTTP.Site == "" || teammate == "" || e.Browsers == nil {
		return
	}
	if chosen {
		e.Browsers.Approve(ctx, req.EnvironmentID, teammate, req.HTTP.Site)
	}
	e.Browsers.Navigating(ctx, req.EnvironmentID, teammate, req.HTTP.Site)
}

// decide is Decide for the sandbox's teammate. chosen is set when the user chose to allow req:
// by a rule of theirs or an approval's answer, not the security level, the AI reviewer's rule
// or an answer for this connection only.
func (e *Engine) decide(ctx context.Context, req Request, teammate string) (r store.Rule, chosen bool, err error) {
	rules, wake, err := e.snapshot(ctx, req.EnvironmentID)
	if err != nil {
		return store.Rule{}, false, err
	}
	if r, ok, err := e.check(ctx, rules, req, teammate); err != nil {
		return store.Rule{}, false, err
	} else if ok {
		return r, r.Origin != store.OriginReviewer, nil
	}
	if req.Allowed != nil {
		if host := Normalize(req.Host); slices.Contains(req.Allowed, host) {
			return store.Rule{EnvironmentID: req.EnvironmentID, Host: host, Action: store.ActionAllow}, false, nil
		}
		return store.Rule{}, false, ErrUndecided
	}
	if host := Normalize(req.Host); (req.Port == 80 || req.Port == 443) && slices.Contains(req.Preapproved, host) {
		return store.Rule{EnvironmentID: req.EnvironmentID, Host: host, Action: store.ActionAllow}, false, nil
	}
	if req.RulesOnly {
		if !req.Curated {
			return store.Rule{}, false, ErrUndecided
		}
		if entry, ok := curatedMatch(req.Host, req.Port, nil); !ok || entry.allowsRequest(Normalize(req.Host), req.HTTP) {
			return store.Rule{EnvironmentID: req.EnvironmentID, Host: Normalize(req.Host), Action: store.ActionAllow}, false, nil
		}
	}
	if req.Deferred && req.HTTP == nil {
		return store.Rule{}, false, ErrPerRequest
	}
	host := Normalize(req.Host)
	if r, ok, err := e.byLevel(ctx, req, teammate, host); err != nil {
		return store.Rule{}, false, err
	} else if ok {
		return r, false, nil
	}
	for {
		r, chosen, again, err := e.ask(ctx, req, host, teammate, wake)
		if !again {
			return r, chosen, err
		}
		// Another request's "Once" didn't answer this one: it gets a card of its own, unless a rule
		// came in the meantime.
		if rules, wake, err = e.snapshot(ctx, req.EnvironmentID); err != nil {
			return store.Rule{}, false, err
		}
		if r, ok, err := e.check(ctx, rules, req, teammate); err != nil {
			return store.Rule{}, false, err
		} else if ok {
			return r, r.Origin != store.OriginReviewer, nil
		}
	}
}

// ask shows req as an approval and holds it until the approval is answered, or for e.Hold. again
// says the answer was for another request, and req must ask for itself.
func (e *Engine) ask(ctx context.Context, req Request, host, teammate string, wake <-chan struct{}) (r store.Rule, chosen, again bool, err error) {
	kind, subject, n := e.approvalOf(ctx, req, host, teammate)
	payload, _ := json.Marshal(n)
	a, _, err := e.Store.RequestApproval(ctx, store.Approval{
		EnvironmentID: req.EnvironmentID,
		SandboxID:     req.SandboxID,
		Kind:          kind,
		Subject:       subject,
		Payload:       payload,
	})
	if err != nil {
		return store.Rule{}, false, false, err
	}
	// Noted before the event, so what a page loads for it says the connection is held.
	release := e.hold(a.ID, time.Now().Add(e.Hold))
	defer release()
	e.Bus.Publish(events.Event{Topic: events.TopicApprovals, EnvironmentID: req.EnvironmentID, ID: a.ID})

	timer := time.NewTimer(e.Hold)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return store.Rule{}, false, false, ctx.Err()
		case <-timer.C:
			// The approval stays pending, and pages now show that nothing holds it open.
			release()
			e.Bus.Publish(events.Event{Topic: events.TopicApprovals, EnvironmentID: req.EnvironmentID, ID: a.ID})
			return store.Rule{}, false, false, ErrUndecided
		case <-wake:
		}
		var rules []store.Rule
		if rules, wake, err = e.snapshot(ctx, req.EnvironmentID); err != nil {
			return store.Rule{}, false, false, err
		}
		if r, ok, err := e.check(ctx, rules, req, teammate); err != nil {
			return store.Rule{}, false, false, err
		} else if ok {
			return r, r.Origin != store.OriginReviewer, false, nil
		}
		cur, err := e.Store.Approval(ctx, req.EnvironmentID, a.ID)
		if err == nil && cur.Status == store.StatusApproved && cur.RuleID == "" && !cur.Once {
			// Approved for its site (decideSite): no rule was made, and none is needed.
			return store.Rule{EnvironmentID: req.EnvironmentID, Host: host, Action: store.ActionAllow}, true, false, nil
		}
		if err != nil || cur.Status != store.StatusPending {
			if err == nil && cur.Once && cur.Status == store.StatusApproved && !onceCovers(cur, req) {
				return store.Rule{}, false, true, nil
			}
			r, err := onceAnswer(cur, err, host, req.Port)
			return r, false, false, err
		}
	}
}

// Terminates reports whether a rule names a method or a path for the host of req, among the
// rules that cover req's sandbox, or the curated list allowed req and takes only the host's reads.
// Such a host's connections are terminated, and their requests are checked one by one.
func (e *Engine) Terminates(ctx context.Context, req Request) bool {
	teammate, err := e.teammate(ctx, req.EnvironmentID, req.SandboxID)
	if err != nil {
		return false
	}
	rules, _, err := e.snapshot(ctx, req.EnvironmentID)
	if err != nil {
		return false
	}
	host := Normalize(req.Host)
	for _, r := range e.active(rules, req.Browser) {
		if r.Method == "" && r.PathPrefix == "" {
			continue
		}
		if r.SandboxID != "" && r.SandboxID != req.SandboxID || r.TeammateID != "" && r.TeammateID != teammate {
			continue
		}
		if len(r.Ports) > 0 && !slices.Contains(r.Ports, req.Port) || hostSpecificity(r.Host, host) < 0 {
			continue
		}
		return true
	}
	return req.Curated && curatedReadOnly(host)
}

// check returns the rule that decides req now, if one does: a matching rule; else, for a
// browser's request, ErrChromium when Chromium makes it for itself, ErrTracker when the request
// is to a tracker and the environment blocks trackers; else the site the user approved for the
// browser session.
func (e *Engine) check(ctx context.Context, rules []store.Rule, req Request, teammate string) (store.Rule, bool, error) {
	if r, ok := e.match(rules, req, teammate); ok {
		return r, true, nil
	}
	if reason := ChromiumService(req.Host, req.HTTP); req.Browser && reason != "" {
		e.recordChromium(ctx, req, teammate, reason)
		return store.Rule{}, false, ErrChromium
	}
	if req.Browser && Tracker(req.Host) {
		env, err := e.Store.Environment(ctx, req.EnvironmentID)
		if err != nil {
			return store.Rule{}, false, err
		}
		if env.BlockTrackers {
			e.recordTracker(ctx, req)
			return store.Rule{}, false, ErrTracker
		}
	}
	if req.Browser && req.HTTP != nil && e.Browsers != nil && e.Browsers.Allows(ctx, req.EnvironmentID, teammate, Normalize(req.Host), *req.HTTP) {
		return store.Rule{EnvironmentID: req.EnvironmentID, Host: Normalize(req.Host), Action: store.ActionAllow}, true, nil
	}
	return store.Rule{}, false, nil
}

// recordTracker logs a browser request denied for its tracker host in the Activity list.
func (e *Engine) recordTracker(ctx context.Context, req Request) {
	host := Normalize(req.Host)
	payload, _ := json.Marshal(NetworkRequest{Host: host, Port: req.Port, SandboxName: req.SandboxName, Patterns: Suggestions(host), Tracker: true})
	err := e.Store.RecordDenied(ctx, store.Approval{
		EnvironmentID: req.EnvironmentID,
		SandboxID:     req.SandboxID,
		Kind:          KindNetwork,
		Subject:       "tracker " + host + ":" + strconv.Itoa(req.Port),
		Payload:       payload,
	})
	if err != nil {
		e.log().Error("network rules: record a tracker", "err", err)
	}
	e.Bus.Publish(events.Event{Topic: events.TopicApprovals, EnvironmentID: req.EnvironmentID})
}

// recordChromium logs a browser request denied as one Chromium makes for itself in the Activity
// list, with the reason.
func (e *Engine) recordChromium(ctx context.Context, req Request, teammate, reason string) {
	host := Normalize(req.Host)
	kind, _, n := e.approvalOf(ctx, req, host, teammate)
	n.Site, n.Decided, n.Reason = "", DecidedChromium, reason
	payload, _ := json.Marshal(n)
	err := e.Store.RecordDenied(ctx, store.Approval{
		EnvironmentID: req.EnvironmentID,
		SandboxID:     req.SandboxID,
		Kind:          kind,
		Subject:       "chromium " + host + ":" + strconv.Itoa(req.Port),
		Payload:       payload,
	})
	if err != nil {
		e.log().Error("network rules: record a request of Chromium", "err", err)
	}
	e.Bus.Publish(events.Event{Topic: events.TopicApprovals, EnvironmentID: req.EnvironmentID})
}

// approvalOf describes the approval for a request that no rule covers: its kind, its subject
// (which repeated attempts share) and its payload. A request on a terminated connection is
// about its top-level site: a navigation's own site, else the site of the browser's page.
func (e *Engine) approvalOf(ctx context.Context, req Request, host, teammate string) (kind, subject string, n NetworkRequest) {
	n = NetworkRequest{Host: host, Port: req.Port, SandboxName: req.SandboxName, Patterns: Suggestions(host)}
	kind, subject = KindNetwork, host+":"+strconv.Itoa(req.Port)
	if req.HTTP != nil {
		kind, n.HTTPRequest = KindRequest, *req.HTTP
		n.PathPattern = PathSuggestion(req.HTTP.Path)
		// A write on a read-only host of the curated list is a sandbox's own, and its card shows the
		// request: another write gets a card of its own.
		if req.Curated {
			n.Write = true
			return kind, subject + " " + req.HTTP.Method + " " + req.HTTP.Path, n
		}
		if !n.Navigation && e.Browsers != nil {
			n.Site = e.Browsers.TopLevel(ctx, req.EnvironmentID, teammate)
		}
		if n.Site != "" {
			subject += " " + n.Site
		}
	}
	return kind, subject, n
}

// onceCovers reports whether an approved "Once" also answers req, held for the same card: a card
// names one request, but a page's other requests to the host wait on it too. The request it shows
// goes, and so does another GET, so that a page loading doesn't raise a card each; anything else
// could change something, and asks for itself.
func onceCovers(a store.Approval, req Request) bool {
	if req.HTTP == nil {
		return true
	}
	var shown NetworkRequest
	if err := json.Unmarshal(a.Payload, &shown); err == nil && shown.Method == req.HTTP.Method && shown.Path == req.HTTP.Path {
		return true
	}
	return req.HTTP.Method == http.MethodGet && !req.HTTP.Override
}

// onceAnswer is what a held connection gets when its request is no longer pending without a
// rule covering it: a once decision answers this connection, anything else dismisses it.
func onceAnswer(a store.Approval, err error, host string, port int) (store.Rule, error) {
	switch {
	case err != nil || !a.Once:
		return store.Rule{}, ErrDismissed
	case a.Status == store.StatusApproved:
		// Not stored: a once decision creates no rule, so there is no rule ID to report.
		return store.Rule{EnvironmentID: a.EnvironmentID, Host: host, Ports: []int{port}, Action: store.ActionAllow}, nil
	case a.Status == store.StatusDenied:
		return store.Rule{}, ErrDenied
	}
	return store.Rule{}, ErrDismissed
}

// Decision is the user's answer to a network approval.
type Decision struct {
	Action    string    // allow, deny or dismiss
	Host      string    // host pattern; empty means the requested host
	Ports     []int     // nil means DefaultPorts of the requested port
	Scope     string    // sandbox, teammate (the requesting sandbox's owner) or environment
	ExpiresAt time.Time // when the rule stops matching; zero means it never expires
	// Method and PathPrefix narrow the rule to the request of a network.request approval; empty
	// means any method and any path.
	Method     string
	PathPrefix string
	// BrowserOnly limits the rule to the requests of browser VMs.
	BrowserOnly bool
	// Note is why the rule was made. Empty gets one that says where it came from (RuleNote).
	Note string
}

// MaxNote is the longest note a rule keeps, in characters.
const MaxNote = 500

// RuleNote is the note of a rule made from an approval without one: what was asked, by which
// sandbox and when, so a rule says later why it is there.
func RuleNote(action string, req NetworkRequest, at time.Time) string {
	verb := "Allowed"
	if action == store.ActionDeny {
		verb = "Denied"
	}
	what := fmt.Sprintf("%s:%d", req.Host, req.Port)
	if req.Method != "" {
		what = req.Method + " " + req.Host + req.Path
	}
	note := fmt.Sprintf("%s for %s on %s (%s)", verb, req.SandboxName, at.Local().Format("2 Jan 2006"), what)
	if req.SandboxName == "" {
		note = fmt.Sprintf("%s on %s (%s)", verb, at.Local().Format("2 Jan 2006"), what)
	}
	if r := []rune(note); len(r) > MaxNote {
		note = string(r[:MaxNote-1]) + "…"
	}
	return note
}

// Resolve answers a pending network approval, creating a rule unless it is dismissed.
// Other pending requests the new rule covers are settled with it.
func (e *Engine) Resolve(ctx context.Context, envID, id string, d Decision) (store.Approval, error) {
	a, err := e.Store.Approval(ctx, envID, id)
	if err != nil {
		return a, err
	}
	if a.Status != store.StatusPending {
		return a, ErrDecided
	}
	if d.Action == "dismiss" {
		if err := e.Store.DecideApproval(ctx, envID, id, store.StatusDismissed, ""); err != nil {
			return a, err
		}
		e.changed(envID, false)
		return e.Store.Approval(ctx, envID, id)
	}
	if d.Action == ActionAllowOnce || d.Action == ActionDenyOnce {
		return e.decideOnce(ctx, a, d.Action == ActionAllowOnce)
	}
	if d.Action == ActionAllowSite {
		return e.decideSite(ctx, a)
	}
	if d.Action == ActionAllowSession {
		return e.decideSession(ctx, a, d.Host)
	}
	if d.Action != store.ActionAllow && d.Action != store.ActionDeny {
		return a, fmt.Errorf("unsupported decision %q", d.Action)
	}
	var req NetworkRequest
	if err := json.Unmarshal(a.Payload, &req); err != nil {
		return a, fmt.Errorf("approval %s: %w", id, err)
	}
	rule := store.Rule{EnvironmentID: envID, Host: req.Host, Ports: d.Ports, Action: d.Action, Origin: store.OriginApproval, BrowserOnly: d.BrowserOnly,
		Note: strings.TrimSpace(d.Note)}
	if len([]rune(rule.Note)) > MaxNote {
		return a, ErrNoteTooLong
	}
	if rule.Note == "" {
		rule.Note = RuleNote(d.Action, req, e.now())
	}
	if d.Host != "" {
		rule.Host = d.Host
	}
	if rule.Ports == nil {
		rule.Ports = DefaultPorts(req.Port)
	}
	var http *HTTPRequest
	if a.Kind == KindRequest {
		http = &req.HTTPRequest
	}
	if d.Method != "" || d.PathPrefix != "" {
		if http == nil {
			return a, ErrNotRequest
		}
		if rule.Method, err = ValidMethod(d.Method); err != nil {
			return a, err
		}
		if rule.PathPrefix, err = ValidPathPrefix(d.PathPrefix); err != nil {
			return a, err
		}
	}
	owner, err := e.teammate(ctx, envID, a.SandboxID)
	if err != nil {
		return a, err
	}
	switch d.Scope {
	case "sandbox":
		rule.SandboxID = a.SandboxID
	case "teammate":
		if owner == "" {
			return a, ErrNoTeammate
		}
		rule.TeammateID = owner
	}
	if rule.Host, err = ValidPattern(rule.Host); err != nil {
		return a, err
	}
	if !Covers(rule.Host, req.Host) || (len(rule.Ports) > 0 && !slices.Contains(rule.Ports, req.Port)) || !requestFits(rule, http) {
		return a, ErrDoesNotCover
	}
	if !d.ExpiresAt.IsZero() {
		if !d.ExpiresAt.After(e.now()) {
			return a, ErrExpired
		}
		rule.ExpiresAt = &d.ExpiresAt
	}
	if _, err := e.Store.CreateRule(ctx, rule); err != nil {
		return a, err
	}
	// The owner's next card starts with the pattern level chosen here.
	if level := PatternLevel(rule.Host, req.Host); owner != "" && level != "" {
		if err := e.Store.SetApprovalPattern(ctx, envID, owner, level); err != nil {
			return a, err
		}
	}
	if err := e.Settle(ctx, envID); err != nil {
		return a, err
	}
	return e.Store.Approval(ctx, envID, id)
}

// decideOnce answers a pending network request for its one connection. No rule is created, so
// the connections held for the request get the answer and the next connection asks again.
func (e *Engine) decideOnce(ctx context.Context, a store.Approval, allow bool) (store.Approval, error) {
	status := store.StatusDenied
	if allow {
		status = store.StatusApproved
	}
	if err := e.Store.DecideApprovalOnce(ctx, a.EnvironmentID, a.ID, status); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return a, ErrDecided
		}
		return a, err
	}
	e.changed(a.EnvironmentID, false)
	return e.Store.Approval(ctx, a.EnvironmentID, a.ID)
}

// decideSite approves the site a browser navigation goes to, for the browser session: no rule is
// made. Pending requests of that site that the browser made from its page are approved with it,
// so the page's requests don't ask again.
func (e *Engine) decideSite(ctx context.Context, a store.Approval) (store.Approval, error) {
	var req NetworkRequest
	if a.Kind != KindRequest || json.Unmarshal(a.Payload, &req) != nil || !req.Navigation || req.Site == "" {
		return a, ErrNotSite
	}
	teammate, err := e.teammate(ctx, a.EnvironmentID, a.SandboxID)
	if err != nil {
		return a, err
	}
	if teammate == "" || e.Browsers == nil {
		return a, ErrNoTeammate
	}
	if err := e.Store.DecideApproval(ctx, a.EnvironmentID, a.ID, store.StatusApproved, ""); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return a, ErrDecided
		}
		return a, err
	}
	e.Browsers.Approve(ctx, a.EnvironmentID, teammate, req.Site)
	pending, err := e.Store.Approvals(ctx, a.EnvironmentID, store.StatusPending, 10000)
	if err != nil {
		return a, err
	}
	owners, err := e.Store.SandboxTeammates(ctx, a.EnvironmentID)
	if err != nil {
		return a, err
	}
	for _, p := range pending {
		var pr NetworkRequest
		if p.Kind != KindRequest || owners[p.SandboxID] != teammate || json.Unmarshal(p.Payload, &pr) != nil || pr.Navigation || pr.Site != req.Site {
			continue
		}
		if err := e.Store.DecideApproval(ctx, a.EnvironmentID, p.ID, store.StatusApproved, ""); err != nil && !errors.Is(err, store.ErrNotFound) {
			return a, err
		}
	}
	e.changed(a.EnvironmentID, false)
	return e.Store.Approval(ctx, a.EnvironmentID, a.ID)
}

// decideSession allows the hosts pattern covers (the requested host if empty) in the browser for
// the browser session: no rule is made, so the sandboxes' own connections to them still ask, and
// the allow ends with the browser VM. Pending requests of the teammate's browser to those hosts
// are approved with it.
func (e *Engine) decideSession(ctx context.Context, a store.Approval, pattern string) (store.Approval, error) {
	var req NetworkRequest
	if a.Kind != KindRequest || json.Unmarshal(a.Payload, &req) != nil {
		return a, ErrNotBrowser
	}
	if pattern == "" {
		pattern = req.Host
	}
	pattern, err := ValidPattern(pattern)
	if err != nil {
		return a, err
	}
	if !Covers(pattern, req.Host) {
		return a, ErrDoesNotCover
	}
	teammate, err := e.teammate(ctx, a.EnvironmentID, a.SandboxID)
	if err != nil {
		return a, err
	}
	if teammate == "" || e.Browsers == nil {
		return a, ErrNoTeammate
	}
	if err := e.Store.DecideApproval(ctx, a.EnvironmentID, a.ID, store.StatusApproved, ""); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return a, ErrDecided
		}
		return a, err
	}
	e.Browsers.ApproveHost(ctx, a.EnvironmentID, teammate, pattern)
	pending, err := e.Store.Approvals(ctx, a.EnvironmentID, store.StatusPending, 10000)
	if err != nil {
		return a, err
	}
	owners, err := e.Store.SandboxTeammates(ctx, a.EnvironmentID)
	if err != nil {
		return a, err
	}
	for _, p := range pending {
		var pr NetworkRequest
		if p.Kind != KindRequest || owners[p.SandboxID] != teammate || json.Unmarshal(p.Payload, &pr) != nil || !Covers(pattern, pr.Host) {
			continue
		}
		if err := e.Store.DecideApproval(ctx, a.EnvironmentID, p.ID, store.StatusApproved, ""); err != nil && !errors.Is(err, store.ErrNotFound) {
			return a, err
		}
	}
	e.changed(a.EnvironmentID, false)
	return e.Store.Approval(ctx, a.EnvironmentID, a.ID)
}

// Settle decides every pending network approval of an environment that a rule now covers,
// then wakes held connections. Call it after changing rules.
func (e *Engine) Settle(ctx context.Context, envID string) error {
	rules, err := e.Store.Rules(ctx, envID)
	if err != nil {
		return err
	}
	pending, err := e.Store.Approvals(ctx, envID, store.StatusPending, 10000)
	if err != nil {
		return err
	}
	teammates, err := e.Store.SandboxTeammates(ctx, envID)
	if err != nil {
		return err
	}
	for _, a := range pending {
		var req NetworkRequest
		if !IsNetworkKind(a.Kind) || json.Unmarshal(a.Payload, &req) != nil {
			continue
		}
		// A browser VM's requests ask as a KindRequest, and so does a sandbox's write to a curated host.
		var http *HTTPRequest
		if a.Kind == KindRequest {
			http = &req.HTTPRequest
		}
		r, ok := matchRule(e.active(rules, http != nil && !req.Write), a.SandboxID, teammates[a.SandboxID], req.Host, req.Port, http)
		if !ok {
			continue
		}
		status := store.StatusApproved
		if r.Action == store.ActionDeny {
			status = store.StatusDenied
		}
		if err := e.Store.DecideApproval(ctx, envID, a.ID, status, r.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
	}
	e.changed(envID, true)
	return nil
}

// Refused returns the deny rule that refuses every connection of sandboxID to host on any port,
// if there is one. The sandbox's lookups of host can then fail at once (see docs/network.md).
// A rule that names ports doesn't count, since it may allow host on other ports, and neither does
// a rule that names another sandbox or teammate, or one that some rule with ports, a method or a
// path overrides: a request rule may allow some of the host's requests.
func (e *Engine) Refused(ctx context.Context, envID, sandboxID, host string) (store.Rule, bool, error) {
	teammate, err := e.teammate(ctx, envID, sandboxID)
	if err != nil {
		return store.Rule{}, false, err
	}
	rules, _, err := e.snapshot(ctx, envID)
	if err != nil {
		return store.Rule{}, false, err
	}
	host = Normalize(host)
	live := e.active(rules, false)
	for _, r := range live {
		narrower := len(r.Ports) > 0 || r.Method != "" || r.PathPrefix != ""
		if narrower && appliesTo(r, sandboxID, teammate) && hostSpecificity(r.Host, host) >= 0 {
			return store.Rule{}, false, nil
		}
	}
	r, ok := Match(live, sandboxID, teammate, host, 0)
	if !ok || r.Action != store.ActionDeny {
		return store.Rule{}, false, nil
	}
	e.hit(r.ID)
	return r, true, nil
}

// Resolvable reports whether a lookup of req.Host may leave Studio, for req's sandbox: whether it
// may connect to the host on some port without anybody being asked, by a rule, its kind or its
// security level. It decides nothing, asks nobody and records nothing. Everything else stays out of
// other resolvers until it is allowed (docs/network.md).
func (e *Engine) Resolvable(ctx context.Context, req Request) (bool, error) {
	teammate, err := e.teammate(ctx, req.EnvironmentID, req.SandboxID)
	if err != nil {
		return false, err
	}
	rules, _, err := e.snapshot(ctx, req.EnvironmentID)
	if err != nil {
		return false, err
	}
	host := Normalize(req.Host)
	for _, r := range e.active(rules, req.Browser) {
		if r.Action != store.ActionDeny && appliesTo(r, req.SandboxID, teammate) && hostSpecificity(r.Host, host) >= 0 {
			return true, nil
		}
	}
	if req.Allowed != nil {
		return slices.Contains(req.Allowed, host), nil
	}
	if slices.Contains(req.Preapproved, host) {
		return true, nil
	}
	level, off, err := e.level(ctx, req.EnvironmentID, teammate)
	if err != nil {
		return false, err
	}
	privateHosts, err := e.Store.PrivateHosts(ctx, req.EnvironmentID)
	if err != nil {
		return false, err
	}
	private := slices.ContainsFunc(privateHosts, func(h store.PrivateHost) bool { return Covers(h.Host, host) })
	in := levelInput{level: level, curated: curatedHost(host, off), public: publicHost(host), private: private}
	switch in.decide() {
	case allowOpen, allowCurated:
		return true, nil
	}
	return false, nil
}

// teammate returns the teammate owning a sandbox, or "" for an unowned or unknown one. It is
// read from the catalog, not from the caller, so a teammate rule can only reach that
// teammate's sandboxes.
func (e *Engine) teammate(ctx context.Context, envID, sandboxID string) (string, error) {
	if sandboxID == "" {
		return "", nil
	}
	sb, err := e.Store.Sandbox(ctx, envID, sandboxID)
	if errors.Is(err, store.ErrNotFound) {
		return "", nil
	}
	return sb.TeammateID, err
}

// snapshot returns the rules of an environment and a channel closed on the next change.
func (e *Engine) snapshot(ctx context.Context, envID string) ([]store.Rule, <-chan struct{}, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.wake == nil {
		e.wake, e.rules = map[string]chan struct{}{}, map[string][]store.Rule{}
	}
	ch, ok := e.wake[envID]
	if !ok {
		ch = make(chan struct{})
		e.wake[envID] = ch
	}
	rules, ok := e.rules[envID]
	if !ok {
		var err error
		if rules, err = e.Store.Rules(ctx, envID); err != nil {
			return nil, nil, err
		}
		e.rules[envID] = rules
	}
	return rules, ch, nil
}

func (e *Engine) changed(envID string, rules bool) {
	e.mu.Lock()
	delete(e.rules, envID)
	if ch, ok := e.wake[envID]; ok {
		close(ch)
		delete(e.wake, envID)
	}
	e.mu.Unlock()
	e.Bus.Publish(events.Event{Topic: events.TopicApprovals, EnvironmentID: envID})
	if rules {
		e.Bus.Publish(events.Event{Topic: events.TopicRules, EnvironmentID: envID})
	}
}

// now is the time rules expire by.
func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

// active returns the rules that match at the current time, for a browser VM's connection or
// request if browser is set. A rule stops matching at its ExpiresAt, before the sweep deletes
// it, and a browser-only rule matches only a browser VM's.
func (e *Engine) active(rules []store.Rule, browser bool) []store.Rule {
	now := e.now()
	var live []store.Rule
	for _, r := range rules {
		if (r.ExpiresAt == nil || now.Before(*r.ExpiresAt)) && (browser || !r.BrowserOnly) {
			live = append(live, r)
		}
	}
	return live
}

// match returns the rule that decides a connection now, and counts the connection as a hit of
// that rule.
func (e *Engine) match(rules []store.Rule, req Request, teammateID string) (store.Rule, bool) {
	r, ok := matchRule(e.active(rules, req.Browser), req.SandboxID, teammateID, req.Host, req.Port, req.HTTP)
	if ok {
		e.hit(r.ID)
	}
	return r, ok
}

// hit counts a connection a rule decided. The counts reach the store in batches (flushHits),
// so a connection never writes.
func (e *Engine) hit(ruleID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.hits == nil {
		e.hits = map[string]ruleHits{}
	}
	h := e.hits[ruleID]
	h.count++
	h.last = e.now()
	e.hits[ruleID] = h
}

// flushHits writes the hits counted since the last flush. If the write fails, the hits are
// kept for the next one.
func (e *Engine) flushHits(ctx context.Context) error {
	e.mu.Lock()
	pending := e.hits
	e.hits = nil
	e.mu.Unlock()
	if len(pending) == 0 {
		return nil
	}
	batch := make([]store.RuleHit, 0, len(pending))
	for id, h := range pending {
		batch = append(batch, store.RuleHit{ID: id, Count: h.count, Last: h.last})
	}
	if err := e.Store.AddRuleHits(ctx, batch); err != nil {
		e.mu.Lock()
		if e.hits == nil {
			e.hits = map[string]ruleHits{}
		}
		for id, h := range pending {
			cur := e.hits[id]
			cur.count += h.count
			if h.last.After(cur.last) {
				cur.last = h.last
			}
			e.hits[id] = cur
		}
		e.mu.Unlock()
		return err
	}
	return nil
}

// Run does the engine's housekeeping until ctx ends: once a minute it writes the rule hits and
// deletes the expired rules. On the way out it writes the hits once more.
func (e *Engine) Run(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			// Shutdown: a bounded write, since ctx is done.
			flush, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if err := e.flushHits(flush); err != nil {
				e.log().Error("network rules: write hits", "err", err)
			}
			return
		case <-t.C:
			if err := e.flushHits(ctx); err != nil && ctx.Err() == nil {
				e.log().Error("network rules: write hits", "err", err)
			}
			if err := e.sweep(ctx); err != nil && ctx.Err() == nil {
				e.log().Error("network rules: delete expired rules", "err", err)
			}
		}
	}
}

// sweep deletes the rules that have expired and tells their environments.
func (e *Engine) sweep(ctx context.Context) error {
	envs, err := e.Store.DeleteExpiredRules(ctx, e.now())
	for _, env := range envs {
		e.changed(env, true)
	}
	return err
}

func (e *Engine) log() *slog.Logger {
	if e.Log != nil {
		return e.Log
	}
	return slog.Default()
}
