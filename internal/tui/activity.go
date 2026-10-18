package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/api"
	"github.com/lukaskoebe/sandbox-studio/internal/gateway"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// activityEvery is how often the Activity screen reads the connection logs again while it is
// on show. Studio keeps them in memory and announces no change, so the screen polls them, as the
// web's connection panel does.
const activityEvery = 2 * time.Second

// maxActivity bounds the entries the Activity screen lists.
const maxActivity = 300

// activityState is the Activity screen: the connections of the environment's sandboxes and the
// decisions on their requests, newest first.
type activityState struct {
	back      screen
	loaded    bool
	conns     map[string][]gateway.Conn // by sandbox ID
	decisions []api.ApprovalView
	rules     map[string]store.Rule // by ID
	cursor    int
	filter    string // narrows the entries to the sandboxes whose name has it
	gen       int    // counts the times the screen opened, so a tick of an earlier visit stops
	entry     *activityEntry
	at        time.Time // when the log was read, which the busy hosts count back from
}

// activityEntry is one line of the log: a connection, or the decision on a network request.
type activityEntry struct {
	at       time.Time
	sandbox  string // its name
	host     string // host:port
	verdict  string
	bad      bool   // refused or failed
	ruleID   string // the rule that decided it, if one did
	why      string // the reviewer's reason, the curated entry, or why a connection failed
	decision bool   // a decision on an approval rather than a connection
	decided  string // how a decision was made without asking: open, curated or reviewer
	key      string // identifies the entry across reloads, to keep the cursor on it
}

type (
	activityLoaded struct {
		at        time.Time
		env       string
		conns     map[string][]gateway.Conn
		decisions []api.ApprovalView
		rules     []store.Rule
		err       error
	}
	activityTick struct{ gen int }
)

var (
	keyRule  = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "rule"))
	keyRules = key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "rules (browser)"))
)

// openActivity shows the Activity screen, narrowed to one sandbox when sandbox is not "".
func (m *app) openActivity(sandbox string) tea.Cmd {
	a := &m.activity
	a.back, a.filter, a.cursor, a.entry = m.screen, sandbox, 0, nil
	a.gen++
	m.screen = screenActivity
	return m.loadActivity()
}

// loadActivity reads the decisions and rules of the environment, and the connections of each
// of its sandboxes.
func (m *app) loadActivity() tea.Cmd {
	ctx, client, env := m.ctx, m.client, m.env.ID
	ids := make([]string, 0, len(m.sandboxes))
	for _, sb := range m.sandboxes {
		ids = append(ids, sb.ID)
	}
	return func() tea.Msg {
		rctx, cancel := requestContext(ctx)
		defer cancel()
		out := activityLoaded{at: time.Now(), env: env, conns: map[string][]gateway.Conn{}}
		if out.decisions, out.err = client.Approvals(rctx, env); out.err != nil {
			return out
		}
		if out.rules, out.err = client.Rules(rctx, env); out.err != nil {
			return out
		}
		for _, id := range ids {
			list, err := client.Connections(rctx, env, id)
			if err != nil {
				continue // a sandbox deleted since: its log went with it
			}
			out.conns[id] = list
		}
		return out
	}
}

func (m *app) activityAfter() tea.Cmd {
	gen := m.activity.gen
	return tea.Tick(activityEvery, func(time.Time) tea.Msg { return activityTick{gen: gen} })
}

// onActivity reports whether the Activity screen, or an entry of it, is on show.
func (m *app) onActivity() bool {
	return m.screen == screenActivity || m.screen == screenEntry ||
		(m.screen == screenHelp && (m.helpBack == screenActivity || m.helpBack == screenEntry))
}

// activityUpdate applies the messages of the Activity screen. ok is false for other messages.
func (m *app) activityUpdate(msg tea.Msg) (cmd tea.Cmd, ok bool) {
	a := &m.activity
	switch msg := msg.(type) {
	case activityLoaded:
		if msg.env != m.env.ID || !m.onActivity() {
			return nil, true
		}
		if msg.err != nil {
			m.fail(msg.err)
			return m.activityAfter(), true
		}
		prev := ""
		if list := m.activityEntries(); a.cursor < len(list) {
			prev = list[a.cursor].key
		}
		a.loaded, a.conns, a.decisions, a.at = true, msg.conns, msg.decisions, msg.at
		a.rules = make(map[string]store.Rule, len(msg.rules))
		for _, r := range msg.rules {
			a.rules[r.ID] = r
		}
		m.keepActivityCursor(prev)
		return m.activityAfter(), true
	case activityTick:
		if msg.gen != a.gen || !m.onActivity() {
			return nil, true
		}
		return m.loadActivity(), true
	}
	return nil, false
}

