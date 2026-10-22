//go:build !e2e

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentbin"
	"github.com/lukaskoebe/sandbox-studio/internal/doctor"
	"github.com/lukaskoebe/sandbox-studio/internal/gateway"
	"github.com/lukaskoebe/sandbox-studio/internal/hostlogins"
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
	// VMs outlive Studio. One still running keeps writing the damaged file, so the rebuilt
	// database may not match what it does; stopping and starting it settles that.
	running := runningVMs(sandboxes.VMPrefix)
	for _, vm := range running {
		log.Warn("a sandbox's VM is still running while microsandbox's database is repaired", "vm", vm.Name, "pid", vm.PID)
	}
	r, err := msbdb.Fix(ctx, path, time.Now())
	if err != nil {
		log.Error("could not repair microsandbox's database", "path", path, "err", err)
		return
	}
	msbRepaired = r.Summary()
	if len(running) > 0 {
		msbRepaired += fmt.Sprintf("; %d sandboxes kept running through the repair: if one shows as stopped or hangs, stop it and start it again", len(running))
	}
	msbOpened.Store(true)
	if n, err := runtime.ReindexSnapshots(ctx); err != nil {
		log.Error("msb could not index its snapshots again", "err", err)
		msbRepaired += "; indexing the checkpoints again failed: " + err.Error()
	} else {
		msbRepaired += fmt.Sprintf("; msb indexed %d snapshots again", n)
	}
	log.Warn("repaired microsandbox's database", "aside", r.Aside, "salvaged", r.Salvaged, "rows", r.Rows,
		"partial", r.Partial, "summary", msbRepaired)
}

// msbOpened is set once this process's msb library has opened msb's database. From then on
// only msb reads it here: SQLite's locks belong to the process, and Studio's own SQLite
// closing the file would drop the library's locks with it, so that another msb process
// could write the database at the same time and damage it.
var msbOpened atomic.Bool

// checkMSBDB is the doctor's probe of msb's database: its integrity, while this process's
// msb library hasn't opened it yet, then whether msb can read it.
func checkMSBDB(ctx context.Context) (problem, repaired, fix string) {
	if path, err := msbdb.Path(); err == nil && !msbOpened.Load() {
		var d *msbdb.Damaged
		if err := msbdb.Check(ctx, path); errors.As(err, &d) {
			return d.Error(), "", runtime.RepairOnRestart
		}
	}
	msbOpened.Store(true)
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

// hostLogins finds the CLIs' logins in the home directory of the user Studio runs as.
func hostLogins(string) *hostlogins.Finder {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return &hostlogins.Finder{Home: home}
}

// registerControl adds the e2e control API to the API mux; production has none.
func registerControl(*http.ServeMux, *store.Store, *policy.Engine, *gateway.Gateway) {}
