package tui

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/api"
)

// groupWindow is how close network requests from one sandbox must arrive to share a card. It
// matches the web stack (docs/approvals.md), which the server's second-resolution times approximate.
const groupWindow = 3 * time.Second

// card is one entry of the approvals screen: an approval, or the network requests of one sandbox
// (or one browser page) that came together, which decide as one.
type card struct {
	approvals []api.ApprovalView
	group     bool
}

func (c card) first() api.ApprovalView { return c.approvals[0] }

// choice is what a network card would decide: the host pattern, the scope (a browser request's
// reach), the ports and whether the rule covers only the request's method and path. It starts as
// the web card does.
type choice struct {
	pattern  int    // index into patterns(a): the host the rule covers
	scope    string // environment, teammate or sandbox
	reach    string // a browser request's: browser, browsers or everywhere
	anyPort  bool
	request  bool // the rule covers the method and path of a browser request, not every request
	remember bool // a browser action's decision holds for the action on its origin from now on
}

// isBrowserRequest reports whether an approval is a browser's request, which names its method.
func isBrowserRequest(a api.ApprovalView) bool {
	return a.Network != nil && a.Network.Method != ""
}

// reaches are who a browser request's rule covers: the teammate's browser (the sandbox's,
// without a teammate), every browser of the environment, or the browsers and the sandboxes.
var reaches = []string{"browser", "browsers", "everywhere"}

// defaultReach is where a browser request's choice starts: the teammate's choice of scope, else
// its browser.
func defaultReach(a api.ApprovalView) string {
	if a.Teammate != nil && a.Teammate.ApprovalScope == "environment" {
		return "browsers"
	}
	return "browser"
}

// scopeOf is the scope a rule gets from a card's choice. A browser request's comes from its
// reach, as in the web card.
func scopeOf(a api.ApprovalView, c choice) string {
	switch {
	case !isBrowserRequest(a):
		return c.scope
	case c.reach != "browser":
		return "environment"
	case a.Teammate != nil:
		return "teammate"
	}
	return "sandbox"
}

// requestAvailable reports whether a browser request names a method and path a rule could cover.
func requestAvailable(a api.ApprovalView) bool {
	return a.Network != nil && a.Network.Method != "" && a.Network.PathPattern != ""
}

// nextPattern steps the card's pattern: through the host patterns, then the request's method and
// path when there is one, then back to the first pattern.
func (c *choice) nextPattern(a api.ApprovalView) {
	switch {
	case c.request:
		c.request, c.pattern = false, 0
	case c.pattern+1 < len(patterns(a)):
		c.pattern++
	case requestAvailable(a):
		c.request = true
	default:
		c.pattern = 0
	}
}

// isNetwork reports whether an approval is a network request: a connection or a request on one.
func isNetwork(a api.ApprovalView) bool {
	return a.Kind == "network" || a.Kind == "network.request"
}

