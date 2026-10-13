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

	"github.com/lukaskoebe/sandbox-studio/internal/api"
	"github.com/lukaskoebe/sandbox-studio/internal/events"
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
		return nil, errors.New("the terminal UI connects to a loopback address only")
	}
	c := &Client{
		addr: addr,
		http: &http.Client{
			Transport:     &http.Transport{Proxy: nil},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
	if !c.running(ctx) {
		return nil, fmt.Errorf("%w at %s. Start it with `studio`, then run `studio tui` again", ErrNotRunning, addr)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var ticket struct {
		Token string `json:"token"`
	}
	if _, err := c.send(ctx, http.MethodPost, "/api/auth/launch", http.Header{"Authorization": {"Bearer " + control}}, nil, &ticket); err != nil {
		return nil, fmt.Errorf("Studio refused the terminal UI's sign-in (run it as the user Studio runs as): %w", err)
	}
	resp, err := c.send(ctx, http.MethodPost, "/api/auth/exchange", nil, map[string]string{"token": ticket.Token}, nil)
	if err != nil {
		return nil, fmt.Errorf("Studio refused the terminal UI's session: %w", err)
	}
	for _, cookie := range resp.Cookies() {
		if cookie.Name == webauth.SessionCookie {
			c.session = cookie.Value
		}
	}
	if c.session == "" {
		return nil, errors.New("Studio did not start a session for the terminal UI")
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

// Lifecycle starts, stops or resumes a sandbox and returns it as it is then. A start or resume
// returns when the VM is up, which can take a while.
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

// send makes one request. in, when not nil, is sent as JSON, and a success answer is decoded
// into out when out is not nil. The response is returned with its body read and closed, so
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
