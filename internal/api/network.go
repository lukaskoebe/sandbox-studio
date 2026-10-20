package api

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/http"
	"net/textproto"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"golang.org/x/net/http/httpguts"

	"github.com/lukaskoebe/sandbox-studio/internal/agentmem"
	"github.com/lukaskoebe/sandbox-studio/internal/browser"
	"github.com/lukaskoebe/sandbox-studio/internal/chat"
	"github.com/lukaskoebe/sandbox-studio/internal/gateway"
	"github.com/lukaskoebe/sandbox-studio/internal/gitreview"
	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/policy"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// ApprovalTeammate is the teammate owning the sandbox of a network approval, with the
// settings its card starts with.
type ApprovalTeammate struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ApprovalScope string `json:"approvalScope" enum:"sandbox,teammate,environment" doc:"The scope the card preselects"`
	PatternLevel  string `json:"patternLevel,omitempty" enum:"exact,wildcard" doc:"The host pattern level the teammate last chose, which the card preselects; empty if none"`
}

// ApprovalView is an approval with the details of its request decoded from the payload.
type ApprovalView struct {
	store.Approval
	Network *policy.NetworkRequest `json:"network,omitempty"`
	// Teammate is set for network approvals from a sandbox that a teammate owns.
	Teammate *ApprovalTeammate    `json:"teammate,omitempty"`
	Git      *gitreview.GitDetail `json:"git,omitempty" doc:"Set for git.push and git.pr approvals"`
	// MemoryShare is set for memory.share approvals.
	MemoryShare *agentmem.SharePayload `json:"memoryShare,omitempty" doc:"A teammate's proposal for shared memory"`
	// MemoryConflict is set for memory.conflict approvals.
	MemoryConflict *agentmem.ConflictPayload `json:"memoryConflict,omitempty" doc:"Two facts that disagree"`
	// BrowserAction, BrowserCredential and BrowserControl are set for browser.action,
	// browser.credential and browser.control.
	BrowserAction     *browser.ActionPayload     `json:"browserAction,omitempty" doc:"An agent's browser action on a new origin"`
	BrowserCredential *browser.CredentialPayload `json:"browserCredential,omitempty" doc:"An agent asks Studio to fill a vault credential into a page"`
	BrowserControl    *browser.ControlPayload    `json:"browserControl,omitempty" doc:"An agent asks the user, who drives the browser, to hand it back"`
	// ChatPermission is set for chat.permission approvals.
	ChatPermission *chat.PermissionPayload `json:"chatPermission,omitempty" doc:"A chat's agent asks to run a tool"`
}

func (s *Server) approvalView(ctx context.Context, a store.Approval) ApprovalView {
	v := ApprovalView{Approval: a}
	switch a.Kind {
	case policy.KindNetwork, policy.KindRequest:
		var req policy.NetworkRequest
		if json.Unmarshal(a.Payload, &req) == nil {
			v.Network = &req
		}
	case gitreview.KindPush, gitreview.KindPR:
		if s.Git != nil {
			v.Git, _ = s.Git.GitDetail(ctx, a)
		}
	case agentmem.ApprovalKind:
		var p agentmem.SharePayload
		if json.Unmarshal(a.Payload, &p) == nil {
			v.MemoryShare = &p
		}
	case browser.KindAction:
		var p browser.ActionPayload
		if json.Unmarshal(a.Payload, &p) == nil {
			v.BrowserAction = &p
		}
	case browser.KindCredential:
		var p browser.CredentialPayload
		if json.Unmarshal(a.Payload, &p) == nil {
			v.BrowserCredential = &p
		}
	case browser.KindControl:
		var p browser.ControlPayload
		if json.Unmarshal(a.Payload, &p) == nil {
			v.BrowserControl = &p
		}
	case chat.ApprovalKind:
		var p chat.PermissionPayload
		if json.Unmarshal(a.Payload, &p) == nil {
			v.ChatPermission = &p
		}
	case agentmem.ConflictApprovalKind:
		var p agentmem.ConflictPayload
		if json.Unmarshal(a.Payload, &p) == nil {
			v.MemoryConflict = &p
		}
	}
	if policy.IsNetworkKind(a.Kind) {
		v.Teammate = s.approvalOwner(ctx, a)
	}
	return v
}

// approvalOwner returns the teammate owning the sandbox of an approval, or nil if the
// sandbox is unowned or gone.
func (s *Server) approvalOwner(ctx context.Context, a store.Approval) *ApprovalTeammate {
	if a.SandboxID == "" {
		return nil
	}
	sb, err := s.Store.Sandbox(ctx, a.EnvironmentID, a.SandboxID)
	if err != nil || sb.TeammateID == "" {
		return nil
	}
	t, err := s.Store.Teammate(ctx, a.EnvironmentID, sb.TeammateID)
	if err != nil {
		return nil
	}
	return &ApprovalTeammate{ID: t.ID, Name: t.Name, ApprovalScope: t.ApprovalScope, PatternLevel: t.ApprovalPattern}
}

