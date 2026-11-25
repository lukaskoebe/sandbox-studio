//go:build linux

package guest

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentproto"
)

// harnessModules is where npm installs packages globally, the harnesses' among them; tests
// point it elsewhere.
var harnessModules = "/usr/local/lib/node_modules"

// npmBinary installs a harness's update; tests point it elsewhere.
var npmBinary = "/usr/local/bin/npm"

const (
	toolsTimeout  = 20 * time.Second
	updateTimeout = 10 * time.Minute
)

// harnessUpdating holds while a harness updates: npm installs one at a time.
var harnessUpdating sync.Mutex

// replyTools answers KindTools: the harnesses' versions and what mise has.
func (a *Agent) replyTools(st net.Conn) error {
	ctx, cancel := context.WithTimeout(context.Background(), toolsTimeout)
	defer cancel()
	return agentproto.WriteJSONLine(st, a.installedTools(ctx))
}

func (a *Agent) installedTools(ctx context.Context) agentproto.Tools {
	t := agentproto.Tools{Harnesses: harnessVersions(harnessModules), Mise: []agentproto.MiseTool{}}
	out, err := a.miseList(ctx)
	if err == nil {
		t.Mise, err = parseMiseList(out)
	}
	if err != nil {
		t.MiseError = err.Error()
	}
	return t
}

// harnessVersions reads the versions of the harnesses' packages in modules.
func harnessVersions(modules string) []agentproto.HarnessVersion {
	out := []agentproto.HarnessVersion{}
	for _, h := range slices.Sorted(maps.Keys(agentproto.HarnessPackages)) {
		if v := packageVersion(modules, agentproto.HarnessPackages[h]); v != "" {
			out = append(out, agentproto.HarnessVersion{Harness: h, Version: v})
		}
	}
	return out
}

func packageVersion(modules, pkg string) string {
	b, err := os.ReadFile(filepath.Join(modules, filepath.FromSlash(pkg), "package.json"))
	if err != nil {
		return ""
	}
	var p struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(b, &p) != nil || len(p.Version) > 64 {
		return ""
	}
	return p.Version
}

// miseList runs `mise ls --json` as the agent user, in its home, with the global config and
// toolchains in MiseDir as its login shells have them. It doesn't download anything.
func (a *Agent) miseList(ctx context.Context) ([]byte, error) {
	if _, err := os.Stat(miseBinary); err != nil {
		return nil, errors.New("mise isn't installed")
	}
	cmd := a.command(miseBinary, "ls", "--json")
	cmd.Env = append(cmd.Env,
		"MISE_DATA_DIR="+MiseDir, "MISE_CONFIG_DIR="+MiseDir+"/config",
		"MISE_INSTALLS_DIR="+MiseDir+"/installs", "MISE_SHIMS_DIR="+MiseDir+"/shims",
		"MISE_OFFLINE=1", "MISE_YES=0", "NO_COLOR=1")
	res, err := execRun(ctx, cmd, agentproto.RunRequest{Args: []string{"mise"}, TimeoutSec: int(toolsTimeout / time.Second)})
	switch {
	case err != nil:
		return nil, err
	case res.Truncated:
		return nil, errors.New("mise listed too much")
	case res.ExitCode != 0:
		return nil, fmt.Errorf("mise ls: exit code %d: %s", res.ExitCode, tail(res.Stderr, 512))
	}
	return []byte(res.Stdout), nil
}

