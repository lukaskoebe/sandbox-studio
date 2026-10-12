# Decisions and history

## Decisions log

| Date | Decision |
|---|---|
| 2026-10-08 | Go backend; microsandbox runtime; React + shadcn preset + TanStack Router (no Start); Linux, macOS and Windows from the start |
| 2026-10-08 | Support OpenCode, Claude Code and Codex through harness adapters |
| 2026-10-08 | Teammates with private memory plus explicit shared memory; gbrain-style consolidation and contradiction handling |
| 2026-10-09 | Environments: isolated top-level containers ("work", "private") that share no rules, secrets, providers, teammates or memory, switched in the sidebar |
| 2026-10-09 | Subscription logins (Claude Pro/Max, ChatGPT/Codex) are required, not only API keys (spike S8; not built yet) |
| 2026-10-09 | Utility model: Anthropic → `claude-haiku-4-5`; OpenAI → `gpt-6-luna`; OpenAI-compatible endpoint → its single model |
| 2026-10-09 | Open source under Apache 2.0 at `github.com/lukaskoebe/sandbox-studio`; images at `ghcr.io/lukaskoebe/` |
| 2026-10-09 | Test hardware: an Apple Silicon Mac, no Windows machine. Windows gets CI builds and unit tests; VM-level tests need a cloud VM with nested virtualization or testers |
| 2026-10-09 | `/workspace` and `/var/lib/docker` are owned disks (S5) |
| 2026-10-12 | `/home/agent` is a third owned disk, seeded once from the image skeleton; fork, rebase and export copy it with `/workspace`. Sandboxes of templates built before the skeleton (exporter version 1) keep their root home and get no home disk |
| 2026-10-09 | Hold unknown connections up to 60 s (S6) |
| 2026-10-09 | Containers trust the CA through a `runc` shim, not a helper command (S4 follow-up) |
| 2026-10-09 | No init system in guests: agentd stays PID 1 so msb can freeze guests; `studio-agent boot` supervises containerd, dockerd and the agent |
| 2026-10-09 | Bulk guest→host data (template layers, workspaces) goes over msb exec, not vsock |
| 2026-10-09 | Checkpoint restore stays disabled until microsandbox [#1736](https://github.com/superradcompany/microsandbox/issues/1736) keeps Studio's egress |
| 2026-10-10 | Suspend is an in-place pause until full snapshots can be restored with Studio's egress |
| 2026-10-10 | Studio's own services are virtual hosts under `*.studio.internal` (S2) |
| 2026-10-11 | Personas renamed to teammates (migration 018) |
| 2026-10-11 | Chats: harnesses driven over ACP alongside the terminal TUI ([design](design/chat-mode.md)) |

## From v1

v2 is a rewrite of v1 (`~/git/sandbox-studio-old`); v1 data isn't migrated.

| v1 | v2 |
|---|---|
| NixOS/QEMU VMs, Python manager | microsandbox microVMs, Go manager |
| DNS → host gateway, per-VM Caddy, pending-domain list | microsandbox's fixed fence, all TCP through the SOCKS5 gateway, hold-and-ask approvals, embedded Caddy for custom rules |
| `{env.NAME}` secrets in rules; global and per-VM rule scopes | environments; `{secret.NAME}`; placeholders swapped only for bound hosts |
| Custom disk snapshots, live snapshots, suspend/continue | microsandbox disk checkpoints, in-place pause, fork and rebase by workspace copy |
| Nix-built guest image | one CI-built OCI base image plus specs built with mise and apt into cached templates |
| OpenCode only, app-state profiles | OpenCode, Claude Code and Codex through harness adapters; teammates |
| gbrain on the host with Postgres/Bun/tokens | built-in memory: SQLite, local embeddings, consolidation |
| Named tmux browser terminals | the same, over the guest agent channel |
| No git approvals | git review: pushes held until approved |
| — | browser broker with approvals and live view |

Dropped: the gbrain install, Postgres and token-renewal machinery; OpenCode release pinning
(the base image pins harness versions); the libvirt probe; Landlock worker confinement (VM
isolation plus host policy replaces it); favicon discovery.

## Milestones

Built in this order, 2026-10-09 to 2026-10-11; v0.1.0 was released 2026-10-11.

| | Scope |
|---|---|
| M0 | Repo skeleton, CI on three OSes, spikes ([spikes](spikes.md)) |
| M1 | Base image, sandbox lifecycle, agent channel, web terminals, previews, UI shell |
| M2 | Gateway, rules and scopes, hold-and-ask, approvals with SSE, secrets vault, CA in guests and containers, connection log, Caddy editor |
| M3 | Specs, template builds and registry, checkpoints, suspend (pause), fork, rebase, export/import |
| M4 | Providers and teammates, harness adapters, sessions in tmux |
| M5 | Memory store, embeddings, hybrid search, hooks, MCP memory tools, extraction, memory UI |
| M6 | Shared memory, consolidation, conflicts, core pages, budget |
| M7 | Browser broker |
| M8 | Git review, Forgejo and then GitHub, HTTPS and SSH forges |
| M9 | Doctor, autostart, update check, release workflow ([release](release.md)) |
| after M9 | Chats over ACP |

What the original plan had and isn't built yet is listed in [PLAN.md](../PLAN.md).
