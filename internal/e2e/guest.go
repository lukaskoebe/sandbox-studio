//go:build e2e

package e2e

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	goruntime "runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/lukaskoebe/sandbox-studio/internal/agentchan"
	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/version"
)

// DefaultPorts are the ports a fresh fake guest reports as listening. Each serves a small
// HTTP page over the tcp stream, so previews work.
var DefaultPorts = []int{3000}

// Guest is a fake guest agent. It dials its sandbox's agent socket like studio-agent does
// over vsock, says hello, and serves the host's streams: config, pty (an echoing
// shell), sessions, kill, ports, tcp, home-files, start-session, acp (a scripted ACP agent in
// place of the harness's), and in a browser VM browser and browser-downloads (browser.go). It
// records what the host sent, for the tests to read through the control API, and makes calls
// to Studio as `studio-agent mcp` would. It also pushes to git remotes through the gateway
// (Push).
type Guest struct {
	id  string
	log *slog.Logger

	mu       sync.Mutex
	stop     chan struct{} // closed when the current boot ends
	sess     *yamux.Session
	egress   runtime.Egress
	config   *agentproto.Config
	configs  int
	home     []string
	sessions map[string]*session
	buffers  map[string]string // tmux paste buffers, see runTmux
	ports    []int
	forwards []int // a browser VM's forwarded ports
	conns    []*Connection
	calls    []*Call
	browser  *browserVM // the browser of this boot, once Studio sent a browser stream
	acp      []*acpProcess
}

type session struct {
	agentproto.Session
	ptys   map[*ptyStream]struct{}
	pasted string // what tmux pasted into it, shown to every terminal that attaches
}

// Connection is an outbound HTTP request the guest made through the gateway.
type Connection struct {
	Host   string `json:"host"`
	Port   int    `json:"port"`
	Done   bool   `json:"done"`
	Status int    `json:"status,omitempty"`
	Body   string `json:"body,omitempty"`
	Error  string `json:"error,omitempty"`
}

func newGuest(id string, log *slog.Logger) *Guest {
	return &Guest{id: id, log: log, ports: slices.Clone(DefaultPorts)}
}

// boot starts the guest's connection loop on socket.
func (g *Guest) boot(socket string, egress runtime.Egress) {
	g.shutdown()
	g.mu.Lock()
	defer g.mu.Unlock()
	stop := make(chan struct{})
	g.stop, g.egress, g.sessions, g.browser = stop, egress, map[string]*session{}, nil
	go g.run(socket, stop)
}

// shutdown disconnects the guest, as powering off its VM would.
func (g *Guest) shutdown() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stop != nil {
		close(g.stop)
		g.stop = nil
	}
	if g.sess != nil {
		g.sess.Close()
		g.sess = nil
	}
}

