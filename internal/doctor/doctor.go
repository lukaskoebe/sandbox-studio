// Package doctor checks that a host can run Studio: the msb runtime, hardware
// virtualization, the base image, the data directory and the loopback ports. Every check
// goes through an injected probe, so the checks are unit-testable on any OS.
//
// The package has no CGO dependencies; the probes that need the microsandbox SDK are
// filled in by cmd/studio.
package doctor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"time"
)

// Status is the outcome of a check.
type Status string

// Check outcomes, from best to worst.
const (
	OK   Status = "ok"
	Warn Status = "warn"
	Fail Status = "fail"
)

func (s Status) rank() int {
	switch s {
	case Fail:
		return 2
	case Warn:
		return 1
	}
	return 0
}

// Check is the result of one check.
type Check struct {
	ID      string `json:"id" doc:"Stable identifier, e.g. msb or kvm"`
	Name    string `json:"name"`
	Status  Status `json:"status" enum:"ok,warn,fail"`
	Message string `json:"message"`
	Fix     string `json:"fix,omitempty" doc:"How to fix a warning or failure"`
}

// Report is the result of a doctor run.
type Report struct {
	Status    Status    `json:"status" enum:"ok,warn,fail" doc:"The worst status of all checks"`
	Checks    []Check   `json:"checks"`
	CheckedAt time.Time `json:"checkedAt"`
}

// Problems returns the checks that are not ok.
func (r Report) Problems() []Check {
	var out []Check
	for _, c := range r.Checks {
		if c.Status != OK {
			out = append(out, c)
		}
	}
	return out
}

// Probes are the doctor's view of the host. A nil probe skips the checks that need it.
type Probes struct {
	GOOS, GOARCH string

	// Embedder reports whether memory's embedding model is loaded, or why it can't load and
	// how to fix that.
	Embedder func() (ready bool, problem, fix string)

	// MSBDB checks msb's own database: problem is what makes it unusable and fix how to fix
	// that; repaired describes a repair Studio made to it when it started.
	MSBDB func(ctx context.Context) (problem, repaired, fix string)

	// MSBPath returns the installed msb executable, or an error if there is none.
	MSBPath func() (string, error)
	// Run runs a program and returns its combined output. A non-zero exit is an error.
	Run func(ctx context.Context, name string, args ...string) (string, error)

	// Stat and Access inspect a device file; Access checks read and write permission.
	Stat   func(path string) (fs.FileInfo, error)
	Access func(path string) error
	// KVMGroup reports whether the user is listed in the kvm group, and whether this
	// process already has that group.
	KVMGroup func() (member, active bool, err error)

	// HVF reports whether Hypervisor.framework is available (macOS).
	HVF func() (bool, error)
	// WHP reports whether the Windows Hypervisor Platform is enabled (Windows).
	WHP func() (bool, error)
	// JobBlocksBreakaway reports whether Studio runs in a Windows job object that doesn't
	// let its child processes leave it (Windows).
	JobBlocksBreakaway func() (bool, error)

	// ImageCached reports whether msb already has the image.
	ImageCached func(ctx context.Context, ref string) (bool, error)
	// ImagePullable checks that the registry serves the image.
	ImagePullable func(ctx context.Context, ref string) error
	// RegistryLogins returns the registry hosts Docker's config has a saved login for.
	RegistryLogins func() ([]string, error)

	// Writable checks that a file can be created in dir.
	Writable func(dir string) error
	// DiskFree returns the bytes available to the user under dir.
	DiskFree func(dir string) (uint64, error)
	// PortFree checks that a TCP address can be listened on.
	PortFree func(addr string) error

	// SSHAgent reports how many keys the host's SSH agent holds, or why Studio can't reach
	// it and how to fix that.
	SSHAgent func(ctx context.Context) (keys int, fix string, err error)
}

// Options are what the checks look at.
type Options struct {
	DataDir    string
	Image      string
	MSBVersion string // the msb version the SDK expects; empty skips the comparison
	// Ports are the loopback addresses Studio listens on. Owned ones are already held by
	// this Studio process and are not probed.
	Ports []string
	Owned map[string]bool
	// AgentForges is the number of SSH forges that sign with the host's SSH agent; with
	// none, the agent isn't checked.
	AgentForges int
}

// LowDisk is the free-space threshold below which the disk check warns.
const LowDisk = 5 << 30

