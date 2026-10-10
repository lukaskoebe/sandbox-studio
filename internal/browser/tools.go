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

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// call is one agent tool call: the persona's browser state and the calling sandbox, which
// the channel named.
type call struct {
	s  *Service
	st *state
	sb store.Sandbox
}

// userError is a refusal the agent should read as is.
type userError struct{ error }

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
}

// HandleCall answers one browser call of sandboxID's guest. The caller is the sandbox the
// channel belongs to; nothing in params can name another sandbox or persona.
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
	if sb.PersonaID == "" {
		return nil, ErrNoPersona
	}
	st := s.state(sb.EnvironmentID, sb.PersonaID)
	release, err := s.acquire(ctx, st)
	if err != nil {
		return nil, err
	}
	defer release()
	s.mu.Lock()
	st.driver, st.lastUsed = sb, s.now()
	s.mu.Unlock()
	if err := s.boot(ctx, st); err != nil {
		s.log().Warn("browser VM unavailable", "persona", sb.PersonaID, "err", err)
		return nil, errors.New("the browser could not be started; the user can look at the Browser page in Studio")
	}
	res, err := t(&call{s: s, st: st, sb: sb}, ctx, params)
	if err != nil {
		s.mu.Lock()
		secrets := st.secrets
		s.mu.Unlock()
		var ue userError
		if errors.As(err, &ue) || errors.Is(err, ErrPaused) || errors.Is(err, context.DeadlineExceeded) {
			return nil, errors.New(Scrub(err.Error(), secrets))
		}
		s.log().Warn("browser tool failed", "tool", method, "sandbox", sandboxID, "err", Scrub(err.Error(), secrets))
		return nil, errors.New(method + " failed: " + Scrub(err.Error(), secrets))
	}
	s.mu.Lock()
	st.lastUsed = s.now()
	s.mu.Unlock()
	return res, nil
}

// run executes a command for the agent; it refuses if the user took over meanwhile (an
// approval may have been awaited).
func (c *call) run(ctx context.Context, args []string, shot bool) (json.RawMessage, []byte, error) {
	c.s.mu.Lock()
	paused := c.st.takeover
	c.s.mu.Unlock()
	if paused {
		return nil, nil, ErrPaused
	}
	return c.s.exec(ctx, c.st, agentproto.BrowserCommand{Args: args, Screenshot: shot})
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
	json.Unmarshal(data, &v)
	c.s.mu.Lock()
	if v.URL != c.st.url {
		c.st.navigated(v.URL)
	}
	c.s.mu.Unlock()
	return v.URL, nil
}

var refPattern = regexp.MustCompile(`^@?(e[0-9]{1,6})$`)

// element resolves a ref from the last snapshot.
func (c *call) element(ref string) (string, refInfo, error) {
	m := refPattern.FindStringSubmatch(strings.TrimSpace(ref))
	if m == nil {
		return "", refInfo{}, refuse("ref must look like e12, from browser_snapshot")
	}
	c.s.mu.Lock()
	info, ok := c.st.refs[m[1]]
	c.s.mu.Unlock()
	if !ok {
		return "", refInfo{}, refuse("unknown ref %s: take a browser_snapshot of the current page first", m[1])
	}
	return m[1], info, nil
}

