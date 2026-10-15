package browser

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// call is one agent tool call: the teammate's browser state, the session it drives and the
// calling sandbox, which the channel named.
type call struct {
	s  *Service
	st *state
	pg *page // nil until browser_open starts a new ephemeral session
	sb store.Sandbox
	// fresh is set on a new ephemeral session until its first command ran.
	fresh bool
}

// userError is a refusal the agent should read as is.
type userError struct{ error }

func (e userError) Unwrap() error { return e.error }

func refuse(format string, args ...any) error { return userError{fmt.Errorf(format, args...)} }

type tool func(c *call, ctx context.Context, params json.RawMessage) (any, error)

var tools = map[string]tool{
	agentproto.MethodBrowserOpen:           (*call).open,
	agentproto.MethodBrowserSnapshot:       (*call).snapshot,
	agentproto.MethodBrowserClick:          (*call).click,
	agentproto.MethodBrowserFill:           (*call).fill,
	agentproto.MethodBrowserType:           (*call).typeText,
	agentproto.MethodBrowserPress:          (*call).press,
	agentproto.MethodBrowserSelect:         (*call).selectOption,
	agentproto.MethodBrowserScroll:         (*call).scroll,
	agentproto.MethodBrowserGetText:        (*call).getText,
	agentproto.MethodBrowserScreenshot:     (*call).screenshot,
	agentproto.MethodBrowserWait:           (*call).wait,
	agentproto.MethodBrowserBack:           (*call).back,
	agentproto.MethodBrowserFillCredential: (*call).fillCredential,
	agentproto.MethodBrowserClose:          (*call).closeSession,
}

// HandleCall answers one browser call of sandboxID's guest. The caller is the sandbox the
// channel belongs to; nothing in params can name another sandbox or teammate.
func (s *Service) HandleCall(ctx context.Context, sandboxID, method string, params json.RawMessage) (any, error) {
	t, ok := tools[method]
	if !ok {
		return nil, fmt.Errorf("unknown call %q", method)
	}
	sb, err := s.Store.LookupSandbox(ctx, sandboxID)
	if err != nil {
		return nil, errors.New("unknown sandbox")
	}
	if sb.Kind != "" || sb.BuildJobID != "" {
		return nil, ErrNoCalls
	}
	if sb.TeammateID == "" {
		return nil, ErrNoTeammate
	}
	addr, params, err := takeSession(method, params)
	if err != nil {
		return nil, err
	}
	st := s.state(sb.EnvironmentID, sb.TeammateID)
	var pg *page
	if !addr.ephemeral {
		if pg, err = s.agentPage(st, addr.session, sb); err != nil {
			return nil, err
		}
	}
	release, err := s.acquire(ctx, st, pg)
	if err != nil {
		return nil, err
	}
	defer release()
	if pg != nil {
		// The session may have ended while the call waited.
		if pg, err = s.agentPage(st, addr.session, sb); err != nil {
			return nil, err
		}
	}
	s.mu.Lock()
	st.driver, st.lastUsed = sb, s.now()
	if pg != nil {
		pg.used = st.lastUsed
	}
	s.mu.Unlock()
	if err := s.boot(ctx, st); err != nil {
		s.log().Warn("browser VM unavailable", "teammate", sb.TeammateID, "err", err)
		return nil, errors.New("the browser could not be started; the user can look at the Browser page in Studio")
	}
	c := &call{s: s, st: st, pg: pg, sb: sb}
	res, err := t(c, ctx, params)
	if err != nil {
		var secrets []string
		s.mu.Lock()
		if c.pg != nil {
			secrets = c.pg.secrets
		}
		s.mu.Unlock()
		var lost lostReply
		if errors.As(err, &lost) {
			s.log().Warn("browser reply lost", "tool", method, "sandbox", sandboxID, "err", Scrub(err.Error(), secrets))
			return nil, errors.New(method + " failed: the browser's reply did not arrive (" + Scrub(err.Error(), secrets) +
				"). Take a browser_snapshot to see the page before you act on it.")
		}
		var ue userError
		if errors.As(err, &ue) || errors.Is(err, ErrPaused) || errors.Is(err, context.DeadlineExceeded) {
			return nil, errors.New(Scrub(err.Error(), secrets))
		}
		s.log().Warn("browser tool failed", "tool", method, "sandbox", sandboxID, "err", Scrub(err.Error(), secrets))
		return nil, errors.New(method + " failed: " + Scrub(err.Error(), secrets))
	}
	s.mu.Lock()
	st.lastUsed = s.now()
	if c.pg != nil {
		c.pg.used = st.lastUsed
	}
	s.mu.Unlock()
	return res, nil
}

