//go:build e2e

package e2e

import (
	"bufio"
	"context"
	"encoding/binary"
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
// shell), sessions, kill, ports, tcp, home-files and start-session. It records what the
// host sent, for the tests to read through the control API.
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
	ports    []int
	conns    []*Connection
}

type session struct {
	agentproto.Session
	ptys map[*ptyStream]struct{}
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
	g.stop, g.egress, g.sessions = stop, egress, map[string]*session{}
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
		err = g.serveTCP(st, br, h.Port)
	default:
		err = agentproto.WriteJSONLine(st, agentproto.Error{Error: "the e2e guest does not support " + h.Kind + " streams"})
	}
	if err != nil && !errors.Is(err, io.EOF) && g.log != nil {
		g.log.Debug("e2e guest stream", "sandbox", g.id, "kind", h.Kind, "err", err)
	}
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

// servePTY runs a line-based fake shell: it echoes input, and answers echo, printenv and
// exit. Secret placeholders from the last config are its environment, as with the real
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
	p.write(prompt)
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

// serveTCP answers HTTP on the listening ports, as a dev server would.
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
		body := fmt.Sprintf("Preview of sandbox %s on port %d, path %s\n", g.id, port, req.URL.Path)
		if _, err := fmt.Fprintf(st, "HTTP/1.1 200 OK\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Length: %d\r\n\r\n%s", len(body), body); err != nil {
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
}

// State reports the guest's recorded state.
func (g *Guest) State() State {
	sessions := g.Sessions()
	env := g.Env()
	g.mu.Lock()
	defer g.mu.Unlock()
	s := State{
		Connected: g.sess != nil && !g.sess.IsClosed(), Configs: g.configs, Env: env,
		CA: g.config != nil && strings.Contains(g.config.CA, "BEGIN CERTIFICATE"), HomeFiles: append([]string{}, g.home...),
		Sessions: sessions, Ports: slices.Clone(g.ports), Connection: []Connection{},
	}
	for _, c := range g.conns {
		s.Connection = append(s.Connection, *c)
	}
	return s
}