// approvalViews lists up to 200 approvals of an environment (all of them if env is empty),
// newest first. The status "all" lists every status.
func (s *Server) approvalViews(ctx context.Context, env, status string) ([]ApprovalView, error) {
	if status == "all" {
		status = ""
	}
	list, err := s.Store.Approvals(ctx, env, status, 200)
	if err != nil {
		return nil, err
	}
	out := make([]ApprovalView, len(list))
	for i, a := range list {
		out[i] = s.approvalView(ctx, a)
	}
	return out, nil
}

// checkEnvironment answers 404 for an unknown environment instead of letting a write fail on
// a foreign key.
func (s *Server) checkEnvironment(ctx context.Context, env string) error {
	_, err := s.Store.Environment(ctx, env)
	return apiError(err)
}

// RuleInput is the editable part of a network rule.
type RuleInput struct {
	Host        string          `json:"host" minLength:"1" doc:"example.com, *.example.com (includes example.com), or an IP address"`
	Ports       []int           `json:"ports,omitempty" minimum:"1" maximum:"65535" doc:"Empty or omitted means any port"`
	Action      string          `json:"action" enum:"allow,proxy,caddy,deny" doc:"allow passes connections through untouched; proxy lets Studio handle the HTTP requests, to set headers; caddy hands them to a Caddyfile"`
	Config      RuleConfigInput `json:"config,omitempty"`
	Method      string          `json:"method,omitempty" doc:"allow and deny rules only: the request method the rule covers, one of GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS; empty covers any method. Only requests on connections Studio terminates (a browser VM's HTTPS, and hosts of proxy rules) are matched against it"`
	PathPrefix  string          `json:"pathPrefix,omitempty" maxLength:"512" doc:"allow and deny rules only: the request paths the rule covers, a prefix ending in * such as /repos/*; empty covers any path"`
	SandboxID   string          `json:"sandboxId,omitempty" doc:"Limits the rule to one sandbox of this environment"`
	TeammateID  string          `json:"teammateId,omitempty" doc:"Limits the rule to the sandboxes one teammate of this environment owns; excludes sandboxId"`
	BrowserOnly bool            `json:"browserOnly,omitempty" doc:"Limits the rule to the requests of the environment's browser VMs: the sandboxes' own connections aren't covered"`
	Note        string          `json:"note,omitempty" maxLength:"500"`
}

// RuleConfigInput is the editable part of a rule's config.
type RuleConfigInput struct {
	Headers    map[string]string `json:"headers,omitempty" maxProperties:"32" doc:"proxy rules only: headers set on every request, replacing the sandbox's. Values may reference secrets as {secret.NAME}, which are only sent over HTTPS to hosts the secret is bound to."`
	Caddyfile  string            `json:"caddyfile,omitempty" maxLength:"32768" doc:"caddy rules only: the routes of a Caddy site, in Caddyfile syntax, that handle the requests. reverse_proxy may send secrets upstream in header_up as {secret.NAME}, over HTTPS to hosts the secret is bound to."`
	Upstream   string            `json:"upstream,omitempty" maxLength:"261" doc:"proxy rules only: a host or host:port to send the requests to instead of the rule's host. The port defaults to the connection's. Secrets go only to the upstream they are bound to."`
	HostHeader string            `json:"hostHeader,omitempty" maxLength:"253" doc:"proxy rules only: the Host header sent upstream, instead of the host the sandbox asked for"`
	SNI        string            `json:"sni,omitempty" maxLength:"253" doc:"proxy rules only: the TLS server name sent upstream and checked against its certificate. Defaults to the upstream host"`
	CA         string            `json:"ca,omitempty" maxLength:"65536" doc:"proxy rules only: PEM certificates trusted for the upstream, in addition to the system's, for upstreams with a private CA"`
}

// headersNotSet are managed by the HTTP machinery or describe the connection, not the
// request, so a rule must not set them.
var headersNotSet = []string{
	"Host", "Content-Length", "Transfer-Encoding", "Connection", "Keep-Alive", "Proxy-Connection",
	"Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Upgrade",
}

// ruleConfigFrom validates the config of a rule with action in environment env.
func (s *Server) ruleConfigFrom(ctx context.Context, env, action string, in RuleConfigInput) (store.RuleConfig, error) {
	secrets, err := s.ruleSecrets(ctx, env)
	if err != nil {
		return store.RuleConfig{}, apiError(err)
	}
	return s.checkRuleConfig(action, in, secrets)
}