// run executes a command for the agent; it refuses if the user took over meanwhile (an
// approval may have been awaited).
func (c *call) run(ctx context.Context, args []string, shot bool) (json.RawMessage, []byte, error) {
	c.s.mu.Lock()
	paused := c.pg.takeover
	c.s.mu.Unlock()
	if paused {
		return nil, nil, ErrPaused
	}
	data, shotData, err := c.s.exec(ctx, c.st, c.pg, agentproto.BrowserCommand{Args: args, Screenshot: shot, Fresh: c.fresh})
	c.fresh = false
	return data, shotData, err
}

// currentURL asks the page where it is; links and scripts move it between commands.
func (c *call) currentURL(ctx context.Context) (string, error) {
	data, _, err := c.run(ctx, []string{"get", "url"}, false)
	if err != nil {
		return "", err
	}
	var v struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return "", errors.New("the page's address was not understood")
	}
	c.s.mu.Lock()
	if v.URL != c.pg.url {
		c.pg.navigated(v.URL)
	}
	c.s.mu.Unlock()
	return v.URL, nil
}

// pageAfterLostReply asks the page where it is, once, after a command's reply was lost. The
// channel to the browser VM reconnects within seconds, so it waits a second first.
func (c *call) pageAfterLostReply(ctx context.Context) (string, error) {
	select {
	case <-time.After(time.Second):
	case <-ctx.Done():
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return c.currentURL(ctx)
}

// lostMessage tells the agent what Studio knows after a command's reply was lost. known says
// whether the page answered, and at is its address then.
func lostMessage(tool string, cause error, known bool, at string) string {
	if !known {
		return fmt.Sprintf("%s: the browser's reply did not arrive (%v), and the page could not be checked. Take a browser_snapshot before you act on it.", tool, cause)
	}
	return fmt.Sprintf("%s: the browser's reply did not arrive (%v). The page is at %s, and the command may have run. Take a browser_snapshot before you act on the page.", tool, cause, at)
}

// scrub removes the credentials filled into the current page from s.
func (c *call) scrub(s string) string {
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	return Scrub(s, c.pg.secrets)
}

var refPattern = regexp.MustCompile(`^@?(e[0-9]{1,6})$`)

// element resolves a ref from the last snapshot.
func (c *call) element(ref string) (string, refInfo, error) {
	m := refPattern.FindStringSubmatch(strings.TrimSpace(ref))
	if m == nil {
		return "", refInfo{}, refuse("ref must look like e12, from browser_snapshot")
	}
	c.s.mu.Lock()
	info, ok := c.pg.refs[m[1]]
	c.s.mu.Unlock()
	if !ok {
		return "", refInfo{}, refuse("unknown ref %s: take a browser_snapshot of the current page first", m[1])
	}
	return m[1], info, nil
}

// authorize applies the policy to an action, asking the user when it says so.
func (c *call) authorize(ctx context.Context, r Request, url, text string) error {
	patterns, err := c.s.Store.BrowserPatterns(ctx, c.st.env, c.st.teammate)
	if err != nil {
		return err
	}
	v, reason := Decide(r, patterns)
	switch v {
	case Allow:
		return nil
	case Deny:
		c.s.record(ctx, c.st, c.pg, entry{actor: "agent", sandbox: c.sb.ID, action: r.Action, target: target(r.Role, r.Label), url: url, outcome: "denied", detail: reason})
		return refuse("%s", reason)
	}
	if c.s.takeGrant(c.st, c.pg.id, r) {
		return nil
	}
	teammate, _ := c.s.Store.Teammate(ctx, c.st.env, c.st.teammate)
	p := ActionPayload{TeammateID: c.st.teammate, TeammateName: teammate.Name, SandboxID: c.sb.ID, SandboxName: c.sb.Name,
		Session: c.pg.id, Action: r.Action, Origin: r.Origin, URL: url, Role: r.Role, Label: r.Label, Text: clip(text, 500)}
	raw, _ := json.Marshal(p)
	subject := fmt.Sprintf("%s: %s %s on %s%s", teammate.Name, r.Action, target(r.Role, r.Label), r.Origin, sessionNote(c.pg.id))
	a, created, err := c.s.Store.RequestApproval(ctx, store.Approval{EnvironmentID: c.st.env, SandboxID: c.sb.ID, Kind: KindAction, Subject: clip(subject, 500), Payload: raw})
	if err != nil {
		return err
	}
	if created {
		c.s.notify(c.st.env)
		c.s.record(ctx, c.st, c.pg, entry{actor: "agent", sandbox: c.sb.ID, action: r.Action, target: target(r.Role, r.Label), url: url, outcome: "pending", detail: "asked the user"})
	}
	status, err := c.s.awaitApproval(ctx, c.st.env, a.ID)
	if err != nil {
		return err
	}
	switch status {
	case store.StatusApproved:
		c.s.takeGrant(c.st, c.pg.id, r) // the approval is used now
		return nil
	case store.StatusPending:
		return refuse("waiting for the user to approve %s on %s in Studio; call again in a minute to retry", r.Action, r.Origin)
	}
	return refuse("the user did not allow %s on %s", r.Action, r.Origin)
}

// act runs a page-changing action on an element: policy, command, log.
func (c *call) act(ctx context.Context, action, ref string, args []string, text, detail string) (any, error) {
	url, err := c.currentURL(ctx)
	if err != nil {
		return nil, err
	}
	var info refInfo
	if ref != "" {
		if ref, info, err = c.element(ref); err != nil {
			return nil, err
		}
		args[1] = "@" + ref
	}
	r := Request{Action: action, Origin: Origin(url), Role: info.Role, Label: info.Name, Sensitive: info.Sensitive}
	if r.Origin == "" {
		return nil, refuse("the page is not a web page (%s); browser_open one first", clip(url, 100))
	}
	if err := c.authorize(ctx, r, url, text); err != nil {
		return nil, err
	}
	c.s.mu.Lock()
	visible := c.pg.visible
	c.s.mu.Unlock()
	_, shot, err := c.run(ctx, args, true)
	if visible {
		shot = nil // taken right after the action, it may still show the credential
	}
	e := entry{actor: "agent", sandbox: c.sb.ID, action: action, target: target(info.Role, info.Name), url: url, outcome: "ok", detail: detail, shot: shot}
	if err != nil {
		var lost lostReply
		if errors.As(err, &lost) {
			at, rerr := c.pageAfterLostReply(ctx)
			err = errors.New(c.scrub(lostMessage("browser_"+action, err, rerr == nil, at)))
		}
		e.outcome, e.detail = "failed", err.Error()
		c.s.record(ctx, c.st, c.pg, e)
		return nil, userError{err}
	}
	after, _ := c.currentURL(ctx)
	c.s.record(ctx, c.st, c.pg, e)
	return fmt.Sprintf("done; the page is at %s. Take a browser_snapshot to see it.", after), nil
}

// decode reads a tool's arguments. Unknown fields are refused: a call has no say in
// whose browser it drives.
func decode(params json.RawMessage, v any) error {
	if len(params) == 0 {
		params = json.RawMessage("{}")
	}
	d := json.NewDecoder(bytes.NewReader(params))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return refuse("bad arguments: %v", err)
	}
	return nil
}