// keepActivityCursor puts the cursor back on the entry it was on, which moves down as newer
// ones arrive, and keeps it in range.
func (m *app) keepActivityCursor(prev string) {
	a := &m.activity
	list := m.activityEntries()
	for i, e := range list {
		if e.key == prev && prev != "" {
			a.cursor = i
		}
	}
	a.cursor = min(max(a.cursor, 0), max(len(list)-1, 0))
}

// activityEntries are the connections and decisions of the sandboxes the filter lets through,
// newest first.
func (m *app) activityEntries() []activityEntry {
	a := &m.activity
	names := map[string]string{}
	for _, sb := range m.sandboxes {
		names[sb.ID] = sb.Name
	}
	var out []activityEntry
	for id, list := range a.conns {
		for _, c := range list {
			e := activityEntry{
				at: c.Started, sandbox: names[id], host: hostPortOf(c.Host, c.Port),
				verdict: connVerdict(c.Verdict), ruleID: c.RuleID, why: c.Error,
				bad: c.Verdict == gateway.VerdictDenied || c.Verdict == gateway.VerdictFailed,
				key: fmt.Sprintf("c%s/%d", id, c.ID),
			}
			if c.Ended != nil {
				e.at = *c.Ended
			}
			out = append(out, e)
		}
	}
	for _, d := range a.decisions {
		if !isNetwork(d) || d.Status == "pending" {
			continue
		}
		e := activityEntry{
			at: d.UpdatedAt, sandbox: firstNonEmpty(names[d.SandboxID], approvalSource(d)), host: hostOf(d),
			verdict: decisionWords(d), bad: d.Status == "denied", ruleID: d.RuleID, decision: true, key: "d" + d.ID,
		}
		if d.Network != nil {
			e.why, e.decided = d.Network.Reason, d.Network.Decided
		}
		out = append(out, e)
	}
	if a.filter != "" {
		out = slices.DeleteFunc(out, func(e activityEntry) bool { return !matches(a.filter, e.sandbox) })
	}
	slices.SortStableFunc(out, func(x, y activityEntry) int {
		if c := y.at.Compare(x.at); c != 0 {
			return c
		}
		return strings.Compare(x.key, y.key)
	})
	if len(out) > maxActivity {
		out = out[:maxActivity]
	}
	return out
}

// hostPortOf is a host and port as the web shows them; port 0 is a name refused at lookup.
func hostPortOf(host string, port int) string {
	if port == 0 {
		return host
	}
	return fmt.Sprintf("%s:%d", host, port)
}

// connVerdict is a connection's verdict as the log words it.
func connVerdict(v string) string {
	switch v {
	case gateway.VerdictPending:
		return "waiting"
	case gateway.VerdictUndecided:
		return "unanswered"
	}
	return v
}

// decisionWords says how a request was decided, as the web's Activity does.
func decisionWords(a api.ApprovalView) string {
	n := a.Network
	switch {
	case n != nil && n.Tracker:
		return "Denied · tracker"
	case a.Reverted:
		return "Reverted · reviewer"
	case n != nil && n.Decided == "open":
		return "Allowed · level Open"
	case n != nil && n.Decided == "curated":
		return "Allowed · curated list"
	case n != nil && n.Decided == "reviewer":
		if a.Status == "denied" {
			return "Denied · reviewer"
		}
		return "Allowed · reviewer"
	}
	word := map[string]string{"approved": "Allowed", "denied": "Denied", "dismissed": "Ignored"}[a.Status]
	if word == "" {
		word = a.Status
	}
	if a.Once {
		word += " once"
	}
	return word
}