// Run performs every check and returns the report.
func Run(ctx context.Context, p Probes, o Options) Report {
	var checks []Check
	checks = append(checks, checkMSB(ctx, p, o)...)
	if p.MSBDB != nil {
		checks = append(checks, checkMSBDB(ctx, p))
	}
	checks = append(checks, checkVirtualization(p)...)
	if o.Image != "" {
		checks = append(checks, checkImage(ctx, p, o.Image))
	}
	checks = append(checks, checkData(p, o.DataDir)...)
	for _, addr := range o.Ports {
		checks = append(checks, checkPort(p, addr, o.Owned[addr]))
	}
	if o.AgentForges > 0 && p.SSHAgent != nil {
		checks = append(checks, checkSSHAgent(ctx, p, o.AgentForges))
	}
	if p.Embedder != nil {
		checks = append(checks, checkEmbedder(p))
	}
	r := Report{Status: OK, Checks: checks, CheckedAt: time.Now().UTC()}
	for _, c := range checks {
		if c.Status.rank() > r.Status.rank() {
			r.Status = c.Status
		}
	}
	return r
}

// checkEmbedder reports memory's embedding model. One that can't load is a warning: search
// still works, by keywords.
func checkEmbedder(p Probes) Check {
	c := Check{ID: "embedder", Name: "Memory embedding model", Status: OK}
	ready, problem, fix := p.Embedder()
	switch {
	case problem != "":
		c.Status, c.Message, c.Fix = Warn, problem+"; memory search uses keywords only", fix
	case ready:
		c.Message = "loaded"
	default:
		c.Message = "loads with the first memory that needs it"
	}
	return c
}

// checkMSBDB reports msb's database. An unusable one fails: no sandbox can be listed,
// started or created. One Studio repaired is a warning, as the repair may have lost rows.
func checkMSBDB(ctx context.Context, p Probes) Check {
	c := Check{ID: "msb-db", Name: "microsandbox database", Status: OK, Message: "passed its integrity check"}
	problem, repaired, fix := p.MSBDB(ctx)
	switch {
	case problem != "":
		c.Status, c.Message, c.Fix = Fail, problem, fix
	case repaired != "":
		c.Status, c.Message = Warn, repaired
		c.Fix = "Sandboxes that msb no longer knows show as without a VM; their disks stay in ~/.microsandbox/sandboxes. " +
			"The damaged file stays where the message says until you delete it."
	}
	return c
}

func checkMSB(ctx context.Context, p Probes, o Options) []Check {
	if p.MSBPath == nil {
		return nil
	}
	path, err := p.MSBPath()
	if err != nil {
		return []Check{{ID: "msb", Name: "microsandbox runtime", Status: Warn,
			Message: "msb is not installed yet.",
			Fix:     "Studio installs msb and libkrunfw into ~/.microsandbox when it starts; it needs network access the first time."}}
	}
	version := Check{ID: "msb", Name: "microsandbox runtime", Status: OK, Message: "msb at " + path}
	if p.Run == nil {
		return []Check{version}
	}
	out, err := p.Run(ctx, path, "--version")
	if err != nil {
		version.Status, version.Message = Fail, fmt.Sprintf("msb at %s does not run: %s", path, firstLine(out, err))
		version.Fix = "Delete ~/.microsandbox/bin/msb and start Studio again to reinstall it."
		return []Check{version}
	}
	got := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(out), "msb"))
	version.Message = fmt.Sprintf("msb %s at %s", got, path)
	if o.MSBVersion != "" && got != strings.TrimPrefix(o.MSBVersion, "v") {
		version.Status = Warn
		version.Message += fmt.Sprintf("; Studio is built for %s", o.MSBVersion)
		version.Fix = "Studio upgrades an older msb in ~/.microsandbox when it starts, which needs network access: restart Studio. " +
			"For a newer msb, run `msb self downgrade` to match."
	}
	doctor := Check{ID: "msb-doctor", Name: "msb doctor", Status: OK, Message: "msb's own host checks passed."}
	if out, err := p.Run(ctx, path, "doctor"); err != nil {
		doctor.Status = Fail
		doctor.Message = "msb doctor reported a problem: " + lastLines(out, err, 6)
		doctor.Fix = "Run `msb doctor` for details; `msb doctor --fix` applies the fixes it supports."
	}
	return []Check{version, doctor}
}

func checkVirtualization(p Probes) []Check {
	switch p.GOOS {
	case "linux":
		return []Check{checkKVM(p)}
	case "darwin":
		return []Check{checkHVF(p)}
	case "windows":
		return []Check{checkWHP(p), checkJob(p)}
	}
	return []Check{{ID: "virtualization", Name: "Virtualization", Status: Fail,
		Message: p.GOOS + " is not supported; Studio runs on Linux, macOS and Windows."}}
}