// checkRuleConfig validates the config of a rule with action. The secrets are the ones its
// references may name; they are read from the environment, or from an import file.
func (s *Server) checkRuleConfig(action string, in RuleConfigInput, secrets []store.Secret) (store.RuleConfig, error) {
	if action == store.ActionCaddy || in.Caddyfile != "" {
		return s.caddyConfigFrom(action, in, secrets)
	}
	config, err := overridesFrom(action, in)
	if err != nil {
		return store.RuleConfig{}, err
	}
	if len(in.Headers) == 0 {
		return config, nil
	}
	if action != store.ActionProxy {
		return store.RuleConfig{}, huma.Error422UnprocessableEntity("only proxy rules set headers")
	}
	headers := make(map[string]string, len(in.Headers))
	for name, value := range in.Headers {
		if !httpguts.ValidHeaderFieldName(name) {
			return store.RuleConfig{}, huma.Error422UnprocessableEntity(fmt.Sprintf("%q is not a valid header name", name))
		}
		canonical := textproto.CanonicalMIMEHeaderKey(name)
		if slices.Contains(headersNotSet, canonical) {
			return store.RuleConfig{}, huma.Error422UnprocessableEntity(fmt.Sprintf("rules can't set the %s header", canonical))
		}
		if _, dup := headers[canonical]; dup {
			return store.RuleConfig{}, huma.Error422UnprocessableEntity(fmt.Sprintf("the %s header is set twice", canonical))
		}
		if !httpguts.ValidHeaderFieldValue(value) || len(value) > 8<<10 {
			return store.RuleConfig{}, huma.Error422UnprocessableEntity(fmt.Sprintf("the value of %s is not a valid header value", canonical))
		}
		for _, ref := range gateway.SecretRefs(value) {
			if !slices.ContainsFunc(secrets, func(s store.Secret) bool { return s.Name == ref }) {
				return store.RuleConfig{}, huma.Error422UnprocessableEntity(fmt.Sprintf("%s refers to {secret.%s}, but this environment has no secret %s", canonical, ref, ref))
			}
		}
		headers[canonical] = value
	}
	config.Headers = headers
	return config, nil
}

// maxCAs bounds the certificates a proxy rule trusts for its upstream.
const maxCAs = 8

// overridesFrom checks where a proxy rule sends its requests: the upstream, the Host header, the
// TLS server name and the CA. Only proxy rules have them.
func overridesFrom(action string, in RuleConfigInput) (store.RuleConfig, error) {
	var c store.RuleConfig
	if in.Upstream == "" && in.HostHeader == "" && in.SNI == "" && in.CA == "" {
		return c, nil
	}
	if action != store.ActionProxy {
		return c, huma.Error422UnprocessableEntity("only proxy rules can send requests to another upstream, host or CA")
	}
	var err error
	if in.Upstream != "" {
		if c.Upstream, err = upstreamFrom(in.Upstream); err != nil {
			return c, err
		}
	}
	if in.HostHeader != "" {
		if c.HostHeader, err = hostNameFrom("hostHeader", in.HostHeader); err != nil {
			return c, err
		}
	}
	if in.SNI != "" {
		if c.SNI, err = hostNameFrom("sni", in.SNI); err != nil {
			return c, err
		}
	}
	if in.CA != "" {
		if err := checkCAs(in.CA); err != nil {
			return c, err
		}
		c.CA = in.CA
	}
	return c, nil
}

// upstreamFrom checks an upstream, a host or host:port, and returns it normalized.
func upstreamFrom(in string) (string, error) {
	host, port := strings.TrimSpace(in), ""
	if h, p, err := net.SplitHostPort(host); err == nil {
		host, port = h, p
	} else {
		host = strings.Trim(host, "[]")
	}
	name, err := hostNameFrom("upstream", host)
	if err != nil {
		return "", err
	}
	if port == "" {
		return name, nil
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", huma.Error422UnprocessableEntity(fmt.Sprintf("upstream %s has port %q; ports are 1 to 65535", in, port))
	}
	return net.JoinHostPort(name, strconv.Itoa(n)), nil
}

// hostNameFrom checks a host name or IP address, with no wildcard, and returns it normalized.
func hostNameFrom(field, in string) (string, error) {
	host, err := policy.ValidPattern(in)
	if err != nil || strings.Contains(host, "*") {
		return "", huma.Error422UnprocessableEntity(fmt.Sprintf("%s %q is not a host name or IP address", field, in))
	}
	return host, nil
}

// checkCAs checks that text holds PEM certificates, and nothing else: a private key has no
// business in a rule.
func checkCAs(text string) error {
	rest := []byte(text)
	n := 0
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			return huma.Error422UnprocessableEntity(fmt.Sprintf("the CA holds a %s block; only CERTIFICATE blocks belong there", block.Type))
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			return huma.Error422UnprocessableEntity("the CA holds an invalid certificate: " + err.Error())
		}
		n++
	}
	switch {
	case n == 0:
		return huma.Error422UnprocessableEntity("the CA is not PEM-encoded certificates")
	case n > maxCAs:
		return huma.Error422UnprocessableEntity(fmt.Sprintf("the CA holds %d certificates; at most %d", n, maxCAs))
	}
	return nil
}

