package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/autostart"
	"github.com/lukaskoebe/sandbox-studio/internal/desktopentry"
	"github.com/lukaskoebe/sandbox-studio/internal/msbdb"
	"github.com/lukaskoebe/sandbox-studio/internal/paths"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// uninstallWord is what the user types to confirm. It isn't y: this deletes every sandbox.
const uninstallWord = "uninstall"

// vmHost is what uninstalling needs of microsandbox.
type vmHost interface {
	// names lists every VM in microsandbox's home, Studio's or not.
	names(ctx context.Context) ([]string, error)
	stop(ctx context.Context, name string) error
	remove(ctx context.Context, name string) error
	// removeCheckpoints removes the snapshots of the checkpoints of these sandboxes.
	removeCheckpoints(ctx context.Context, sandboxIDs []string) error
}

// loginEntry is something Studio added to the user's setup outside its own directories.
type loginEntry struct {
	name    string
	disable func() error
}

// uninstaller removes one installation. Its functions are the host's; tests replace them.
type uninstaller struct {
	exe, data, msbHome string
	keepData, keepMSB  bool

	vms        vmHost
	entries    []loginEntry
	sandboxes  []string // the names the user knows the sandboxes by
	ids        []string // every sandbox in this installation's catalog, which its VMs are named after
	catalogErr error    // why the catalog couldn't be read
	lockMSB    func() (release func() error, err error)
	running    func() bool
	stopStudio func(context.Context) error
	closeApps  func()
}

// uninstallPlan is what an uninstall will do, which the user sees before saying yes.
type uninstallPlan struct {
	programs []string
	data     string // "" when it stays
	vms      []string
	others   []string // VMs that aren't this installation's
	msbHome  string   // "" when it stays
	notes    []string // what stays, and why
	blocked  error    // why nothing can be removed: there's no telling which VMs are whose
}

// owns says whether VM name belongs to this installation: its sandbox is in the catalog, or
// it only warms up a base image. Another installation, with another data directory, shares
// microsandbox's home and names its VMs the same way, so the prefix alone says nothing.
func (u *uninstaller) owns(name string) bool {
	if runtime.IsPrewarmVM(name) {
		return true
	}
	id, ok := sandboxes.VMSandboxID(name)
	return ok && slices.Contains(u.ids, id)
}

func (u *uninstaller) plan(ctx context.Context) uninstallPlan {
	p := uninstallPlan{programs: studioPrograms(u.exe)}
	if !u.keepData {
		if err := removableDir(u.data, "studio.db", "studio.lock"); err != nil {
			p.notes = append(p.notes, err.Error())
		} else {
			p.data = u.data
		}
	}
	if !exists(filepath.Join(u.msbHome, "db", "msb.db")) {
		return p // microsandbox never ran here, so it has no VMs to ask about
	}
	all, err := u.vms.names(ctx)
	if err != nil {
		err = fmt.Errorf("reading microsandbox's list of VMs failed: %w", err)
	} else if u.catalogErr != nil {
		err = fmt.Errorf("reading Studio's list of sandboxes failed: %w", u.catalogErr)
	}
	if err != nil {
		if u.keepData {
			p.notes = append(p.notes, fmt.Sprintf("Studio's VMs stay as they are: %v", err))
		} else {
			p.blocked = err
		}
		return p
	}
	for _, name := range all {
		if u.owns(name) {
			p.vms = append(p.vms, name)
		} else {
			p.others = append(p.others, name)
		}
	}
	switch {
	case u.keepData:
	case u.keepMSB:
		p.notes = append(p.notes, "microsandbox stays in "+u.msbHome+", as asked.")
	case len(p.others) > 0:
		p.notes = append(p.notes, fmt.Sprintf("microsandbox stays in %s: it has %s that aren't this Studio's (%s).", u.msbHome, counted(len(p.others), "VM"), someOf(p.others)))
	default:
		if err := removableDir(u.msbHome, "db"); err != nil {
			p.notes = append(p.notes, err.Error())
		} else {
			p.msbHome = u.msbHome
		}
	}
	return p
}

