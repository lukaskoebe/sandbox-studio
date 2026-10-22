package runtime

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

// Upgrade is a replacement of the installed msb by the release the SDK was built against.
type Upgrade struct {
	From, To string
}

// UpgradeError is an upgrade of msb that failed. The old msb is still in place: Studio can
// serve, but the SDK won't launch VMs on it.
type UpgradeError struct {
	From, To string
	Err      error
}

func (e *UpgradeError) Error() string {
	return fmt.Sprintf("upgrade msb %s to %s: %v", e.From, e.To, e.Err)
}

func (e *UpgradeError) Unwrap() error { return e.Err }

// installer is what Ensure needs from the SDK and the installed msb, so tests can fake it.
type installer struct {
	ensure  func(context.Context) (msb.ResolvedRuntime, error)
	version func(ctx context.Context, msbPath string) (string, error)
	install func(ctx context.Context, version string) error
	sdk     string
}

// Ensure installs the msb runtime when it is missing, and replaces one in msb's home that is
// older than the release the SDK was built against: the SDK refuses to launch VMs on an
// older msb. It returns the upgrade it made, if any, and a failed upgrade as an *UpgradeError.
// It is idempotent.
//
// A newer msb is left alone, and so is one the environment or configuration points at; the
// doctor reports the mismatch. The install renames the new msb and libkrunfw over the old
// ones, so VMs that run on the old msb keep running, and boot on the new one next time.
func Ensure(ctx context.Context) (*Upgrade, error) {
	return installer{
		ensure: func(ctx context.Context) (msb.ResolvedRuntime, error) {
			return msb.EnsureRuntime(ctx, msb.RuntimeConfig{}, msb.InstallOptions{})
		},
		version: installedVersion,
		install: func(ctx context.Context, version string) error {
			_, err := msb.InstallRuntime(ctx, msb.RuntimeConfig{}, msb.InstallOptions{Version: version, Force: true})
			return err
		},
		sdk: msb.SDKVersion(),
	}.run(ctx)
}

func (in installer) run(ctx context.Context) (*Upgrade, error) {
	r, err := in.ensure(ctx)
	if err != nil {
		return nil, err
	}
	if r.Origin != msb.RuntimeOriginHome && r.Origin != msb.RuntimeOriginInstalled {
		return nil, nil
	}
	want := strings.TrimPrefix(in.sdk, "v")
	have, err := in.version(ctx, r.MSBPath)
	if err != nil {
		return nil, fmt.Errorf("msb at %s: %w", r.MSBPath, err)
	}
	if !older(have, want) {
		return nil, nil
	}
	if err := in.install(ctx, want); err != nil {
		return nil, &UpgradeError{From: have, To: want, Err: err}
	}
	return &Upgrade{From: have, To: want}, nil
}

// installedVersion runs `msb --version`, which prints "msb 0.7.7".
func installedVersion(ctx context.Context, msbPath string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, msbPath, "--version").Output()
	if err != nil {
		return "", err
	}
	v := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(out)), "msb"))
	if v == "" {
		return "", fmt.Errorf("`msb --version` printed %q", out)
	}
	return v, nil
}

// older reports whether release a comes before release b. Both are dotted numbers, maybe with
// a pre-release suffix after "-", which counts as before the release itself. A version that
// doesn't parse is never older: Studio doesn't replace what it can't compare.
func older(a, b string) bool {
	an, apre, aok := parseVersion(a)
	bn, bpre, bok := parseVersion(b)
	if !aok || !bok {
		return false
	}
	for i := range an {
		if an[i] != bn[i] {
			return an[i] < bn[i]
		}
	}
	return apre && !bpre
}

func parseVersion(v string) (nums [3]int, pre, ok bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexByte(v, '-'); i >= 0 {
		v, pre = v[:i], true
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return nums, false, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nums, false, false
		}
		nums[i] = n
	}
	return nums, pre, true
}
