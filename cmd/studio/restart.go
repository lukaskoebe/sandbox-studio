package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/autostart"
	"github.com/lukaskoebe/sandbox-studio/internal/launcher"
	"github.com/lukaskoebe/sandbox-studio/internal/paths"
)

// defaultAddr is -addr's default, the only address the autostart entry serves on.
const defaultAddr = "127.0.0.1:7878"

// runRestart is `studio restart`: it stops the running Studio, if there is one, and starts this
// binary in its place. The installer runs it, as a Studio keeps running the binary it started
// from until it stops.
func runRestart(ctx context.Context, w io.Writer, addr string) int {
	p, err := paths.Default()
	if err != nil {
		fmt.Fprintln(w, err)
		return 1
	}
	if !holdsData(p) {
		fmt.Fprintln(w, "Studio isn't running, so there is nothing to restart.")
		return 0
	}
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		fmt.Fprintln(w, err)
		return 1
	}
	fmt.Fprintln(w, "Stopping the running Studio; its sandboxes keep running.")
	if err := stopStudio(ctx, p, addr); err != nil {
		fmt.Fprintln(w, err)
		return 1
	}
	if !startService(ctx, addr) {
		if _, err := launcher.StartServer(exe, addr); err != nil {
			fmt.Fprintf(w, "Studio stopped, but didn't start again: %v\n", err)
			return 1
		}
	}
	waited, cancel := context.WithTimeout(ctx, launcher.StartWait)
	defer cancel()
	serving := launcher.HTTPServing(addr)
	for !serving(waited) {
		select {
		case <-waited.Done():
			fmt.Fprintf(w, "Studio started, but doesn't answer on %s yet; see logs/studio.log in %s.\n", addr, p.Data)
			return 1
		case <-time.After(250 * time.Millisecond):
		}
	}
	fmt.Fprintf(w, "Studio restarted on http://%s.\n", addr)
	if len(runningApps(appPaths(exe))) > 0 {
		fmt.Fprintln(w, "The app that is open keeps its old version until you close it and run `studio app`.")
	}
	return 0
}

// startService starts Studio through the service manager when its login entry starts this
// binary. It reports whether that worked; the entry only serves on the default address.
func startService(ctx context.Context, addr string) bool {
	if addr != defaultAddr {
		return false
	}
	svc, err := autostart.Default()
	if err != nil {
		return false
	}
	st, err := svc.Status()
	if err != nil || !st.Enabled || st.Stale {
		return false
	}
	cmd := svc.StartCommand(os.Getuid())
	if cmd == nil {
		return false
	}
	return exec.CommandContext(ctx, cmd[0], cmd[1:]...).Run() == nil
}

// appPaths are the places of the Studio app next to the binary exe.
func appPaths(exe string) []string {
	dir := filepath.Dir(exe)
	return []string{filepath.Join(dir, "studio-app"), filepath.Join(dir, "Sandbox Studio.app", "Contents", "MacOS", "studio-app")}
}

// runningApps returns the processes of this user's that run one of the binaries at paths.
func runningApps(paths []string) []int {
	out, err := exec.Command("pgrep", "-x", "-U", strconv.Itoa(os.Getuid()), "studio-app").Output()
	if err != nil {
		return nil
	}
	var pids []int
	for _, field := range strings.Fields(string(out)) {
		pid, err := strconv.Atoi(field)
		if err != nil {
			continue
		}
		for _, p := range paths {
			if processBinary(pid) == p {
				pids = append(pids, pid)
				break
			}
		}
	}
	return pids
}

// processBinary is the path of the binary the process runs, or "". The binary may have been
// replaced or removed since it started, which Linux marks in the link.
func processBinary(pid int) string {
	if goruntime.GOOS == "linux" {
		link, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/exe")
		if err != nil {
			return ""
		}
		return strings.TrimSuffix(link, " (deleted)")
	}
	out, err := exec.Command("ps", "-o", "comm=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