// approvalCards groups the approvals as the web stack does: one browser page's requests to one
// site are a card, and other network requests from one sandbox form a card while each arrives
// within groupWindow of the one before. Requests in split stay single, as does a navigation,
// which needs its own decision.
func approvalCards(list []api.ApprovalView, split map[string]bool) []card {
	var arrivals []api.ApprovalView
	for _, a := range list {
		if isNetwork(a) && !split[a.ID] {
			arrivals = append(arrivals, a)
		}
	}
	slices.SortStableFunc(arrivals, func(a, b api.ApprovalView) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	type open struct {
		last time.Time
		list *[]api.ApprovalView
	}
	members := map[string]*[]api.ApprovalView{} // by approval ID: the list its card is in
	openBySandbox := map[string]*open{}
	pages := map[string]*[]api.ApprovalView{}
	for _, a := range arrivals {
		n := a.Network
		if n == nil || n.Navigation {
			continue
		}
		if n.Site != "" {
			key := a.EnvironmentID + "/" + a.SandboxID + "/" + n.Site
			page := pages[key]
			if page == nil {
				page = &[]api.ApprovalView{}
				pages[key] = page
			}
			*page = append(*page, a)
			members[a.ID] = page
			continue
		}
		key := a.EnvironmentID + "/" + a.SandboxID
		g := openBySandbox[key]
		if g == nil || a.CreatedAt.Sub(g.last) > groupWindow {
			g = &open{list: &[]api.ApprovalView{}}
			openBySandbox[key] = g
		}
		g.last = a.CreatedAt
		*g.list = append(*g.list, a)
		members[a.ID] = g.list
	}
	var cards []card
	emitted := map[*[]api.ApprovalView]bool{}
	for _, a := range list {
		group, ok := members[a.ID]
		switch {
		case !ok || len(*group) < 2:
			cards = append(cards, card{approvals: []api.ApprovalView{a}})
		case !emitted[group]:
			emitted[group] = true
			cards = append(cards, card{approvals: *group, group: true})
		}
	}
	return cards
}

// cards are the approvals screen's cards, from the pending approvals.
func (m *app) cards() []card {
	return approvalCards(m.approvals, m.split)
}

// patterns are the host patterns a network card offers: the server's suggestions, or the host.
func patterns(a api.ApprovalView) []string {
	if a.Network == nil {
		return nil
	}
	if len(a.Network.Patterns) > 0 {
		return a.Network.Patterns
	}
	return []string{a.Network.Host}
}

// patternLevel is "wildcard" for a domain's wildcard pattern and "exact" for a host.
func patternLevel(pattern string) string {
	if strings.HasPrefix(pattern, "*.") {
		return "wildcard"
	}
	return "exact"
}

// defaultChoice is a card's first choice: the pattern level its teammate chose last, the scope
// its teammate sets (a browser request's reach from it), and the default ports.
func defaultChoice(a api.ApprovalView) choice {
	c := choice{scope: "environment", reach: defaultReach(a)}
	if a.Teammate != nil && a.Teammate.ApprovalScope != "" {
		c.scope = a.Teammate.ApprovalScope
	}
	if a.Teammate != nil && a.Teammate.PatternLevel != "" {
		for i, p := range patterns(a) {
			if patternLevel(p) == a.Teammate.PatternLevel {
				c.pattern = i
				break
			}
		}
	}
	return c
}

// choiceOf is the card's choice, which the user changes with p, s and x.
func (m *app) choiceOf(c card) *choice {
	if m.choices == nil {
		m.choices = map[string]*choice{}
	}
	id := c.first().ID
	if m.choices[id] == nil {
		ch := defaultChoice(c.first())
		m.choices[id] = &ch
	}
	return m.choices[id]
}

// scopes are the scopes a card offers: the teammate's sandboxes only when a teammate owns the sandbox.
func scopes(a api.ApprovalView) []string {
	if a.Teammate != nil {
		return []string{"environment", "teammate", "sandbox"}
	}
	return []string{"environment", "sandbox"}
}

// hostOf is the host a network card names, with its port.
func hostOf(a api.ApprovalView) string {
	if a.Network == nil {
		return a.Subject
	}
	return fmt.Sprintf("%s:%d", a.Network.Host, a.Network.Port)
}

// Decision is the body of a decide call. Ports nil means the default ports of the request, and
// an empty list any port. Method and PathPrefix narrow a rule to a browser request, and
// BrowserOnly keeps the sandboxes' own connections out of it. Remember makes a browser action's
// or an agent permission's decision hold from now on.
type Decision struct {
	Action      string     `json:"action"`
	Host        string     `json:"host,omitempty"`
	Ports       *[]int     `json:"ports,omitempty"`
	Scope       string     `json:"scope,omitempty"`
	Method      string     `json:"method,omitempty"`
	PathPrefix  string     `json:"pathPrefix,omitempty"`
	BrowserOnly bool       `json:"browserOnly,omitempty"`
	ExpiresAt   *time.Time `json:"expiresAt,omitempty"`
	Remember    bool       `json:"remember,omitempty"`
	Note        string     `json:"note,omitempty"`
	// PullRequest, for a push whose review offers one, opens it with the push or not.
	PullRequest *PullRequestChoice `json:"pullRequest,omitempty"`
	// Force approves a push that rewrites its branch.
	Force bool `json:"force,omitempty"`
}

// PullRequestChoice is whether approving a push opens its pull request too.
type PullRequestChoice struct {
	Open bool `json:"open"`
}

// decisionFor is the decide call a key of a network card makes, with the card's choice. A
// browser request's rule covers only the browsers, unless its reach is everywhere.
func decisionFor(a api.ApprovalView, c choice, action string) Decision {
	d := Decision{Action: action, Scope: scopeOf(a, c)}
	switch action {
	case "dismiss":
		d.Scope = c.scope
	case "allow", "deny":
		d.BrowserOnly = isBrowserRequest(a) && c.reach != "everywhere"
		pats := patterns(a)
		if c.pattern < len(pats) {
			d.Host = pats[c.pattern]
		}
		if c.anyPort {
			anyPort := []int{}
			d.Ports = &anyPort
		}
		if c.request && requestAvailable(a) {
			d.Method, d.PathPrefix = a.Network.Method, a.Network.PathPattern
		}
	}
	return d
}

// decide answers one approval, or every host of a group with the same decision, and reports it
// as "<verb> <what>.": what defaults to the host. A host an earlier decision already covered
// answers 409 and is skipped, as in the web group card.
func (m *app) decide(c card, d Decision, verb, what string) tea.Cmd {
	ctx, client := m.ctx, m.client
	return func() tea.Msg {
		rctx, cancel := requestContext(ctx)
		defer cancel()
		var failure error
		var made string // the rule a single decision made
		decided := 0
		for _, a := range c.approvals {
			body := d
			if c.group {
				body = Decision{Action: d.Action, Scope: d.Scope, Host: a.Network.Host, BrowserOnly: d.BrowserOnly}
			}
			v, err := client.Decide(rctx, a.EnvironmentID, a.ID, body)
			if err != nil {
				if apiStatus(err) == http.StatusConflict {
					continue
				}
				failure = err
				continue
			}
			decided++
			made = v.RuleID
		}
		switch {
		case failure != nil:
			return actionDone{err: fmt.Errorf("could not decide: %w", failure)}
		case decided == 0:
			return actionDone{done: "Already decided."}
		case c.group:
			return actionDone{done: fmt.Sprintf("%s %d %s.", verb, decided, plural(decided, "host", "hosts"))}
		}
		if what == "" {
			what = hostOf(c.first())
		}
		done := actionDone{done: verb + " " + what + "."}
		if made != "" && isNetwork(c.first()) {
			done.undo = &undoState{env: c.first().EnvironmentID, rule: made, what: strings.ToLower(verb[:1]) + verb[1:] + " " + what}
			done.done += " u undoes it."
		}
		return done
	}
}

// apiStatus is the HTTP status of an API error, or 0.
func apiStatus(err error) int {
	if e, ok := err.(*APIError); ok {
		return e.Status
	}
	return 0
}

// approvalsKey answers the approvals screen: the cursor moves between cards, and the keys decide
// the card under it.
func (m *app) approvalsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	cards := m.cards()
	m.approvalCursor = min(m.approvalCursor, max(len(cards)-1, 0))
	if len(cards) == 0 {
		switch {
		case key.Matches(msg, keyBack):
			m.screen = m.back
		case key.Matches(msg, keyGit):
			return m, m.openGit()
		case key.Matches(msg, keyHelp):
			m.openHelp()
		}
		return m, nil
	}
	c := cards[m.approvalCursor]
	a := c.first()
	ch := m.choiceOf(c)
	if k, ok := m.buttonKey(msg, cardOwner(c), cardControls(c, *ch), len(cardToggles(c))); ok {
		if k == "" {
			return m, nil
		}
		msg = pressOf(k) // the focused control's key
	}
	switch {
	case key.Matches(msg, keyBack) && m.resuming != nil:
		r := m.resuming
		m.resuming = nil
		m.screen = m.back
		if r.pane && m.screen == screenHome {
			m.pane.focus = true
		}
	case key.Matches(msg, keyBack):
		m.screen = m.back
	case key.Matches(msg, keyHelp):
		m.openHelp()
	case key.Matches(msg, keyGit):
		return m, m.openGit()
	case key.Matches(msg, keyUp):
		m.approvalCursor = max(m.approvalCursor-1, 0)
	case key.Matches(msg, keyDown):
		m.approvalCursor = min(m.approvalCursor+1, len(cards)-1)
	case isGit(a) && key.Matches(msg, keyOpen):
		m.openPager(a)
	case isGit(a):
		if key.Matches(msg, keyAllow, keyDeny, keyIgnore) {
			m.setNotice("Review it first: enter opens the diff.", false)
		}
	case !isNetwork(a) || c.group:
		return m.otherCardKey(msg, c)
	case key.Matches(msg, keyPattern):
		ch.nextPattern(a)
	case key.Matches(msg, keyScope):
		ch.cycleScope(a)
	case key.Matches(msg, keyPort):
		ch.anyPort = !ch.anyPort
	case key.Matches(msg, keyAllow):
		return m, m.decide(c, decisionFor(a, *ch, "allow"), "Allowed", "")
	case key.Matches(msg, keyDeny):
		return m, m.decide(c, decisionFor(a, *ch, "deny"), "Denied", "")
	case key.Matches(msg, keyHour):
		d := decisionFor(a, *ch, "allow")
		expires := time.Now().Add(time.Hour)
		d.ExpiresAt = &expires
		return m, m.decide(c, d, "Allowed", hostOf(a)+" for 1 hour")
	case key.Matches(msg, keyOnce):
		return m, m.decide(c, decisionFor(a, *ch, "allow_once"), "Allowed", hostOf(a)+" once")
	case key.Matches(msg, keyOnceDeny):
		return m, m.decide(c, decisionFor(a, *ch, "deny_once"), "Denied", hostOf(a)+" once")
	case key.Matches(msg, keyIgnore):
		return m, m.decide(c, decisionFor(a, *ch, "dismiss"), "Ignored", "")
	case key.Matches(msg, keySite) && a.Network != nil && a.Network.Navigation && a.Network.Site != "":
		return m, m.decide(c, decisionFor(a, *ch, "allow_site"), "Allowed", "site "+a.Network.Site+" for this browser session")
	}
	return m, nil
}