// ruleFrom validates in and returns the rule it describes in environment env.
// caddyConfigFrom validates the config of a caddy rule: a Caddyfile that Caddy accepts and
// only sends secrets to hosts they are bound to.
func (s *Server) caddyConfigFrom(action string, in RuleConfigInput, secrets []store.Secret) (store.RuleConfig, error) {
	switch {
	case action != store.ActionCaddy:
		return store.RuleConfig{}, huma.Error422UnprocessableEntity("only caddy rules have a Caddyfile")
	case len(in.Headers) > 0:
		return store.RuleConfig{}, huma.Error422UnprocessableEntity("only proxy rules set headers; a Caddyfile sets them with header_up")
	case in.Upstream != "" || in.HostHeader != "" || in.SNI != "" || in.CA != "":
		return store.RuleConfig{}, huma.Error422UnprocessableEntity("only proxy rules can send requests to another upstream, host or CA")
	case strings.TrimSpace(in.Caddyfile) == "":
		return store.RuleConfig{}, huma.Error422UnprocessableEntity("caddy rules need a Caddyfile")
	case s.Caddy == nil:
		return store.RuleConfig{}, huma.Error422UnprocessableEntity("caddy rules are not available")
	}
	compiled, err := s.Caddy.Check(in.Caddyfile)
	if err != nil {
		return store.RuleConfig{}, huma.Error422UnprocessableEntity("Caddyfile: " + err.Error())
	}
	for _, name := range slices.Sorted(maps.Keys(compiled.Secrets)) {
		named := slices.DeleteFunc(slices.Clone(secrets), func(s store.Secret) bool { return s.Name != name })
		if len(named) == 0 {
			return store.RuleConfig{}, huma.Error422UnprocessableEntity(fmt.Sprintf("the Caddyfile refers to {secret.%s}, but this environment has no secret %s", name, name))
		}
		for _, host := range compiled.Secrets[name] {
			// Secrets of the same name in other scopes are checked per sandbox when sent.
			if !slices.ContainsFunc(named, func(s store.Secret) bool {
				return slices.ContainsFunc(s.Hosts, func(p string) bool { return policy.Covers(p, host) })
			}) {
				return store.RuleConfig{}, huma.Error422UnprocessableEntity(fmt.Sprintf("the Caddyfile sends {secret.%s} to %s, but the secret is not bound to %s", name, host, host))
			}
		}
	}
	return store.RuleConfig{Caddyfile: in.Caddyfile}, nil
}

func (s *Server) ruleFrom(ctx context.Context, env string, in RuleInput) (store.Rule, error) {
	host, err := policy.ValidPattern(in.Host)
	if err != nil {
		return store.Rule{}, apiError(err)
	}
	if in.SandboxID != "" {
		_, err := s.publicSandbox(ctx, env, in.SandboxID)
		if errors.Is(err, store.ErrNotFound) {
			return store.Rule{}, huma.Error422UnprocessableEntity(fmt.Sprintf("sandbox %s is not in this environment", in.SandboxID))
		}
		if err != nil {
			return store.Rule{}, apiError(err)
		}
	}
	if in.TeammateID != "" && in.SandboxID != "" {
		return store.Rule{}, huma.Error422UnprocessableEntity("a rule is scoped to a sandbox or a teammate, not both")
	}
	if err := s.checkTeammate(ctx, env, in.TeammateID); err != nil {
		return store.Rule{}, err
	}
	config, err := s.ruleConfigFrom(ctx, env, in.Action, in.Config)
	if err != nil {
		return store.Rule{}, err
	}
	method, path, err := requestPattern(in.Action, in.Method, in.PathPrefix)
	if err != nil {
		return store.Rule{}, err
	}
	return store.Rule{EnvironmentID: env, SandboxID: in.SandboxID, TeammateID: in.TeammateID, Host: host, Ports: in.Ports, Action: in.Action, Config: config, Note: in.Note, Method: method, PathPrefix: path, BrowserOnly: in.BrowserOnly}, nil
}

// requestPattern checks the method and path a rule names: only allow and deny rules can name
// them, and they must be valid.
func requestPattern(action, method, pathPrefix string) (string, string, error) {
	if method == "" && pathPrefix == "" {
		return "", "", nil
	}
	if action != store.ActionAllow && action != store.ActionDeny {
		return "", "", huma.Error422UnprocessableEntity("only allow and deny rules can name a method or path")
	}
	m, err := policy.ValidMethod(method)
	if err != nil {
		return "", "", huma.Error422UnprocessableEntity(err.Error())
	}
	p, err := policy.ValidPathPrefix(pathPrefix)
	if err != nil {
		return "", "", huma.Error422UnprocessableEntity(err.Error())
	}
	return m, p, nil
}

