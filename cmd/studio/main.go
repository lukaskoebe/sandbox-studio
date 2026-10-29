// Command studio is the Sandbox Studio host binary: API, gateway and embedded UI.
//
//	studio            run the server
//	studio openapi    print the API description (used to generate the web client types)
//	studio login-url  print a fresh one-time browser login link
//	studio tui        open the terminal UI of the running Studio
//	studio quit       stop the running Studio
//	studio doctor     check that this host can run Studio
//	studio version    print the version and the default base image
//	studio autostart enable|disable|status
//	                  start Studio at login (a user-scope entry; off by default)
//	studio ssh enable|disable|status
//	                  reach sandboxes as <name>.studio from any SSH client (off by default)
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentchan"
	"github.com/lukaskoebe/sandbox-studio/internal/agentmem"
	"github.com/lukaskoebe/sandbox-studio/internal/api"
	"github.com/lukaskoebe/sandbox-studio/internal/autostart"
	"github.com/lukaskoebe/sandbox-studio/internal/ca"
	"github.com/lukaskoebe/sandbox-studio/internal/caddyrule"
	"github.com/lukaskoebe/sandbox-studio/internal/chat"
	"github.com/lukaskoebe/sandbox-studio/internal/cli"
	"github.com/lukaskoebe/sandbox-studio/internal/dnsproxy"
	"github.com/lukaskoebe/sandbox-studio/internal/doctor"
	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/gateway"
	"github.com/lukaskoebe/sandbox-studio/internal/gitreview"
	"github.com/lukaskoebe/sandbox-studio/internal/heartbeat"
	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/login"
	"github.com/lukaskoebe/sandbox-studio/internal/memory"
	"github.com/lukaskoebe/sandbox-studio/internal/netreview"
	"github.com/lukaskoebe/sandbox-studio/internal/paths"
	"github.com/lukaskoebe/sandbox-studio/internal/policy"
	"github.com/lukaskoebe/sandbox-studio/internal/preview"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxinfo"
	"github.com/lukaskoebe/sandbox-studio/internal/secretcheck"
	"github.com/lukaskoebe/sandbox-studio/internal/secrets"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/tasks"
	"github.com/lukaskoebe/sandbox-studio/internal/teammates"
	"github.com/lukaskoebe/sandbox-studio/internal/templatebuild"
	"github.com/lukaskoebe/sandbox-studio/internal/templateregistry"
	"github.com/lukaskoebe/sandbox-studio/internal/updates"
	"github.com/lukaskoebe/sandbox-studio/internal/version"
	"github.com/lukaskoebe/sandbox-studio/internal/webauth"
	"github.com/lukaskoebe/sandbox-studio/internal/webui"
	"golang.org/x/term"
)