// ruleCell names the rule that decided an entry: its host pattern, or why none did.
func (m *app) ruleCell(e activityEntry) string {
	if e.ruleID != "" {
		if r, ok := m.activity.rules[e.ruleID]; ok {
			return r.Host
		}
		return "a deleted rule"
	}
	switch {
	case e.decided == "curated":
		return "curated list"
	case e.decided == "open":
		return "level Open"
	case e.decision:
		return "no rule"
	}
	return "—"
}

// activityKey answers the Activity screen.
func (m *app) activityKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	a := &m.activity
	list := m.activityEntries()
	switch {
	case key.Matches(msg, keyEsc) && a.filter != "":
		a.filter, a.cursor = "", 0
	case key.Matches(msg, keyBack):
		m.screen = a.back
	case key.Matches(msg, keyUp):
		a.cursor = max(a.cursor-1, 0)
	case key.Matches(msg, keyDown):
		a.cursor = min(a.cursor+1, max(len(list)-1, 0))
	case key.Matches(msg, keyRule):
		if a.cursor < len(list) {
			e := list[a.cursor]
			a.entry = &e
			m.screen = screenEntry
		}
	case key.Matches(msg, keyFilter):
		m.filtering = true
	case key.Matches(msg, keyRules):
		m.setNotice("open in browser: "+m.browserURL("/e/"+m.env.ID+"/network/rules"), false)
	case key.Matches(msg, keyApprovals):
		m.openApprovals()
	case key.Matches(msg, keyGit):
		return m, m.openGit()
	case key.Matches(msg, keyHelp):
		m.openHelp()
	}
	return m, nil
}

// entryKey answers an entry's detail: any of the back keys returns to the log.
func (m *app) entryKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keyBack):
		m.screen = screenActivity
	case key.Matches(msg, keyRules):
		m.setNotice("open in browser: "+m.browserURL("/e/"+m.env.ID+"/network/rules"), false)
	case key.Matches(msg, keyHelp):
		m.openHelp()
	}
	return m, nil
}

// Column widths of the Activity log: when, sandbox and verdict. The host and the rule share
// the rest, the rule a third of it.
const (
	colWhen    = 8
	colSandbox = 12
	colVerdict = 19
)

// activityBody is the log, the cursor's entry in view.
func (m *app) activityBody(w, h int) []string {
	a := &m.activity
	head := styleBold.Render("Activity") + styleDim.Render(" · connections and decisions, newest first")
	if a.filter != "" {
		head += styleDim.Render(" · ") + "sandbox “" + a.filter + "”"
	}
	if !a.loaded {
		return []string{head, "", styleDim.Render("Loading…")}
	}
	list := m.activityEntries()
	if len(list) == 0 {
		empty := "Connections and decisions show up here as sandboxes make them."
		if a.filter != "" {
			empty = "Nothing from a sandbox matching “" + a.filter + "” yet."
		}
		return []string{head, "", styleDim.Render(empty)}
	}
	rest := w - 2 - colWhen - colSandbox - colVerdict - 4
	ruleW := max(rest/3, 12)
	hostW := max(rest-ruleW, 12)
	lines := []string{head}
	if strip := m.busyHosts(w, h); len(strip) > 0 {
		lines = append(append(lines, strip...), "")
	}
	rows := max(h-len(lines), 1)
	offset := 0
	if a.cursor >= rows {
		offset = a.cursor - rows + 1
	}
	for i := offset; i < len(list) && len(lines) < h; i++ {
		m.hitRow(len(lines), 0, w, rowEntry, i)
		e := list[i]
		cursor, host := "  ", fit(e.host, hostW)
		if i == a.cursor {
			cursor, host = m.cursorMark(true), styleBold.Render(host)
		}
		// The rule column names the curated list or the level, so the verdict needn't.
		word := e.verdict
		if e.decided == "curated" || e.decided == "open" {
			word, _, _ = strings.Cut(word, " · ")
		}
		verdict := fit(word, colVerdict)
		switch {
		case !m.look.plain:
			verdict = m.verdictDot(e) + " " + fit(word, colVerdict-2)
		case e.bad:
			verdict = styleFailed.Render(verdict)
		case e.decision:
			verdict = styleAccent.Render(verdict)
		}
		lines = append(lines, cursor+styleDim.Render(fit(age(m.now.Sub(e.at)), colWhen))+" "+
			fit(firstNonEmpty(e.sandbox, "—"), colSandbox)+" "+host+" "+verdict+" "+
			styleDim.Render(fit(m.ruleCell(e), ruleW)))
	}
	return lines
}