// otherCardKey answers the keys of a group card, or of a card of another kind: the group decides
// all its hosts or splits into single cards, and the other kinds decide as kindKey says.
func (m *app) otherCardKey(msg tea.KeyPressMsg, c card) (tea.Model, tea.Cmd) {
	a := c.first()
	if !c.group {
		return m, m.kindKey(msg, c)
	}
	ch := m.choiceOf(c)
	switch {
	case key.Matches(msg, keyScope):
		ch.cycleScope(a)
	case key.Matches(msg, keyAllowAll):
		return m, m.decide(c, decisionFor(a, *ch, "allow"), "Allowed", "")
	case key.Matches(msg, keyDenyAll):
		return m, m.decide(c, decisionFor(a, *ch, "deny"), "Denied", "")
	case key.Matches(msg, keySplit):
		for _, x := range c.approvals {
			m.split[x.ID] = true
		}
		m.approvalCursor = 0
	}
	return m, nil
}

// cycleScope steps the card's scope, or a browser request's reach.
func (c *choice) cycleScope(a api.ApprovalView) {
	if isBrowserRequest(a) {
		c.reach = nextOf(c.reach, reaches)
		return
	}
	c.scope = nextOf(c.scope, scopes(a))
}

// nextOf is the option after cur in opts, wrapping around.
func nextOf(cur string, opts []string) string {
	i := slices.Index(opts, cur)
	return opts[(i+1)%len(opts)]
}