// describe says what the plan removes, as plainly as a command that can't be undone should.
func (u *uninstaller) describe(w io.Writer, p uninstallPlan) {
	fmt.Fprintln(w, "This removes Sandbox Studio from this computer:")
	fmt.Fprintln(w)
	for _, program := range p.programs {
		fmt.Fprintf(w, "  %s\n", program)
	}
	for _, e := range u.entries {
		fmt.Fprintf(w, "  %s\n", e.name)
	}
	if p.data != "" {
		fmt.Fprintf(w, "  %s (%s): settings, secrets, chats and logs\n", p.data, humanSize(dirSize(p.data)))
		if n := len(u.sandboxes); n > 0 {
			fmt.Fprintf(w, "  %s with disks and checkpoints (%s).\n", counted(n, "sandbox"), someOf(u.sandboxes))
			fmt.Fprintln(w, "    What is in them and isn't pushed anywhere is lost.")
		}
	}
	if n := len(p.vms); n > 0 {
		verb := "go"
		if u.keepData {
			verb = "stop"
		}
		fmt.Fprintf(w, "  %s of this Studio's %s (%s).\n", counted(n, "VM"), verb, someOf(p.vms))
	}
	if p.msbHome != "" {
		fmt.Fprintf(w, "  %s (%s): microsandbox and its images; -keep-microsandbox keeps them\n", p.msbHome, humanSize(dirSize(p.msbHome)))
	}
	if u.keepData {
		fmt.Fprintf(w, "\nThe data in %s and the sandboxes stay, stopped, and come back when you install Studio again.\n", u.data)
	}
	if len(p.notes) > 0 {
		fmt.Fprintln(w)
		for _, note := range p.notes {
			fmt.Fprintln(w, note)
		}
	}
	fmt.Fprintln(w)
}

// run does the plan in stages and returns what failed. A VM or disk that can't be removed ends
// it there: the catalog says which VMs are this Studio's, and the program is what runs this
// again, so the data and the programs go last. An entry that can't be removed doesn't hold up
// the rest, only the programs. A Studio that won't stop, or another one using microsandbox,
// changes nothing at all.
func (u *uninstaller) run(ctx context.Context, w io.Writer, p uninstallPlan) []error {
	if u.running() {
		fmt.Fprintln(w, "Stopping Studio.")
		if err := u.stopStudio(ctx); err != nil {
			return []error{fmt.Errorf("nothing was removed, as Studio didn't stop: %w", err)}
		}
	}
	u.closeApps()
	msbDB := filepath.Join(u.msbHome, "db", "msb.db")
	release := func() error { return nil }
	if !u.keepData && exists(msbDB) {
		var err error
		switch release, err = u.lockMSB(); {
		case errors.Is(err, store.ErrLocked):
			return []error{fmt.Errorf("nothing was removed: another Studio, with other data, is using microsandbox in %s. Quit it and try again", u.msbHome)}
		case err != nil:
			return []error{fmt.Errorf("nothing was removed: locking microsandbox: %w", err)}
		}
	}
	var failed []error
	note := func(err error, format string, args ...any) {
		if err != nil {
			failed = append(failed, fmt.Errorf(format+": %w", append(args, err)...))
		}
	}
	for _, e := range u.entries {
		note(e.disable(), "%s", e.name)
	}
	entriesFailed := len(failed)
	for _, name := range p.vms {
		if u.keepData {
			note(u.vms.stop(ctx, name), "stop VM %s", name)
		} else {
			fmt.Fprintf(w, "Removing VM %s.\n", name)
			note(u.vms.remove(ctx, name), "remove VM %s", name)
		}
	}
	_ = release() // a held lock can't be removed with its directory on Windows
	if len(failed) > entriesFailed {
		return failed
	}
	switch {
	case p.msbHome != "":
		fmt.Fprintf(w, "Removing %s.\n", p.msbHome)
		note(removeTree(p.msbHome), "remove %s", p.msbHome)
	case !u.keepData && exists(msbDB):
		note(u.vms.removeCheckpoints(ctx, u.ids), "remove this Studio's checkpoints")
		if err := os.Remove(filepath.Join(u.msbHome, "db", "studio.lock")); err != nil && !errors.Is(err, fs.ErrNotExist) {
			note(err, "remove microsandbox's lock of Studio's")
		}
	}
	if len(failed) > entriesFailed {
		return failed
	}
	if p.data != "" {
		fmt.Fprintf(w, "Removing %s.\n", p.data)
		note(removeTree(p.data), "remove %s", p.data)
	}
	if len(failed) > 0 {
		return failed
	}
	for _, program := range p.programs {
		note(removeTree(program), "remove %s", program)
	}
	return failed
}