// expiryFrom checks when a rule expires: it must still be in the future.
func expiryFrom(expires *time.Time) (*time.Time, error) {
	if expires != nil && !expires.After(time.Now()) {
		return nil, huma.Error422UnprocessableEntity(policy.ErrExpired.Error())
	}
	return expires, nil
}

type rulePath struct {
	Env string `path:"env" doc:"Environment ID"`
	ID  string `path:"id" doc:"Rule ID"`
}

func (s *Server) registerNetwork(api huma.API) {
	type listIn struct {
		Status string `query:"status" enum:"pending,approved,denied,dismissed,all" default:"pending" doc:"Statuses to list; all lists every status"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "listApprovals", Method: http.MethodGet, Path: "/api/approvals", Tags: []string{"approvals"},
		Description: "Approvals of every environment, newest first. Pending ones feed the approval inbox.",
	}, func(ctx context.Context, in *listIn) (*struct{ Body []ApprovalView }, error) {
		list, err := s.approvalViews(ctx, "", in.Status)
		return &struct{ Body []ApprovalView }{list}, apiError(err)
	})

	type envApprovalsIn struct {
		Env    string `path:"env" doc:"Environment ID"`
		Status string `query:"status" enum:"pending,approved,denied,dismissed,all" default:"all" doc:"Statuses to list; all lists every status"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "listEnvironmentApprovals", Method: http.MethodGet, Path: "/api/environments/{env}/approvals", Tags: []string{"approvals"},
	}, func(ctx context.Context, in *envApprovalsIn) (*struct{ Body []ApprovalView }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		list, err := s.approvalViews(ctx, in.Env, in.Status)
		return &struct{ Body []ApprovalView }{list}, apiError(err)
	})

	type approvalIn struct {
		Env string `path:"env" doc:"Environment ID"`
		ID  string `path:"id" doc:"Approval ID"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "getApproval", Method: http.MethodGet, Path: "/api/environments/{env}/approvals/{id}", Tags: []string{"approvals"},
		Description: "One approval in any status, such as a git push review a link points to.",
	}, func(ctx context.Context, in *approvalIn) (*struct{ Body ApprovalView }, error) {
		a, err := s.Store.Approval(ctx, in.Env, in.ID)
		if err != nil {
			return nil, apiError(err)
		}
		return &struct{ Body ApprovalView }{s.approvalView(ctx, a)}, nil
	})

	type decideIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		ID   string `path:"id" doc:"Approval ID"`
		Body struct {
			Action      string     `json:"action" enum:"allow,deny,dismiss,allow_once,deny_once,allow_site,allow_session" doc:"For network requests allow or deny creates a rule; allow_once and deny_once answer only the held connections of this request, without a rule; allow_site, for a browser navigation to a new site, allows the site for the browser session without a rule; allow_session, for a browser's request, allows the hosts host covers in that browser until its VM stops, without a rule; dismiss only closes the request. For git.push, allow pushes upstream; for git.pr, allow opens the pull request"`
			Host        string     `json:"host,omitempty" doc:"Host pattern the rule covers; empty means the requested host"`
			Ports       []int      `json:"ports,omitempty" minimum:"1" maximum:"65535" doc:"Ports the rule covers; omitted means the default ports for the request, empty means any port"`
			Scope       string     `json:"scope,omitempty" enum:"sandbox,teammate,environment" default:"environment" doc:"Whether the rule covers only the requesting sandbox, the sandboxes of its teammate, or the environment"`
			Note        string     `json:"note,omitempty" maxLength:"8000" doc:"For git approvals: a note passed on to the sandbox, such as why a push was rejected, with the reviewer's line comments"`
			Remember    bool       `json:"remember,omitempty" doc:"For browser.action: allow (or deny) the action on the origin from now on"`
			Method      string     `json:"method,omitempty" doc:"For network requests allowed or denied with a rule: the request method the rule covers; empty covers any"`
			PathPrefix  string     `json:"pathPrefix,omitempty" doc:"For network requests allowed or denied with a rule: a path prefix ending in *, such as /repos/*, that the rule covers; empty covers any path"`
			ExpiresAt   *time.Time `json:"expiresAt,omitempty" doc:"For network requests: when the rule created by allow or deny stops matching, which must be in the future. Empty means it never expires"`
			BrowserOnly bool       `json:"browserOnly,omitempty" doc:"For network requests allowed or denied with a rule: the rule covers only the requests of the environment's browser VMs"`
			PullRequest *struct {
				Open  bool   `json:"open" doc:"Open the review's pull request along with the push; false pushes without one"`
				Title string `json:"title,omitempty" maxLength:"200" doc:"The pull request's title; empty keeps the proposed one"`
			} `json:"pullRequest,omitempty" doc:"For git.push approved with allow, when the review offers a pull request: whether to open it now. Omitted, the pull request waits for a git.pr approval"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "decideApproval", Method: http.MethodPost, Path: "/api/environments/{env}/approvals/{id}/decide", Tags: []string{"approvals"},
	}, func(ctx context.Context, in *decideIn) (*struct{ Body ApprovalView }, error) {
		id := integrations.Decision{Action: in.Body.Action, Note: strings.TrimSpace(in.Body.Note), Remember: in.Body.Remember}
		if pr := in.Body.PullRequest; pr != nil {
			id.PullRequest = &integrations.PullRequestChoice{Open: pr.Open, Title: pr.Title}
		}
		if a, ok, err := s.decideIntegration(ctx, in.Env, in.ID, id); ok || err != nil {
			if err != nil {
				return nil, err
			}
			return &struct{ Body ApprovalView }{s.approvalView(ctx, a)}, nil
		}
		d := policy.Decision{Action: in.Body.Action, Host: in.Body.Host, Ports: in.Body.Ports, Scope: in.Body.Scope, Method: in.Body.Method, PathPrefix: in.Body.PathPrefix, BrowserOnly: in.Body.BrowserOnly}
		if in.Body.ExpiresAt != nil {
			d.ExpiresAt = *in.Body.ExpiresAt
		}
		a, err := s.Policy.Resolve(ctx, in.Env, in.ID, d)
		if err != nil {
			return nil, apiError(err)
		}
		return &struct{ Body ApprovalView }{s.approvalView(ctx, a)}, nil
	})

	type revertIn struct {
		Env string `path:"env" doc:"Environment ID"`
		ID  string `path:"id" doc:"Approval ID"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "revertApproval", Method: http.MethodPost, Path: "/api/environments/{env}/approvals/{id}/revert", Tags: []string{"approvals"},
		Description: "Undoes a decision the AI reviewer made: its rule is deleted, and the request is asked about from then on. Other decisions can't be reverted.",
	}, func(ctx context.Context, in *revertIn) (*struct{ Body ApprovalView }, error) {
		a, err := s.Policy.Revert(ctx, in.Env, in.ID)
		if err != nil {
			return nil, apiError(err)
		}
		return &struct{ Body ApprovalView }{s.approvalView(ctx, a)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "getReviewer", Method: http.MethodGet, Path: "/api/environments/{env}/reviewer", Tags: []string{"network"},
		Description: "Whether the assisted level can ask an AI reviewer: the environment needs a provider whose utility model can be called.",
	}, func(ctx context.Context, in *envPath) (*struct{ Body reviewerView }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		v, err := s.reviewer(ctx, in.Env)
		return &struct{ Body reviewerView }{v}, apiError(err)
	})

	huma.Register(api, huma.Operation{
		OperationID: "listRules", Method: http.MethodGet, Path: "/api/environments/{env}/rules", Tags: []string{"network"},
	}, func(ctx context.Context, in *envPath) (*struct{ Body []store.Rule }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		list, err := s.Store.Rules(ctx, in.Env)
		for i := range list {
			list[i] = s.withReach(ctx, list[i])
		}
		return &struct{ Body []store.Rule }{list}, apiError(err)
	})

	type createRuleIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		Body struct {
			RuleInput
			ExpiresAt *time.Time `json:"expiresAt,omitempty" doc:"When the rule stops matching; it must be in the future. Empty means it never expires"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "createRule", Method: http.MethodPost, Path: "/api/environments/{env}/rules", Tags: []string{"network"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *createRuleIn) (*struct{ Body store.Rule }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		expires, err := expiryFrom(in.Body.ExpiresAt)
		if err != nil {
			return nil, err
		}
		r, err := s.ruleFrom(ctx, in.Env, in.Body.RuleInput)
		if err != nil {
			return nil, err
		}
		r.ExpiresAt = expires
		if r, err = s.Store.CreateRule(ctx, r); err != nil {
			return nil, apiError(err)
		}
		if err := s.Policy.Settle(ctx, in.Env); err != nil {
			return nil, apiError(err)
		}
		return &struct{ Body store.Rule }{s.withReach(ctx, r)}, nil
	})

	type updateRuleIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		ID   string `path:"id" doc:"Rule ID"`
		Body RuleInput
	}
	huma.Register(api, huma.Operation{
		OperationID: "updateRule", Method: http.MethodPut, Path: "/api/environments/{env}/rules/{id}", Tags: []string{"network"},
	}, func(ctx context.Context, in *updateRuleIn) (*struct{ Body store.Rule }, error) {
		old, err := s.Store.Rule(ctx, in.Env, in.ID)
		if err != nil {
			return nil, apiError(err)
		}
		r, err := s.ruleFrom(ctx, in.Env, in.Body)
		if err != nil {
			return nil, err
		}
		r.ID = old.ID
		if r, err = s.Store.UpdateRule(ctx, r); err != nil {
			return nil, apiError(err)
		}
		if err := s.Policy.Settle(ctx, in.Env); err != nil {
			return nil, apiError(err)
		}
		return &struct{ Body store.Rule }{s.withReach(ctx, r)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "deleteRule", Method: http.MethodDelete, Path: "/api/environments/{env}/rules/{id}", Tags: []string{"network"},
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *rulePath) (*struct{}, error) {
		if err := s.Store.DeleteRule(ctx, in.Env, in.ID); err != nil {
			return nil, apiError(err)
		}
		return nil, apiError(s.Policy.Settle(ctx, in.Env))
	})

	huma.Register(api, huma.Operation{
		OperationID: "getTrackers", Method: http.MethodGet, Path: "/api/environments/{env}/trackers", Tags: []string{"network"},
		Description: "The built-in tracker list and whether the environment's browsers are denied it.",
	}, func(ctx context.Context, in *envPath) (*struct{ Body trackersView }, error) {
		v, err := s.trackers(ctx, in.Env)
		return &struct{ Body trackersView }{v}, err
	})

	type setTrackersIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		Body struct {
			Block bool `json:"block" doc:"Deny the listed domains to the environment's browsers unless a rule allows them"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "setTrackers", Method: http.MethodPut, Path: "/api/environments/{env}/trackers", Tags: []string{"network"},
	}, func(ctx context.Context, in *setTrackersIn) (*struct{ Body trackersView }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		if err := s.Store.SetBlockTrackers(ctx, in.Env, in.Body.Block); err != nil {
			return nil, apiError(err)
		}
		v, err := s.trackers(ctx, in.Env)
		return &struct{ Body trackersView }{v}, err
	})

	type setLevelIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		Body struct {
			Level string `json:"level" enum:"locked,guarded,assisted,open" doc:"What happens when no rule matches: locked asks, guarded allows the curated list and asks, assisted also asks an AI reviewer, open allows public addresses"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "setSecurityLevel", Method: http.MethodPut, Path: "/api/environments/{env}/security-level", Tags: []string{"network"},
		Description: "Sets the environment's security level. It applies to the connections made after the change; approvals already waiting stay as they are. Teammates can override it.",
	}, func(ctx context.Context, in *setLevelIn) (*struct{ Body store.Environment }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		if err := s.Store.SetSecurityLevel(ctx, in.Env, in.Body.Level); err != nil {
			return nil, apiError(err)
		}
		e, err := s.Store.Environment(ctx, in.Env)
		return &struct{ Body store.Environment }{e}, apiError(err)
	})

	type setTeammateLevelIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		ID   string `path:"id" doc:"Teammate ID"`
		Body struct {
			Level string `json:"level" doc:"The level of the teammate's sandboxes, one of locked, guarded, assisted or open; empty hands the teammate back to the environment's level"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "setTeammateSecurityLevel", Method: http.MethodPut, Path: "/api/environments/{env}/teammates/{id}/security-level", Tags: []string{"network"},
		Description: "Overrides the environment's security level for one teammate's sandboxes, or clears the override.",
	}, func(ctx context.Context, in *setTeammateLevelIn) (*struct{ Body store.Teammate }, error) {
		if in.Body.Level != "" && !slices.Contains(store.Levels, in.Body.Level) {
			return nil, huma.Error422UnprocessableEntity(fmt.Sprintf("%q is not a security level", in.Body.Level))
		}
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		t, err := s.Store.SetTeammateSecurityLevel(ctx, in.Env, in.ID, in.Body.Level)
		if err != nil {
			return nil, apiError(err)
		}
		s.publishTeammates(in.Env)
		return &struct{ Body store.Teammate }{t}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "getCurated", Method: http.MethodGet, Path: "/api/environments/{env}/curated", Tags: []string{"network"},
		Description: "The curated list of development infrastructure, shipped with Studio, and the entries this environment turned off.",
	}, func(ctx context.Context, in *envPath) (*struct{ Body curatedView }, error) {
		v, err := s.curated(ctx, in.Env)
		return &struct{ Body curatedView }{v}, err
	})

	type setCuratedIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		Body struct {
			Disabled []string `json:"disabled" maxItems:"64" doc:"The IDs of the curated entries this environment turns off. The whole list, which replaces the current one"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "setCurated", Method: http.MethodPut, Path: "/api/environments/{env}/curated", Tags: []string{"network"},
		Description: "Turns curated entries off or on for the environment. A turned-off entry is asked about like any other host.",
	}, func(ctx context.Context, in *setCuratedIn) (*struct{ Body curatedView }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		_, entries := policy.CuratedList()
		off := []string{}
		for _, id := range in.Body.Disabled {
			if !slices.ContainsFunc(entries, func(e policy.CuratedEntry) bool { return e.ID == id }) {
				return nil, huma.Error422UnprocessableEntity(fmt.Sprintf("%q is not an entry of the curated list", id))
			}
			if !slices.Contains(off, id) {
				off = append(off, id)
			}
		}
		slices.Sort(off)
		if err := s.Store.SetCuratedOff(ctx, in.Env, off); err != nil {
			return nil, apiError(err)
		}
		v, err := s.curated(ctx, in.Env)
		return &struct{ Body curatedView }{v}, err
	})

	huma.Register(api, huma.Operation{
		OperationID: "listConnections", Method: http.MethodGet, Path: "/api/environments/{env}/sandboxes/{id}/connections", Tags: []string{"network"},
		Description: "The sandbox's most recent connections, newest first. The log is kept in memory only.",
	}, func(ctx context.Context, in *sandboxPath) (*struct{ Body []gateway.Conn }, error) {
		if _, err := s.publicSandbox(ctx, in.Env, in.ID); err != nil {
			return nil, apiError(err)
		}
		return &struct{ Body []gateway.Conn }{s.Conns.List(in.ID)}, nil
	})
}