// approvalURL is the page of the web UI that decides an approval of another kind.
func (m *app) approvalURL(a api.ApprovalView) string {
	env := "/e/" + a.EnvironmentID
	switch {
	case strings.HasPrefix(a.Kind, "git."):
		return m.browserURL(env + "/git/reviews/" + a.ID)
	case a.BrowserAction != nil:
		return m.browserURL(env + "/teammates/" + a.BrowserAction.TeammateID + "/browser")
	case a.BrowserCredential != nil:
		return m.browserURL(env + "/teammates/" + a.BrowserCredential.TeammateID + "/browser")
	case a.BrowserControl != nil:
		return m.browserURL(env + "/teammates/" + a.BrowserControl.TeammateID + "/browser")
	case a.MemoryShare != nil || a.MemoryConflict != nil:
		return m.browserURL(env + "/settings/memory")
	case a.ChatPermission != nil:
		return m.browserURL(env + "/chats/" + a.ChatPermission.ChatID)
	}
	return m.browserURL(env)
}

// openApprovals shows the approvals screen, from wherever the user is.
func (m *app) openApprovals() {
	if m.screen != screenApprovals {
		m.back = m.screen
	}
	m.screen = screenApprovals
	m.approvalCursor = 0
}

// approvalsBody lists the cards, the cursor's card in view.
func (m *app) approvalsBody(w, h int) []string {
	cards := m.cards()
	if len(cards) == 0 {
		return []string{
			styleDim.Render("No approvals are waiting."),
			"",
			styleDim.Render("Requests from sandboxes show up here as they come in."),
		}
	}
	lines := []string{styleBold.Render("Approvals") + styleDim.Render(" from every environment, newest first"), ""}
	cursorAt, cursorLen := len(lines), 0
	starts := make([]int, 0, len(cards)+1)
	var keys []keyHit // the cursor's card's keycaps, on the lines of the list
	for i, c := range cards {
		var block []string
		if m.look.plain {
			block = m.cardLines(c, i == m.approvalCursor, w)
		} else {
			var caps []keyHit
			block, caps = m.visualCard(c, i == m.approvalCursor, w)
			hidden := len(block) - m.sliding(c, len(block))
			block = block[hidden:]
			for _, k := range caps {
				if k.y >= hidden {
					k.y += len(lines) - hidden
					keys = append(keys, k)
				}
			}
		}
		if i == m.approvalCursor {
			cursorAt, cursorLen = len(lines), len(block)
		}
		starts = append(starts, len(lines))
		lines = append(lines, block...)
	}
	starts = append(starts, len(lines))
	// Keep the cursor's card in view: the list scrolls just far enough to show it.
	offset := 0
	if cursorAt+cursorLen > h {
		offset = min(cursorAt+cursorLen-h, cursorAt)
	}
	for i := range cards {
		for line := max(starts[i], offset); line < starts[i+1] && line-offset < h; line++ {
			m.hitRow(line-offset, 0, w, rowCard, i)
		}
	}
	for _, k := range keys {
		if line := k.y - offset; line >= 0 && line < h {
			m.hitKeys(bodyTop+line, k.x0, []keySpan{{x1: k.x1 - k.x0, key: k.key}})
		}
	}
	return lines[offset:]
}

