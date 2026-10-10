# Sandbox Studio

Run coding agents (OpenCode, Claude Code, Codex) as persistent team members inside
[microsandbox](https://docs.microsandbox.dev) microVMs. Every outward-facing action — network
access, git pushes, browser actions, credential use — goes through the host and can be
approved from a web UI.

> **Status: early development (milestone M3 in progress).** Sandboxes, terminals, network
> approvals, secrets, Caddy rules, and disk checkpoints are available for development use. See
> [PLAN.md](PLAN.md) for the design and roadmap and [docs/spikes.md](docs/spikes.md) for the
> results of the technical spikes.

## Highlights (planned)

- **Sandboxes** with persistent workspaces, Docker inside, checkpoints, suspend/resume and forks.
- **Network gateway**: every connection is checked against rules; unknown destinations pop
  up for approval while the connection is held. Secrets never enter a sandbox.
- **Environments** (e.g. "work", "private") that share no rules, secrets, providers or memory.
- **Personas**: named agents with their own identity and long-term memory, plus a shared team
  memory with consolidation and contradiction handling. Local embeddings, nothing to configure.
- **Browser broker**: agents drive a browser in a separate VM; actions are approved in the UI,
  passwords are never shown to the agent, and you can watch and take over live.
- Linux, macOS (Apple Silicon) and Windows.

## Development

Requirements: Go 1.26, Node 22 with pnpm 11, and a host that can run microsandbox (Linux with
KVM, Apple Silicon macOS, or Windows 11 with the Windows Hypervisor Platform).

```sh
make web      # build the UI into the Go binary's embed directory
make build    # bin/studio
make agent    # bin/studio-agent-linux-{amd64,arm64}
make test
make image    # dev only: build the base image with Docker and load it into microsandbox
./bin/studio  # prints a one-time login link for http://127.0.0.1:7878
```

During UI work, run `go run ./cmd/studio` and `pnpm dev` in `web/` (Vite proxies `/api`).
Paste the launch link into the UI's Connect screen. For a fresh link while Studio is
running, use `./bin/studio login-url`. Links expire after ten minutes and can be used once;
the browser keeps an HttpOnly session cookie. For the LAN development UI, run
`pnpm dev:lan` and paste the same kind of launch link into that page. Authentication still
passes through Studio; the dev proxy does not bypass it.

Open sandbox previews through the **Previews** menu. Each preview gets its own host-scoped
session, and Studio removes its authentication cookies before forwarding requests to the
sandbox. Preview links use `.localhost`, so they open on the machine running Studio.

Use **Checkpoints** to save a sandbox's root, workspace and Docker disks, while it runs or
while it is stopped. Checkpoints can depend on earlier ones; delete newer checkpoints before
their parents. Restore is currently disabled: microsandbox 0.7.7 restores a snapshot with its
default network, which would bypass Studio's gateway
([upstream #1736](https://github.com/superradcompany/microsandbox/issues/1736)).
Disk checkpoints do not preserve running processes, and one of a running sandbox is
crash-consistent. Quiesce applications first when they need application-level consistency;
**Suspend** pauses a running sandbox in place and **Resume** continues it where it was,
terminals included. A suspended sandbox keeps its memory and does not survive a host reboot.

## License

[Apache License 2.0](LICENSE)
