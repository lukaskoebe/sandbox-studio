package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/paths"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/sshconfig"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// sshStartWait bounds how long `studio ssh-proxy` waits for a sandbox to start or resume.
const sshStartWait = 3 * time.Minute

// sshService is the SSH access of the installation at p, for the Studio at addr.
func sshService(p paths.Paths, addr string) (*sshconfig.Service, error) {
	userConfig, err := sshUserConfig(p)
	if err != nil {
		return nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return &sshconfig.Service{Dir: filepath.Join(p.Data, "ssh"), UserConfig: userConfig, Exe: exe, Addr: addr,
		Data: p.Data, MSBHome: os.Getenv("MSB_HOME")}, nil
}

// runSSH is `studio ssh enable|disable|status`: whether the user's SSH config includes
// Studio's, which names a host per sandbox.
func runSSH(w io.Writer, addr string, args []string) int {
	p, err := paths.Default()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	svc, err := sshService(p, addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	var st sshconfig.Status
	switch strings.Join(args, " ") {
	case "enable":
		st, err = svc.Enable()
	case "disable":
		st, err = svc.Disable()
	case "status", "":
		st, err = svc.Status()
	default:
		fmt.Fprintln(os.Stderr, "usage: studio ssh enable|disable|status")
		return 2
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if !st.Enabled {
		fmt.Fprintf(w, "SSH access is off: %s doesn't include %s\n", st.UserConfig, st.Config)
		return 0
	}
	fmt.Fprintf(w, "SSH access is on: %s includes %s, so `ssh <sandbox>.studio` reaches a sandbox\n", st.UserConfig, st.Config)
	return 0
}

// runSSHProxy is `studio ssh-proxy [-data dir] [-msb-home dir] <host>`, the ProxyCommand of
// Studio's SSH config: it finds the sandbox a <name or ID>.studio host names through the
// running Studio, starts or resumes it if it doesn't run, and serves the SSH connection over
// stdin and stdout. -data and -msb-home stand in for SANDBOX_STUDIO_HOME and MSB_HOME, which
// ssh doesn't pass on from Studio.
func runSSHProxy(ctx context.Context, addr string, args []string) int {
	fs := flag.NewFlagSet("ssh-proxy", flag.ContinueOnError)
	data := fs.String("data", "", "the installation's data directory")
	msbHome := fs.String("msb-home", "", "microsandbox's MSB_HOME")
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: studio ssh-proxy [-data dir] [-msb-home dir] <sandbox>.studio")
		return 2
	}
	for name, value := range map[string]string{"SANDBOX_STUDIO_HOME": *data, "MSB_HOME": *msbHome} {
		if value != "" {
			_ = os.Setenv(name, value)
		}
	}
	if err := sshProxy(ctx, addr, fs.Arg(0)); err != nil {
		fmt.Fprintln(os.Stderr, "studio ssh-proxy:", err)
		return 1
	}
	return 0
}

func sshProxy(ctx context.Context, addr, host string) error {
	p, err := paths.Default()
	if err != nil {
		return err
	}
	svc, err := sshService(p, addr)
	if err != nil {
		return err
	}
	c, err := connectCLI(ctx, addr)
	if err != nil {
		return err
	}
	sb, err := sshSandbox(ctx, c, host, sshStartWait, func(msg string) { fmt.Fprintln(os.Stderr, msg) })
	_ = c.Close(context.WithoutCancel(ctx))
	if err != nil {
		return err
	}
	return serveSSH(ctx, sandboxes.VMName(sb), runtime.SSHKeys{HostKey: svc.HostKey(), AuthorizedKeys: svc.AuthorizedKeys()})
}

// sshStudio is what `studio ssh-proxy` asks the running Studio.
type sshStudio interface {
	Environments(ctx context.Context) ([]store.Environment, error)
	Sandboxes(ctx context.Context, env string) ([]sandboxes.View, error)
	Lifecycle(ctx context.Context, env, id, action string) (sandboxes.View, error)
}

// sshSandbox finds the sandbox host names and has it running: it starts a stopped one,
// resumes a suspended one and waits for one that is being created, started or stopped.
func sshSandbox(ctx context.Context, c sshStudio, host string, wait time.Duration, say func(string)) (store.Sandbox, error) {
	key := strings.TrimSuffix(strings.ToLower(host), sshconfig.Suffix)
	deadline := time.Now().Add(wait)
	acted := false
	for {
		sb, err := findSSHSandbox(ctx, c, key)
		if err != nil {
			return store.Sandbox{}, err
		}
		action := ""
		switch sb.Status {
		case runtime.StatusRunning:
			return sb.Sandbox, nil
		case runtime.StatusSuspended:
			action = "resume"
		case runtime.StatusStopped, runtime.StatusCreated, runtime.StatusCrashed:
			action = "start"
		case "failed", runtime.StatusAbsent:
			return store.Sandbox{}, fmt.Errorf("sandbox %s can't start (%s); open it in Studio", sb.Name, sb.Status)
		}
		if action != "" && !acted {
			say(fmt.Sprintf("Studio: %s %s…", map[string]string{"resume": "resuming", "start": "starting"}[action], sb.Name))
			if _, err := c.Lifecycle(ctx, sb.EnvironmentID, sb.ID, action); err != nil {
				return store.Sandbox{}, fmt.Errorf("%s sandbox %s: %w", action, sb.Name, err)
			}
			acted = true
			continue
		}
		if time.Now().After(deadline) {
			return store.Sandbox{}, fmt.Errorf("sandbox %s is still %s after %s", sb.Name, sb.Status, wait)
		}
		select {
		case <-ctx.Done():
			return store.Sandbox{}, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// findSSHSandbox finds the sandbox key names: its ID, its name, or the name-<id> alias of a
// name that sandboxes of several environments have.
func findSSHSandbox(ctx context.Context, c sshStudio, key string) (sandboxes.View, error) {
	envs, err := c.Environments(ctx)
	if err != nil {
		return sandboxes.View{}, err
	}
	var named []sandboxes.View
	for _, env := range envs {
		list, err := c.Sandboxes(ctx, env.ID)
		if err != nil {
			return sandboxes.View{}, err
		}
		for _, sb := range list {
			if sb.ID == key || sb.Name+"-"+sb.ID == key {
				return sb, nil
			}
			if sb.Name == key {
				named = append(named, sb)
			}
		}
	}
	switch len(named) {
	case 0:
		return sandboxes.View{}, fmt.Errorf("Studio has no sandbox %s", key)
	case 1:
		return named[0], nil
	}
	aliases := make([]string, len(named))
	for i, sb := range named {
		aliases[i] = sb.Name + "-" + sb.ID + sshconfig.Suffix
	}
	return sandboxes.View{}, errors.New("several environments have a sandbox " + key + "; connect to one of " + strings.Join(aliases, ", "))
}
