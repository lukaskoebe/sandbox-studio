//go:build e2e

package main

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/lukaskoebe/sandbox-studio/internal/doctor"
	"github.com/lukaskoebe/sandbox-studio/internal/e2e"
	"github.com/lukaskoebe/sandbox-studio/internal/gateway"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
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

func withControl(h http.Handler) http.Handler { return fakeRuntime.Control(h) }
