//go:build e2e

package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// A teammate's browser VM runs the same fake guest as a sandbox. Its browser and
// browser-downloads streams are answered by browserVM, a small page model that speaks
// agent-browser 0.39's --json format (the shapes of internal/browser's fakeBrowser). Each
// session slot (0 the persistent profile, 1 to MaxBrowserEphemeral the ephemeral ones) is a
// tab with its own history, so back and forward work. A started slot serves its screencast on
// BrowserStreamPort+slot, as agent-browser's stream server does: {"type":"url"} and
// {"type":"frame"} messages with a JPEG of the page, and mouse and keyboard input that clicks
// and types into it. Nothing leaves the process: pages are made up from their URL.

// Size of the fake viewport: frames and screenshots.
const viewW, viewH = 640, 400

// maxInputs bounds the input messages a browser VM remembers for the control API.
const maxInputs = 50

// element is something on a fake page, with where the frame draws it.
type element struct {
	ref, role, name, typ, autocomplete string
	rect                               image.Rectangle
}

// Pages: every http(s) page is a sign-in form, except /welcome, where signing in leads; its
// link signs out to /login.
var (
	loginPage = []element{
		{"e1", "textbox", "Email", "email", "username", image.Rect(40, 110, 600, 146)},
		{"e2", "textbox", "Password", "password", "current-password", image.Rect(40, 166, 600, 202)},
		{"e3", "textbox", "API token", "text", "off", image.Rect(40, 222, 600, 258)},
		{"e4", "button", "Sign in", "submit", "", image.Rect(40, 286, 200, 326)},
	}
	welcomePage = []element{
		{"e1", "link", "Sign out", "", "", image.Rect(40, 110, 200, 140)},
	}
)

// browserVM is the browser of one boot of a browser VM.
type browserVM struct {
	mu     sync.Mutex
	tabs   [agentproto.MaxBrowserEphemeral + 1]*tab // nil until the slot's session started
	inputs []json.RawMessage
	// fetch loads a localhost page through the VM's forwarded ports, as Chromium would.
	fetch func(u *url.URL) (string, bool)
}

// tab is one agent-browser session: its history, the values typed into the page, and the
// live views watching it.
type tab struct {
	history  []string
	at       int
	values   map[string]string // ref → value
	focus    string            // the ref the live view's user clicked into
	watchers map[chan struct{}]struct{}
	// served are the localhost pages that loaded, by URL; fetch loads one.
	served map[string]string
	fetch  func(u *url.URL) (string, bool)
}

func newTab() *tab {
	return &tab{history: []string{"about:blank"}, values: map[string]string{}, watchers: map[chan struct{}]struct{}{}}
}

func (t *tab) url() string { return t.history[t.at] }

func (t *tab) elements() []element {
	u, err := url.Parse(t.url())
	switch {
	case err != nil || (u.Scheme != "http" && u.Scheme != "https"):
		return nil
	case u.Path == "/welcome":
		return welcomePage
	}
	return loginPage
}

func (t *tab) title() string {
	u, err := url.Parse(t.url())
	if err != nil || u.Host == "" {
		return ""
	}
	if localhost(u) {
		// A forwarded page's title is its first line; one nothing serves fails, as in Chromium.
		if body, ok := t.served[t.url()]; ok {
			first, _, _ := strings.Cut(body, "\n")
			return first
		}
		return u.Host + " refused to connect"
	}
	if u.Path == "/welcome" {
		return "Welcome · " + u.Hostname()
	}
	return "Sign in · " + u.Hostname()
}

func (t *tab) element(ref string) (element, bool) {
	for _, e := range t.elements() {
		if "@"+e.ref == ref || e.ref == ref {
			return e, true
		}
	}
	return element{}, false
}