// run connects until stop is closed, reconnecting like the real agent when the host
// drops the session.
func (g *Guest) run(socket string, stop chan struct{}) {
	for {
		select {
		case <-stop:
			return
		default:
		}
		if err := g.serveOnce(socket, stop); err != nil && g.log != nil {
			g.log.Debug("e2e guest disconnected", "sandbox", g.id, "err", err)
		}
		select {
		case <-stop:
			return
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (g *Guest) serveOnce(socket string, stop chan struct{}) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	conn, err := agentchan.Dial(ctx, socket)
	cancel()
	if err != nil {
		return err
	}
	sess, err := yamux.Client(conn, yamux.DefaultConfig())
	if err != nil {
		conn.Close()
		return err
	}
	defer sess.Close()
	g.mu.Lock()
	select {
	case <-stop:
		g.mu.Unlock()
		return nil
	default:
	}
	g.sess = sess
	g.mu.Unlock()

	st, err := sess.Open()
	if err != nil {
		return err
	}
	host, _ := os.Hostname()
	agentproto.WriteJSONLine(st, agentproto.Header{Kind: agentproto.KindHello})
	agentproto.WriteJSONLine(st, agentproto.Hello{Version: version.Version, Arch: goruntime.GOARCH, Hostname: host})
	st.Close()
	for {
		st, err := sess.Accept()
		if err != nil {
			return err
		}
		go g.handle(st)
	}
}

func (g *Guest) handle(st net.Conn) {
	defer st.Close()
	br := bufio.NewReader(st)
	var h agentproto.Header
	if err := agentproto.ReadJSONLine(br, &h); err != nil {
		return
	}
	var err error
	switch h.Kind {
	case agentproto.KindConfig:
		var cfg agentproto.Config
		if err = agentproto.ReadJSONLine(br, &cfg); err == nil {
			g.mu.Lock()
			g.config = &cfg
			g.configs++
			g.mu.Unlock()
			err = agentproto.WriteJSONLine(st, struct{}{})
		}
	case agentproto.KindHomeFiles:
		var files agentproto.HomeFiles
		if err = agentproto.ReadJSONLineLimit(br, &files, agentproto.MaxHomeFilesLine); err == nil {
			g.mu.Lock()
			for _, f := range files.Files {
				g.home = append(g.home, f.Path)
			}
			g.mu.Unlock()
			err = agentproto.WriteJSONLine(st, struct{}{})
		}
	case agentproto.KindStartSession:
		var req agentproto.StartSession
		if err = agentproto.ReadJSONLine(br, &req); err == nil {
			g.mu.Lock()
			s := g.sessionLocked(req.Name)
			s.Harness, s.Chat = req.Harness, req.Chat
			g.mu.Unlock()
			err = agentproto.WriteJSONLine(st, struct{}{})
		}
	case agentproto.KindSessions:
		err = agentproto.WriteJSONLine(st, g.Sessions())
	case agentproto.KindKill:
		g.kill(h.Session)
		err = agentproto.WriteJSONLine(st, struct{}{})
	case agentproto.KindPorts:
		g.mu.Lock()
		ports := make([]agentproto.Port, 0, len(g.ports))
		for _, p := range g.ports {
			ports = append(ports, agentproto.Port{Port: p})
		}
		g.mu.Unlock()
		err = agentproto.WriteJSONLine(st, ports)
	case agentproto.KindPTY:
		err = g.servePTY(st, br, h.Session)
	case agentproto.KindTCP:
		if b, t := g.screencastTab(h.Port); t != nil {
			err = g.serveScreencast(st, br, b, t)
		} else {
			err = g.serveTCP(st, br, h.Port)
		}
	case agentproto.KindBrowser:
		err = g.serveBrowser(st, br)
	case agentproto.KindBrowserDownloads:
		err = g.serveBrowserDownloads(st, br)
	case agentproto.KindForwards:
		err = g.serveForwards(st, br)
	case agentproto.KindACP:
		err = g.serveACP(st, br)
	case agentproto.KindPaste:
		var req agentproto.UploadRequest
		if err = agentproto.ReadJSONLine(br, &req); err == nil {
			if _, err = io.CopyN(io.Discard, br, req.Size); err == nil {
				err = agentproto.WriteJSONLine(st, agentproto.UploadReply{Name: req.Name, Size: req.Size})
			}
		}
	case agentproto.KindRun:
		var req agentproto.RunRequest
		if err = agentproto.ReadJSONLineLimit(br, &req, agentproto.MaxRunLine); err == nil {
			err = agentproto.WriteJSONLine(st, g.runTmux(req))
		}
	default:
		err = agentproto.WriteJSONLine(st, agentproto.Error{Error: "the e2e guest does not support " + h.Kind + " streams"})
	}
	if err != nil && !errors.Is(err, io.EOF) && g.log != nil {
		g.log.Debug("e2e guest stream", "sandbox", g.id, "kind", h.Kind, "err", err)
	}
}

// runTmux fakes the tmux commands Studio uses to type into an agent's terminal
// (sandboxes.Manager.SendToAgent): a paste and an Enter show on the session's terminals.
func (g *Guest) runTmux(req agentproto.RunRequest) agentproto.RunResult {
	a := req.Args
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.buffers == nil {
		g.buffers = map[string]string{}
	}
	show := func(name, text string) agentproto.RunResult {
		s, ok := g.sessions[name]
		if !ok {
			return agentproto.RunResult{ExitCode: 1, Stderr: "can't find session: " + name}
		}
		text = strings.ReplaceAll(text, "\n", "\r\n")
		s.pasted += text
		for p := range s.ptys {
			p.write(text)
		}
		return agentproto.RunResult{}
	}
	switch {
	case len(a) == 5 && a[0] == "tmux" && a[1] == "load-buffer" && a[4] == "-":
		g.buffers[a[3]] = req.Stdin
		return agentproto.RunResult{}
	case len(a) == 8 && a[0] == "tmux" && a[1] == "paste-buffer":
		text := g.buffers[a[5]]
		delete(g.buffers, a[5])
		return show(a[7], text)
	case len(a) == 5 && a[0] == "tmux" && a[1] == "send-keys" && a[4] == "Enter":
		return show(a[3], "\n")
	}
	return agentproto.RunResult{ExitCode: 127, Stderr: "the e2e guest doesn't run " + strings.Join(a, " ")}
}

func (g *Guest) sessionLocked(name string) *session {
	s, ok := g.sessions[name]
	if !ok {
		s = &session{Session: agentproto.Session{Name: name, Windows: 1, Created: time.Now().Unix()}, ptys: map[*ptyStream]struct{}{}}
		g.sessions[name] = s
	}
	return s
}

// Sessions lists the guest's terminal sessions, oldest first.
func (g *Guest) Sessions() []agentproto.Session {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]agentproto.Session, 0, len(g.sessions))
	for _, s := range g.sessions {
		v := s.Session
		v.Attached = len(s.ptys)
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Created != out[j].Created {
			return out[i].Created < out[j].Created
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// kill ends a session; its attached terminals see the shell exit.
func (g *Guest) kill(name string) {
	g.mu.Lock()
	var ptys []*ptyStream
	if s, ok := g.sessions[name]; ok {
		for p := range s.ptys {
			ptys = append(ptys, p)
		}
	}
	delete(g.sessions, name)
	g.mu.Unlock()
	for _, p := range ptys {
		p.exit()
	}
}

type ptyStream struct {
	mu sync.Mutex
	st net.Conn
}

func (p *ptyStream) write(s string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	agentproto.WriteFrame(p.st, agentproto.FrameData, []byte(s))
}

func (p *ptyStream) exit() {
	p.mu.Lock()
	defer p.mu.Unlock()
	agentproto.WriteFrame(p.st, agentproto.FrameExit, []byte("0"))
	p.st.Close()
}

const prompt = "agent@sandbox:/workspace$ "

// servePTY runs a line-based fake shell: it echoes input, and answers echo, printf, printenv
// and exit. Secret placeholders from the last config are its environment, as with the real
// agent's profile hook.
func (g *Guest) servePTY(st net.Conn, br *bufio.Reader, name string) error {
	if name == "" {
		name = "main"
	}
	p := &ptyStream{st: st}
	g.mu.Lock()
	s := g.sessionLocked(name)
	s.ptys[p] = struct{}{}
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		delete(s.ptys, p)
		g.mu.Unlock()
	}()
	p.write(prompt + s.pasted)
	var line []byte
	for {
		typ, payload, err := agentproto.ReadFrame(br)
		if err != nil {
			return err
		}
		if typ != agentproto.FrameData {
			continue
		}
		for _, b := range payload {
			switch {
			case b == '\r' || b == '\n':
				p.write("\r\n")
				out, exit := g.shell(string(line))
				line = line[:0]
				if exit {
					g.kill(name)
					return nil
				}
				p.write(out + prompt)
			case b == 0x7f || b == 0x08:
				if len(line) > 0 {
					line = line[:len(line)-1]
					p.write("\b \b")
				}
			case b == 0x03:
				line = line[:0]
				p.write("^C\r\n" + prompt)
			case b >= 0x20:
				line = append(line, b)
				p.write(string([]byte{b}))
			}
		}
	}
}

func (g *Guest) shell(line string) (out string, exit bool) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "", false
	}
	env := g.Env()
	switch fields[0] {
	case "exit":
		return "", true
	case "echo":
		args := fields[1:]
		for i, a := range args {
			if v, ok := strings.CutPrefix(a, "$"); ok {
				args[i] = env[v]
			}
		}
		return strings.Join(args, " ") + "\r\n", false
	case "printf":
		// The escapes a terminal test needs, such as an OSC 52 copy: printf '\e]52;;aGk=\a'.
		arg := strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "printf")), "'\"")
		return strings.NewReplacer(`\e`, "\x1b", `\a`, "\a", `\n`, "\r\n").Replace(arg), false
	case "printenv":
		if len(fields) > 1 {
			if v, ok := env[fields[1]]; ok {
				return v + "\r\n", false
			}
			return "", false
		}
		keys := make([]string, 0, len(env))
		for k := range env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			out += k + "=" + env[k] + "\r\n"
		}
		return out, false
	}
	return fields[0] + ": command not found\r\n", false
}

