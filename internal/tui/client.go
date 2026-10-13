// Package tui is the terminal client of a running Studio (docs/tui.md). It is a client of the
// HTTP API the web UI uses: it adds no endpoints, and it signs in as the local owner.
package tui

import (
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
	stream  *http.Client // no timeout: the event stream stays open
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
	transport := &http.Transport{Proxy: nil}
	c := &Client{
		addr: addr,
		http: &http.Client{
			Transport: transport, Timeout: 30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		stream: &http.Client{Transport: transport},
	}
	if !c.running(ctx) {
		return nil, fmt.Errorf("%w at %s. Start it with `studio`, then run `studio tui` again", ErrNotRunning, addr)
	}
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