// reviewerView says whether the AI reviewer of the assisted level can answer, and with which model.
type reviewerView struct {
	Available bool   `json:"available" doc:"Whether the assisted level asks the AI reviewer; without a utility model it asks the user, as guarded does"`
	Provider  string `json:"provider,omitempty" doc:"The provider whose utility model answers"`
	Model     string `json:"model,omitempty" doc:"The utility model that answers"`
}

// withReach fills in r.Unreachable.
func (s *Server) withReach(ctx context.Context, r store.Rule) store.Rule {
	if s.Reach != nil {
		r.Unreachable = s.Reach.Unreachable(ctx, r)
	}
	return r
}

func (s *Server) reviewer(ctx context.Context, env string) (reviewerView, error) {
	prov, ok, err := agentmem.EnvironmentUtility(ctx, s.Store, env)
	if err != nil {
		return reviewerView{}, err
	}
	if !ok {
		return reviewerView{}, nil
	}
	m, _ := agentmem.UtilityFor(prov)
	return reviewerView{Available: true, Provider: prov.Name, Model: m.Model}, nil
}

// curatedView is the curated list, with the environment's switch for each entry.
type curatedView struct {
	Version string             `json:"version" doc:"The version of the curated list"`
	Entries []curatedEntryView `json:"entries"`
}