// kvmFix explains the two ways to get access to /dev/kvm.
const kvmFix = "Either log in on the machine's own seat (its local display and keyboard): " +
	"systemd-logind then gives your session an ACL on /dev/kvm, which SSH and other remote sessions do not get. " +
	"Or add yourself to the kvm group with `sudo usermod -aG kvm $USER`, then log out and back in."

func checkKVM(p Probes) Check {
	c := Check{ID: "kvm", Name: "KVM (/dev/kvm)"}
	if p.Stat == nil || p.Access == nil {
		return c.with(Warn, "KVM was not checked.", "")
	}
	if _, err := p.Stat("/dev/kvm"); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return c.with(Fail, "/dev/kvm does not exist, so this host cannot run microVMs.",
				"Enable hardware virtualization (VT-x or AMD-V) in the firmware settings and load the kvm_intel or kvm_amd module. "+
					"Inside a VM, enable nested virtualization.")
		}
		return c.with(Fail, "/dev/kvm cannot be inspected: "+err.Error(), kvmFix)
	}
	if err := p.Access("/dev/kvm"); err == nil {
		return c.with(OK, "/dev/kvm is readable and writable.", "")
	}
	msg := "/dev/kvm exists, but this user cannot read and write it."
	if p.KVMGroup != nil {
		if member, active, err := p.KVMGroup(); err == nil && member && !active {
			return c.with(Fail, msg+" You are in the kvm group, but this session started before you were added.",
				"Log out and back in (or reboot) so the session picks up the kvm group.")
		}
	}
	return c.with(Fail, msg, kvmFix)
}

func checkHVF(p Probes) Check {
	c := Check{ID: "hvf", Name: "Hypervisor.framework"}
	if p.GOARCH != "arm64" {
		return c.with(Fail, "Intel Macs are not supported; microsandbox needs Apple Silicon.", "")
	}
	if p.HVF == nil {
		return c.with(Warn, "Hypervisor.framework was not checked.", "")
	}
	ok, err := p.HVF()
	switch {
	case err != nil:
		return c.with(Warn, "Hypervisor.framework support is unknown: "+err.Error(), "")
	case !ok:
		return c.with(Fail, "Hypervisor.framework is not available on this Mac.",
			"Run Studio on the Mac itself rather than in a VM without nested virtualization.")
	}
	return c.with(OK, "Hypervisor.framework is available.", "")
}

func checkWHP(p Probes) Check {
	c := Check{ID: "whp", Name: "Windows Hypervisor Platform"}
	if p.WHP == nil {
		return c.with(Warn, "The Windows Hypervisor Platform was not checked.", "")
	}
	ok, err := p.WHP()
	switch {
	case err != nil:
		return c.with(Fail, "The Windows Hypervisor Platform is not available: "+err.Error(), whpFix)
	case !ok:
		return c.with(Fail, "The Windows Hypervisor Platform is not enabled.", whpFix)
	}
	return c.with(OK, "The Windows Hypervisor Platform is enabled.", "")
}

// checkJob looks for a job object that keeps sandboxes from starting: msb starts each VM
// detached, breaking away from Studio's job, and Windows refuses that with "Access is denied"
// when the job doesn't allow it. CI runners and some service managers start programs in one.
func checkJob(p Probes) Check {
	c := Check{ID: "job", Name: "Detached sandboxes"}
	if p.JobBlocksBreakaway == nil {
		return c.with(Warn, "Studio's job object was not checked.", "")
	}
	blocked, err := p.JobBlocksBreakaway()
	switch {
	case err != nil:
		return c.with(Warn, "Studio's job object could not be checked: "+err.Error(), "")
	case blocked:
		return c.with(Fail, "Studio runs in a Windows job object that doesn't let sandboxes start detached from it; "+
			"creating or starting one fails with \"Access is denied\".",
			"Start Studio from a normal terminal or the Start menu rather than from the program that started it now "+
				"(a CI runner, a service manager or a tool that confines its children).")
	}
	return c.with(OK, "Sandboxes can start detached from Studio.", "")
}

const whpFix = "Turn on \"Windows Hypervisor Platform\" in Windows Features (optionalfeatures.exe), or run " +
	"`Enable-WindowsOptionalFeature -Online -FeatureName HypervisorPlatform` in an administrator PowerShell, then reboot. " +
	"Virtualization must also be enabled in the firmware."

