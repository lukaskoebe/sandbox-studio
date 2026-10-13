package gateway

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/lukaskoebe/sandbox-studio/internal/policy"
	"github.com/lukaskoebe/sandbox-studio/internal/secrets"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// decideRequest decides one request on a terminated connection of a browser VM. The request
// goes on if the policy allows it, which may take the user's answer; otherwise the message says
// why it was refused, and is returned. all are the secrets the sandbox's requests may carry.
func (g *Gateway) decideRequest(r *http.Request, decider store.Sandbox, host string, port int, all []secrets.Binding) string {
	info := requestInfo(r, host)
	rule, err := g.Policy.Decide(r.Context(), policy.Request{
		EnvironmentID: decider.EnvironmentID, SandboxID: decider.ID, SandboxName: decider.Name,
		Host: host, Port: port, Browser: true, Deferred: true, HTTP: info,
		Placeholder: carriesPlaceholder(r, all, host),
	})
	where := fmt.Sprintf("%s:%d", host, port)
	switch {
	case err == nil && rule.Action == store.ActionDeny:
		return fmt.Sprintf("A Sandbox Studio network rule blocks %s %s on %s.", info.Method, info.Path, where)
	case err == nil:
		return ""
	case errors.Is(err, policy.ErrUndecided):
		return fmt.Sprintf("Requests to %s are waiting for approval in Sandbox Studio. Retry once the user has allowed them.", where)
	case errors.Is(err, policy.ErrDismissed):
		return fmt.Sprintf("The user dismissed the request to %s.", where)
	case errors.Is(err, policy.ErrDenied):
		return fmt.Sprintf("The user denied the request to %s.", where)
	case errors.Is(err, policy.ErrTracker):
		return fmt.Sprintf("%s is on the tracker list that Sandbox Studio blocks in browsers.", host)
	}
	g.Log.Error("network policy", "sandbox", decider.ID, "host", host, "err", err)
	return fmt.Sprintf("Sandbox Studio could not decide the request to %s: %v", where, err)
}

// carriesPlaceholder reports whether a request holds the placeholder of a secret that host isn't
// bound to, which the rewrite refuses (intercept.go). The headers, the path and the query are
// checked, and the credentials of a Basic Authorization header decoded. A security level never
// allows such a request.
func carriesPlaceholder(r *http.Request, all []secrets.Binding, host string) bool {
	texts := []string{r.URL.EscapedPath(), r.URL.RawQuery}
	for name, values := range r.Header {
		for _, v := range values {
			texts = append(texts, v)
			if strings.EqualFold(name, "Authorization") && len(v) > 6 && strings.EqualFold(v[:6], "Basic ") {
				if decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(v[6:])); err == nil {
					texts = append(texts, string(decoded))
				}
			}
		}
	}
	for _, b := range all {
		if b.Placeholder == "" || !b.Foreign && covers(b, host) {
			continue
		}
		for _, t := range texts {
			if strings.Contains(t, b.Placeholder) {
				return true
			}
		}
	}
	return false
}

// ruleRequest applies the request-level rules to a request on a connection that a proxy rule
// allowed: a rule that denies the request refuses it. Nothing is asked about the others.
func (g *Gateway) ruleRequest(r *http.Request, decider store.Sandbox, host string, port int) string {
	info := requestInfo(r, host)
	rule, err := g.Policy.Decide(r.Context(), policy.Request{
		EnvironmentID: decider.EnvironmentID, SandboxID: decider.ID, SandboxName: decider.Name,
		Host: host, Port: port, HTTP: info, RulesOnly: true,
	})
	switch {
	case errors.Is(err, policy.ErrUndecided), err == nil && rule.Action != store.ActionDeny:
		return ""
	case err != nil:
		g.Log.Error("network policy", "sandbox", decider.ID, "host", host, "err", err)
		return fmt.Sprintf("Sandbox Studio could not check the request %s %s on %s: %v", info.Method, info.Path, host, err)
	}
	return fmt.Sprintf("A Sandbox Studio network rule blocks %s %s on %s.", info.Method, info.Path, host)
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