type curatedEntryView struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Group   string   `json:"group"`
	Hosts   []string `json:"hosts"`
	Ports   []int    `json:"ports"`
	Enabled bool     `json:"enabled" doc:"Whether the guarded and assisted levels allow the entry's hosts"`
}

func (s *Server) curated(ctx context.Context, env string) (curatedView, error) {
	e, err := s.Store.Environment(ctx, env)
	if err != nil {
		return curatedView{}, apiError(err)
	}
	version, entries := policy.CuratedList()
	v := curatedView{Version: version, Entries: make([]curatedEntryView, 0, len(entries))}
	for _, c := range entries {
		v.Entries = append(v.Entries, curatedEntryView{ID: c.ID, Name: c.Name, Group: c.Group, Hosts: c.Hosts, Ports: c.Ports, Enabled: !slices.Contains(e.CuratedOff, c.ID)})
	}
	return v, nil
}

// trackersView is the built-in tracker list and the environment's switch for it.
type trackersView struct {
	Version string   `json:"version" doc:"The version of the built-in tracker list"`
	Block   bool     `json:"block" doc:"Whether the environment's browsers are denied the listed domains"`
	Domains []string `json:"domains" doc:"The listed domains, sorted; an entry also covers its subdomains"`
}

func (s *Server) trackers(ctx context.Context, env string) (trackersView, error) {
	e, err := s.Store.Environment(ctx, env)
	if err != nil {
		return trackersView{}, apiError(err)
	}
	version, domains := policy.TrackerList()
	return trackersView{Version: version, Block: e.BlockTrackers, Domains: domains}, nil
}

// streamEvents sends change notifications as server-sent events; clients refetch what changed.
// The bus drops a subscriber that falls behind, which ends the stream so the client reconnects.
func (s *Server) streamEvents(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	ch, unsubscribe := s.Bus.Subscribe()
	defer unsubscribe()
	rc := http.NewResponseController(w)
	fmt.Fprint(w, "retry: 2000\n\n")
	if rc.Flush() != nil {
		return
	}
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
		case e, ok := <-ch:
			if !ok {
				return
			}
			data, _ := json.Marshal(e)
			fmt.Fprintf(w, "data: %s\n\n", data)
		}
		if rc.Flush() != nil {
			return
		}
	}
}