// serverCommands are the commands main runs itself, for the help.
var serverCommands = []cli.ServerCommand{
	{Name: "tui", Summary: "Open the terminal UI of the running Studio.",
		Detail: "It signs in as the local owner, like the web UI's login link. q quits."},
	{Name: "quit", Summary: "Stop the running Studio, as Ctrl-C in its window does.",
		Detail: "It waits until Studio has shut down. Sandboxes keep running, and their agents reconnect\nwhen Studio starts again."},
	{Name: "doctor", Summary: "Check that this machine can run Studio, and say how to fix what can't.",
		Detail: "It checks the microsandbox runtime, KVM or the hypervisor, the base image, the data\ndirectory, free disk space and the -addr ports. It exits 1 if a check fails."},
	{Name: "autostart", Args: "enable|disable|status", Summary: "Start Studio when you log in, or stop doing so.",
		Detail: "status is the default. It writes a systemd user unit on Linux, a LaunchAgent on macOS\nand a Run key value on Windows, which take effect at the next login."},
	{Name: "ssh", Args: "enable|disable|status", Summary: "Reach the sandboxes from any SSH client, as <name>.studio.",
		Detail: "status is the default. enable adds an Include line for Studio's SSH config, which names a\nhost per sandbox, at the top of ~/.ssh/config; disable removes it. Then `ssh my-app.studio`,\nscp, sftp, VS Code Remote-SSH, JetBrains Gateway and Zed reach sandbox my-app, and Studio\nstarts or resumes it first. Settings → General has the same switch."},
	{Name: "ssh-proxy", Args: "<sandbox>.studio", Summary: "Serve one SSH connection to a sandbox over stdin and stdout.",
		Detail: "It is the ProxyCommand of Studio's SSH config, which `studio ssh enable` includes; you don't\nrun it yourself. It asks the running Studio for the sandbox, by name or ID, and starts or\nresumes it if it doesn't run."},
	{Name: "restore-msb-db", Args: "[-replace] [file]", Summary: "Rebuild microsandbox's database from one a repair moved aside.",
		Detail: "When Studio finds microsandbox's database damaged, it moves it aside to\nmsb.db.broken-<time> and rebuilds it from the rows it can read. If it could read none,\nmicrosandbox starts empty and its sandboxes can't start. This rebuilds the database from\nfile, by default the newest one moved aside, and keeps the one in place aside in turn.\nQuit Studio first. It refuses while the database in place knows sandboxes, unless -replace."},
	{Name: "app", Summary: "Open Studio in its app window, or in the browser without the app.",
		Detail: "It starts studio-app from next to studio, which starts Studio if it isn't running. Without\nthe app, or when it can't start, such as without WebKitGTK on Linux, Studio opens in the\nbrowser instead. The app's log is logs/studio-app.log in the data directory."},
	{Name: "login-url", Summary: "Print a one-time login link for the running Studio."},
	{Name: "version", Summary: "Print Studio's version and its base image."},
	{Name: "openapi", Summary: "Print the OpenAPI document of Studio's HTTP API."},
}

func main() {
	addr := flag.String("addr", "127.0.0.1:7878", "listen address (loopback only)")
	image := flag.String("image", defaultImage(), "base image for new sandboxes")
	noBrowser := flag.Bool("no-browser", false, "don't open the login link in the browser on start")
	flag.Usage = func() { cli.Help(flag.CommandLine.Output(), "", serverCommands, flag.CommandLine) }
	if len(os.Args) > 1 && cli.IsHelp(os.Args[1]) {
		cli.Help(os.Stdout, "", serverCommands, flag.CommandLine)
		return
	}
	flag.Parse()

	// "studio help …" and "studio <command> -h" print the help instead of running anything.
	if name := flag.Arg(0); name == "help" || (name != "" && slices.ContainsFunc(flag.Args()[1:], cli.IsHelp)) {
		topic := name
		if name == "help" {
			topic = flag.Arg(1)
		}
		if !cli.Help(os.Stdout, topic, serverCommands, flag.CommandLine) {
			fmt.Fprintf(os.Stderr, "studio: unknown command %q. Run `studio help` for the commands.\n", topic)
			os.Exit(cli.ExitUsage)
		}
		return
	}

	if flag.Arg(0) == "openapi" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(api.OpenAPI()); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	switch flag.Arg(0) {
	case "version":
		fmt.Printf("studio %s\nbase image: %s\n", version.Version, *image)
		return
	case "doctor":
		os.Exit(runDoctor(os.Stdout, *addr, *image))
	case "quit":
		os.Exit(runQuit(context.Background(), os.Stdout, *addr))
	case "app":
		os.Exit(runApp(context.Background(), os.Stdout, *addr))
	case "autostart":
		os.Exit(runAutostart(os.Stdout, flag.Args()[1:]))
	case "ssh":
		os.Exit(runSSH(os.Stdout, *addr, flag.Args()[1:]))
	case "ssh-proxy":
		os.Exit(runSSHProxy(context.Background(), *addr, flag.Args()[1:]))
	case "restore-msb-db":
		os.Exit(runRestoreMSBDB(os.Stdout, *addr, flag.Args()[1:]))
	}
	if flag.Arg(0) == "login-url" {
		link, err := requestLoginURL(context.Background(), *addr)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(link)
		return
	}
	if cli.Commands(flag.Arg(0)) {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		opts := cli.DefaultOptions(*addr, connectCLI)
		opts.Server, opts.Flags = serverCommands, flag.CommandLine
		status := cli.Run(ctx, opts, flag.Args())
		stop()
		os.Exit(status)
	}
	if flag.Arg(0) == "tui" {
		if err := runTUI(context.Background(), *addr); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	prepareConsole()
	log, logPath := serverLogger()
	// Studio opens the login link itself when it runs in a terminal, as when studio.exe is
	// double-clicked; not under a service manager or in tests.
	open := !*noBrowser && term.IsTerminal(int(os.Stderr.Fd()))
	err := run(*addr, *image, open, logPath, log)
	var restart *updateRestart
	if errors.As(err, &restart) {
		os.Exit(restartForUpdate(restart.resume, log))
	}
	if err != nil {
		log.Error("studio stopped", "err", err)
		os.Exit(1)
	}
}

// serverLogger logs to stderr, except on Windows, where the console is a window of its own:
// there the log goes to a file in the data directory and the console gets only warnings and
// errors. It returns the file's path, if any.
func serverLogger() (*slog.Logger, string) {
	stderr := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if goruntime.GOOS != "windows" {
		return stderr, ""
	}
	p, err := paths.Default()
	if err != nil {
		return stderr, ""
	}
	log, path, _, err := fileLogger(filepath.Join(p.Data, "logs"), os.Stderr)
	if err != nil {
		stderr.Warn("logging to the console: can't open the log file", "err", err)
		return stderr, ""
	}
	return log, path
}

// serviceAddrs derives the loopback addresses of the network gateway (port+1) and the
// template registry (port+2) from the -addr port, so the default is 7878, 7879, 7880.
// Each VM's configuration stores the gateway address and template image references
// persist the registry address: both follow -addr deterministically and are never
// silently chosen, so keep -addr stable for a data directory.
func serviceAddrs(addr string) (gateway, registry string, err error) {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", "", err
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65533 {
		return "", "", fmt.Errorf("listen port %q must be between 1 and 65533", port)
	}
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(n+1)), net.JoinHostPort("127.0.0.1", strconv.Itoa(n+2)), nil
}