// runUninstall is `studio uninstall`.
func runUninstall(ctx context.Context, in io.Reader, w io.Writer, addr string, interactive bool, args []string) int {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	fs.SetOutput(w)
	keepData := fs.Bool("keep-data", false, "keep the data directory, the sandboxes and microsandbox")
	keepMSB := fs.Bool("keep-microsandbox", false, "keep microsandbox and its images, without Studio's VMs")
	yes := fs.Bool("yes", false, "don't ask; for scripts")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		fmt.Fprintln(w, "usage: studio uninstall [-keep-data] [-keep-microsandbox] [-yes]")
		return 2
	}
	if !*yes && !interactive {
		fmt.Fprintln(w, "studio uninstall asks for confirmation: run it in a terminal, or pass -yes.")
		return 2
	}
	u, err := hostUninstaller(ctx, addr)
	if err != nil {
		fmt.Fprintln(w, err)
		return 1
	}
	u.keepData, u.keepMSB = *keepData, *keepMSB
	return u.confirmAndRun(ctx, in, w, *yes)
}

func (u *uninstaller) confirmAndRun(ctx context.Context, in io.Reader, w io.Writer, yes bool) int {
	p := u.plan(ctx)
	if p.blocked != nil {
		fmt.Fprintf(w, "Nothing was removed: %v.\n", p.blocked)
		fmt.Fprintln(w, "Studio can't tell which VMs are its own without that. Fix it and try again, or pass -keep-data to remove only the programs.")
		return 1
	}
	u.describe(w, p)
	if !yes {
		fmt.Fprintf(w, "This can't be undone. Type %s to go on: ", uninstallWord)
		line, _ := bufio.NewReader(in).ReadString('\n')
		if strings.TrimSpace(line) != uninstallWord {
			fmt.Fprintln(w, "Nothing was removed.")
			return 1
		}
	}
	failed := u.run(ctx, w, p)
	if len(failed) > 0 {
		for _, err := range failed {
			fmt.Fprintln(w, err)
		}
		if exists(u.exe) {
			fmt.Fprintln(w, "Sandbox Studio isn't fully uninstalled. What wasn't reached stays: fix this and run studio uninstall again.")
		}
		return 1
	}
	fmt.Fprintln(w, "Sandbox Studio is uninstalled.")
	return 0
}

