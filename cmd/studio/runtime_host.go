//go:build !e2e

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentbin"
	"github.com/lukaskoebe/sandbox-studio/internal/doctor"
	"github.com/lukaskoebe/sandbox-studio/internal/gateway"
	"github.com/lukaskoebe/sandbox-studio/internal/hostlogins"
	"github.com/lukaskoebe/sandbox-studio/internal/msbdb"
	"github.com/lukaskoebe/sandbox-studio/internal/paths"
	"github.com/lukaskoebe/sandbox-studio/internal/policy"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/sshconfig"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// The production side of the seams the e2e build (runtime_e2e.go) fills with fakes.

func installGuestAgent(dir, arch string) error { return agentbin.Install(dir, arch) }

func ensureRuntime(ctx context.Context) (*runtime.Upgrade, error) { return runtime.Ensure(ctx) }

func newRuntime(opts runtime.Options, _ *slog.Logger) studioRuntime { return runtime.New(opts) }

// msbVMs is vmHost on the real microsandbox.
type msbVMs struct{ rt *runtime.Runtime }

func hostVMs() vmHost { return msbVMs{runtime.New(runtime.Options{})} }

func (m msbVMs) names(ctx context.Context) ([]string, error) {
	statuses, err := m.rt.Statuses(ctx, "")
	if err != nil {
		return nil, err
	}
	return slices.Sorted(maps.Keys(statuses)), nil
}

func (m msbVMs) stop(ctx context.Context, name string) error   { return m.rt.Stop(ctx, name) }
func (m msbVMs) remove(ctx context.Context, name string) error { return m.rt.Remove(ctx, name) }
func (m msbVMs) removeCheckpoints(ctx context.Context, sandboxIDs []string) error {
	return runtime.RemoveCheckpoints(ctx, sandboxIDs)
}

func hostProbes() doctor.Probes {
	p := doctor.HostProbes()
	p.MSBPath = runtime.MSBPath
	p.ImageCached = runtime.ImageCached
	p.MSBDB = checkMSBDB
	return p
}

// msbRepaired describes the repair repairMSBDB made when Studio started, if it made one;
// msbHeldFix says what to do when it couldn't, because other processes had the database open.
var msbRepaired, msbHeldFix string

// lockMSB takes a lock in microsandbox's home for this Studio. Two Studios with different
// data directories share it otherwise, and one repairing msb's database while the other uses
// it damages the database.
func lockMSB() (release func() error, dir string, err error) {
	path, err := msbdb.Path()
	if err != nil {
		return nil, "", err
	}
	dir = filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, dir, err
	}
	release, err = store.TryLock(filepath.Join(dir, "studio.lock"))
	return release, dir, err
}

// repairMSBDB checks msb's database before any msb call opens it. A damaged one is moved
// aside and rebuilt from the rows that can still be read; then msb indexes the snapshots on
// disk again, so the checkpoints are found.
// disksEvery is how often Studio asks the guests how full their disks are: 0 is the
// sandbox manager's minute.
const disksEvery = 0

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
	// VMs outlive Studio, and Studio's SSH proxies and msb commands open the database too.
	// Moving it aside under them damages the new one: SQLite there deletes the -wal and -shm
	// files at the old paths when it closes them, which by then are the new database's.
	if held := msbHeld(runningVMs(sandboxes.VMPrefix), msbHolders(path)); held != "" {
		msbHeldFix = held + " Then start Studio again: it moves the damaged database aside, keeping it, and rebuilds it from what it can still read."
		log.Error("microsandbox's database is damaged, but other processes have it open, so Studio leaves it as it is", "path", path, "problem", d.Detail, "held", held)
		return
	}
	log.Error("microsandbox's database is damaged; repairing it", "path", path, "problem", d.Detail)
	r, err := msbdb.Fix(ctx, path, time.Now())
	if err != nil {
		log.Error("could not repair microsandbox's database", "path", path, "err", err)
		return
	}
	msbRepaired = r.Summary()
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
			if msbHeldFix != "" {
				return d.Error(), "", msbHeldFix
			}
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

// lostVMsProbe is the doctor's comparison of Studio's sandboxes, from list, with the VMs msb's
// catalog has.
func lostVMsProbe(list func(context.Context) ([]store.Sandbox, error)) func(context.Context) ([]doctor.LostVM, error) {
	return func(ctx context.Context) ([]doctor.LostVM, error) {
		sbs, err := list(ctx)
		if err != nil {
			return nil, err
		}
		if len(sbs) == 0 {
			return nil, nil
		}
		statuses, err := runtime.New(runtime.Options{}).Statuses(ctx, sandboxes.VMPrefix)
		if err != nil {
			return nil, err
		}
		return findLostVMs(sbs, statuses, runtime.LostDisks), nil
	}
}

func sandboxEgress(gw *gateway.Gateway) sandboxes.Egress { return gw }

func serveSSH(ctx context.Context, vm string, keys runtime.SSHKeys) error {
	return runtime.ServeSSH(ctx, vm, keys)
}

func sshUserConfig(paths.Paths) (string, error) { return sshconfig.DefaultUserConfig() }

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

// msbBackupEvery is how often Studio looks whether msb's catalog changed since its last copy,
// and msbBackupsKept how many copies it keeps.
const (
	msbBackupEvery = 5 * time.Minute
	msbBackupsKept = 10
)

// msbBackuper keeps copies of msb's catalog next to it, so a catalog damaged later can be
// restored with its sandboxes' current VMs: `studio restore-msb-db` picks the copy that
// knows the most of them.
type msbBackuper struct{ soon chan struct{} }

// Soon asks for a copy now, after a change of a sandbox's VM.
func (b *msbBackuper) Soon() {
	select {
	case b.soon <- struct{}{}:
	default:
	}
}

// backupMSBDB copies msb's catalog whenever it changed, every msbBackupEvery or when asked.
func backupMSBDB(ctx context.Context, log *slog.Logger) *msbBackuper {
	b := &msbBackuper{soon: make(chan struct{}, 1)}
	path, err := msbdb.Path()
	if err != nil {
		return b
	}
	go func() {
		var copied time.Time // when the catalog had last changed at the last copy
		tick := time.NewTicker(msbBackupEvery)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			case <-b.soon:
			}
			changed := msbdb.Changed(path)
			if changed.IsZero() || !changed.After(copied) {
				continue
			}
			if out, err := msbdb.Backup(ctx, path, time.Now(), msbBackupsKept); err != nil {
				log.Warn("could not back up microsandbox's database", "path", path, "err", err)
			} else {
				copied = changed
				log.Debug("backed up microsandbox's database", "copy", out)
			}
		}
	}()
	return b
}