// defaultImage is the digest-pinned base image set by the release build
// (-X .../version.BaseImage=ghcr.io/...@sha256:...), else the image for this version.
// Local builds stamped by `git describe` have no published image and use the one
// `make image` loads.
func defaultImage() string {
	if version.BaseImage != "" {
		return version.BaseImage
	}
	if !releaseTag.MatchString(version.Version) || strings.HasSuffix(version.Version, "-dirty") {
		return "sandbox-studio-base:dev"
	}
	return "ghcr.io/lukaskoebe/sandbox-studio-base:" + version.Version
}

// releaseTag matches the tags release.yml builds (v1.2.3, v1.2.3-rc.1), but not
// `git describe` output such as v1.2.3-4-gabc1234 or a bare commit hash.
var releaseTag = regexp.MustCompile(`^v\d+\.\d+\.\d+(-[0-9A-Za-z.]+)?$`)

func run(addr, image string, open bool, logPath string, log *slog.Logger) (err error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return errors.New("refusing to listen on a non-loopback address")
	}
	gatewayAddr, templateRegistryAddr, err := serviceAddrs(addr)
	if err != nil {
		return err
	}
	// Closing the terminal Studio runs in sends SIGHUP, and a service manager SIGTERM: both
	// shut it down like Ctrl-C does, closing its databases, instead of killing it mid-write.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGHUP, syscall.SIGTERM)
	defer stop()

	p, err := paths.Default()
	if err != nil {
		return err
	}
	if err := p.Ensure(); err != nil {
		return err
	}
	// One Studio per data directory: a second start, such as a double-click on studio.exe
	// while it runs, opens the running one instead of migrating and serving the same data.
	release, err := store.TryLock(filepath.Join(p.Data, "studio.lock"))
	if errors.Is(err, store.ErrLocked) {
		return openRunning(ctx, addr, p.Data, open, logPath, log)
	}
	if err != nil {
		return err
	}
	defer func() { _ = release() }()
	// The install record of an update, before the database opens: a rollback restores it.
	resume, err := updates.ResumeInstall(updatesDir(p), version.Version, log)
	if err != nil {
		log.Warn("read the update's install record", "err", err)
	}
	if resume.Exit {
		return &updateRestart{resume}
	}
	// A new binary that took the old one's place in this process and stops before serving
	// rolls back, and the old binary takes its place again.
	defer func() {
		var restart *updateRestart
		if err != nil && !errors.As(err, &restart) {
			if back, ok := resume.StoppedBeforeServing(err, log); ok {
				err = &updateRestart{back}
			}
		}
	}()
	served := make(chan struct{})
	resume.Watchdog(served, log)
	repairMSBDB(ctx, log)
	logDoctor(log, doctorCheck(ctx, p.Data, image, addr, false, 0, nil))
	if err := installGuestAgent(p.Guest(), goruntime.GOARCH); err != nil {
		return err
	}
	st, err := store.Open(ctx, p.DB())
	if err != nil {
		return err
	}
	defer st.Close()
	authKey, err := st.Secret(ctx, "web-auth-key", 32)
	if err != nil {
		return err
	}
	auth, err := webauth.New(authKey, func(host string) bool {
		_, _, ok := preview.Parse(host)
		return ok
	})
	if err != nil {
		return err
	}
	auth.Quit = stop // `studio quit`, and the Windows tray's Quit
	// Studio must not run without its vault: without the key the stored secrets are lost.
	vault, err := secrets.Open(ctx, st, p.Data, log)
	if err != nil {
		return err
	}
	if err := ensureEnvironment(ctx, st); err != nil {
		return err
	}
	rl, err := net.Listen("tcp4", templateRegistryAddr)
	if err != nil {
		return fmt.Errorf("template registry: %w", err)
	}
	defer rl.Close()
	registry, err := templateregistry.Open(ctx, st, filepath.Join(p.Data, "templates"), templateRegistryAddr, vault)
	if err != nil {
		return fmt.Errorf("open template registry: %w", err)
	}
	defer registry.Close()
	registryServer := &http.Server{Handler: registry.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	defer registryServer.Close()
	go func() {
		if err := registryServer.Serve(rl); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("template registry stopped", "err", err)
			stop()
		}
	}()
	log.Info("checking the microsandbox runtime")
	upgrade, err := ensureRuntime(ctx)
	var upErr *runtime.UpgradeError
	switch {
	case errors.As(err, &upErr):
		// Studio still serves; sandboxes can't start until msb is upgraded, which the doctor shows.
		log.Warn("could not upgrade msb; sandboxes won't start until it is, at the next start of Studio", "err", err)
	case err != nil:
		return fmt.Errorf("microsandbox runtime: %w", err)
	}
	if upgrade != nil {
		log.Info("upgraded msb; running sandboxes stay on the old one until they restart", "from", upgrade.From, "to", upgrade.To)
	}

	// Every sandbox connection goes through the gateway. Its address is stored in each VM's
	// configuration, so it only changes with -addr.
	key, err := st.Secret(ctx, "gateway-key", 32)
	if err != nil {
		return err
	}
	bus := &events.Bus{}
	engine := &policy.Engine{Store: st, Bus: bus, Hold: 60 * time.Second, Log: log, Reviewer: &netreview.Reviewer{Store: st, Keys: vault}}
	policyDone := make(chan struct{})
	go func() { defer close(policyDone); engine.Run(ctx) }()
	defer func() {
		stop()
		select {
		case <-policyDone:
		case <-time.After(5 * time.Second):
		}
	}()
	resolvers := &gateway.Resolvers{Upstreams: dnsproxy.SystemUpstreams(), Log: log}
	defer resolvers.Close()
	conns := &gateway.ConnLog{}
	authority := &ca.Authority{Store: st, Sealer: vault}
	caddy := &caddyrule.Engine{Dir: filepath.Join(p.Data, "caddy"), Dial: gateway.DialPublic}
	gitRemote := &gitreview.Service{Store: st, Secrets: vault, Bus: bus, Dir: filepath.Join(p.Data, "git-staging"), Log: log,
		Client: forgeClient(p.Data)}
	integs := integrations.Set{gitRemote}
	brw := newBrowser(st, vault, bus, p.Data, image, log) // settles browser.action, browser.credential and browser.control approvals
	integs = append(integs, brw)
	engine.Browsers = brw                                              // a browser's requests are attributed to its page
	secretCheck := &secretcheck.Service{Store: st, Bus: bus, Log: log} // settles secret.rejected approvals
	integs = append(integs, secretCheck)
	gw := &gateway.Gateway{
		Addr: gatewayAddr, Key: key, Policy: engine, Sandbox: st.LookupSandbox,
		Resolvers: resolvers, Conns: conns, Log: log, CA: authority, Secrets: vault, Caddy: caddy,
		Virtual: integs.Routes(), Dial: upstreamDial(),
		PrivateHosts: st.PrivateHosts, HostPorts: st.HostPorts, Own: []string{addr, gatewayAddr, templateRegistryAddr},
		Rejected: secretCheck.Rejected,
	}
	gl, err := net.Listen("tcp", gatewayAddr)
	if err != nil {
		return fmt.Errorf("network gateway: %w", err)
	}
	defer gl.Close()
	gw.Driver = brw.Driver // a browser VM's connections follow the rules of the agent driving it
	go func() {
		if err := gw.Serve(gl); err != nil {
			log.Error("network gateway stopped", "err", err)
		}
	}()

	// Memory embeds in the background. The model is downloaded on the first memory write,
	// never at startup.
	embedder := &memory.Llama{Assets: memory.DefaultAssets(filepath.Join(p.Data, "memory"))}
	mem := memory.New(st.DB(), embedder, log)
	memDone := make(chan struct{})
	go func() { defer close(memDone); mem.Run(ctx) }()
	defer func() {
		stop()
		select {
		case <-memDone:
			embedder.Close()
		case <-time.After(5 * time.Second): // a download that ignores cancellation; the process ends anyway
		}
	}()

	// Memory in agent sessions: guests' hooks and memory tools arrive as calls on their
	// channel; extraction runs in the background.
	agentMem := agentmem.New(st, mem, vault, log)
	agentMem.Notify = func(envID string) { bus.Publish(events.Event{Topic: events.TopicApprovals, EnvironmentID: envID}) }
	integs = append(integs, agentMem) // settles memory.share and memory.conflict approvals; it serves no hosts

	hub := agentchan.NewHub(log)
	go agentMem.RunBrowse(ctx, hub) // ~/memory copies in the connected sandboxes, rewritten as memory changes
	rt := newRuntime(runtime.Options{Image: image, GuestDir: p.Guest()}, log)
	mgr := &sandboxes.Manager{
		Store:     st,
		Runtime:   rt,
		Templates: registry,
		Hub:       hub,
		Egress:    sandboxEgress(gw),
		CA:        authority,
		Secrets:   vault,
		Paths:     p,
		Log:       log,
		Forget:    gitRemote.ForgetSandbox,
		Bus:       bus,
		// New sandboxes record the base they're made from; one on another base is outdated.
		BaseImage:   image,
		BaseVersion: version.Version,
	}
	hub.OnConnect = mgr.Configure
	integs = append(integs, &sandboxinfo.Service{Store: st, Ports: hub.Ports, Level: engine.Level})
	// Every integration that serves MCP tools is in integs by now; the chat service serves none.
	tsk := &tasks.Service{Store: st, Bus: bus, Log: log}
	hub.OnCall = routeCalls(st, integs, brw, tsk, log, agentMem.HandleCall)
	hub.OnForward = brw.DialForward
	brw.Runner, brw.VMs, brw.Conns = hub, mgr, conns
	go brw.Run(ctx)
	go brw.FollowOwners(ctx, bus, sandboxStopped(mgr))
	go mgr.FollowEnvironments(ctx, bus)
	go mgr.RunCheckpointSchedules(ctx)
	go mgr.WatchTerminals(ctx)
	mgr.DisksEvery = disksEvery
	go mgr.WatchDisks(ctx)
	if err := mgr.Reconcile(ctx); err != nil {
		return err
	}
	// Chats drive the teammates' harnesses over ACP. Turns that ran when Studio stopped
	// end interrupted before anything new starts.
	chats := &chat.Service{Store: st, Guest: hub, Bus: bus, Log: log, PromptNote: gitRemote.PromptNote, OnIdle: agentMem.ChatIdled,
		OnTaskDone: mgr.TaskDone}
	if err := chats.Recover(ctx); err != nil {
		return fmt.Errorf("chats: %w", err)
	}
	defer chats.Close()
	integs = append(integs, chats) // settles chat.permission approvals; it serves no hosts
	go integs.RunJobs(ctx, log)
	if err := gitRemote.Sweep(ctx); err != nil {
		log.Warn("git staging cleanup", "err", err)
	}
	builds, err := templatebuild.New(templatebuild.Options{Store: st, Runtime: rt, Guests: mgr, Registry: registry, Bus: bus, Log: log})
	if err != nil {
		return err
	}
	mgr.Builds = builds
	buildCtx, cancelBuilds := context.WithCancel(ctx)
	buildsDone := make(chan struct{})
	go func() {
		defer close(buildsDone)
		if err := builds.Run(buildCtx); err != nil && buildCtx.Err() == nil {
			log.Error("template build worker stopped", "err", err)
			stop()
		}
	}()
	defer func() {
		cancelBuilds()
		select {
		case <-buildsDone:
		case <-time.After(45 * time.Second):
			log.Warn("template build cleanup remains recorded for the next start")
		}
	}()

	checker := &updates.Checker{Settings: st, Current: version.Version, Log: log}
	system := api.System{Updates: checker, Doctor: func(ctx context.Context) doctor.Report {
		agentForges, err := st.AgentForges(ctx)
		if err != nil {
			log.Warn("doctor: count SSH agent forges", "err", err)
		}
		return doctorCheck(ctx, p.Data, image, addr, true, agentForges, embedder)
	}}
	if system.Autostart, err = autostart.Default(); err != nil {
		log.Warn("autostart is unavailable", "err", err)
	}
	// Studio's SSH config names a host per sandbox; ~/.ssh/config includes it while SSH
	// access is on (docs/sandboxes.md#ssh-access).
	if system.SSH, err = sshService(p, addr); err != nil {
		log.Warn("SSH access is unavailable", "err", err)
	} else {
		system.SSHSandboxes = func(ctx context.Context) ([]store.Sandbox, error) {
			all, err := st.AllSandboxes(ctx)
			return slices.DeleteFunc(all, func(sb store.Sandbox) bool { return !sandboxes.Public(sb) }), err
		}
		go system.SSH.Follow(ctx, bus, system.SSHSandboxes, log)
	}

	// Subscription sign-ins run in system VMs, and the CLI calls of subscription providers' memory
	// work run in their utility VMs (docs/teammates.md, subscription logins). The service also
	// removes the login VMs an earlier run left, and refreshes ChatGPT logins.
	logins := login.New(st, vault, mgr, hub, log)
	logins.Notify = func(envID string) {
		bus.Publish(events.Event{Topic: events.TopicTeammates, EnvironmentID: envID})
		bus.Publish(events.Event{Topic: events.TopicSecrets, EnvironmentID: envID})
	}
	go logins.Run(ctx)
	// Automatic updates restart Studio: run ends, and main starts the new binary.
	var restarting atomic.Bool
	if checker.Installer, checker.CannotInstall = newInstaller(ctx, p, st, logins, func() { restarting.Store(true); stop() }, log); checker.Installer != nil {
		checker.Installer.SetProgress(resume.Progress)
	}
	go checker.Run(ctx)
	agentMem.Utility = func(p store.Provider, key string) (agentmem.LLM, agentmem.UtilityModel, error) {
		if k, _ := teammates.LookupKind(p.Kind); k.Login {
			return logins.Utility(p)
		}
		return agentmem.DefaultUtility(p, key)
	}
	// Extraction and dreams start once they know the utility models.
	go agentMem.Run(ctx)
	go agentMem.RunDreams(ctx) // nightly, after enough new facts, and interrupted runs

	mux := http.NewServeMux()
	apiServer := &api.Server{Store: st, Sandboxes: mgr, Builds: builds, Policy: engine, Vault: vault, Bus: bus, Conns: conns, Caddy: caddy, Auth: auth, Memory: mem, AgentMem: agentMem, Log: log, Addr: addr,
		Integrations: integs, Git: gitRemote, System: system, Browser: brw, Chats: chats, Login: logins, Reach: gw, HostGit: api.HostGitIdentity,
		HostLogins: hostLogins(p.Data), ModelsClient: modelsClient()}
	// Heartbeats give teammates with open tasks their check-ins, and run scheduled tasks at their
	// times, until ctx ends (docs/teammates.md).
	beats := &heartbeat.Service{Store: st, Sandboxes: mgr, Chats: chats, Plan: apiServer.ChatLaunch, Bus: bus, Log: log}
	apiServer.TaskRuns = beats
	go beats.Run(ctx)
	mgr.SetHarnessConfig(apiServer.HarnessConfig)
	apiServer.Register(mux)
	mux.Handle("/", webui.Handler())
	registerControl(mux, st, engine, gw)
	handler := api.Guard(auth.Middleware(preview.Route(mgr.DialPreviewTCP, mux)))

	// Long-lived requests (event streams, terminals) end with the server instead of holding up
	// the shutdown.
	base, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()
	srv := &http.Server{
		Addr: addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second,
		BaseContext: func(net.Listener) context.Context { return base },
	}
	srv.RegisterOnShutdown(cancelRequests)
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()

	// Sandboxes are detached VMs: they keep running when Studio stops, and their agents
	// reconnect when it comes back.
	log.Info("sandbox studio listening", "url", "http://"+addr, "version", version.Version, "data", p.Data, "image", image)
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	markServed(addr, resume, checker.Installer, served, log)
	link := loginURL(addr, auth.IssueLogin())
	log.Info("open Studio with this one-time login link", "url", link)
	printLoginBanner(link, logPath)
	if open {
		// A link of its own, so the banner's still works once the browser used this one.
		openLogin(loginURL(addr, auth.IssueLogin()), log)
	}
	if err := srv.Serve(l); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	if restarting.Load() {
		return &updateRestart{resume}
	}
	return nil
}

