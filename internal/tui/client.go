// Package tui is the terminal client of a running Studio (docs/tui.md). It is a client of the
// HTTP API the web UI uses: it adds no endpoints, and it signs in as the local owner.
package tui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
	"github.com/lukaskoebe/sandbox-studio/internal/api"
	"github.com/lukaskoebe/sandbox-studio/internal/browser"
	"github.com/lukaskoebe/sandbox-studio/internal/chat"
	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/gateway"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/webauth"
)

// ErrNotRunning means no Studio answers at the address the TUI was started for.
var ErrNotRunning = errors.New("Studio is not running")

// maxAnswer bounds what the client reads from one answer.
const maxAnswer = 16 << 20

// APIError is an answer that is not a success. Detail is the API's problem detail when it
// has one, else the status text.
type APIError struct {
	Status int
	Detail string
}

func (e *APIError) Error() string { return e.Detail }

// Client is a session with one running Studio. Connect opens it and Close ends it. The
// session is a Studio session cookie that only this client holds: it is not a browser's.
type Client struct {
	addr    string
	session string // the value of the session cookie
	http    *http.Client
}

// Connect signs in to the Studio at addr as the local owner. control is the control credential
// (webauth.ControlToken), which only a process that can read the catalog has. Studio answers
// with a one-time login ticket, and the ticket is exchanged for the session, as the web login
// does. Connect fails with ErrNotRunning when no Studio answers at addr.
func Connect(ctx context.Context, addr, control string) (*Client, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return nil, errors.New("Studio's clients connect to a loopback address only")
	}
	c := &Client{
		addr: addr,
		http: &http.Client{
			Transport:     &http.Transport{Proxy: nil},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
	if !c.running(ctx) {
		return nil, fmt.Errorf("%w at %s. Start it with `studio`, then try again", ErrNotRunning, addr)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var ticket struct {
		Token string `json:"token"`
	}
	if _, err := c.send(ctx, http.MethodPost, "/api/auth/launch", http.Header{"Authorization": {"Bearer " + control}}, nil, &ticket); err != nil {
		return nil, fmt.Errorf("Studio refused the sign-in (run it as the user Studio runs as): %w", err)
	}
	resp, err := c.send(ctx, http.MethodPost, "/api/auth/exchange", nil, map[string]string{"token": ticket.Token}, nil)
	if err != nil {
		return nil, fmt.Errorf("Studio refused the session: %w", err)
	}
	for _, cookie := range resp.Cookies() {
		if cookie.Name == webauth.SessionCookie {
			c.session = cookie.Value
		}
	}
	if c.session == "" {
		return nil, errors.New("Studio did not start a session")
	}
	return c, nil
}

// running reports whether a Studio answers at the client's address.
func (c *Client) running(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var status struct {
		Authenticated *bool `json:"authenticated"`
	}
	_, err := c.send(ctx, http.MethodGet, "/api/auth/status", nil, nil, &status)
	return err == nil && status.Authenticated != nil
}

// Close ends the session. Studio clears the cookie; the client keeps nothing else.
func (c *Client) Close(ctx context.Context) error {
	_, err := c.send(ctx, http.MethodPost, "/api/auth/logout", nil, nil, nil)
	c.session = ""
	return err
}

// Environments lists the environments of the installation.
func (c *Client) Environments(ctx context.Context) ([]store.Environment, error) {
	var out []store.Environment
	_, err := c.send(ctx, http.MethodGet, "/api/environments", nil, nil, &out)
	return out, err
}

// Sandboxes lists an environment's sandboxes with their live state.
func (c *Client) Sandboxes(ctx context.Context, env string) ([]sandboxes.View, error) {
	var out []sandboxes.View
	_, err := c.send(ctx, http.MethodGet, "/api/environments/"+env+"/sandboxes", nil, nil, &out)
	return out, err
}

// Teammates lists an environment's teammates, whose names the sandboxes show.
func (c *Client) Teammates(ctx context.Context, env string) ([]store.Teammate, error) {
	var out []store.Teammate
	_, err := c.send(ctx, http.MethodGet, "/api/environments/"+env+"/teammates", nil, nil, &out)
	return out, err
}

// PendingApprovals lists the pending approvals of every environment, newest first.
func (c *Client) PendingApprovals(ctx context.Context) ([]api.ApprovalView, error) {
	var out []api.ApprovalView
	_, err := c.send(ctx, http.MethodGet, "/api/approvals?status=pending", nil, nil, &out)
	return out, err
}

// Lifecycle starts, stops, suspends or resumes a sandbox and returns it as it is then. A start or
// resume returns when the VM is up, which can take a while.
func (c *Client) Lifecycle(ctx context.Context, env, id, action string) (sandboxes.View, error) {
	var out sandboxes.View
	_, err := c.send(ctx, http.MethodPost, "/api/environments/"+env+"/sandboxes/"+id+"/"+action, nil, nil, &out)
	return out, err
}

// DeleteSandbox deletes a sandbox and its disks.
func (c *Client) DeleteSandbox(ctx context.Context, env, id string) error {
	_, err := c.send(ctx, http.MethodDelete, "/api/environments/"+env+"/sandboxes/"+id, nil, nil, nil)
	return err
}

// Decide answers an approval: a rule or one connection for a network request, or ignoring it.
func (c *Client) Decide(ctx context.Context, env, id string, d Decision) (api.ApprovalView, error) {
	var out api.ApprovalView
	_, err := c.send(ctx, http.MethodPost, "/api/environments/"+env+"/approvals/"+id+"/decide", nil, d, &out)
	return out, err
}

// Fork copies a sandbox into a new one named name. It returns at once, with the new sandbox in
// state creating; the copy and the boot go on in Studio, as a create's do.
func (c *Client) Fork(ctx context.Context, env, id, name string) (sandboxes.View, error) {
	var out sandboxes.View
	_, err := c.send(ctx, http.MethodPost, "/api/environments/"+env+"/sandboxes/"+id+"/fork", nil, map[string]string{"name": name}, &out)
	return out, err
}

// Checkpoints lists a sandbox's checkpoints.
func (c *Client) Checkpoints(ctx context.Context, env, sandbox string) ([]store.Checkpoint, error) {
	var out []store.Checkpoint
	_, err := c.send(ctx, http.MethodGet, "/api/environments/"+env+"/sandboxes/"+sandbox+"/checkpoints", nil, nil, &out)
	return out, err
}

// CreateCheckpoint saves a checkpoint of a sandbox's disks. It returns once they are captured.
func (c *Client) CreateCheckpoint(ctx context.Context, env, sandbox, name string) (store.Checkpoint, error) {
	var out store.Checkpoint
	_, err := c.send(ctx, http.MethodPost, "/api/environments/"+env+"/sandboxes/"+sandbox+"/checkpoints", nil, map[string]string{"name": name}, &out)
	return out, err
}

// RestoreCheckpoint puts a stopped sandbox's disks back to a checkpoint.
func (c *Client) RestoreCheckpoint(ctx context.Context, env, sandbox, checkpoint string) (sandboxes.View, error) {
	var out sandboxes.View
	_, err := c.send(ctx, http.MethodPost, "/api/environments/"+env+"/sandboxes/"+sandbox+"/checkpoints/"+checkpoint+"/restore", nil, nil, &out)
	return out, err
}

// Ports lists the ports a running sandbox listens on, with their preview addresses.
func (c *Client) Ports(ctx context.Context, env, sandbox string) ([]api.PreviewPort, error) {
	var out []api.PreviewPort
	_, err := c.send(ctx, http.MethodGet, "/api/environments/"+env+"/sandboxes/"+sandbox+"/ports", nil, nil, &out)
	return out, err
}

// Terminals lists a running sandbox's terminals: its tmux sessions.
func (c *Client) Terminals(ctx context.Context, env, sandbox string) ([]agentproto.Session, error) {
	var out []agentproto.Session
	_, err := c.send(ctx, http.MethodGet, "/api/environments/"+env+"/sandboxes/"+sandbox+"/terminals", nil, nil, &out)
	return out, err
}

// StartSession starts the sandbox teammate's harness in a new terminal, which it names.
func (c *Client) StartSession(ctx context.Context, env, sandbox string) (agentproto.Session, error) {
	var out agentproto.Session
	_, err := c.send(ctx, http.MethodPost, "/api/environments/"+env+"/sandboxes/"+sandbox+"/sessions", nil, nil, &out)
	return out, err
}

// Usage reads a running sandbox's CPU and memory use, as the workspace's status line does. A
// runtime that reports none answers 501, a sandbox that doesn't run 409.
func (c *Client) Usage(ctx context.Context, env, sandbox string) (sandboxes.ResourceUse, error) {
	var out sandboxes.ResourceUse
	_, err := c.send(ctx, http.MethodGet, "/api/environments/"+env+"/sandboxes/"+sandbox+"/usage", nil, nil, &out)
	return out, err
}

// Lineage reads a sandbox's history: its events, its forks and its checkpoints.
func (c *Client) Lineage(ctx context.Context, env, sandbox string) (sandboxes.Lineage, error) {
	var out sandboxes.Lineage
	_, err := c.send(ctx, http.MethodGet, "/api/environments/"+env+"/sandboxes/"+sandbox+"/lineage", nil, nil, &out)
	return out, err
}

// ResolveConflict settles a memory conflict: keep_a or keep_b retracts the other fact. Studio
// closes the conflict's approval with it.
func (c *Client) ResolveConflict(ctx context.Context, env, id, resolution string) error {
	_, err := c.send(ctx, http.MethodPost, "/api/environments/"+env+"/memory/conflicts/"+id+"/resolve", nil, map[string]string{"resolution": resolution}, nil)
	return err
}

// Answer is the body of an answer to a chat's question: accept with the form's values, or
// decline.
type Answer struct {
	Action  string `json:"action"`
	Content any    `json:"content,omitempty"`
}

// AnswerQuestion answers a question the agent of a chat waits on. Studio answers 409 once the
// agent no longer waits.
func (c *Client) AnswerQuestion(ctx context.Context, env, chatID, item string, a Answer) error {
	_, err := c.send(ctx, http.MethodPost, "/api/environments/"+env+"/chats/"+chatID+"/items/"+item+"/answer", nil, a, nil)
	return err
}

// Templates lists an environment's ready templates, which a new sandbox can start from.
func (c *Client) Templates(ctx context.Context, env string) ([]api.TemplateView, error) {
	var out []api.TemplateView
	_, err := c.send(ctx, http.MethodGet, "/api/environments/"+env+"/templates", nil, nil, &out)
	return out, err
}

// CreateSandbox creates a sandbox for a teammate ("" for none) from a template ("" for the base
// image). It returns at once, with the sandbox in state creating; the create goes on in Studio,
// and the sandboxes topic of the event stream announces each step.
func (c *Client) CreateSandbox(ctx context.Context, env, name, teammate, template string) (sandboxes.View, error) {
	path := "/api/environments/" + env + "/sandboxes"
	var body any = sandboxes.CreateRequest{Name: name, TeammateID: teammate}
	if template != "" {
		path = "/api/environments/" + env + "/templates/" + template + "/sandboxes"
		body = map[string]string{"name": name, "teammateId": teammate}
	}
	var out sandboxes.View
	_, err := c.send(ctx, http.MethodPost, path, nil, body, &out)
	return out, err
}

// SandboxChats lists the chats of a sandbox.
func (c *Client) SandboxChats(ctx context.Context, env, sandbox string) ([]store.Chat, error) {
	var out []store.Chat
	_, err := c.send(ctx, http.MethodGet, "/api/environments/"+env+"/sandboxes/"+sandbox+"/chats", nil, nil, &out)
	return out, err
}

// Chat reads a chat with its turns and items. Its stream follows on from the chat's rev.
func (c *Client) Chat(ctx context.Context, env, id string) (chat.Log, error) {
	var out chat.Log
	_, err := c.send(ctx, http.MethodGet, "/api/environments/"+env+"/chats/"+id, nil, nil, &out)
	return out, err
}

// prompt is the body of a prompt: the TUI sends text only.
type prompt struct {
	Text string `json:"text"`
}

// CreateChat starts a chat with the sandbox's teammate and sends it the first prompt.
func (c *Client) CreateChat(ctx context.Context, env, sandbox, text string) (store.Chat, error) {
	var out store.Chat
	_, err := c.send(ctx, http.MethodPost, "/api/environments/"+env+"/sandboxes/"+sandbox+"/chats", nil, prompt{Text: text}, &out)
	return out, err
}

// SendPrompt starts a turn of a chat. Studio refuses it while a turn runs.
func (c *Client) SendPrompt(ctx context.Context, env, id, text string) (chat.Turn, error) {
	var out chat.Turn
	_, err := c.send(ctx, http.MethodPost, "/api/environments/"+env+"/chats/"+id+"/prompt", nil, prompt{Text: text}, &out)
	return out, err
}

// CancelChat stops the chat's running turn, if any. The turn ends on the stream.
func (c *Client) CancelChat(ctx context.Context, env, id string) error {
	_, err := c.send(ctx, http.MethodPost, "/api/environments/"+env+"/chats/"+id+"/cancel", nil, nil, nil)
	return err
}

// Approvals lists an environment's approvals of every status, newest first.
func (c *Client) Approvals(ctx context.Context, env string) ([]api.ApprovalView, error) {
	var out []api.ApprovalView
	_, err := c.send(ctx, http.MethodGet, "/api/environments/"+env+"/approvals?status=all", nil, nil, &out)
	return out, err
}

// Rules lists an environment's network rules.
func (c *Client) Rules(ctx context.Context, env string) ([]store.Rule, error) {
	var out []store.Rule
	_, err := c.send(ctx, http.MethodGet, "/api/environments/"+env+"/rules", nil, nil, &out)
	return out, err
}

// Connections lists a sandbox's most recent connections, newest first. Studio keeps them in
// memory only.
func (c *Client) Connections(ctx context.Context, env, sandbox string) ([]gateway.Conn, error) {
	var out []gateway.Conn
	_, err := c.send(ctx, http.MethodGet, "/api/environments/"+env+"/sandboxes/"+sandbox+"/connections", nil, nil, &out)
	return out, err
}

// Browser reports a teammate's browser: its VM's state and the page it is on.
func (c *Client) Browser(ctx context.Context, env, teammate string) (browser.BrowserStatus, error) {
	var out browser.BrowserStatus
	_, err := c.send(ctx, http.MethodGet, "/api/environments/"+env+"/teammates/"+teammate+"/browser", nil, nil, &out)
	return out, err
}

// BrowserFrame is the latest picture of the page a teammate's browser shows, as a JPEG.
func (c *Client) BrowserFrame(ctx context.Context, env, teammate string) ([]byte, error) {
	var out []byte
	_, err := c.send(ctx, http.MethodGet, "/api/environments/"+env+"/teammates/"+teammate+"/browser/frame", nil, nil, &out)
	return out, err
}

// Do makes one API call for clients other than the TUI, such as the command line: in, when not
// nil, is sent as JSON, and a success answer is decoded into out when out is not nil.
func (c *Client) Do(ctx context.Context, method, path string, in, out any) error {
	_, err := c.send(ctx, method, path, nil, in, out)
	return err
}

// Subscribe opens the change notifications (/api/events). The channel closes when the stream
// ends for any reason, and the caller then reconnects and refetches what it shows.
func (c *Client) Subscribe(ctx context.Context) (<-chan events.Event, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+c.addr+"/api/events", nil)
	if err != nil {
		return nil, err
	}
	c.authorize(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, &APIError{Status: resp.StatusCode, Detail: http.StatusText(resp.StatusCode)}
	}
	ch := make(chan events.Event)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			data, ok := strings.CutPrefix(scanner.Text(), "data: ")
			if !ok {
				continue // keep-alive comments and the retry hint
			}
			var ev events.Event
			if json.Unmarshal([]byte(data), &ev) != nil {
				continue
			}
			select {
			case ch <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

// ChatEvent is one change on a chat's stream: the chat, a turn or an item, at a rev. Exactly
// one of Chat, Turn and Item is set.
type ChatEvent struct {
	Rev  int64
	Chat *store.Chat
	Turn *chat.Turn
	Item *chat.Item
}

// maxChatEvent bounds one event of a chat's stream: an item holds at most a few hundred KiB.
const maxChatEvent = 4 << 20

// ChatStream follows a chat from a rev (/api/environments/{env}/chats/{chat}/stream): every turn
// and item written after it, then each change as it happens. The channel closes when the stream
// ends for any reason; the caller reopens it from the last rev it saw, so nothing is missed. A
// deleted chat fails with a 404 APIError.
func (c *Client) ChatStream(ctx context.Context, env, id string, after int64) (<-chan ChatEvent, error) {
	url := fmt.Sprintf("http://%s/api/environments/%s/chats/%s/stream?after=%d", c.addr, env, id, after)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	c.authorize(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, apiError(resp.StatusCode, data)
	}
	ch := make(chan ChatEvent)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64<<10), maxChatEvent)
		var name, data string
		var rev int64
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "id: "):
				_, _ = fmt.Sscan(strings.TrimPrefix(line, "id: "), &rev) // a malformed id keeps the last one
			case strings.HasPrefix(line, "data: "):
				data = strings.TrimPrefix(line, "data: ")
			case line == "" && data != "":
				ev, ok := decodeChatEvent(name, rev, data)
				name, data = "", ""
				if !ok {
					continue
				}
				select {
				case ch <- ev:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return ch, nil
}

// decodeChatEvent reads one event of a chat's stream. Events it doesn't know are skipped.
func decodeChatEvent(name string, rev int64, data string) (ChatEvent, bool) {
	ev := ChatEvent{Rev: rev}
	var err error
	switch name {
	case "chat":
		ev.Chat = &store.Chat{}
		err = json.Unmarshal([]byte(data), ev.Chat)
	case "turn":
		ev.Turn = &chat.Turn{}
		err = json.Unmarshal([]byte(data), ev.Turn)
	case "item":
		ev.Item = &chat.Item{}
		err = json.Unmarshal([]byte(data), ev.Item)
	default:
		return ev, false
	}
	return ev, err == nil
}

// send makes one request. in, when not nil, is sent as JSON, and a success answer is decoded
// into out when out is not nil, or kept as it is when out is a *[]byte. The response is returned with its body read and closed, so
// its headers, such as cookies, stay available.
func (c *Client) send(ctx context.Context, method, path string, header http.Header, in, out any) (*http.Response, error) {
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://"+c.addr+path, body)
	if err != nil {
		return nil, err
	}
	for k, v := range header {
		req.Header[k] = v
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	c.authorize(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswer))
	if err != nil {
		return resp, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp, apiError(resp.StatusCode, data)
	}
	if raw, ok := out.(*[]byte); ok {
		*raw = data
		return resp, nil
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return resp, fmt.Errorf("Studio answered with invalid JSON: %w", err)
		}
	}
	return resp, nil
}

// authorize adds the session cookie to a request.
func (c *Client) authorize(req *http.Request) {
	if c.session != "" {
		req.AddCookie(&http.Cookie{Name: webauth.SessionCookie, Value: c.session})
	}
}

// apiError reads a failed answer: the problem detail the API sends, or the plain text of
// the answers the auth endpoints give.
func apiError(status int, body []byte) *APIError {
	e := &APIError{Status: status, Detail: http.StatusText(status)}
	var problem struct {
		Detail string `json:"detail"`
	}
	if json.Unmarshal(body, &problem) == nil && problem.Detail != "" {
		e.Detail = problem.Detail
	} else if text := strings.TrimSpace(string(body)); text != "" && !strings.HasPrefix(text, "{") {
		e.Detail = text
	}
	return e
}
