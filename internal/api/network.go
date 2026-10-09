package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/textproto"
	"slices"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"golang.org/x/net/http/httpguts"

	"github.com/lukaskoebe/sandbox-studio/internal/gateway"
	"github.com/lukaskoebe/sandbox-studio/internal/policy"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// ApprovalView is an approval with the details of its request decoded from the payload.
type ApprovalView struct {
	store.Approval
	Network *policy.NetworkRequest `json:"network,omitempty"`
}

func approvalView(a store.Approval) ApprovalView {
	v := ApprovalView{Approval: a}
	if a.Kind == policy.KindNetwork {
		var req policy.NetworkRequest
		if json.Unmarshal(a.Payload, &req) == nil {
			v.Network = &req
		}
	}
	return v
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
		out[i] = approvalView(a)
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
	Host      string          `json:"host" minLength:"1" doc:"example.com, *.example.com (includes example.com), or an IP address"`
	Ports     []int           `json:"ports,omitempty" minimum:"1" maximum:"65535" doc:"Empty or omitted means any port"`
	Action    string          `json:"action" enum:"allow,proxy,deny" doc:"allow passes connections through untouched; proxy lets Studio handle the HTTP requests, to set headers"`
	Config    RuleConfigInput `json:"config,omitempty"`
	SandboxID string          `json:"sandboxId,omitempty" doc:"Limits the rule to one sandbox of this environment"`
	Note      string          `json:"note,omitempty" maxLength:"500"`
}

// RuleConfigInput is the editable part of a rule's config.
type RuleConfigInput struct {
	Headers map[string]string `json:"headers,omitempty" maxProperties:"32" doc:"proxy rules only: headers set on every request, replacing the sandbox's. Values may reference secrets as {secret.NAME}, which are only sent over HTTPS to hosts the secret is bound to."`
}

// headersNotSet are managed by the HTTP machinery or describe the connection, not the
// request, so a rule must not set them.
var headersNotSet = []string{
	"Host", "Content-Length", "Transfer-Encoding", "Connection", "Keep-Alive", "Proxy-Connection",
	"Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Upgrade",
}

// ruleConfigFrom validates the config of a rule with action in environment env.
func (s *Server) ruleConfigFrom(ctx context.Context, env, action string, in RuleConfigInput) (store.RuleConfig, error) {
	if len(in.Headers) == 0 {
		return store.RuleConfig{}, nil
	}
	if action != store.ActionProxy {
		return store.RuleConfig{}, huma.Error422UnprocessableEntity("only proxy rules set headers")
	}
	secrets, err := s.Store.Secrets(ctx, env)
	if err != nil {
		return store.RuleConfig{}, apiError(err)
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
	return store.RuleConfig{Headers: headers}, nil
}

// ruleFrom validates in and returns the rule it describes in environment env.
func (s *Server) ruleFrom(ctx context.Context, env string, in RuleInput) (store.Rule, error) {
	host, err := policy.ValidPattern(in.Host)
	if err != nil {
		return store.Rule{}, apiError(err)
	}
	if in.SandboxID != "" {
		_, err := s.Store.Sandbox(ctx, env, in.SandboxID)
		if errors.Is(err, store.ErrNotFound) {
			return store.Rule{}, huma.Error422UnprocessableEntity(fmt.Sprintf("sandbox %s is not in this environment", in.SandboxID))
		}
		if err != nil {
			return store.Rule{}, apiError(err)
		}
	}
	config, err := s.ruleConfigFrom(ctx, env, in.Action, in.Config)
	if err != nil {
		return store.Rule{}, err
	}
	return store.Rule{EnvironmentID: env, SandboxID: in.SandboxID, Host: host, Ports: in.Ports, Action: in.Action, Config: config, Note: in.Note}, nil
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

	type decideIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		ID   string `path:"id" doc:"Approval ID"`
		Body struct {
			Action string `json:"action" enum:"allow,deny,dismiss" doc:"allow or deny creates a rule; dismiss only closes the request"`
			Host   string `json:"host,omitempty" doc:"Host pattern the rule covers; empty means the requested host"`
			Ports  []int  `json:"ports,omitempty" minimum:"1" maximum:"65535" doc:"Ports the rule covers; omitted means the default ports for the request, empty means any port"`
			Scope  string `json:"scope,omitempty" enum:"sandbox,environment" default:"environment" doc:"Whether the rule covers only the requesting sandbox"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "decideApproval", Method: http.MethodPost, Path: "/api/environments/{env}/approvals/{id}/decide", Tags: []string{"approvals"},
	}, func(ctx context.Context, in *decideIn) (*struct{ Body ApprovalView }, error) {
		d := policy.Decision{Action: in.Body.Action, Host: in.Body.Host, Ports: in.Body.Ports, Scope: in.Body.Scope}
		a, err := s.Policy.Resolve(ctx, in.Env, in.ID, d)
		if err != nil {
			return nil, apiError(err)
		}
		return &struct{ Body ApprovalView }{approvalView(a)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "listRules", Method: http.MethodGet, Path: "/api/environments/{env}/rules", Tags: []string{"network"},
	}, func(ctx context.Context, in *envPath) (*struct{ Body []store.Rule }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		list, err := s.Store.Rules(ctx, in.Env)
		return &struct{ Body []store.Rule }{list}, apiError(err)
	})

	type createRuleIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		Body RuleInput
	}
	huma.Register(api, huma.Operation{
		OperationID: "createRule", Method: http.MethodPost, Path: "/api/environments/{env}/rules", Tags: []string{"network"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *createRuleIn) (*struct{ Body store.Rule }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		r, err := s.ruleFrom(ctx, in.Env, in.Body)
		if err != nil {
			return nil, err
		}
		if r, err = s.Store.CreateRule(ctx, r); err != nil {
			return nil, apiError(err)
		}
		if err := s.Policy.Settle(ctx, in.Env); err != nil {
			return nil, apiError(err)
		}
		return &struct{ Body store.Rule }{r}, nil
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
		return &struct{ Body store.Rule }{r}, nil
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
		OperationID: "listConnections", Method: http.MethodGet, Path: "/api/environments/{env}/sandboxes/{id}/connections", Tags: []string{"network"},
		Description: "The sandbox's most recent connections, newest first. The log is kept in memory only.",
	}, func(ctx context.Context, in *sandboxPath) (*struct{ Body []gateway.Conn }, error) {
		if _, err := s.Store.Sandbox(ctx, in.Env, in.ID); err != nil {
			return nil, apiError(err)
		}
		return &struct{ Body []gateway.Conn }{s.Conns.List(in.ID)}, nil
	})
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
