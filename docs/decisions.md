# Decisions and history

## Decisions log

| Date | Decision |
|---|---|
| 2026-10-08 | Go backend; microsandbox runtime; React + shadcn preset + TanStack Router (no Start); Linux, macOS and Windows from the start |
| 2026-10-08 | Support OpenCode, Claude Code and Codex through harness adapters |
| 2026-10-08 | Teammates with private memory plus explicit shared memory; gbrain-style consolidation and contradiction handling |
| 2026-10-09 | Environments: isolated top-level containers ("work", "private") that share no rules, secrets, providers, teammates or memory, switched in the sidebar |
| 2026-10-09 | Subscription logins (Claude Pro/Max, ChatGPT/Codex) are required, not only API keys (spike S8): built, pending a live run with real accounts |
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
| 2026-10-12 | Dev dependencies `@playwright/test` (e2e) and `@codemirror/lang-yaml` (spec editor) |
| 2026-10-12 | Resizable panels use our own splitter (`components/side-panel.tsx`), not shadcn's `Resizable` and its dependency |
| 2026-10-12 | The default teammate is "Iris", a generalist engineer: curious, careful and direct, with a short soul |
| 2026-10-12 | SSH forges sign with the host's ssh-agent by default, reversing "never use the host agent" to "only host-side, never forwarded"; when signing waits on a host confirmation (hardware key, password manager), a dismissible hint suggests a dedicated Studio key ([git review](git-review.md#ssh-forges)) |
| 2026-10-13 | Network security levels: locked, guarded (default for new environments), assisted and open decide what no rule covers, per environment with a teammate override; existing environments migrate to locked |
| 2026-10-13 | The curated list of development infrastructure ships as data and is allowed by guarded and up; assisted asks an AI reviewer on the environment's utility model, whose allows and denies expire after a day and can be reverted |
| 2026-10-13 | Visual direction "Workbench" (dense and tool-like, monospace identifiers, lists over cards, one accent hue per approval kind) over "Calm studio", with the accents toned down ([UI style](design/ui-style.md)) |
| 2026-10-13 | Terminal UI on Bubble Tea v2, Lip Gloss v2 and Bubbles v2; `studio tui` is the client and plain `studio` stays the server ([terminal UI](tui.md#why-a-terminal-client)) |
| 2026-10-13 | Teammate tasks and check-ins: one schedule per teammate, which starts a chat only with open tasks, in the latest sandbox, starting it if it's stopped ([teammates](teammates.md#check-ins-heartbeats)) |
| 2026-10-13 | The guest's writes to the host go in pieces of at most 16 KiB, and a reply stays within one 256 KiB stream window, because msb's vsock breaks on larger ones ([guest agent](guest-agent.md#limits-of-the-channel-on-msbs-vsock)) |

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

### Second round

Driven by feedback on v0.1.x, 2026-10-12 to 2026-10-15. N1 to N11 shipped in v0.2.0
(2026-10-13), C1 in v0.3.0 (2026-10-14) and C2 in v0.3.2 (2026-10-15).

| | Scope |
|---|---|
| N1 | End-to-end tests first, since every later item restructures the UI: the `-tags e2e` build with a fake runtime and guest channel, the Playwright suite in `web/e2e`, and the KVM smoke workflow ([contributing](../CONTRIBUTING.md)) |
| N2 | Quick fixes: the live view's click mapping, the login link banner, the copy pass, the Network page's tabs |
| N3 | Information architecture: the sidebar, teammate pages, settings subpages, the sandbox dashboard and workspace, side panels instead of sheets ([frontend](frontend.md)) |
| N4 | Visual identity, "Workbench" ([UI style](design/ui-style.md)) |
| N5 | Guided setup, the default teammate, sandbox creation progress over SSE ([sandboxes](sandboxes.md#creation-progress)) |
| N6 | Git over the host SSH agent and forges from a pasted repository URL ([git review](git-review.md#ssh-forges)) |
| N7 | `/home/agent` as an owned disk, mise toolchains moved to `/opt/mise` ([sandboxes](sandboxes.md#storage-and-mounts)) |
| N8 | Secrets scoped to the environment, a teammate or a sandbox, the most specific winning ([network](network.md#secrets-vault)) |
| N9 | Lineage: the sandbox timeline and the family tree on Home ([sandboxes](sandboxes.md#lineage)) |
| N10 | The template spec editor with validation as you type ([template builds](template-builds.md#edit-the-spec-in-the-editor)) |
| N11 | Ephemeral browser sessions ([browser](browser.md)) |
| C1 | Network security levels, the curated list, site-scoped browsing approvals, grouped approvals, rule hygiene, the AI reviewer ([network](network.md#security-levels)) |
| C2 | The terminal UI ([terminal UI](tui.md)) |

Why some of it is the way it is:

- **Security levels (C1).** Browsing raised one approval per third-party host (trackers,
  CDNs, consent banners, image hosts), and package installs did the same on a smaller scale,
  so the rules list grew into hundreds of one-off hosts nobody reviewed. The parts landed in
  order of risk: grouped approvals and rule hygiene first, which add none; then site-scoped
  browsing with the tracker list; then the levels with the curated list; the Assisted level's
  AI reviewer last.
- **Lineage (N9)** is plain SVG with no graph library. The timeline is the sandbox over
  time: generations are segments labeled with their template, a rebase is a template change
  on the line, checkpoints are nodes and forks branch off to their children. Deleted
  sandboxes stay as tombstones in `sandbox_events`, so lineage survives deletion.
- **Ephemeral sessions (N11)** run as a second agent-browser session with a tmpfs profile in
  the teammate's existing VM. The fallback, had agent-browser not run two profiles in one
  VM, was a throwaway browser VM per request.

What is still open is listed in [PLAN.md](../PLAN.md).