// cardLines is one card: its kind, source and age, what it asks, and its choices. A card is
// separated from the next by a blank line.
func (m *app) cardLines(c card, selected bool, w int) []string {
	a := c.first()
	cursor := "  "
	if selected {
		cursor = styleAccent.Render("▸") + " "
	}
	source := approvalSource(a)
	when := age(m.now.Sub(a.CreatedAt))
	var title string
	var details []string
	if c.group {
		// As the web's group card: a browser page's requests name the page and its sandbox.
		title = firstNonEmpty(a.Network.Site, source, "A sandbox") + " wants " + fmt.Sprint(len(c.approvals)) + " hosts"
		if a.Network.Site != "" {
			details = append(details, source)
		}
		details = append(details, m.envName(a.EnvironmentID), when)
	} else {
		title = kindTitle(a.Kind)
		details = append(nonEmpty(source, m.envName(a.EnvironmentID)), when)
	}
	head := cursor + styleAccent.Render(title) + "  " + styleDim.Render(strings.Join(nonEmpty(details...), " · "))
	at := -1
	if selected {
		at = m.buttonAt(cardOwner(c))
	}
	toggle := func(i int, text string) string { // a setting the arrows may be on
		if i == at {
			return m.focused(ansiStrip(text))
		}
		return text
	}
	lines := []string{spread(head, "", w)}
	switch {
	case c.group:
		hosts := make([]string, 0, len(c.approvals))
		for _, x := range c.approvals {
			hosts = append(hosts, hostOf(x))
		}
		lines = append(lines, "    "+strings.Join(hosts, " · "))
		lines = append(lines, "    "+toggle(0, whoText(a, *m.choiceOf(c))))
	case isNetwork(a):
		ch := m.choiceOf(c)
		lines = append(lines, "    "+styleBold.Render(hostOf(a)))
		lines = append(lines, "    "+styleDim.Render("pattern ")+toggle(0, patternText(a, *ch))+
			"   "+toggle(1, whoText(a, *ch))+
			"   "+styleDim.Render("ports ")+toggle(2, portsText(*ch)))
	case isGit(a):
		lines = append(lines, "    "+summary(a))
		lines = append(lines, "    "+styleDim.Render("enter opens its diff"))
	default:
		for _, line := range m.kindLines(c, w-4) {
			lines = append(lines, "    "+line)
		}
	}
	// The plain look leaves the choices to the key bar until the arrows are on them.
	if at >= len(cardToggles(c)) {
		row, _ := m.buttonsLine(cardCaps(c, *m.choiceOf(c)), at-len(cardToggles(c)))
		lines = append(lines, "    "+row)
	}
	return append(lines, "")
}