// Env is the environment the guest's last config set: secret names and placeholders.
func (g *Guest) Env() map[string]string {
	g.mu.Lock()
	defer g.mu.Unlock()
	env := map[string]string{}
	if g.config != nil {
		for k, v := range g.config.Env {
			env[k] = v
		}
	}
	return env
}

// A page on the fake dev server that registers a service worker, which answers every
// navigation from its cache, as an offline-first app does.
const (
	pwaPage   = `<!doctype html><title>PWA</title><p id="app">PWA</p><script>navigator.serviceWorker.register("/pwa-sw.js")</script>`
	pwaWorker = `self.addEventListener("install",()=>self.skipWaiting());self.addEventListener("activate",e=>e.waitUntil(clients.claim()));self.addEventListener("fetch",e=>{if(e.request.mode==="navigate")e.respondWith(new Response("<p>cached app</p>",{headers:{"Content-Type":"text/html"}}))})`
)

// serveTCP answers HTTP on the listening ports, as a dev server would: plain text, except for
// /pwa and its service worker.
func (g *Guest) serveTCP(st net.Conn, br *bufio.Reader, port int) error {
	g.mu.Lock()
	listening := slices.Contains(g.ports, port)
	g.mu.Unlock()
	if !listening {
		return nil
	}
	for {
		req, err := http.ReadRequest(br)
		if err != nil {
			return err
		}
		io.Copy(io.Discard, req.Body)
		body, typ := fmt.Sprintf("Preview of sandbox %s on port %d, path %s\n", g.id, port, req.URL.Path), "text/plain"
		switch req.URL.Path {
		case "/pwa":
			body, typ = pwaPage, "text/html"
		case "/pwa-sw.js":
			body, typ = pwaWorker, "text/javascript"
		}
		if _, err := fmt.Fprintf(st, "HTTP/1.1 200 OK\r\nContent-Type: %s; charset=utf-8\r\nContent-Length: %d\r\n\r\n%s", typ, len(body), body); err != nil {
			return err
		}
		if req.Close {
			return nil
		}
	}
}

