// Command studio-agent runs inside every sandbox and connects back to Studio over vsock.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"slices"
	"syscall"
	"time"

	"github.com/lukaskoebe/sandbox-studio/internal/agentcall"
	"github.com/lukaskoebe/sandbox-studio/internal/agenthook"
	"github.com/lukaskoebe/sandbox-studio/internal/claudeacp"
	"github.com/lukaskoebe/sandbox-studio/internal/version"
)

func main() {
	cmd := "connect"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "version":
		fmt.Println(version.Version)
	case "connect":
		log := slog.New(slog.NewTextHandler(os.Stderr, nil))
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := connect(ctx, log); err != nil && ctx.Err() == nil {
			log.Error("studio-agent stopped", "err", err)
			os.Exit(1)
		}
	case "export-layer":
		flags := flag.NewFlagSet("export-layer", flag.ExitOnError)
		maxFreeze := flags.Duration("max-freeze", 0, "longest time to hold the root filesystem frozen (default 60s)")
		_ = flags.Parse(os.Args[2:])
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := exportLayer(ctx, os.Stdout, *maxFreeze); err != nil {
			fmt.Fprintf(os.Stderr, "studio-agent: export-layer: %v\n", err)
			os.Exit(1)
		}
	case "workspace-export":
		// Studio runs these as root to copy a workspace between sandboxes.
		if err := workspaceExport(os.Stdout, os.Stderr, slices.Contains(os.Args[2:], "--zstd")); err != nil {
			fmt.Fprintf(os.Stderr, "studio-agent: workspace-export: %v\n", err)
			os.Exit(1)
		}
	case "workspace-import":
		if err := workspaceImport(os.Stdin); err != nil {
			fmt.Fprintf(os.Stderr, "studio-agent: workspace-import: %v\n", err)
			os.Exit(1)
		}
	case "home-export":
		// The same for the agent user's home.
		if err := homeExport(os.Stdout, os.Stderr, slices.Contains(os.Args[2:], "--zstd")); err != nil {
			fmt.Fprintf(os.Stderr, "studio-agent: home-export: %v\n", err)
			os.Exit(1)
		}
	case "home-import":
		if err := homeImport(os.Stdin); err != nil {
			fmt.Fprintf(os.Stderr, "studio-agent: home-import: %v\n", err)
			os.Exit(1)
		}
	case "docker-export":
		// The same for Docker's data, which a disk resize copies with Docker stopped.
		if err := dockerExport(os.Stdout, os.Stderr, slices.Contains(os.Args[2:], "--zstd")); err != nil {
			fmt.Fprintf(os.Stderr, "studio-agent: docker-export: %v\n", err)
			os.Exit(1)
		}
	case "docker-import":
		if err := dockerImport(os.Stdin); err != nil {
			fmt.Fprintf(os.Stderr, "studio-agent: docker-import: %v\n", err)
			os.Exit(1)
		}
	case "merge-base", "merge-export", "merge-apply", "merge-status", "merge-replay-export", "merge-replay":
		// Studio runs these as the agent user to merge a fork back: see forkmerge.
		if err := merge(cmd, os.Args[2:], os.Stdin, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "studio-agent: %s: %v\n", cmd, err)
			os.Exit(1)
		}
	case "__capture-watchdog":
		if len(os.Args) != 3 {
			fmt.Fprintln(os.Stderr, "studio-agent: capture watchdog: missing freeze limit")
			os.Exit(2)
		}
		if err := captureWatchdog(os.Args[2]); err != nil {
			fmt.Fprintf(os.Stderr, "studio-agent: capture watchdog: %v\n", err)
			os.Exit(1)
		}
	case "boot":
		// Studio runs this after every VM boot: see guest.Boot.
		if err := boot(); err != nil {
			fmt.Fprintf(os.Stderr, "studio-agent: %v\n", err)
			os.Exit(1)
		}
	case "shutdown":
		// Studio runs this before stopping the VM: see guest.Shutdown.
		if err := shutdown(); err != nil {
			fmt.Fprintf(os.Stderr, "studio-agent: %v\n", err)
			os.Exit(1)
		}
	case "supervise":
		log := slog.New(slog.NewTextHandler(os.Stderr, nil))
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := supervise(ctx, log); err != nil {
			log.Error("supervisor stopped", "err", err)
			os.Exit(1)
		}
	case "hook":
		// Harness hooks run this as the agent user: see agentcall.RunHook. It always exits 0
		// with an answer the harness accepts, so memory can never block a session.
		flags := flag.NewFlagSet("hook", flag.ContinueOnError)
		name := flags.String("harness", "", "the harness that runs the hook: claude, codex or opencode")
		event := ""
		if len(os.Args) > 2 {
			event = os.Args[2]
			_ = flags.Parse(os.Args[3:])
		}
		h, ok := agenthook.Lookup(*name)
		if !ok {
			fmt.Fprintf(os.Stderr, "studio-agent: hook: unknown harness %q\n", *name)
			fmt.Println("{}")
			return
		}
		if err := agentcall.RunHook(context.Background(), h, event, os.Stdin, os.Stdout, agentcall.HookOptions{}); err != nil {
			fmt.Fprintf(os.Stderr, "studio-agent: hook %s: %v\n", event, err)
		}
	case "mcp":
		// The harness's MCP server for memory tools: see agentcall.ServeMCP.
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := agentcall.ServeMCP(ctx, os.Stdin, os.Stdout, agentcall.SocketCaller{}); err != nil && ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "studio-agent: mcp: %v\n", err)
			os.Exit(1)
		}
	case "info":
		// This sandbox, its ports and its network policy, as the sandbox_info tool returns them.
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := agentcall.RunInfo(ctx, agentcall.SocketCaller{}, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "studio-agent: info: %v\n", err)
			os.Exit(1)
		}
	case "git-credential":
		// git's credential helper: see agentcall.RunGitCredential. It always exits 0 without a
		// credential, so git goes on as it would without it.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		action := ""
		if len(os.Args) > 2 {
			action = os.Args[2]
		}
		if err := agentcall.RunGitCredential(ctx, agentcall.SocketCaller{}, action, os.Stdin, os.Stderr); err != nil {
			fmt.Fprintf(os.Stderr, "studio-agent: git-credential: %v\n", err)
		}
	case "credential":
		// A tool's credential helper, such as AWS's credential_process: see
		// agentcall.RunCredential. Studio may wait for the user's approval.
		if len(os.Args) != 4 {
			fmt.Fprintln(os.Stderr, "usage: studio-agent credential <secret> <format>")
			os.Exit(2)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := agentcall.RunCredential(ctx, agentcall.SocketCaller{}, os.Args[2], os.Args[3], os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "Sandbox Studio: %v\n", err)
			os.Exit(1)
		}
	case "oci-runtime":
		// Docker's runtime inside the sandbox: see guest.OCIRuntime.
		if err := ociRuntime(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "studio-agent: %v\n", err)
			os.Exit(1)
		}
	case "claude-acp":
		// The chat agent of a Claude subscription, which runs Claude Code itself: see claudeacp.Serve.
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := claudeacp.Serve(ctx, os.Stdin, os.Stdout, claudeacp.Options{Log: os.Stderr}); err != nil && ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "studio-agent: claude-acp: %v\n", err)
			os.Exit(1)
		}
	case "claude-permission":
		// Claude Code's permission prompts for that agent, from its plugin: see claudeacp.ServePermission.
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := claudeacp.ServePermission(ctx, os.Stdin, os.Stdout, os.Getenv(claudeacp.SocketEnv)); err != nil && ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "studio-agent: claude-permission: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "usage: studio-agent [connect|boot|shutdown|version|hook|mcp|info|git-credential|credential|oci-runtime|export-layer|workspace-export|workspace-import|home-export|home-import|docker-export|docker-import|merge-base|merge-export|merge-apply|merge-status|merge-replay-export|merge-replay|claude-acp|claude-permission]\n")
		os.Exit(2)
	}
}