func checkImage(ctx context.Context, p Probes, ref string) Check {
	c := Check{ID: "image", Name: "Base image"}
	if p.ImageCached != nil {
		if ok, err := p.ImageCached(ctx, ref); err == nil && ok {
			return c.with(OK, ref+" is cached.", "")
		}
	}
	if !hasRegistry(ref) {
		return c.with(Fail, ref+" is not cached and has no registry to pull it from.",
			"Build and load the development image with `make image`.")
	}
	if p.ImagePullable == nil {
		return c.with(Warn, ref+" is not cached yet.", "")
	}
	if err := p.ImagePullable(ctx, ref); err != nil {
		return c.with(Warn, ref+" is not cached and its registry did not answer: "+err.Error(),
			"Check the network connection; the image is pulled when the first sandbox is created.")
	}
	// microsandbox pulls with a saved login when there is one, and an expired one fails
	// the pull of this public image (runtime.StaleLoginError).
	if host := savedLogin(p, ref); host != "" {
		return c.with(Warn, ref+" is not cached yet. Docker has a saved login for "+host+", which the pull uses; if it has expired, the pull fails.",
			"If creating a sandbox says "+host+" refused the login, run `docker logout "+host+"`.")
	}
	return c.with(OK, ref+" is not cached yet; it is pulled when the first sandbox is created.", "")
}

// savedLogin returns ref's registry host if Docker's config has a login for it.
func savedLogin(p Probes, ref string) string {
	if p.RegistryLogins == nil {
		return ""
	}
	host, _, _ := strings.Cut(ref, "/")
	logins, err := p.RegistryLogins()
	if err != nil || !slices.Contains(logins, host) {
		return ""
	}
	return host
}

func checkData(p Probes, dir string) []Check {
	if dir == "" {
		return nil
	}
	var out []Check
	if p.Writable != nil {
		c := Check{ID: "data-dir", Name: "Data directory"}
		if err := p.Writable(dir); err != nil {
			out = append(out, c.with(Fail, dir+" is not writable: "+err.Error(),
				"Fix the directory's ownership and permissions, or point SANDBOX_STUDIO_HOME at a writable directory."))
		} else {
			out = append(out, c.with(OK, dir+" is writable.", ""))
		}
	}
	if p.DiskFree != nil {
		c := Check{ID: "disk", Name: "Free disk space"}
		free, err := p.DiskFree(dir)
		switch {
		case err != nil:
			out = append(out, c.with(Warn, "Free disk space is unknown: "+err.Error(), ""))
		case free < LowDisk:
			out = append(out, c.with(Warn, fmt.Sprintf("Only %s free under %s.", gib(free), dir),
				"Sandboxes, images and checkpoints need several GB. Free some space, or move the data directory with SANDBOX_STUDIO_HOME."))
		default:
			out = append(out, c.with(OK, fmt.Sprintf("%s free.", gib(free)), ""))
		}
	}
	return out
}

func checkPort(p Probes, addr string, owned bool) Check {
	c := Check{ID: "port:" + addr, Name: "Port " + addr}
	if owned {
		return c.with(OK, addr+" is in use by Studio.", "")
	}
	if p.PortFree == nil {
		return c.with(Warn, addr+" was not checked.", "")
	}
	if err := p.PortFree(addr); err != nil {
		return c.with(Fail, addr+" is not free: "+err.Error(),
			"Another program, or another Studio, is listening on it. Stop it; `studio login-url` reaches a Studio that is already running.")
	}
	return c.with(OK, addr+" is free.", "")
}

// checkSSHAgent warns when SSH forges sign with an agent Studio can't reach, typically
// after an autostart without SSH_AUTH_SOCK. Studio still runs; only those forges fail.
func checkSSHAgent(ctx context.Context, p Probes, forges int) Check {
	c := Check{ID: "ssh-agent", Name: "SSH agent"}
	keys, fix, err := p.SSHAgent(ctx)
	if err != nil {
		return c.with(Warn, fmt.Sprintf("%s (needed by %d SSH %s).", strings.TrimSuffix(err.Error(), "."), forges, plural(forges, "forge", "forges")), fix)
	}
	return c.with(OK, fmt.Sprintf("The SSH agent holds %d %s.", keys, plural(keys, "key", "keys")), "")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func (c Check) with(s Status, msg, fix string) Check {
	c.Status, c.Message, c.Fix = s, msg, fix
	return c
}

// hasRegistry reports whether ref names a registry host, as in ghcr.io/owner/image.
func hasRegistry(ref string) bool {
	host, _, ok := strings.Cut(ref, "/")
	return ok && (strings.ContainsAny(host, ".:") || host == "localhost")
}

func gib(n uint64) string { return fmt.Sprintf("%.1f GB", float64(n)/(1<<30)) }

func firstLine(out string, err error) string {
	if s := strings.TrimSpace(out); s != "" {
		line, _, _ := strings.Cut(s, "\n")
		return line
	}
	return err.Error()
}

func lastLines(out string, err error, n int) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return err.Error()
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " / ")
}