// SetPorts replaces the ports the guest reports as listening.
func (g *Guest) SetPorts(ports []int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ports = slices.Clone(ports)
}

// Connect makes an HTTP request to host:port through the gateway in the background, like
// a process in the sandbox would. The gateway holds it while the user decides.
func (g *Guest) Connect(host string, port int) {
	c := &Connection{Host: host, Port: port}
	g.mu.Lock()
	g.conns = append(g.conns, c)
	egress := g.egress
	g.mu.Unlock()
	go func() {
		status, body, err := request(egress, host, port)
		g.mu.Lock()
		defer g.mu.Unlock()
		c.Done, c.Status, c.Body = true, status, body
		if err != nil {
			c.Error = err.Error()
		}
	}()
}

// Call is a call the guest made to Studio, as `studio-agent mcp` forwards a tool call: the
// host answers it for the sandbox whose channel it came on.
type Call struct {
	ID     int             `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
	Done   bool            `json:"done"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// callTimeout is longer than Studio holds a call for an approval.
const callTimeout = 3 * time.Minute

// Call makes a call to Studio in the background. done is closed once it is answered.
func (g *Guest) Call(method string, params json.RawMessage) (id int, done <-chan struct{}) {
	g.mu.Lock()
	c := &Call{ID: len(g.calls), Method: method, Params: params}
	g.calls = append(g.calls, c)
	sess := g.sess
	g.mu.Unlock()
	ch := make(chan struct{})
	go func() {
		defer close(ch)
		reply, err := call(sess, agentproto.Call{Method: method, Params: params})
		g.mu.Lock()
		defer g.mu.Unlock()
		c.Done, c.Result, c.Error = true, reply.Result, reply.Error
		if err != nil {
			c.Error = err.Error()
		}
	}()
	return c.ID, ch
}

// CallState returns a call the guest made.
func (g *Guest) CallState(id int) (Call, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if id < 0 || id >= len(g.calls) {
		return Call{}, false
	}
	return *g.calls[id], true
}

// call sends one call over the guest's channel and reads Studio's reply.
func call(sess *yamux.Session, c agentproto.Call) (agentproto.CallReply, error) {
	var reply agentproto.CallReply
	if sess == nil || sess.IsClosed() {
		return reply, errors.New("not connected to Studio")
	}
	st, err := sess.Open()
	if err != nil {
		return reply, err
	}
	defer st.Close()
	st.SetDeadline(time.Now().Add(callTimeout))
	if err := agentproto.WriteJSONLine(st, agentproto.Header{Kind: agentproto.KindCall}); err != nil {
		return reply, err
	}
	if err := agentproto.WriteJSONLine(st, c); err != nil {
		return reply, err
	}
	err = agentproto.ReadJSONLineLimit(bufio.NewReader(st), &reply, agentproto.MaxCallLine)
	return reply, err
}

// request speaks SOCKS5 with the sandbox's gateway credentials, then HTTP/1.1.
func request(egress runtime.Egress, host string, port int) (int, string, error) {
	if egress.Proxy == "" {
		return 0, "", errors.New("the sandbox has no gateway")
	}
	c, err := net.DialTimeout("tcp", egress.Proxy, 5*time.Second)
	if err != nil {
		return 0, "", err
	}
	defer c.Close()
	// Longer than the gateway holds a connection for a decision.
	c.SetDeadline(time.Now().Add(90 * time.Second))
	user, pass := egress.User, os.Getenv(egress.PasswordEnv)
	if len(host) > 255 || len(user) > 255 || len(pass) > 255 {
		return 0, "", errors.New("name too long for SOCKS5")
	}
	msg := []byte{5, 1, 2, 1, byte(len(user))}
	msg = append(msg, user...)
	msg = append(msg, byte(len(pass)))
	msg = append(msg, pass...)
	msg = append(msg, 5, 1, 0, 3, byte(len(host)))
	msg = append(msg, host...)
	msg = binary.BigEndian.AppendUint16(msg, uint16(port))
	if _, err := c.Write(msg); err != nil {
		return 0, "", err
	}
	reply := make([]byte, 2+2+10)
	if _, err := io.ReadFull(c, reply); err != nil {
		return 0, "", fmt.Errorf("SOCKS5 handshake: %w", err)
	}
	if reply[1] != 2 || reply[3] != 0 || reply[5] != 0 {
		return 0, "", fmt.Errorf("SOCKS5 refused: % x", reply)
	}
	if _, err := fmt.Fprintf(c, "GET / HTTP/1.1\r\nHost: %s\r\nUser-Agent: e2e-guest\r\nConnection: close\r\n\r\n", host); err != nil {
		return 0, "", err
	}
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return resp.StatusCode, string(body), err
}

// State is what the control API reports about a guest.
type State struct {
	Connected  bool                 `json:"connected"`
	Configs    int                  `json:"configs"`
	Env        map[string]string    `json:"env"`
	CA         bool                 `json:"ca"`
	HomeFiles  []string             `json:"homeFiles"`
	Sessions   []agentproto.Session `json:"sessions"`
	Ports      []int                `json:"ports"`
	Connection []Connection         `json:"connections"`
	Calls      []Call               `json:"calls"`
	ACP        []ACPAgent           `json:"acp"`
	// Browser is the fake browser of a browser VM, once Studio drove it.
	Browser *BrowserState `json:"browser,omitempty"`
}

// State reports the guest's recorded state.
func (g *Guest) State() State {
	sessions := g.Sessions()
	env := g.Env()
	var browser *BrowserState
	if b, ok := g.BrowserState(); ok {
		browser = &b
	}
	agents := g.acpState()
	g.mu.Lock()
	defer g.mu.Unlock()
	s := State{
		Connected: g.sess != nil && !g.sess.IsClosed(), Configs: g.configs, Env: env,
		CA: g.config != nil && strings.Contains(g.config.CA, "BEGIN CERTIFICATE"), HomeFiles: append([]string{}, g.home...),
		Sessions: sessions, Ports: slices.Clone(g.ports), Connection: []Connection{}, Calls: []Call{}, ACP: agents, Browser: browser,
	}
	for _, c := range g.conns {
		s.Connection = append(s.Connection, *c)
	}
	for _, c := range g.calls {
		s.Calls = append(s.Calls, *c)
	}
	return s
}