// hostUninstaller is the uninstaller of this computer's installation.
func hostUninstaller(ctx context.Context, addr string) (*uninstaller, error) {
	p, err := paths.Default()
	if err != nil {
		return nil, err
	}
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return nil, err
	}
	home, err := msbdb.Home()
	if err != nil {
		return nil, err
	}
	u := &uninstaller{
		exe: exe, data: p.Data, msbHome: home,
		vms: hostVMs(),
		lockMSB: func() (func() error, error) {
			release, _, err := lockMSB()
			return release, err
		},
		running:    func() bool { return holdsData(p) },
		stopStudio: func(ctx context.Context) error { return stopStudio(ctx, p, addr) },
		closeApps: func() {
			for _, pid := range runningApps(appPaths(exe)) {
				if proc, err := os.FindProcess(pid); err == nil {
					_ = proc.Signal(syscall.SIGTERM)
				}
			}
		},
	}
	if names, err := store.PeekSandboxNames(ctx, p.DB()); err == nil {
		u.sandboxes = names
	}
	if exists(p.DB()) {
		u.ids, u.catalogErr = store.PeekSandboxIDs(ctx, p.DB())
	}
	if svc, err := autostart.Default(); err == nil {
		if st, err := svc.Status(); err == nil && st.Enabled {
			u.entries = append(u.entries, loginEntry{"The start at login entry, " + st.Path, func() error {
				_, err := svc.Disable()
				return err
			}})
		}
	}
	if f, err := desktopentry.Locate(); err == nil && f.Installed() {
		u.entries = append(u.entries, loginEntry{"The launcher entry, " + f.Entry, f.Remove})
	}
	if svc, err := sshService(p, addr); err == nil {
		if st, err := svc.Status(); err == nil && st.Enabled {
			u.entries = append(u.entries, loginEntry{"The Include line for Studio in " + svc.UserConfig, func() error {
				_, err := svc.Disable()
				return err
			}})
		}
	}
	return u, nil
}

// counted says how many of something there are: 1 VM, 2 VMs.
func counted(n int, what string) string {
	switch {
	case n == 1:
		return "1 " + what
	case strings.HasSuffix(what, "x"):
		return fmt.Sprintf("%d %ses", n, what)
	}
	return fmt.Sprintf("%d %ss", n, what)
}

// studioPrograms are exe and what the installer put next to it.
func studioPrograms(exe string) []string {
	dir := filepath.Dir(exe)
	programs := []string{exe}
	for _, name := range []string{"studio-app", "studio-app.exe", "studiow.exe", "Sandbox Studio.app"} {
		if p := filepath.Join(dir, name); exists(p) {
			programs = append(programs, p)
		}
	}
	return programs
}

// removableDir refuses to remove a directory that isn't what it should be: the locations come
// from the environment (SANDBOX_STUDIO_HOME, MSB_HOME), and a wrong one mustn't cost the user
// their home directory.
func removableDir(dir string, markers ...string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	home, _ := os.UserHomeDir()
	sep := string(filepath.Separator)
	if abs == filepath.Dir(abs) || abs == home || strings.HasPrefix(home, strings.TrimSuffix(abs, sep)+sep) {
		return fmt.Errorf("%s stays: it holds your home directory, or is it", dir)
	}
	if !slices.ContainsFunc(markers, func(m string) bool { return exists(filepath.Join(abs, m)) }) {
		return fmt.Errorf("%s stays: it doesn't look like Studio's", dir)
	}
	return nil
}

// removeTree removes path, also when a directory in it is read-only. A missing path is fine.
func removeTree(path string) error {
	err := os.RemoveAll(path)
	if err == nil || !errors.Is(err, fs.ErrPermission) {
		return err
	}
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(p, 0o700)
		}
		return nil
	})
	return os.RemoveAll(path)
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// someOf names a few of names, and how many more there are.
func someOf(names []string) string {
	const shown = 8
	if len(names) <= shown {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:shown], ", "), len(names)-shown)
}

// dirSize is what the files under dir take on the disk, with a limit on how long it looks: a
// wrong size matters less than a prompt that doesn't come.
func dirSize(dir string) int64 {
	deadline := time.Now().Add(20 * time.Second)
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if time.Now().After(deadline) {
			return filepath.SkipAll
		}
		if info, err := d.Info(); err == nil && !d.IsDir() {
			total += diskUsage(info)
		}
		return nil
	})
	return total
}

func humanSize(n int64) string {
	const mib, gib = 1 << 20, 1 << 30
	if n >= gib {
		return fmt.Sprintf("%.1f GB", float64(n)/gib)
	}
	return fmt.Sprintf("%d MB", n/mib)
}
