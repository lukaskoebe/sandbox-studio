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
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/autostart"
	"github.com/lukaskoebe/sandbox-studio/internal/doctor"
	"github.com/lukaskoebe/sandbox-studio/internal/gitreview"
	"github.com/lukaskoebe/sandbox-studio/internal/paths"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// doctorCheck runs the host checks. Studio's own listeners count as healthy when owned.
// The SSH agent is checked when agentForges SSH forges sign with it.
func doctorCheck(ctx context.Context, dataDir, image, addr string, owned bool, agentForges int) doctor.Report {
	p := hostProbes()
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
	r := doctorCheck(ctx, p.Data, image, addr, studioRunning(ctx, addr), agentForges)
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
		fmt.Fprintf(w, "autostart is on (%s), but it starts a different binary; run `studio autostart enable` to update it\n", st.Path)
	default:
		fmt.Fprintf(w, "autostart is on: %s starts %s at login\n", st.Path, st.Command)
	}
	return 0
}
