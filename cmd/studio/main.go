// Command studio is the Sandbox Studio host binary: API, gateway and embedded UI.
//
//	studio            run the server
//	studio openapi    print the API description (used to generate the web client types)
//	studio login-url  print a fresh one-time browser login link
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
	goruntime "runtime"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentbin"
	"github.com/lukaskoebe/sandbox-studio/internal/agentchan"
	"github.com/lukaskoebe/sandbox-studio/internal/agentmem"
	"github.com/lukaskoebe/sandbox-studio/internal/api"
	"github.com/lukaskoebe/sandbox-studio/internal/ca"
	"github.com/lukaskoebe/sandbox-studio/internal/caddyrule"
	"github.com/lukaskoebe/sandbox-studio/internal/dnsproxy"
	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/gateway"
	"github.com/lukaskoebe/sandbox-studio/internal/gitreview"
	"github.com/lukaskoebe/sandbox-studio/internal/integrations"
	"github.com/lukaskoebe/sandbox-studio/internal/memory"
	"github.com/lukaskoebe/sandbox-studio/internal/paths"
	"github.com/lukaskoebe/sandbox-studio/internal/policy"
	"github.com/lukaskoebe/sandbox-studio/internal/preview"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/secrets"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/templatebuild"
	"github.com/lukaskoebe/sandbox-studio/internal/templateregistry"
	"github.com/lukaskoebe/sandbox-studio/internal/version"
	"github.com/lukaskoebe/sandbox-studio/internal/webauth"
	"github.com/lukaskoebe/sandbox-studio/internal/webui"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:7878", "listen address (loopback only)")
	image := flag.String("image", defaultImage(), "base image for new sandboxes")
	flag.Parse()

	if flag.Arg(0) == "openapi" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(api.OpenAPI()); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
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

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(*addr, *image, log); err != nil {
		log.Error("studio stopped", "err", err)
		os.Exit(1)
	}
}

// gatewayAddr is where sandbox VMs reach the network gateway.
const gatewayAddr = "127.0.0.1:7879"

// Template image references persist this address; never silently select a new port.
const templateRegistryAddr = "127.0.0.1:7880"

func defaultImage() string {
	if version.Version == "dev" {
		return "sandbox-studio-base:dev" // built locally with `make image`
	}
	return "ghcr.io/lukaskoebe/sandbox-studio-base:" + version.Version
}

func run(addr, image string, log *slog.Logger) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return errors.New("refusing to listen on a non-loopback address")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	p, err := paths.Default()
	if err != nil {
		return err
	}
	if err := p.Ensure(); err != nil {
		return err
	}
	if err := agentbin.Install(p.Guest(), goruntime.GOARCH); err != nil {
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
	if err := runtime.Ensure(ctx); err != nil {
		return fmt.Errorf("microsandbox runtime: %w", err)
	}

	// Every sandbox connection goes through the gateway. Its address is stored in each VM's
	// configuration, so it never changes.
	key, err := st.Secret(ctx, "gateway-key", 32)
	if err != nil {
		return err
	}
	bus := &events.Bus{}
	engine := &policy.Engine{Store: st, Bus: bus, Hold: 60 * time.Second}
	resolvers := &gateway.Resolvers{Upstreams: dnsproxy.SystemUpstreams(), Log: log}
	defer resolvers.Close()
	conns := &gateway.ConnLog{}
	authority := &ca.Authority{Store: st, Sealer: vault}
	caddy := &caddyrule.Engine{Dir: filepath.Join(p.Data, "caddy"), Dial: gateway.DialPublic}
	gitRemote := &gitreview.Service{Store: st, Secrets: vault, Bus: bus, Dir: filepath.Join(p.Data, "git-staging"), Log: log}
	integs := integrations.Set{gitRemote}
	gw := &gateway.Gateway{
		Addr: gatewayAddr, Key: key, Policy: engine, Sandbox: st.LookupSandbox,
		Resolvers: resolvers, Conns: conns, Log: log, CA: authority, Secrets: vault, Caddy: caddy,
		Virtual: integs.Routes(),
	}
	gl, err := net.Listen("tcp", gatewayAddr)
	if err != nil {
		return fmt.Errorf("network gateway: %w", err)
	}
	defer gl.Close()
	go gw.Serve(gl)

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
	go agentMem.Run(ctx)
	go agentMem.RunDreams(ctx) // nightly, after enough new facts, and interrupted runs
	integs = append(integs, agentMem) // settles memory.share and memory.conflict approvals; it serves no hosts

	hub := agentchan.NewHub(log)
	rt := runtime.New(runtime.Options{Image: image, GuestDir: p.Guest()})
	mgr := &sandboxes.Manager{
		Store:     st,
		Runtime:   rt,
		Templates: registry,
		Hub:       hub,
		Egress:    gw,
		CA:        authority,
		Secrets:   vault,
		Paths:     p,
		Log:       log,
	}
	hub.OnConnect = mgr.Configure
	hub.OnCall = agentMem.HandleCall
	go mgr.FollowEnvironments(ctx, bus)
	if err := mgr.Reconcile(ctx); err != nil {
		return err
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

	mux := http.NewServeMux()
	(&api.Server{Store: st, Sandboxes: mgr, Builds: builds, Policy: engine, Vault: vault, Bus: bus, Conns: conns, Caddy: caddy, Auth: auth, Memory: mem, AgentMem: agentMem, Log: log, Addr: addr,
		Integrations: integs, Git: gitRemote}).Register(mux)
	mux.Handle("/", webui.Handler())
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
	log.Info("open Studio with this one-time login link", "url", loginURL(addr, auth.IssueLogin()))
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// ensureEnvironment creates the first environment on a fresh install.
func ensureEnvironment(ctx context.Context, st *store.Store) error {
	envs, err := st.Environments(ctx)
	if err != nil || len(envs) > 0 {
		return err
	}
	_, err = st.CreateEnvironment(ctx, "Default")
	return err
}
