//go:build e2e

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/doctor"
	"github.com/lukaskoebe/sandbox-studio/internal/e2e"
	"github.com/lukaskoebe/sandbox-studio/internal/gateway"
	"github.com/lukaskoebe/sandbox-studio/internal/hostlogins"
	"github.com/lukaskoebe/sandbox-studio/internal/paths"
	"github.com/lukaskoebe/sandbox-studio/internal/policy"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// The e2e build (`go build -tags e2e`) runs Studio against in-process fakes: no VMs, no
// msb, no guest agent binary and no outbound connections. See internal/e2e.

var fakeRuntime *e2e.Runtime

func installGuestAgent(string, string) error { return nil }

// disksEvery has the fake guests' disks, which the tests set, show within a second.
const disksEvery = time.Second

func ensureRuntime(context.Context) (*runtime.Upgrade, error) { return nil, nil }

func repairMSBDB(context.Context, *slog.Logger) {}

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

// serveSSH has no VM to serve.
func serveSSH(context.Context, string, runtime.SSHKeys) error {
	return errors.New("the e2e build has no VMs to serve over SSH")
}

// sshUserConfig keeps SSH access away from the user's own ~/.ssh/config.
func sshUserConfig(p paths.Paths) (string, error) {
	return filepath.Join(p.Data, "e2e-home", ".ssh", "config"), nil
}

func upstreamDial() gatewayDial { return e2e.Dial }

// fakeUpstream stands in for the forges; the control API creates its repositories.
var fakeUpstream *e2e.Upstream

// forgeClient sends every forge request of the git remote to the fake upstream, whose bare
// repositories live in the data directory.
func forgeClient(data string) *http.Client {
	fakeUpstream = e2e.NewUpstream(filepath.Join(data, "e2e-upstream"))
	return fakeUpstream.Client()
}

// modelsClient sends the model listings of OpenAI-compatible providers to the fake upstream,
// which answers every /models with the same two models. It follows forgeClient.
func modelsClient() *http.Client { return fakeUpstream.Client() }

// hostLogins looks in a home of its own in the data directory, which the tests fill: never in
// the home of the user who runs them, and without the CLIs' variables.
func hostLogins(data string) *hostlogins.Finder {
	return &hostlogins.Finder{Home: filepath.Join(data, "e2e-host-home"), Getenv: func(string) string { return "" }}
}

// registerControl serves the control API behind the API's login, like the rest of the API. Its
// requests are decided by the policy, as a sandbox's connections are.
func registerControl(mux *http.ServeMux, st *store.Store, engine *policy.Engine, gw *gateway.Gateway) {
	e2e.Rejected = func(ctx context.Context, sandboxID, host string, names []string) error {
		sb, err := st.LookupSandbox(ctx, sandboxID)
		if err != nil {
			return err
		}
		gw.Rejected(sb, host, names)
		return nil
	}
	e2e.Requests = func(ctx context.Context, sandboxID, host string, port int, req policy.HTTPRequest) (store.Rule, error) {
		sb, err := st.LookupSandbox(ctx, sandboxID)
		if err != nil {
			return store.Rule{}, err
		}
		return engine.Decide(ctx, policy.Request{EnvironmentID: sb.EnvironmentID, SandboxID: sb.ID, SandboxName: sb.Name,
			Host: host, Port: port, Browser: true, Deferred: true, HTTP: &req})
	}
	e2e.SeedChat = func(ctx context.Context, chatID string, count int) error {
		for i := 1; i <= count; i++ {
			turn, err := st.StartChatTurn(ctx, chatID, store.OriginChat, "")
			if err != nil {
				return err
			}
			for _, it := range [][2]string{{"user", fmt.Sprintf("Request %d", i)}, {"agent", fmt.Sprintf("Answer %d", i)}} {
				body, _ := json.Marshal(map[string]string{"text": it[1]})
				if _, err := st.AddChatItem(ctx, store.ChatItem{ChatID: chatID, TurnID: turn.ID, Kind: it[0], Body: body}); err != nil {
					return err
				}
			}
			if _, err := st.EndChatTurn(ctx, chatID, turn.ID, "end_turn", nil); err != nil {
				return err
			}
		}
		return nil
	}
	e2e.SetBase = func(ctx context.Context, sandboxID, image, version string) error {
		sb, err := st.LookupSandbox(ctx, sandboxID)
		if err != nil {
			return err
		}
		return st.SetSandboxBase(ctx, sb.EnvironmentID, sb.ID, image, version)
	}
	fakeRuntime.Upstream = fakeUpstream
	mux.Handle(e2e.ControlPrefix, fakeRuntime.Control())
}