// busyBuckets is how many minutes back the busy hosts' sparklines go, one cell a minute.
const busyBuckets = 10

// busyHosts is the visual look's strip above the log: one row per busy host, the sparkline of its
// connections over the last ten minutes, and the verdict and rule of its latest one. A short
// screen leaves it out.
func (m *app) busyHosts(w, h int) []string {
	a := &m.activity
	if m.look.plain || h < 14 {
		return nil
	}
	limit := 3
	if h >= 30 {
		limit = 5
	}
	now := a.at
	if now.IsZero() {
		now = m.now
	}
	names := map[string]string{}
	for _, sb := range m.sandboxes {
		names[sb.ID] = sb.Name
	}
	type busy struct {
		host    string
		counts  []float64
		total   int
		latest  time.Time
		verdict string
		rule    string
	}
	hosts := map[string]*busy{}
	for id, list := range a.conns {
		if a.filter != "" && !matches(a.filter, names[id]) {
			continue
		}
		for _, c := range list {
			ago := max(now.Sub(c.Started), 0)
			if ago >= busyBuckets*time.Minute {
				continue
			}
			name := hostPortOf(c.Host, c.Port)
			b := hosts[name]
			if b == nil {
				b = &busy{host: name, counts: make([]float64, busyBuckets)}
				hosts[name] = b
			}
			b.counts[busyBuckets-1-int(ago/time.Minute)]++
			b.total++
			if c.Started.After(b.latest) || b.verdict == "" {
				b.latest, b.verdict = c.Started, connVerdict(c.Verdict)
				b.rule = m.ruleCell(activityEntry{ruleID: c.RuleID})
			}
		}
	}
	list := make([]*busy, 0, len(hosts))
	for _, b := range hosts {
		list = append(list, b)
	}
	slices.SortFunc(list, func(x, y *busy) int {
		if x.total != y.total {
			return y.total - x.total
		}
		return strings.Compare(x.host, y.host)
	})
	if len(list) > limit {
		list = list[:limit]
	}
	hostW := min(max(w-2-busyBuckets-colVerdict-6-20, 16), 32)
	if len(list) == 0 {
		return nil
	}
	lines := []string{styleDim.Render(fit("  Busiest hosts", hostW+2) + " " + fit("10 min", busyBuckets))}
	for _, b := range list {
		top := slices.Max(b.counts)
		style := m.verdictStyle(b.verdict)
		lines = append(lines, "  "+fit(b.host, hostW)+" "+style.Render(sparkline(b.counts, top, busyBuckets))+"   "+
			style.Render("●")+" "+fit(b.verdict, colVerdict-2)+" "+styleDim.Render(clip(b.rule, max(w-2-hostW-busyBuckets-colVerdict-6, 0))))
	}
	return lines
}

// verdictStyle is the status color of a verdict: allowed is running, denied and failed are
// failed, waiting is waiting, the rest stopped.
func (m *app) verdictStyle(verdict string) lipgloss.Style {
	switch word, _, _ := strings.Cut(strings.ToLower(verdict), " "); word {
	case "allowed":
		return m.pal.running
	case "denied", "failed", "reverted":
		return m.pal.failed
	case "waiting":
		return m.pal.waiting
	}
	return m.pal.stopped
}

// verdictDot is an entry's verdict as a status dot.
func (m *app) verdictDot(e activityEntry) string {
	return m.verdictStyle(e.verdict).Render("●")
}