// authorize applies the policy to an action, asking the user when it says so.
func (c *call) authorize(ctx context.Context, r Request, url, text string) error {
	patterns, err := c.s.Store.BrowserPatterns(ctx, c.st.env, c.st.persona)
	if err != nil {
		return err
	}
	v, reason := Decide(r, patterns)
	switch v {
	case Allow:
		return nil
	case Deny:
		c.s.record(ctx, c.st, entry{actor: "agent", sandbox: c.sb.ID, action: r.Action, target: target(r.Role, r.Label), url: url, outcome: "denied", detail: reason})
		return refuse("%s", reason)
	}
	if c.s.takeGrant(c.st, r) {
		return nil
	}
	persona, _ := c.s.Store.Persona(ctx, c.st.env, c.st.persona)
	p := ActionPayload{PersonaID: c.st.persona, PersonaName: persona.Name, SandboxID: c.sb.ID, SandboxName: c.sb.Name,
		Action: r.Action, Origin: r.Origin, URL: url, Role: r.Role, Label: r.Label, Text: clip(text, 500)}
	raw, _ := json.Marshal(p)
	subject := fmt.Sprintf("%s: %s %s on %s", persona.Name, r.Action, target(r.Role, r.Label), r.Origin)
	a, created, err := c.s.Store.RequestApproval(ctx, store.Approval{EnvironmentID: c.st.env, SandboxID: c.sb.ID, Kind: KindAction, Subject: clip(subject, 500), Payload: raw})
	if err != nil {
		return err
	}
	if created {
		c.s.notify(c.st.env)
		c.s.record(ctx, c.st, entry{actor: "agent", sandbox: c.sb.ID, action: r.Action, target: target(r.Role, r.Label), url: url, outcome: "pending", detail: "asked the user"})
	}
	status, err := c.s.awaitApproval(ctx, c.st.env, a.ID)
	if err != nil {
		return err
	}
	switch status {
	case store.StatusApproved:
		c.s.takeGrant(c.st, r) // the approval is used now
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
	visible := c.st.visible
	c.s.mu.Unlock()
	_, shot, err := c.run(ctx, args, true)
	if visible {
		shot = nil // taken right after the action, it may still show the credential
	}
	e := entry{actor: "agent", sandbox: c.sb.ID, action: action, target: target(info.Role, info.Name), url: url, outcome: "ok", detail: detail, shot: shot}
	if err != nil {
		e.outcome, e.detail = "failed", err.Error()
		c.s.record(ctx, c.st, e)
		return nil, userError{err}
	}
	after, _ := c.currentURL(ctx)
	c.s.record(ctx, c.st, e)
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
	if err := c.authorize(ctx, Request{Action: "open", Origin: origin}, in.URL, ""); err != nil {
		return nil, err
	}
	data, shot, err := c.run(ctx, []string{"open", in.URL}, true)
	e := entry{actor: "agent", sandbox: c.sb.ID, action: "open", url: in.URL, outcome: "ok", shot: shot}
	if err != nil {
		e.outcome, e.detail = "failed", err.Error()
		c.s.record(ctx, c.st, e)
		return nil, userError{err}
	}
	var v struct {
		URL   string `json:"url"`
		Title string `json:"title"`
	}
	json.Unmarshal(data, &v)
	c.s.mu.Lock()
	c.st.navigated(v.URL)
	secrets := c.st.secrets
	c.s.mu.Unlock()
	e.detail = v.Title
	c.s.record(ctx, c.st, e)
	return Scrub(fmt.Sprintf("opened %s (%s). Take a browser_snapshot to see the page.", v.URL, v.Title), secrets), nil
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
	text, refs, err := c.s.redactSnapshot(ctx, c.st, snap)
	if err != nil {
		return nil, err
	}
	c.s.mu.Lock()
	if snap.Origin != c.st.url {
		c.st.navigated(snap.Origin)
	}
	c.st.refs = refs
	c.s.mu.Unlock()
	if text == "" {
		text = "(no elements)"
	}
	return "page: " + snap.Origin + "\n\n" + clip(text, maxAgentText), nil
}

// redactSnapshot reads the attributes of the snapshot's valued fields and blanks the
// sensitive ones; it returns the text the agent may see and the refs it knows.
func (s *Service) redactSnapshot(ctx context.Context, st *state, snap snapshotData) (string, map[string]refInfo, error) {
	patterns, err := s.Store.BrowserPatterns(ctx, st.env, st.persona)
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
		items, err := s.batch(ctx, st, cmds)
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
	secrets := st.secrets
	for ref := range st.credRefs {
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
	c.s.record(ctx, c.st, entry{actor: "agent", sandbox: c.sb.ID, action: "scroll", url: url, outcome: "ok", detail: in.Direction, shot: shot})
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
	json.Unmarshal(data, &v) // verify (live): the key of get text's response
	c.s.mu.Lock()
	secrets := c.st.secrets
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
	visible := c.st.visible
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
	c.s.record(ctx, c.st, entry{actor: "agent", sandbox: c.sb.ID, action: "back", url: after, outcome: "ok", shot: shot})
	return "went back to " + after, nil
}