// open goes to a new page: the forward history and the old page's values are gone.
func (t *tab) open(u string) {
	t.history = append(t.history[:t.at+1], u)
	t.at = len(t.history) - 1
	t.values, t.focus = map[string]string{}, ""
	if pu, err := url.Parse(u); err == nil && localhost(pu) && t.fetch != nil {
		if body, ok := t.fetch(pu); ok {
			if t.served == nil {
				t.served = map[string]string{}
			}
			t.served[u] = body
		}
	}
}

// localhost reports whether u is on the browser VM's loopback.
func localhost(u *url.URL) bool {
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

// move goes back or forward in the history, if there is somewhere to go.
func (t *tab) move(by int) {
	if at := t.at + by; at >= 0 && at < len(t.history) {
		t.at = at
		t.values, t.focus = map[string]string{}, ""
	}
}

// activate follows a link or submits the form, as clicking it would.
func (t *tab) activate(e element) {
	u, err := url.Parse(t.url())
	if err != nil {
		return
	}
	switch e.role {
	case "button":
		u.Path = "/welcome"
	case "link":
		u.Path = "/login"
	default:
		t.focus = e.ref
		return
	}
	u.RawQuery, u.Fragment = "", ""
	t.open(u.String())
}

// changed tells the tab's live views to send the page again.
func (t *tab) changed() {
	for ch := range t.watchers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// end closes the tab's live views, as closing its session does.
func (t *tab) end() {
	for ch := range t.watchers {
		close(ch)
	}
	t.watchers = map[chan struct{}]struct{}{}
}

// snapshot renders the page like agent-browser's render_tree; values show as typed.
func (t *tab) snapshot(full bool) string {
	var b strings.Builder
	if full && t.title() != "" {
		fmt.Fprintf(&b, "- heading %q [level=1]\n", t.title())
	}
	for _, e := range t.elements() {
		q, _ := json.Marshal(e.name)
		fmt.Fprintf(&b, "- %s %s [ref=%s]", e.role, q, e.ref)
		if v := t.values[e.ref]; v != "" {
			b.WriteString(": " + v)
		}
		b.WriteString("\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// answer runs one agent-browser command on the tab; it reports whether the page changed.
func (t *tab) answer(args []string) (any, bool, error) {
	arg := func(i int) string {
		if i < len(args) {
			return args[i]
		}
		return ""
	}
	switch args[0] {
	case "get":
		switch arg(1) {
		case "url":
			return map[string]any{"url": t.url()}, false, nil
		case "title":
			return map[string]any{"title": t.title()}, false, nil
		case "attr":
			e, ok := t.element(arg(2))
			if !ok {
				return nil, false, fmt.Errorf("Element not found: %s", arg(2))
			}
			v, ok := map[string]string{"type": e.typ, "autocomplete": e.autocomplete}[arg(3)]
			if !ok || v == "" {
				return map[string]any{"value": nil, "origin": t.url()}, false, nil
			}
			return map[string]any{"value": v, "origin": t.url()}, false, nil
		case "text":
			e, ok := t.element(arg(2))
			if !ok {
				return nil, false, fmt.Errorf("Element not found: %s", arg(2))
			}
			text := e.name // inputs have no innerText
			if e.role == "textbox" {
				text = ""
			}
			return map[string]any{"text": text}, false, nil
		}
		return nil, false, fmt.Errorf("unknown get %q", arg(1))
	case "open":
		t.open(arg(1))
		return map[string]any{"url": t.url(), "title": t.title()}, true, nil
	case "back":
		t.move(-1)
		return map[string]any{"url": t.url()}, true, nil
	case "forward":
		t.move(1)
		return map[string]any{"url": t.url()}, true, nil
	case "reload":
		t.values, t.focus = map[string]string{}, ""
		return map[string]any{"url": t.url()}, true, nil
	case "snapshot":
		refs := map[string]any{}
		for _, e := range t.elements() {
			refs[e.ref] = map[string]string{"role": e.role, "name": e.name}
		}
		return map[string]any{"snapshot": t.snapshot(arg(1) != "-i"), "origin": t.url(), "refs": refs}, false, nil
	case "fill", "type":
		e, ok := t.element(arg(1))
		if !ok || e.role != "textbox" {
			return nil, false, fmt.Errorf("Element not found or not editable: %s", arg(1))
		}
		if args[0] == "type" {
			t.values[e.ref] += arg(2)
		} else {
			t.values[e.ref] = arg(2)
		}
		return map[string]any{"filled": arg(1)}, true, nil
	case "click":
		e, ok := t.element(arg(1))
		if !ok {
			return nil, false, fmt.Errorf("Element not found: %s", arg(1))
		}
		t.activate(e)
		return map[string]any{}, true, nil
	case "press":
		if arg(1) == "Enter" {
			for _, e := range t.elements() {
				if e.typ == "submit" {
					t.activate(e)
				}
			}
		}
		return map[string]any{}, true, nil
	case "select", "scroll", "wait", "screenshot":
		return map[string]any{}, false, nil
	}
	return nil, false, fmt.Errorf("unknown command %q", args[0])
}

// render draws the page: a bar in a colour of its URL, and its elements, filled in by
// what was typed. Password fields show dots like the rest; no text is drawn.
func (t *tab) render() []byte {
	img := image.NewRGBA(image.Rect(0, 0, viewW, viewH))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{250, 250, 250, 255}), image.Point{}, draw.Src)
	h := fnv.New32a()
	io.WriteString(h, t.url())
	sum := h.Sum32()
	bar := color.RGBA{uint8(sum), uint8(sum >> 8), uint8(sum >> 16), 255}
	draw.Draw(img, image.Rect(0, 0, viewW, 56), image.NewUniform(bar), image.Point{}, draw.Src)
	for _, e := range t.elements() {
		fill := color.RGBA{255, 255, 255, 255}
		switch {
		case e.role == "button":
			fill = color.RGBA{37, 99, 235, 255}
		case e.role == "link":
			fill = color.RGBA{191, 219, 254, 255}
		}
		border := color.RGBA{160, 160, 160, 255}
		if e.ref == t.focus {
			border = color.RGBA{37, 99, 235, 255}
		}
		draw.Draw(img, e.rect, image.NewUniform(border), image.Point{}, draw.Src)
		draw.Draw(img, e.rect.Inset(2), image.NewUniform(fill), image.Point{}, draw.Src)
		for i := range min(len([]rune(t.values[e.ref])), (e.rect.Dx()-20)/10) {
			x := e.rect.Min.X + 10 + 10*i
			draw.Draw(img, image.Rect(x, e.rect.Min.Y+12, x+6, e.rect.Max.Y-12), image.NewUniform(color.RGBA{30, 30, 30, 255}), image.Point{}, draw.Src)
		}
	}
	var b bytes.Buffer
	jpeg.Encode(&b, img, &jpeg.Options{Quality: 60})
	return b.Bytes()
}

// input applies one live-view input message to the tab. It reports whether the page changed.
func (t *tab) input(msg []byte) bool {
	var m struct {
		Type      string  `json:"type"`
		EventType string  `json:"eventType"`
		X         float64 `json:"x"`
		Y         float64 `json:"y"`
		Button    string  `json:"button"`
		Key       string  `json:"key"`
		Text      *string `json:"text"`
	}
	if json.Unmarshal(msg, &m) != nil {
		return false
	}
	switch {
	case m.Type == "input_mouse" && m.EventType == "mousePressed" && m.Button == "left":
		at := image.Pt(int(m.X), int(m.Y))
		t.focus = ""
		for _, e := range t.elements() {
			if at.In(e.rect) {
				t.activate(e)
				break
			}
		}
		return true
	case m.Type != "input_keyboard" || t.focus == "":
		return false
	case m.EventType == "keyDown" && m.Key == "Backspace":
		v := []rune(t.values[t.focus])
		if len(v) > 0 {
			t.values[t.focus] = string(v[:len(v)-1])
		}
		return true
	case (m.EventType == "keyDown" || m.EventType == "char") && m.Text != nil:
		if *m.Text == "\r" {
			for _, e := range t.elements() {
				if e.typ == "submit" {
					t.activate(e)
				}
			}
			return true
		}
		t.values[t.focus] += *m.Text
		return true
	}
	return false
}

// command answers a browser stream's command, as the real guest and agent-browser do.
func (b *browserVM) command(req agentproto.BrowserCommand) (agentproto.BrowserResult, error) {
	var res agentproto.BrowserResult
	if req.Ephemeral < 0 || req.Ephemeral > agentproto.MaxBrowserEphemeral || (req.Fresh && req.Ephemeral == 0) {
		return res, errors.New("bad browser session")
	}
	if len(req.Args) == 0 || len(req.Args) > agentproto.MaxBrowserArgs {
		return res, errors.New("bad browser command")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	t := b.tabs[req.Ephemeral]
	if req.Ephemeral > 0 && len(req.Args) == 1 && req.Args[0] == "close" && !req.Fresh {
		if t != nil {
			t.end()
			b.tabs[req.Ephemeral] = nil
		}
		res.Output = json.RawMessage(`{"success":true,"data":null,"error":null}`)
		return res, nil
	}
	if req.Args[0] == "close" {
		return res, errors.New(`browser command "close" is not allowed`)
	}
	switch {
	case req.Fresh:
		if t != nil {
			t.end()
		}
		t = newTab()
		t.fetch = b.fetch
		b.tabs[req.Ephemeral] = t
	case t == nil && req.Ephemeral > 0:
		return res, errors.New(agentproto.ErrBrowserEphemeralGone)
	case t == nil:
		// The first command starts the profile's daemon, and with it the live view.
		t = newTab()
		t.fetch = b.fetch
		b.tabs[0] = t
	}
	changed := false
	if req.Args[0] == "batch" {
		var cmds [][]string
		if err := json.Unmarshal([]byte(req.Stdin), &cmds); err != nil || len(cmds) == 0 {
			return res, errors.New("batch stdin is not a list of commands")
		}
		items := make([]map[string]any, 0, len(cmds))
		for _, c := range cmds {
			if len(c) == 0 {
				return res, errors.New("bad browser command")
			}
			data, ch, err := t.answer(c)
			changed = changed || ch
			item := map[string]any{"command": c, "success": err == nil, "result": data, "error": nil}
			if err != nil {
				item["error"] = err.Error()
			}
			items = append(items, item)
		}
		res.Output, _ = json.Marshal(items)
	} else {
		data, ch, err := t.answer(req.Args)
		changed = ch
		if err != nil {
			res.Output, _ = json.Marshal(map[string]any{"success": false, "data": nil, "error": err.Error()})
		} else {
			res.Output, _ = json.Marshal(map[string]any{"success": true, "data": data, "error": nil})
		}
	}
	if changed {
		t.changed()
	}
	if req.Screenshot {
		res.Screenshot = t.render()
	}
	return res, nil
}

// serveBrowser answers a browser stream.
func (g *Guest) serveBrowser(st net.Conn, br *bufio.Reader) error {
	var req agentproto.BrowserCommand
	if err := agentproto.ReadJSONLineLimit(br, &req, agentproto.MaxCallLine); err != nil {
		return agentproto.WriteJSONLine(st, agentproto.Error{Error: err.Error()})
	}
	res, err := g.browserVM().command(req)
	if err != nil {
		return agentproto.WriteJSONLine(st, agentproto.Error{Error: err.Error()})
	}
	return agentproto.WriteJSONLine(st, res)
}

// serveBrowserDownloads answers a browser-downloads stream: the fake browser saves nothing.
func (g *Guest) serveBrowserDownloads(st net.Conn, br *bufio.Reader) error {
	var req agentproto.BrowserDownloadsRequest
	if err := agentproto.ReadJSONLineLimit(br, &req, 64<<10); err != nil {
		return agentproto.WriteJSONLine(st, agentproto.Error{Error: err.Error()})
	}
	if req.Ephemeral < 0 || req.Ephemeral > agentproto.MaxBrowserEphemeral {
		return agentproto.WriteJSONLine(st, agentproto.Error{Error: "bad browser session"})
	}
	if req.Name != "" {
		return agentproto.WriteJSONLine(st, agentproto.Error{Error: "no such download"})
	}
	return agentproto.WriteJSONLine(st, agentproto.BrowserDownloadList{Files: []agentproto.BrowserDownload{}})
}

// browserVM returns the guest's browser, starting one on the first browser stream.
func (g *Guest) browserVM() *browserVM {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.browser == nil {
		g.browser = &browserVM{fetch: g.fetchForwarded}
	}
	return g.browser
}

// serveForwards answers a forwards stream: the fake VM listens nowhere, it only remembers
// the ports, and its pages on them load through fetchForwarded.
func (g *Guest) serveForwards(st net.Conn, br *bufio.Reader) error {
	var req agentproto.Forwards
	if err := agentproto.ReadJSONLineLimit(br, &req, 64<<10); err != nil {
		return agentproto.WriteJSONLine(st, agentproto.Error{Error: err.Error()})
	}
	g.mu.Lock()
	g.forwards = slices.Clone(req.Ports)
	g.mu.Unlock()
	return agentproto.WriteJSONLine(st, struct{}{})
}

// fetchForwarded GETs u over a forward stream, as the real guest relays a connection to a
// forwarded port. ok is false when the port isn't forwarded or the host refuses it.
func (g *Guest) fetchForwarded(u *url.URL) (string, bool) {
	port, err := strconv.Atoi(u.Port())
	if u.Port() == "" {
		port, err = 80, nil
	}
	g.mu.Lock()
	sess, forwarded := g.sess, slices.Contains(g.forwards, port)
	g.mu.Unlock()
	if err != nil || !forwarded || sess == nil {
		return "", false
	}
	st, err := sess.Open()
	if err != nil {
		return "", false
	}
	defer st.Close()
	st.SetDeadline(time.Now().Add(10 * time.Second))
	if agentproto.WriteJSONLine(st, agentproto.Header{Kind: agentproto.KindForward, Port: port}) != nil {
		return "", false
	}
	br := bufio.NewReader(st)
	var e agentproto.Error
	if agentproto.ReadJSONLineLimit(br, &e, 4096) != nil || e.Error != "" {
		return "", false
	}
	if _, err := fmt.Fprintf(st, "GET %s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", u.RequestURI(), u.Host); err != nil {
		return "", false
	}
	res, err := http.ReadResponse(br, nil)
	if err != nil {
		return "", false
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	return string(body), err == nil
}

// screencastTab returns the started session that streams on port, if any.
func (g *Guest) screencastTab(port int) (*browserVM, *tab) {
	g.mu.Lock()
	b := g.browser
	g.mu.Unlock()
	slot := port - agentproto.BrowserStreamPort
	if b == nil || slot < 0 || slot > agentproto.MaxBrowserEphemeral {
		return nil, nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if t := b.tabs[slot]; t != nil {
		return b, t
	}
	return nil, nil
}

// serveScreencast serves a session's screencast WebSocket on one tcp stream.
func (g *Guest) serveScreencast(st net.Conn, br *bufio.Reader, b *browserVM, t *tab) error {
	conn := &streamConn{Conn: st, r: br, closed: make(chan struct{})}
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		b.screencast(r.Context(), c, t)
	})}
	srv.Serve(&oneListener{conn: conn})
	<-conn.closed
	return nil
}

// screencast sends the tab's page whenever it changes, and applies the live view's input,
// until either side ends or the session closes.
func (b *browserVM) screencast(ctx context.Context, c *websocket.Conn, t *tab) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ch := make(chan struct{}, 1)
	b.mu.Lock()
	t.watchers[ch] = struct{}{}
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(t.watchers, ch)
		b.mu.Unlock()
	}()
	go func() {
		defer cancel()
		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			b.mu.Lock()
			var m struct {
				Type string `json:"type"`
			}
			json.Unmarshal(data, &m)
			if strings.HasPrefix(m.Type, "input_") {
				b.inputs = append(b.inputs, json.RawMessage(bytes.Clone(data)))
				if len(b.inputs) > maxInputs {
					b.inputs = b.inputs[len(b.inputs)-maxInputs:]
				}
			}
			if t.input(data) || m.Type == "config" {
				t.changed()
			}
			b.mu.Unlock()
		}
	}()
	seq := int64(0)
	for {
		b.mu.Lock()
		u, shot := t.url(), t.render()
		b.mu.Unlock()
		msg, _ := json.Marshal(map[string]any{"type": "url", "url": u})
		if c.Write(ctx, websocket.MessageText, msg) != nil {
			return
		}
		seq++
		frame, _ := json.Marshal(map[string]any{"type": "frame", "seq": seq, "data": base64.StdEncoding.EncodeToString(shot),
			"metadata": map[string]any{"deviceWidth": viewW, "deviceHeight": viewH, "offsetTop": 0, "pageScaleFactor": 1,
				"scrollOffsetX": 0, "scrollOffsetY": 0, "timestamp": float64(time.Now().UnixMilli()) / 1000}})
		if c.Write(ctx, websocket.MessageText, frame) != nil {
			return
		}
		select {
		case _, ok := <-ch:
			if !ok {
				c.Close(websocket.StatusNormalClosure, "the browser session closed")
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

// BrowserState is what the control API reports about a browser VM's fake browser.
type BrowserState struct {
	Sessions []BrowserSession `json:"sessions"`
	// Inputs are the last mouse and keyboard messages its live views passed on, oldest first.
	Inputs []json.RawMessage `json:"inputs"`
}

// BrowserSession is one started session slot: 0 is the persistent profile.
type BrowserSession struct {
	Slot     int               `json:"slot"`
	URL      string            `json:"url"`
	Title    string            `json:"title"`
	History  []string          `json:"history"`
	At       int               `json:"at"`
	Values   map[string]string `json:"values"`
	Focus    string            `json:"focus,omitempty"`
	Watchers int               `json:"watchers"`
}

// BrowserState reports the guest's browser; ok is false before its first browser stream.
func (g *Guest) BrowserState() (BrowserState, bool) {
	g.mu.Lock()
	b := g.browser
	g.mu.Unlock()
	if b == nil {
		return BrowserState{}, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	out := BrowserState{Sessions: []BrowserSession{}, Inputs: append([]json.RawMessage{}, b.inputs...)}
	for slot, t := range b.tabs {
		if t == nil {
			continue
		}
		values := map[string]string{}
		for k, v := range t.values {
			values[k] = v
		}
		out.Sessions = append(out.Sessions, BrowserSession{Slot: slot, URL: t.url(), Title: t.title(),
			History: append([]string{}, t.history...), At: t.at, Values: values, Focus: t.focus, Watchers: len(t.watchers)})
	}
	return out, true
}

// streamConn is a tcp stream whose first bytes its header reader may have read ahead.
type streamConn struct {
	net.Conn
	r      io.Reader
	once   sync.Once
	closed chan struct{}
}

func (c *streamConn) Read(b []byte) (int, error) { return c.r.Read(b) }

func (c *streamConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

// oneListener hands an http.Server one connection, and ends once that connection closes.
type oneListener struct {
	conn *streamConn
	used bool
}

func (l *oneListener) Accept() (net.Conn, error) {
	if !l.used {
		l.used = true
		return l.conn, nil
	}
	<-l.conn.closed
	return nil, net.ErrClosed
}

func (l *oneListener) Close() error   { return nil }
func (l *oneListener) Addr() net.Addr { return l.conn.LocalAddr() }