// typedDetail describes typed text for the log without echoing what went into a
// sensitive field.
func (c *call) typedDetail(ref, text string) string {
	if _, info, err := c.element(ref); err == nil && info.Sensitive {
		return fmt.Sprintf("%d characters into a sensitive field", len([]rune(text)))
	}
	return strconv.Quote(clip(text, 200))
}

func (c *call) open(ctx context.Context, params json.RawMessage) (any, error) {
	var in struct {
		URL string `json:"url"`
	}
	if err := decode(params, &in); err != nil {
		return nil, err
	}
	origin := Origin(in.URL)
	if origin == "" || len(in.URL) > 4000 {
		return nil, refuse("url must be an absolute http or https URL")
	}
	if c.pg == nil {
		// A new ephemeral session: its first command starts it with an empty profile. If
		// the open fails, the session ends again.
		pg, err := c.s.reserveEphemeral(c.st, c.sb, c.s.chatOf(ctx, c.sb))
		if err != nil {
			return nil, err
		}
		c.pg, c.fresh = pg, true
		opened := false
		defer func() {
			if !opened {
				_ = c.s.closeEphemeral(context.WithoutCancel(ctx), c.st, pg, "ended because its first page did not open") // it logs a failure
			}
		}()
		res, err := c.openPage(ctx, in.URL, origin)
		if err != nil {
			return nil, err
		}
		opened = true
		return map[string]string{"session": pg.id, "result": res,
			"note": "Pass this session to the other browser tools to use it; without it they drive your persistent profile. browser_close it when you are done; nothing it stores is kept."}, nil
	}
	return c.openPage(ctx, in.URL, origin)
}

