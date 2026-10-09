// Command studio is the Sandbox Studio host binary: API, gateway and embedded UI.
//
//	studio            run the server
//	studio openapi    print the API description (used to generate the web client types)
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
	goruntime "runtime"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentbin"
	"github.com/lukaskoebe/sandbox-studio/internal/agentchan"
	"github.com/lukaskoebe/sandbox-studio/internal/api"
	"github.com/lukaskoebe/sandbox-studio/internal/ca"
	"github.com/lukaskoebe/sandbox-studio/internal/dnsproxy"
	"github.com/lukaskoebe/sandbox-studio/internal/events"
	"github.com/lukaskoebe/sandbox-studio/internal/gateway"
	"github.com/lukaskoebe/sandbox-studio/internal/paths"
	"github.com/lukaskoebe/sandbox-studio/internal/policy"
	"github.com/lukaskoebe/sandbox-studio/internal/preview"
	"github.com/lukaskoebe/sandbox-studio/internal/runtime"
	"github.com/lukaskoebe/sandbox-studio/internal/sandboxes"
	"github.com/lukaskoebe/sandbox-studio/internal/secrets"
	"github.com/lukaskoebe/sandbox-studio/internal/store"
	"github.com/lukaskoebe/sandbox-studio/internal/version"
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

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(*addr, *image, log); err != nil {
		log.Error("studio stopped", "err", err)
		os.Exit(1)
	}
}

// gatewayAddr is where sandbox VMs reach the network gateway.
const gatewayAddr = "127.0.0.1:7879"

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
	// Studio must not run without its vault: without the key the stored secrets are lost.
	vault, err := secrets.Open(ctx, st, p.Data, log)
	if err != nil {
		return err
	}
	if err := ensureEnvironment(ctx, st); err != nil {
		return err
	}
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
	gw := &gateway.Gateway{
		Addr: gatewayAddr, Key: key, Policy: engine, Sandbox: st.LookupSandbox,
		Resolvers: resolvers, Conns: conns, Log: log, CA: authority, Secrets: vault,
	}
	gl, err := net.Listen("tcp", gatewayAddr)
	if err != nil {
		return fmt.Errorf("network gateway: %w", err)
	}
	defer gl.Close()
	go gw.Serve(gl)

	hub := agentchan.NewHub(log)
	mgr := &sandboxes.Manager{
		Store:   st,
		Runtime: runtime.New(runtime.Options{Image: image, GuestDir: p.Guest()}),
		Hub:     hub,
		Egress:  gw,
		CA:      authority,
		Secrets: vault,
		Paths:   p,
		Log:     log,
	}
	hub.OnConnect = mgr.Configure
	go mgr.FollowEnvironments(ctx, bus)
	if err := mgr.Reconcile(ctx); err != nil {
		return err
	}

	mux := http.NewServeMux()
	(&api.Server{Store: st, Sandboxes: mgr, Policy: engine, Vault: vault, Bus: bus, Conns: conns, Log: log, Addr: addr}).Register(mux)
	mux.Handle("/", webui.Handler())
	handler := api.Guard(preview.Route(hub.DialTCP, mux))

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
