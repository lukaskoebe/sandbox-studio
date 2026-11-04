package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/autostart"
	"github.com/lukaskoebe/sandbox-studio/internal/cli"
	"github.com/lukaskoebe/sandbox-studio/internal/doctor"
	"github.com/lukaskoebe/sandbox-studio/internal/gitreview"
	"github.com/lukaskoebe/sandbox-studio/internal/memory"
	"github.com/lukaskoebe/sandbox-studio/internal/msbdb"
	"github.com/lukaskoebe/sandbox-studio/internal/paths"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// doctorCheck runs the host checks. Studio's own listeners count as healthy when owned.
// The SSH agent is checked when agentForges SSH forges sign with it.
func doctorCheck(ctx context.Context, dataDir, image, addr string, owned bool, agentForges int, embedder *memory.Llama) doctor.Report {
	p := hostProbes()
	if embedder != nil {
		p.Embedder = func() (bool, string, string) {
			var u memory.Unavailable
			if errors.As(embedder.Failure(), &u) {
				return false, u.Err.Error(), u.Fix
			}
			return embedder.Ready(), "", ""
		}
	}
	p.SSHAgent = func(ctx context.Context) (int, string, error) {
		st := gitreview.CheckAgent(ctx)
		if !st.Available {
			return 0, st.Fix, errors.New(st.Message)
		}
		return len(st.Keys), "", nil
	}
	ports := []string{addr}
	if gatewayAddr, registryAddr, err := serviceAddrs(addr); err == nil {
		ports = append(ports, gatewayAddr, registryAddr)
	}
	o := doctor.Options{DataDir: dataDir, Image: image, MSBVersion: runtime.MSBVersion(), Ports: ports, Owned: map[string]bool{},
		AgentForges: agentForges}
	for _, port := range ports {
		o.Owned[port] = owned
	}
	return doctor.Run(ctx, p, o)
}

// logDoctor logs a summary of the first-run check; Studio starts regardless.
func logDoctor(log *slog.Logger, r doctor.Report) {
	problems := r.Problems()
	if len(problems) == 0 {
		log.Info("host checks passed", "checks", len(r.Checks))
		return
	}
	for _, c := range problems {
		log.Warn("host check: "+c.Name, "status", c.Status, "problem", c.Message, "fix", c.Fix)
	}
	log.Warn("some host checks did not pass; Studio starts anyway. Run `studio doctor` or open Settings for details", "problems", len(problems))
}

// runDoctor is `studio doctor`. It exits non-zero if a check fails.
func runDoctor(w io.Writer, addr, image string) int {
	p, err := paths.Default()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	ctx := context.Background()
	// The catalog is only read, so a running Studio keeps it. The agent checked is this
	// shell's; the Git page shows the one the running Studio reaches.
	agentForges, _ := store.CountAgentForges(ctx, p.DB())
	r := doctorCheck(ctx, p.Data, image, addr, studioRunning(ctx, addr), agentForges, nil)
	for _, c := range r.Checks {
		fmt.Fprintf(w, "%-5s %-28s %s\n", c.Status, c.Name, c.Message)
		if c.Fix != "" {
			fmt.Fprintf(w, "%-5s %-28s fix: %s\n", "", "", c.Fix)
		}
	}
	if r.Status == doctor.Fail {
		return 1
	}
	return 0
}

// runningVMs lists the VM processes msb runs, and msbHolders the other processes with msb's
// database open; tests replace them.
var (
	runningVMs = msbdb.RunningVMs
	msbHolders = msbdb.Holders
)

// msbHeld says which processes have msb's database open and how to stop them, or "" when none
// does: msb's VMs by name, other processes by PID.
func msbHeld(vms []msbdb.VM, holders []int) string {
	var list []string
	seen := map[int]bool{}
	for _, vm := range vms {
		list = append(list, fmt.Sprintf("the VM of %s (pid %d)", vm.Name, vm.PID))
		seen[vm.PID] = true
	}
	for _, pid := range holders {
		if !seen[pid] {
			list = append(list, fmt.Sprintf("pid %d", pid))
		}
	}
	if len(list) == 0 {
		return ""
	}
	return fmt.Sprintf("Other processes have microsandbox's database open: %s. Stop them first, for example with kill <pid> "+
		"(a sandbox's disks are kept), and quit any other Studio.", strings.Join(list, ", "))
}