// parseMiseList reads `mise ls --json`: each tool's versions, those a config pins with the
// config's path and what it asks for.
func parseMiseList(b []byte) ([]agentproto.MiseTool, error) {
	var raw map[string][]struct {
		Version   string `json:"version"`
		Requested string `json:"requested_version"`
		Source    *struct {
			Path string `json:"path"`
		} `json:"source"`
		Installed *bool `json:"installed"` // older mise lists only what is installed
		Active    bool  `json:"active"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("reading mise ls: %w", err)
	}
	out := []agentproto.MiseTool{}
	for name, versions := range raw {
		for _, v := range versions {
			t := agentproto.MiseTool{
				Name: clip(name, 200), Version: clip(v.Version, 100), Requested: clip(v.Requested, 100),
				Installed: v.Installed == nil || *v.Installed, Active: v.Active,
			}
			if v.Source != nil {
				t.Config = clip(v.Source.Path, 500)
			}
			out = append(out, t)
		}
	}
	slices.SortFunc(out, func(x, y agentproto.MiseTool) int {
		if c := strings.Compare(x.Name, y.Name); c != 0 {
			return c
		}
		return strings.Compare(x.Version, y.Version)
	})
	if len(out) > agentproto.MaxMiseTools {
		out = out[:agentproto.MaxMiseTools]
	}
	return out, nil
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return "…" + s[len(s)-n:]
	}
	return s
}

// updateHarness answers KindHarnessUpdate: npm installs the version over the base image's,
// in /usr/local as root, and the reply lists the tools as they are then. Sessions already
// running keep the version they started with.
func (a *Agent) updateHarness(st net.Conn, br *bufio.Reader) error {
	var req agentproto.HarnessUpdate
	if err := readBody(br, 4<<10, &req); err != nil {
		return replyError(st, err)
	}
	spec, err := harnessSpec(req)
	if err != nil {
		return replyError(st, err)
	}
	if !harnessUpdating.TryLock() {
		return replyError(st, errors.New("a harness is being updated already"))
	}
	defer harnessUpdating.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), updateTimeout)
	defer cancel()
	if err := npmInstall(ctx, spec); err != nil {
		return replyError(st, err)
	}
	pkg := agentproto.HarnessPackages[req.Harness]
	if got := packageVersion(harnessModules, pkg); got != req.Version {
		return replyError(st, fmt.Errorf("npm left %s at %q, not %s", pkg, got, req.Version))
	}
	tctx, tcancel := context.WithTimeout(context.Background(), toolsTimeout)
	defer tcancel()
	return agentproto.WriteJSONLine(st, a.installedTools(tctx))
}

// harnessSpec is the npm package spec of an update: an updatable harness at an exact version.
func harnessSpec(req agentproto.HarnessUpdate) (string, error) {
	if !slices.Contains(agentproto.UpdatableHarnesses, req.Harness) {
		return "", fmt.Errorf("%q can't be updated here", req.Harness)
	}
	if !agentproto.HarnessVersionPattern.MatchString(req.Version) {
		return "", fmt.Errorf("%q is not an exact version", req.Version)
	}
	return agentproto.HarnessPackages[req.Harness] + "@" + req.Version, nil
}

// npmInstall installs spec globally, as the base image does, with a cache of its own that it
// removes after. Its downloads go through the environment's network rules, which trust the
// environment's CA through /etc/environment's NODE_EXTRA_CA_CERTS. No secret is passed.
func npmInstall(ctx context.Context, spec string) error {
	cache, err := os.MkdirTemp("", "studio-npm-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(cache)
	cmd := exec.CommandContext(ctx, npmBinary, "install", "-g", "--no-fund", "--no-audit", "--no-update-notifier", spec)
	cmd.Env = append([]string{
		"HOME=/root", "LANG=C.UTF-8", "NO_COLOR=1",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"npm_config_cache=" + cache,
	}, readEnvironmentFile("/etc/environment")...)
	cmd.Dir = "/"
	cmd.WaitDelay = 5 * time.Second
	var out tailBuffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("npm install %s took longer than %s", spec, updateTimeout)
		}
		return fmt.Errorf("npm install %s: %v: %s", spec, err, tail(out.String(), agentproto.MaxUpdateOutput))
	}
	return nil
}

// tailBuffer keeps the last bytes written to it.
type tailBuffer struct{ b []byte }

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if over := len(t.b) - 4*agentproto.MaxUpdateOutput; over > 0 {
		t.b = append(t.b[:0], t.b[over:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return string(t.b) }