// openRunning is a start while another Studio holds the data directory: it opens that Studio
// in the browser with a fresh login link and exits.
func openRunning(ctx context.Context, addr, data string, open bool, logPath string, log *slog.Logger) error {
	link, err := requestLoginURL(ctx, addr)
	if err != nil {
		return fmt.Errorf("Studio is already running with the data in %s, but not on %s (start this one with that Studio's -addr): %w", data, addr, err)
	}
	log.Info("Studio is already running", "url", "http://"+addr, "data", data)
	if open {
		openLogin(link, log)
		return nil
	}
	log.Info("open Studio with this one-time login link", "url", link)
	printLoginBanner(link, logPath)
	return nil
}

func openLogin(link string, log *slog.Logger) {
	if err := openBrowser(link); err != nil && !errors.Is(err, errNoDisplay) {
		log.Warn("could not open the browser; open the login link instead", "err", err)
	}
}

// studioRuntime is what Studio needs from the VM runtime: sandbox lifecycles and template
// builds. newRuntime returns microsandbox's, or the e2e build's fake (runtime_e2e.go).
type studioRuntime interface {
	sandboxes.SandboxRuntime
	templatebuild.Runner
}

type gatewayDial = func(ctx context.Context, network, addr string) (net.Conn, error)

// ensureEnvironment creates the first environment on a fresh install.
func ensureEnvironment(ctx context.Context, st *store.Store) error {
	envs, err := st.Environments(ctx)
	if err != nil || len(envs) > 0 {
		return err
	}
	_, err = st.CreateEnvironment(ctx, "Default")
	return err
}
