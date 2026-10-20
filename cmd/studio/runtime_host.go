//go:build !e2e

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentbin"
	"github.com/lukaskoebe/sandbox-studio/internal/doctor"
	"github.com/lukaskoebe/sandbox-studio/internal/gateway"
	"github.com/lukaskoebe/sandbox-studio/internal/msbdb"
	"github.com/lukaskoebe/sandbox-studio/internal/policy"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// The production side of the seams the e2e build (runtime_e2e.go) fills with fakes.

func installGuestAgent(dir, arch string) error { return agentbin.Install(dir, arch) }

func ensureRuntime(ctx context.Context) error { return runtime.Ensure(ctx) }

func newRuntime(opts runtime.Options, _ *slog.Logger) studioRuntime { return runtime.New(opts) }

func hostProbes() doctor.Probes {
	p := doctor.HostProbes()
	p.MSBPath = runtime.MSBPath
	p.ImageCached = runtime.ImageCached
	p.MSBDB = checkMSBDB
	return p
}

// msbRepaired describes the repair repairMSBDB made when Studio started, if it made one.
var msbRepaired string

// repairMSBDB checks msb's database before any msb call opens it. A damaged one is moved
// aside and rebuilt from the rows that can still be read; then msb indexes the snapshots on
// disk again, so the checkpoints are found.
func repairMSBDB(ctx context.Context, log *slog.Logger) {
	path, err := msbdb.Path()
	if err != nil {
		return
	}
	var d *msbdb.Damaged
	if err := msbdb.Check(ctx, path); !errors.As(err, &d) {
		if err != nil {
			log.Warn("could not check microsandbox's database", "path", path, "err", err)
		}
		return
	}
	log.Error("microsandbox's database is damaged; repairing it", "path", path, "problem", d.Detail)
	r, err := msbdb.Fix(ctx, path, time.Now())
	if err != nil {
		log.Error("could not repair microsandbox's database", "path", path, "err", err)
		return
	}
	msbRepaired = r.Summary()
	if n, err := runtime.ReindexSnapshots(ctx); err != nil {
		log.Error("msb could not index its snapshots again", "err", err)
		msbRepaired += "; indexing the checkpoints again failed: " + err.Error()
	} else {
		msbRepaired += fmt.Sprintf("; msb indexed %d snapshots again", n)
	}
	log.Warn("repaired microsandbox's database", "aside", r.Aside, "salvaged", r.Salvaged, "rows", r.Rows,
		"partial", r.Partial, "summary", msbRepaired)
}

// checkMSBDB is the doctor's probe of msb's database: its integrity, then whether msb can
// read it.
func checkMSBDB(ctx context.Context) (problem, repaired, fix string) {
	if path, err := msbdb.Path(); err == nil {
		var d *msbdb.Damaged
		if err := msbdb.Check(ctx, path); errors.As(err, &d) {
			return d.Error(), "", runtime.RepairOnRestart
		}
	}
	var ce *runtime.CatalogError
	if err := runtime.CheckCatalog(ctx); errors.As(err, &ce) {
		return ce.Err.Error(), "", ce.Fix
	}
	return "", msbRepaired, ""
}

func sandboxEgress(gw *gateway.Gateway) sandboxes.Egress { return gw }

// upstreamDial is the gateway's dialer; nil is its default, public addresses only.
func upstreamDial() gatewayDial { return nil }

// forgeClient is the git remote's client for forges; nil is its default, public addresses
// only.
func forgeClient(string) *http.Client { return nil }

// modelsClient lists the models of OpenAI-compatible endpoints; nil is its default, public
// addresses only.
func modelsClient() *http.Client { return nil }

// registerControl adds the e2e control API to the API mux; production has none.
func registerControl(*http.ServeMux, *store.Store, *policy.Engine) {}
