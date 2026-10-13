package gateway

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/lukaskoebe/sandbox-studio/internal/policy"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// decideRequest decides one request on a terminated connection of a browser VM. The request
// goes on if the policy allows it, which may take the user's answer; otherwise the message says
// why it was refused, and is returned.
func (g *Gateway) decideRequest(r *http.Request, decider store.Sandbox, host string, port int) string {
	_, err := g.Policy.Decide(r.Context(), policy.Request{
		EnvironmentID: decider.EnvironmentID, SandboxID: decider.ID, SandboxName: decider.Name,
		Host: host, Port: port, HTTP: requestInfo(r, host),
	})
	where := fmt.Sprintf("%s:%d", host, port)
	switch {
	case err == nil:
		return ""
	case errors.Is(err, policy.ErrUndecided):
		return fmt.Sprintf("Requests to %s are waiting for approval in Sandbox Studio. Retry once the user has allowed them.", where)
	case errors.Is(err, policy.ErrDismissed):
		return fmt.Sprintf("The user dismissed the request to %s.", where)
	case errors.Is(err, policy.ErrDenied):
		return fmt.Sprintf("The user denied the request to %s.", where)
	}
	g.Log.Error("network policy", "sandbox", decider.ID, "host", host, "err", err)
	return fmt.Sprintf("Sandbox Studio could not decide the request to %s: %v", where, err)
}

// requestInfo reads what a request says about itself, for its approval. Chromium sets the
// Sec-Fetch headers and page scripts can't, so they tell a top-level navigation from a
// subresource; the Referer is shown, never trusted. The query is left out: it can carry tokens.
func requestInfo(r *http.Request, host string) *policy.HTTPRequest {
	info := &policy.HTTPRequest{Method: clip(r.Method, 16), Path: clip(r.URL.Path, 1024)}
	if r.Header.Get("Sec-Fetch-Mode") == "navigate" && r.Header.Get("Sec-Fetch-Dest") == "document" {
		info.Navigation, info.Site = true, policy.Site(host)
	}
	if ref, err := url.Parse(r.Header.Get("Referer")); err == nil && (ref.Scheme == "http" || ref.Scheme == "https") && ref.Host != "" {
		info.Initiator = clip(ref.Scheme+"://"+ref.Host, 256)
	}
	return info
}

// clip shortens s to n characters, keeping it valid UTF-8.
func clip(s string, n int) string {
	s = strings.ToValidUTF8(s, "�")
	if utf8.RuneCountInString(s) > n {
		return string([]rune(s)[:n])
	}
	return s
}