// entryBody is one entry of the log and the rule that decided it.
func (m *app) entryBody(w, h int) []string {
	e := m.activity.entry
	if e == nil {
		return []string{styleDim.Render("No entry is selected.")}
	}
	kind := "Connection"
	if e.decision {
		kind = "Decision"
	}
	lines := []string{
		styleBold.Render(kind) + "  " + e.host,
		"",
		fact("Sandbox", firstNonEmpty(e.sandbox, "—")),
		fact("Verdict", e.verdict),
		fact("When", age(m.now.Sub(e.at))),
	}
	if e.why != "" {
		lines = append(lines, fact("Why", fit(oneLine(e.why), w-13)))
	}
	lines = append(lines, "")
	r, ok := m.activity.rules[e.ruleID]
	switch {
	case e.ruleID == "":
		lines = append(lines, styleBold.Render("No rule"), "  "+noRuleWhy(*e))
		return lines
	case !ok:
		lines = append(lines, styleBold.Render("Rule"), "  "+styleDim.Render("The rule that decided it was deleted since."))
		return lines
	}
	lines = append(lines,
		styleBold.Render("Rule")+"  "+r.Host,
		fact("Action", r.Action),
		fact("Ports", rulePorts(r)),
		fact("Covers", m.ruleScope(r)),
	)
	if r.BrowserOnly {
		lines = append(lines, fact("Where", "the browsers only"))
	}
	if r.Method != "" || r.PathPrefix != "" {
		lines = append(lines, fact("Requests", strings.TrimSpace(firstNonEmpty(r.Method, "any method")+" "+r.PathPrefix)))
	}
	lines = append(lines, fact("Origin", ruleOrigin(r.Origin)))
	hits := fmt.Sprintf("%d", r.Hits)
	if r.LastHitAt != nil {
		hits += ", the last " + age(m.now.Sub(*r.LastHitAt))
	}
	lines = append(lines, fact("Hits", hits))
	if r.ExpiresAt != nil {
		lines = append(lines, fact("Expires", expiry(r.ExpiresAt.Sub(m.now))))
	}
	lines = append(lines, fact("Created", age(m.now.Sub(r.CreatedAt))))
	if r.Note != "" {
		lines = append(lines, fact("Note", fit(oneLine(r.Note), w-13)))
	}
	return lines
}

// noRuleWhy says why no rule decided an entry.
func noRuleWhy(e activityEntry) string {
	switch {
	case e.decided == "curated":
		return "The curated list of development hosts allowed it."
	case e.decided == "open":
		return "The environment's level Open allows what no rule covers."
	case e.decision && strings.Contains(e.verdict, "once"):
		return "It was answered for this connection only."
	case e.decision && strings.HasPrefix(e.verdict, "Ignored"):
		return "It was ignored: refused this time, with no rule."
	case e.decision:
		return "No rule was made for it."
	case e.verdict == "unanswered":
		return "Nobody answered in time, so it was refused."
	case e.verdict == "waiting":
		return "It waits for a decision: see the approvals (a)."
	}
	return "No rule decided it: a decision, the level or the curated list did."
}

func rulePorts(r store.Rule) string {
	if len(r.Ports) == 0 {
		return "any"
	}
	ports := make([]string, len(r.Ports))
	for i, p := range r.Ports {
		ports[i] = fmt.Sprint(p)
	}
	return strings.Join(ports, ", ")
}

// ruleScope says which sandboxes a rule covers.
func (m *app) ruleScope(r store.Rule) string {
	switch {
	case r.SandboxID != "":
		for _, sb := range m.sandboxes {
			if sb.ID == r.SandboxID {
				return "only " + sb.Name
			}
		}
		return "one sandbox"
	case r.TeammateID != "":
		if name := m.teammates[r.TeammateID]; name != "" {
			return name + "'s sandboxes"
		}
		return "a teammate's sandboxes"
	}
	return "the environment"
}

// ruleOrigin says where a rule came from.
func ruleOrigin(origin string) string {
	switch origin {
	case "approval":
		return "an approval"
	case "curated":
		return "the curated list"
	case "reviewer":
		return "the AI reviewer"
	case "manual":
		return "made by hand"
	}
	return firstNonEmpty(origin, "—")
}

// expiry says when a rule stops matching.
func expiry(d time.Duration) string {
	if d <= 0 {
		return "expired"
	}
	return "in " + strings.TrimPrefix(uptime(d), "up ")
}

// activityBindings are the keys the key bar shows on the Activity screens.
func (m *app) activityBindings() []key.Binding {
	if m.screen == screenEntry {
		return []key.Binding{keyRules, keyBack, keyHelp}
	}
	return []key.Binding{keyRule, keyUp, keyFilterSandbox, keyRules, keyBack, keyHelp}
}

var keyFilterSandbox = key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "sandbox"))