// cardOwner names a card's row of controls for the arrows.
func cardOwner(c card) string {
	return "card:" + c.first().ID
}

// cardToggles are the settings of a card its keys step, in the order they are drawn: a network
// card's pattern, scope and ports, and a group's scope.
func cardToggles(c card) []choiceCap {
	switch {
	case c.group:
		return []choiceCap{{"s", "scope"}}
	case isNetwork(c.first()):
		return []choiceCap{{"p", "pattern"}, {"s", "scope"}, {"x", "ports"}}
	}
	return nil
}

// cardControls are what the arrows move over on a card: its settings, then its choices.
func cardControls(c card, ch choice) []choiceCap {
	return append(cardToggles(c), cardCaps(c, ch)...)
}

// patternText is the host pattern a card would decide, or the browser request it names.
func patternText(a api.ApprovalView, ch choice) string {
	if ch.request && requestAvailable(a) {
		return a.Network.Method + " " + a.Network.PathPattern
	}
	if pats := patterns(a); ch.pattern < len(pats) {
		return pats[ch.pattern]
	}
	return ""
}

// whoText says who a card's rule covers: its scope, or a browser request's reach.
func whoText(a api.ApprovalView, ch choice) string {
	if isBrowserRequest(a) {
		return styleDim.Render("who ") + reachLabel(a, ch.reach)
	}
	return styleDim.Render("scope ") + scopeLabel(a, ch.scope)
}

// reachLabel names who a browser request's rule covers, as the web's toggle does.
func reachLabel(a api.ApprovalView, reach string) string {
	switch {
	case reach == "browsers":
		return "all browsers"
	case reach == "everywhere":
		return "browsers and sandboxes"
	case a.Teammate != nil:
		return a.Teammate.Name + "'s browser"
	}
	return "this browser"
}

// scopeLabel names the scope a rule covers, as the card's toggle does.
func scopeLabel(a api.ApprovalView, scope string) string {
	switch {
	case scope == "teammate" && a.Teammate != nil:
		return a.Teammate.Name + "'s sandboxes"
	case scope == "sandbox":
		return "only " + firstNonEmpty(approvalSource(a), "this sandbox")
	}
	return "environment"
}

func portsText(ch choice) string {
	if ch.anyPort {
		return "any"
	}
	return "default"
}

// envName is the name of an environment, or "" when it is not known.
func (m *app) envName(id string) string {
	for _, env := range m.envs {
		if env.ID == id {
			return env.Name
		}
	}
	return ""
}