func (c *call) openPage(ctx context.Context, url, origin string) (string, error) {
	if err := c.authorize(ctx, Request{Action: "open", Origin: origin}, url, ""); err != nil {
		return "", err
	}
	data, shot, err := c.run(ctx, []string{"open", url}, true)
	e := entry{actor: "agent", sandbox: c.sb.ID, action: "open", url: url, outcome: "ok", shot: shot}
	if err != nil {
		var lost lostReply
		if !errors.As(err, &lost) {
			e.outcome, e.detail = "failed", err.Error()
			c.s.record(ctx, c.st, c.pg, e)
			return "", userError{err}
		}
		// The open may have run. On the origin it asked for, it did: the agent gets the address
		// and no screenshot, which came in the lost reply.
		at, rerr := c.pageAfterLostReply(ctx)
		if rerr == nil && Origin(at) == origin {
			e.outcome, e.detail = "ok", "the reply did not arrive, so there is no screenshot"
			c.s.record(ctx, c.st, c.pg, e)
			return c.scrub(fmt.Sprintf("opened %s, but its reply did not arrive (%v), so there is no screenshot or title. The page is at %s. Take a browser_snapshot to see the page.", url, err, at)), nil
		}
		msg := c.scrub(lostMessage("browser_open", err, rerr == nil, at))
		e.outcome, e.detail = "failed", msg
		c.s.record(ctx, c.st, c.pg, e)
		return "", userError{errors.New(msg)}
	}
	var v struct {
		URL   string `json:"url"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		e.outcome, e.detail = "failed", "the reply was not understood"
		c.s.record(ctx, c.st, c.pg, e)
		return "", userError{errors.New("the browser's reply to browser_open was not understood")}
	}
	c.s.mu.Lock()
	c.pg.navigated(v.URL)
	secrets := c.pg.secrets
	c.s.mu.Unlock()
	e.detail = v.Title
	c.s.record(ctx, c.st, c.pg, e)
	return Scrub(fmt.Sprintf("opened %s (%s). Take a browser_snapshot to see the page.", v.URL, v.Title), secrets), nil
}

// sessionNote marks approval subjects of an ephemeral session.
func sessionNote(id string) string {
	if id == "" {
		return ""
	}
	return " (ephemeral session " + id + ")"
}

// snapshotData is agent-browser's snapshot response.
type snapshotData struct {
	Snapshot string `json:"snapshot"`
	Origin   string `json:"origin"` // the page URL
	Refs     map[string]struct {
		Role string `json:"role"`
		Name string `json:"name"`
	} `json:"refs"`
}

func (c *call) snapshot(ctx context.Context, params json.RawMessage) (any, error) {
	var in struct {
		Full bool `json:"full"`
	}
	if err := decode(params, &in); err != nil {
		return nil, err
	}
	args := []string{"snapshot", "-i"}
	if in.Full {
		args = []string{"snapshot"}
	}
	data, _, err := c.run(ctx, args, false)
	if err != nil {
		return nil, userError{err}
	}
	var snap snapshotData
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, errors.New("the snapshot was not understood")
	}
	text, refs, err := c.s.redactSnapshot(ctx, c.st, c.pg, snap)
	if err != nil {
		return nil, err
	}
	c.s.mu.Lock()
	if snap.Origin != c.pg.url {
		c.pg.navigated(snap.Origin)
	}
	c.pg.refs = refs
	c.s.mu.Unlock()
	if text == "" {
		text = "(no elements)"
	}
	return "page: " + snap.Origin + "\n\n" + clip(text, maxAgentText), nil
}

// redactSnapshot reads the attributes of the snapshot's valued fields and blanks the
// sensitive ones; it returns the text the agent may see and the refs it knows.
func (s *Service) redactSnapshot(ctx context.Context, st *state, pg *page, snap snapshotData) (string, map[string]refInfo, error) {
	patterns, err := s.Store.BrowserPatterns(ctx, st.env, st.teammate)
	if err != nil {
		return "", nil, err
	}
	origin := Origin(snap.Origin)
	probes := Probes(snap.Snapshot)
	if len(probes) > maxProbes {
		probes = probes[:maxProbes] // the rest stay unprobed and so are blanked
	}
	fields := map[string]Field{}
	if len(probes) > 0 {
		cmds := make([][]string, 0, 2*len(probes))
		for _, l := range probes {
			cmds = append(cmds, []string{"get", "attr", "@" + l.Ref, "type"}, []string{"get", "attr", "@" + l.Ref, "autocomplete"})
		}
		items, err := s.batch(ctx, st, pg, cmds)
		for i, l := range probes {
			f := Field{Ref: l.Ref, Role: l.Role, Name: l.Name}
			if err == nil && items[2*i].Success && items[2*i+1].Success {
				f.Type, f.Probed = attrValue(items[2*i].Result), true
				f.Autocomplete = attrValue(items[2*i+1].Result)
			}
			fields[l.Ref] = f
		}
	}
	s.mu.Lock()
	secrets := pg.secrets
	for ref := range pg.credRefs {
		if f, ok := fields[ref]; ok {
			f.Credential = true
			fields[ref] = f
		}
	}
	s.mu.Unlock()
	refs := make(map[string]refInfo, len(snap.Refs))
	for ref, r := range snap.Refs {
		info := refInfo{Role: r.Role, Name: r.Name}
		if f, ok := fields[ref]; ok {
			info.Sensitive = f.Sensitive(origin, patterns)
		} else {
			info.Sensitive = Field{Role: r.Role, Name: r.Name, Probed: true}.Sensitive(origin, patterns)
		}
		refs[ref] = info
	}
	return Scrub(Redact(snap.Snapshot, origin, fields, patterns), secrets), refs, nil
}

func attrValue(raw json.RawMessage) string {
	var v struct {
		Value *string `json:"value"`
	}
	if json.Unmarshal(raw, &v) != nil || v.Value == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(*v.Value))
}

func (c *call) click(ctx context.Context, params json.RawMessage) (any, error) {
	var in struct {
		Ref string `json:"ref"`
	}
	if err := decode(params, &in); err != nil {
		return nil, err
	}
	return c.act(ctx, "click", in.Ref, []string{"click", "@"}, "", "")
}

func (c *call) fill(ctx context.Context, params json.RawMessage) (any, error) {
	var in struct {
		Ref  string `json:"ref"`
		Text string `json:"text"`
	}
	if err := decode(params, &in); err != nil {
		return nil, err
	}
	return c.act(ctx, "fill", in.Ref, []string{"fill", "@", in.Text}, in.Text, c.typedDetail(in.Ref, in.Text))
}

func (c *call) typeText(ctx context.Context, params json.RawMessage) (any, error) {
	var in struct {
		Ref  string `json:"ref"`
		Text string `json:"text"`
	}
	if err := decode(params, &in); err != nil {
		return nil, err
	}
	return c.act(ctx, "type", in.Ref, []string{"type", "@", in.Text}, in.Text, c.typedDetail(in.Ref, in.Text))
}

var keyPattern = regexp.MustCompile(`^[A-Za-z0-9+]{1,32}$|^[[:punct:]]$`)

func (c *call) press(ctx context.Context, params json.RawMessage) (any, error) {
	var in struct {
		Key string `json:"key"`
	}
	if err := decode(params, &in); err != nil {
		return nil, err
	}
	if !keyPattern.MatchString(in.Key) || in.Key == "-" {
		return nil, refuse("key must be a key name such as Enter, Tab or Control+a")
	}
	return c.act(ctx, "press", "", []string{"press", in.Key}, "", in.Key)
}

func (c *call) selectOption(ctx context.Context, params json.RawMessage) (any, error) {
	var in struct {
		Ref   string `json:"ref"`
		Value string `json:"value"`
	}
	if err := decode(params, &in); err != nil {
		return nil, err
	}
	return c.act(ctx, "select", in.Ref, []string{"select", "@", in.Value}, in.Value, strconv.Quote(clip(in.Value, 200)))
}

func (c *call) scroll(ctx context.Context, params json.RawMessage) (any, error) {
	var in struct {
		Direction string `json:"direction"`
		Amount    int    `json:"amount"`
	}
	if err := decode(params, &in); err != nil {
		return nil, err
	}
	switch in.Direction {
	case "up", "down", "left", "right":
	default:
		return nil, refuse("direction must be up, down, left or right")
	}
	if in.Amount <= 0 || in.Amount > 10000 {
		in.Amount = 600
	}
	url, _ := c.currentURL(ctx)
	if err := c.authorize(ctx, Request{Action: "scroll", Origin: Origin(url)}, url, ""); err != nil {
		return nil, err
	}
	_, shot, err := c.run(ctx, []string{"scroll", in.Direction, strconv.Itoa(in.Amount)}, true)
	if err != nil {
		return nil, userError{err}
	}
	c.s.record(ctx, c.st, c.pg, entry{actor: "agent", sandbox: c.sb.ID, action: "scroll", url: url, outcome: "ok", detail: in.Direction, shot: shot})
	return "scrolled " + in.Direction, nil
}

func (c *call) getText(ctx context.Context, params json.RawMessage) (any, error) {
	var in struct {
		Ref string `json:"ref"`
	}
	if err := decode(params, &in); err != nil {
		return nil, err
	}
	ref, info, err := c.element(in.Ref)
	if err != nil {
		return nil, err
	}
	url, _ := c.currentURL(ctx)
	if err := c.authorize(ctx, Request{Action: "get_text", Origin: Origin(url), Role: info.Role, Label: info.Name}, url, ""); err != nil {
		return nil, err
	}
	// get text reads innerText, which holds no input values.
	data, _, err := c.run(ctx, []string{"get", "text", "@" + ref}, false)
	if err != nil {
		return nil, userError{err}
	}
	var v struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(data, &v); err != nil { // verify (live): the key of get text's response
		return nil, errors.New("the text was not understood")
	}
	c.s.mu.Lock()
	secrets := c.pg.secrets
	c.s.mu.Unlock()
	return clip(Scrub(v.Text, secrets), maxAgentText), nil
}

// Image is a tool result the MCP server returns as image content.
type Image struct {
	Image struct {
		MIMEType string `json:"mimeType"`
		Data     string `json:"data"`
	} `json:"image"`
	Text string `json:"text"`
}

func (c *call) screenshot(ctx context.Context, params json.RawMessage) (any, error) {
	if err := decode(params, &struct{}{}); err != nil {
		return nil, err
	}
	c.s.mu.Lock()
	visible := c.pg.visible
	c.s.mu.Unlock()
	if visible {
		return nil, refuse("a credential Studio filled may be visible on this page, so screenshots are off until the page changes; use browser_snapshot")
	}
	url, _ := c.currentURL(ctx)
	if err := c.authorize(ctx, Request{Action: "screenshot", Origin: Origin(url)}, url, ""); err != nil {
		return nil, err
	}
	_, shot, err := c.run(ctx, []string{"get", "url"}, true)
	if err != nil {
		return nil, userError{err}
	}
	if len(shot) == 0 {
		return nil, refuse("the screenshot failed or was too large")
	}
	var img Image
	img.Image.MIMEType = "image/jpeg"
	img.Image.Data = base64.StdEncoding.EncodeToString(shot)
	img.Text = "page: " + url
	return img, nil
}

func (c *call) wait(ctx context.Context, params json.RawMessage) (any, error) {
	var in struct {
		Text string `json:"text"`
		URL  string `json:"url"`
		MS   int    `json:"ms"`
	}
	if err := decode(params, &in); err != nil {
		return nil, err
	}
	var args []string
	switch {
	case in.Text != "":
		args = []string{"wait", "--text", in.Text}
	case in.URL != "":
		args = []string{"wait", "--url", in.URL}
	case in.MS > 0 && in.MS <= 10000:
		args = []string{"wait", strconv.Itoa(in.MS)}
	default:
		return nil, refuse("give text, url or ms (at most 10000)")
	}
	url, _ := c.currentURL(ctx)
	if err := c.authorize(ctx, Request{Action: "wait", Origin: Origin(url)}, url, ""); err != nil {
		return nil, err
	}
	if _, _, err := c.run(ctx, args, false); err != nil {
		return nil, userError{err}
	}
	return "done", nil
}

func (c *call) back(ctx context.Context, params json.RawMessage) (any, error) {
	if err := decode(params, &struct{}{}); err != nil {
		return nil, err
	}
	url, _ := c.currentURL(ctx)
	if err := c.authorize(ctx, Request{Action: "back", Origin: Origin(url)}, url, ""); err != nil {
		return nil, err
	}
	_, shot, err := c.run(ctx, []string{"back"}, true)
	if err != nil {
		return nil, userError{err}
	}
	after, _ := c.currentURL(ctx)
	c.s.record(ctx, c.st, c.pg, entry{actor: "agent", sandbox: c.sb.ID, action: "back", url: after, outcome: "ok", shot: shot})
	return "went back to " + after, nil
}
