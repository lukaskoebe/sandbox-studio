package api

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/lukaskoebe/sandbox-studio/internal/gateway"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// registerPrivateHosts adds the environment's private hosts list (docs/network.md). Only the
// owner changes it, in Network settings: approvals, rules, imports and reviewers never do.
func (s *Server) registerPrivateHosts(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "listPrivateHosts", Method: http.MethodGet, Path: "/api/environments/{env}/private-hosts", Tags: []string{"network"},
		Description: "The names whose connections may reach private addresses, such as company hosts behind a VPN.",
	}, func(ctx context.Context, in *envPath) (*struct{ Body []store.PrivateHost }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		list, err := s.Store.PrivateHosts(ctx, in.Env)
		return &struct{ Body []store.PrivateHost }{list}, apiError(err)
	})

	type setPrivateHostsIn struct {
		Env  string `path:"env" doc:"Environment ID"`
		Body struct {
			Hosts []privateHostInput `json:"hosts" maxItems:"64" doc:"The whole list, which replaces the current one"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "setPrivateHosts", Method: http.MethodPut, Path: "/api/environments/{env}/private-hosts", Tags: []string{"network"},
		Description: "Replaces the private hosts list. Only the owner changes it, in Network settings.",
	}, func(ctx context.Context, in *setPrivateHostsIn) (*struct{ Body []store.PrivateHost }, error) {
		if err := s.checkEnvironment(ctx, in.Env); err != nil {
			return nil, err
		}
		list, err := privateHostsFrom(in.Body.Hosts)
		if err != nil {
			return nil, err
		}
		if err := s.Store.SetPrivateHosts(ctx, in.Env, list); err != nil {
			return nil, apiError(err)
		}
		return &struct{ Body []store.PrivateHost }{list}, nil
	})
}

// privateHostInput is an entry of a private hosts list. Ports empty means 80 and 443.
type privateHostInput struct {
	Host  string `json:"host" minLength:"1" maxLength:"253" doc:"A host name or a wildcard, such as git.company.local or *.company.local"`
	Ports []int  `json:"ports,omitempty" maxItems:"64" doc:"Ports the host may be reached on. Empty or omitted means 80 and 443"`
}

// privateHostsFrom checks a private hosts list and returns it normalized and sorted by host.
// Each host is listed once; its ports say where it may be reached.
func privateHostsFrom(in []privateHostInput) ([]store.PrivateHost, error) {
	out := []store.PrivateHost{}
	for _, h := range in {
		v, err := gateway.ValidPrivateHost(store.PrivateHost{Host: h.Host, Ports: h.Ports})
		if err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		if slices.ContainsFunc(out, func(o store.PrivateHost) bool { return o.Host == v.Host }) {
			return nil, huma.Error422UnprocessableEntity(fmt.Sprintf("%s is listed twice; list each host once with all its ports", v.Host))
		}
		out = append(out, v)
	}
	slices.SortFunc(out, func(a, b store.PrivateHost) int { return strings.Compare(a.Host, b.Host) })
	return out, nil
}