// firstNonEmpty is the first of the values that is not empty.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// kindTitle is what an approval kind is called.
func kindTitle(kind string) string {
	switch kind {
	case "network", "network.request":
		return "Network access"
	case "git.push":
		return "Git push"
	case "git.pr":
		return "Pull request"
	case "memory.share":
		return "Memory share"
	case "memory.conflict":
		return "Memory conflict"
	case "browser.action":
		return "Browser action"
	case "browser.credential":
		return "Browser credential"
	case "browser.control":
		return "Browser handback"
	case "chat.permission":
		return "Agent permission"
	}
	return "Approval"
}

// approvalSource is the sandbox or teammate that asked, when the request names one.
func approvalSource(a api.ApprovalView) string {
	switch {
	case a.Network != nil:
		return a.Network.SandboxName
	case a.Git != nil && a.Git.Review != nil:
		return a.Git.Review.Sandbox
	case a.MemoryShare != nil:
		return a.MemoryShare.TeammateName
	case a.BrowserAction != nil:
		return a.BrowserAction.SandboxName
	case a.BrowserCredential != nil:
		return a.BrowserCredential.SandboxName
	case a.BrowserControl != nil:
		return a.BrowserControl.SandboxName
	case a.ChatPermission != nil:
		return a.ChatPermission.SandboxName
	}
	return ""
}

// summary says what a non-network approval asks, from its payload. It never shows a secret's
// value: a credential approval names the vault secret.
func summary(a api.ApprovalView) string {
	switch {
	case a.Git != nil:
		return gitSummary(a)
	case a.MemoryShare != nil:
		return firstNonEmpty(a.MemoryShare.TeammateName, "A teammate") + " wants to share a fact with every teammate"
	case a.MemoryConflict != nil:
		return conflictSummary(a.MemoryConflict.Verdict)
	case a.BrowserAction != nil:
		act := a.BrowserAction
		target := ""
		switch {
		case act.Label != "":
			target = " “" + act.Label + "”"
		case act.Role != "":
			target = " " + act.Role
		}
		return act.Action + target + " on " + act.Origin
	case a.BrowserCredential != nil:
		return "Fill " + a.BrowserCredential.Credential + " on " + a.BrowserCredential.Origin
	case a.BrowserControl != nil:
		return firstNonEmpty(a.BrowserControl.TeammateName, "An agent") + " asks for the browser back: " + oneLine(a.BrowserControl.Reason)
	case a.ChatPermission != nil:
		return a.ChatPermission.Title
	}
	return a.Subject
}

// gitSummary says what a push or pull request asks.
func gitSummary(a api.ApprovalView) string {
	git := a.Git
	where := strings.Join(nonEmpty(git.Push.ForgeName, git.Push.Owner, git.Push.Repo), "/")
	if git.PullRequest != nil {
		return fmt.Sprintf("Open a pull request %s → %s on %s", git.PullRequest.Head, git.PullRequest.Base, where)
	}
	commits := 0
	branch := strings.TrimPrefix(git.Push.Ref, "refs/heads/")
	if git.Review != nil {
		commits = len(git.Review.Commits) + git.Review.MoreCommits
		if git.Review.Branch != "" {
			branch = git.Review.Branch
		}
	}
	return fmt.Sprintf("Push %d %s to %s on %s", commits, plural(commits, "commit", "commits"), branch, where)
}

// conflictSummary says why two memory facts disagree.
func conflictSummary(verdict string) string {
	switch verdict {
	case "contradiction":
		return "Two memory facts contradict each other"
	case "temporal_supersession":
		return "A memory fact may replace an older one"
	case "context_dependent":
		return "Two memory facts disagree depending on context"
	case "duplicate":
		return "Two memory facts look like duplicates"
	}
	return "Two memory facts disagree"
}

func nonEmpty(values ...string) []string {
	var out []string
	for _, v := range values {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// age is how long ago an approval arrived, in the web's words: "just now", "3m ago".
func age(d time.Duration) string {
	s := int(d / time.Second)
	switch {
	case s < 5:
		return "just now"
	case s < 60:
		return fmt.Sprintf("%ds ago", s)
	case s < 3600:
		return fmt.Sprintf("%dm ago", s/60)
	case s < 86400:
		return fmt.Sprintf("%dh ago", s/3600)
	}
	return fmt.Sprintf("%dd ago", s/86400)
}
