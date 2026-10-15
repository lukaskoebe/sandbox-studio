//go:build e2e

package main

import (
	"context"
	"log/slog"
	"net/http"
	"path/filepath"

	"github.com/lukaskoebe/sandbox-studio/internal/doctor"
	"github.com/lukaskoebe/sandbox-studio/internal/e2e"
	"github.com/lukaskoebe/sandbox-studio/internal/gateway"
	"github.com/lukaskoebe/sandbox-studio/internal/policy"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// The e2e build (`go build -tags e2e`) runs Studio against in-process fakes: no VMs, no
// msb, no guest agent binary and no outbound connections. See internal/e2e.

var fakeRuntime *e2e.Runtime

func installGuestAgent(string, string) error { return nil }

func ensureRuntime(context.Context) error { return nil }

func newRuntime(opts runtime.Options, log *slog.Logger) studioRuntime {
	fakeRuntime = e2e.NewRuntime(opts.Image, log)
	return fakeRuntime
}

// hostProbes leaves out msb, and reports the base image as present: the real checks would
// run msb or reach a registry.
func hostProbes() doctor.Probes {
	p := doctor.HostProbes()
	p.ImageCached = func(context.Context, string) (bool, error) { return true, nil }
	p.ImagePullable = nil
	return p
}

func sandboxEgress(gw *gateway.Gateway) sandboxes.Egress { return e2e.Egress{Gateway: gw} }

func upstreamDial() gatewayDial { return e2e.Dial }

// fakeUpstream stands in for the forges; the control API creates its repositories.
var fakeUpstream *e2e.Upstream

// forgeClient sends every forge request of the git remote to the fake upstream, whose bare
// repositories live in the data directory.
func forgeClient(data string) *http.Client {
	fakeUpstream = e2e.NewUpstream(filepath.Join(data, "e2e-upstream"))
	return fakeUpstream.Client()
}

// registerControl serves the control API behind the API's login, like the rest of the API. Its
// requests are decided by the policy, as a sandbox's connections are.
func registerControl(mux *http.ServeMux, st *store.Store, engine *policy.Engine) {
	e2e.Requests = func(ctx context.Context, sandboxID, host string, port int, req policy.HTTPRequest) (store.Rule, error) {
		sb, err := st.LookupSandbox(ctx, sandboxID)
		if err != nil {
			return store.Rule{}, err
		}
		return engine.Decide(ctx, policy.Request{EnvironmentID: sb.EnvironmentID, SandboxID: sb.ID, SandboxName: sb.Name,
			Host: host, Port: port, Browser: true, Deferred: true, HTTP: &req})
	}
	fakeRuntime.Upstream = fakeUpstream
	mux.Handle(e2e.ControlPrefix, fakeRuntime.Control())
}
