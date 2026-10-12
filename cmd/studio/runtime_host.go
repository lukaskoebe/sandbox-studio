//go:build !e2e

package main

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/lukaskoebe/sandbox-studio/internal/agentbin"
	"github.com/lukaskoebe/sandbox-studio/internal/doctor"
	"github.com/lukaskoebe/sandbox-studio/internal/gateway"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
)

// The production side of the seams the e2e build (runtime_e2e.go) fills with fakes.

func installGuestAgent(dir, arch string) error { return agentbin.Install(dir, arch) }

func ensureRuntime(ctx context.Context) error { return runtime.Ensure(ctx) }

func newRuntime(opts runtime.Options, _ *slog.Logger) studioRuntime { return runtime.New(opts) }

func hostProbes() doctor.Probes {
	p := doctor.HostProbes()
	p.MSBPath = runtime.MSBPath
	p.ImageCached = runtime.ImageCached
	return p
}

func sandboxEgress(gw *gateway.Gateway) sandboxes.Egress { return gw }

// upstreamDial is the gateway's dialer; nil is its default, public addresses only.
func upstreamDial() gatewayDial { return nil }

// registerControl adds the e2e control API to the API mux; production has none.
func registerControl(*http.ServeMux) {}