// runRestoreMSBDB rebuilds microsandbox's catalog from one that a repair moved aside, by
// default the newest: a repair that kept none of its rows left msb an empty catalog, and so
// lost its sandboxes, though their disks are kept. The catalog in place is moved aside in
// turn. It refuses while Studio runs, and while the catalog in place knows sandboxes unless
// -replace is given, since those would be lost from msb then.
func runRestoreMSBDB(w io.Writer, addr string, args []string) int {
	replace := false
	var from string
	for _, a := range args {
		switch {
		case a == "-replace" || a == "--replace":
			replace = true
		case strings.HasPrefix(a, "-") || from != "":
			fmt.Fprintf(os.Stderr, "studio restore-msb-db: unexpected %q. Usage: studio restore-msb-db [-replace] [file]\n", a)
			return cli.ExitUsage
		default:
			from = a
		}
	}
	ctx := context.Background()
	if studioRunning(ctx, addr) {
		fmt.Fprintf(os.Stderr, "studio restore-msb-db: Studio is running on %s. Quit it first, then run this again.\n", addr)
		return 1
	}
	// Studio may run on another address; holding its lock keeps one from starting meanwhile.
	if p, err := paths.Default(); err == nil {
		release, err := store.TryLock(filepath.Join(p.Data, "studio.lock"))
		if errors.Is(err, store.ErrLocked) {
			fmt.Fprintf(os.Stderr, "studio restore-msb-db: Studio is running with its data in %s. Quit it first, then run this again.\n", p.Data)
			return 1
		}
		if err == nil {
			defer func() { _ = release() }()
		}
	}
	release, dir, err := lockMSB()
	if errors.Is(err, store.ErrLocked) {
		fmt.Fprintf(os.Stderr, "studio restore-msb-db: another Studio uses microsandbox's home %s. Quit it first, then run this again.\n", filepath.Dir(dir))
		return 1
	}
	if err == nil {
		defer func() { _ = release() }()
	}
	path, err := msbdb.Path()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	// A VM that outlived Studio, or another process, holds the catalog open and writes to it.
	if held := msbHeld(runningVMs(sandboxes.VMPrefix), msbHolders(path)); held != "" {
		fmt.Fprintln(os.Stderr, "studio restore-msb-db: "+held+" Then run this again.")
		return 1
	}
	if from == "" {
		asides, err := msbdb.Asides(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if len(asides) == 0 {
			fmt.Fprintf(os.Stderr, "studio restore-msb-db: there is no damaged database next to %s to restore from.\n", path)
			return 1
		}
		from = asides[0]
	}
	if known, err := msbdb.Sandboxes(ctx, path); err == nil && len(known) > 0 && !replace {
		fmt.Fprintf(os.Stderr, "studio restore-msb-db: %s knows %d sandboxes (%s), which msb would lose. "+
			"Run it with -replace to restore anyway; the database in place is kept aside.\n", path, len(known), strings.Join(known, ", "))
		return 1
	}
	r, err := msbdb.Restore(ctx, path, from, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "studio restore-msb-db:", err)
		return 1
	}
	known, _ := msbdb.Sandboxes(ctx, path)
	fmt.Fprintf(w, "Rebuilt %s from %s: %d rows, %d sandboxes.\n", path, from, r.Rows, len(known))
	if len(r.Partial) > 0 {
		fmt.Fprintf(w, "Rows were lost from %s.\n", strings.Join(r.Partial, ", "))
	}
	if _, err := os.Stat(r.Aside); err == nil {
		fmt.Fprintf(w, "The database that was in place is kept at %s.\n", r.Aside)
	}
	fmt.Fprintln(w, "Start Studio again; the sandboxes start from their disks. If one hangs while starting, stop it and start it again.")
	return 0
}

// studioRunning reports whether a Studio answers on addr, whose ports are then in use by it.
func studioRunning(ctx context.Context, addr string) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/api/auth/status", nil)
	if err != nil {
		return false
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil}}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var body map[string]any
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&body) != nil {
		return false
	}
	_, ok := body["authenticated"]
	return ok
}

// runAutostart is `studio autostart enable|disable|status`.
func runAutostart(w io.Writer, args []string) int {
	svc, err := autostart.Default()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	var st autostart.Status
	switch strings.Join(args, " ") {
	case "enable":
		st, err = svc.Enable()
	case "disable":
		st, err = svc.Disable()
	case "status", "":
		st, err = svc.Status()
	default:
		fmt.Fprintln(os.Stderr, "usage: studio autostart enable|disable|status")
		return 2
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	switch {
	case !st.Enabled:
		fmt.Fprintln(w, "autostart is off")
	case st.Stale:
		fmt.Fprintf(w, "autostart is on (%s), but its entry is out of date; run `studio autostart enable` to update it\n", st.Path)
	default:
		fmt.Fprintf(w, "autostart is on: %s starts %s at login\n", st.Path, st.Command)
	}
	return 0
}
