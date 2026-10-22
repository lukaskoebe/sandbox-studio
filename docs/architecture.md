# Architecture

Sandbox Studio runs coding agents as persistent teammates inside microVM sandboxes. Every
outward action (network access, git pushes, browser actions, credential use) goes through
the host, which applies rules or asks the user in the web UI.

This page is the overview. Component details are linked from the [index](README.md).

## Goals and non-goals

Goals:

- Linux, macOS (Apple Silicon) and Windows 11 hosts.
- One Go binary that serves the API and the embedded React UI.
- [microsandbox](https://docs.microsandbox.dev) as the VM runtime, with Docker working inside
  sandboxes.
- Network approvals, secret injection, Caddy rules and persistent workspaces (carried over
  from v1).
- Teammates: named agents with their own identity, memory and settings, each running
  OpenCode, Claude Code or Codex.
- Built-in memory with local embeddings, private and shared scopes, consolidation and
  conflict handling.
- An agent-controlled browser whose actions are approved in the UI, with live view and
  takeover.
- One place where every pending approval pops up.

Non-goals:

- Cloud or multi-user hosting.
- Messaging-channel integrations (Slack and the like).
- A third-party plugin runtime.
- Migrating v1 data. v2 is a fresh install.
- Intel Macs (microsandbox doesn't support them).

## Principles

1. **One way to do each thing.** Each feature has one opinionated path. Alternatives wait
   until someone needs them.
2. **The host decides; the guest is untrusted.** Agents have root in their sandbox. All
   policy lives on the host: the gateway, approvals, the secrets vault, the browser broker.
3. **Secrets never enter a sandbox.** Guests see placeholders; the gateway substitutes real
   values on the way out, only for hosts the secret is bound to.
4. **Ask, don't fail.** A blocked action becomes an approval request that can succeed once
   approved: a new host, a git push, a browser click.
5. **Automatic over voluntary.** Memory recall and capture run from harness hooks at fixed
   moments. Agents are not relied on to call memory tools.
6. **Declarative where it matters.** Templates are built from a small YAML spec and cached.

## Concepts

- **Environment.** The top-level isolation boundary, e.g. "work" or "private". It owns its
  providers, secrets, rules, teammates, sandboxes, memory, approvals and forges. Nothing is
  shared between environments, and each has its own TLS CA. The UI shows one environment at
  a time (`/e/$env/...`), with a switcher in the sidebar. A fresh install gets one
  environment called "Default".
- **Teammate.** A named agent identity: name, role, soul (markdown), harness, provider,
  model and git identity, plus a private memory scope. A teammate owns sandboxes and one
  browser VM. See [teammates](teammates.md).
- **Provider.** An environment's LLM access: Anthropic API key, OpenAI API key, an
  OpenAI-compatible endpoint, or a Claude or ChatGPT subscription. The kind
  decides which harnesses can use it and which utility model memory jobs use.
- **Sandbox.** A microVM with a root filesystem, a `/workspace` disk, a `/home/agent` disk and
  a `/var/lib/docker` disk. It persists across stop and start. See [sandboxes](sandboxes.md).
- **Template.** The image built from a spec (resources, mise tools, apt packages, a setup
  script) on the base image, cached by spec, base digest, platform and exporter version.
  See [template builds](template-builds.md).
- **Session.** One harness TUI in a named tmux session in a sandbox. A **chat** is the same
  harness driven over ACP and shown in the web UI. See [harnesses](harnesses.md).
- **Gateway.** The host-side SOCKS5 proxy that every TCP connection from every sandbox goes
  through. See [network](network.md).
- **Rule.** A network policy entry (`allow`, `proxy`, `caddy`, `deny`) scoped to a sandbox,
  a teammate or the environment.
- **Secret.** A named credential in the vault, scoped to the environment, one teammate or one
  sandbox, and bound to the hosts allowed to receive it.
- **Approval.** A typed request waiting for the user. See [approvals](approvals.md).
- **Memory.** Facts, pages and sources in two scopes: `teammate:<id>` and `shared`. See
  [memory](memory.md).

## Overview

```
React SPA (embedded) ── HTTP · SSE · WebSocket ──┐
┌────────────────────────── studio (one Go binary) ──────────────────────────────┐
│ API · auth · approvals · secrets vault · teammates · templates + registry      │
│ gateway: SOCKS5 in → sandbox from credentials → name (SOCKS domain, DNS map,   │
│          SNI/Host) → rules → splice | intercept (env CA): headers, secrets,    │
│          Caddy | hold and ask | refuse                                         │
│ per-sandbox DNS resolvers · connection log                                     │
│ memory: SQLite FTS5 + int8 vectors · llama.cpp embeddings (yzma) · dream job   │
│ sandbox manager (microsandbox Go SDK) · browser broker · git review · chats    │
└──────┬──────────────────────────────────────────┬──────────────────────────────┘
       │ TCP egress (SOCKS5, user = sandbox ID)    │ one vsock host socket per sandbox
 ┌─────┴───────────────┐  ┌─────────────┐   ┌──────┴────────────────────────────┐
 │ sandbox (teammate A)│  │ sandbox (B) │   │ studio-agent in every guest:      │
 │ harness TUI in tmux │  │ …           │   │ terminals, ports, hooks, MCP,     │
 │ dockerd, /workspace │  │             │   │ ACP, files, CA and placeholders   │
 └─────────────────────┘  └─────────────┘   └───────────────────────────────────┘
                     ┌─────────────────────────────┐
                     │ browser VM per teammate:    │ ← driven only by the broker;
                     │ Chromium + agent-browser    │   agents never get CDP
                     └─────────────────────────────┘
```

## Process model

- Studio is a normal user process, optionally started at login (`studio autostart`). There
  is no system service.
- The SDK installs `msb` and `libkrunfw` into `~/.microsandbox` on first start, and Studio
  replaces an msb there that is older than the SDK's release (`runtime.Ensure`). The new files
  are renamed over the old ones, so running VMs keep the old msb until they restart. A failed
  upgrade is a warning: Studio serves, but sandboxes don't start until the next start of
  Studio upgrades msb. An msb that is newer, or that `MSB_PATH` or the configuration picks, is
  left alone. msb's home must stay short: Unix socket paths are limited to 108 bytes.
- Listeners, all on loopback: the UI and API on `-addr` (default `127.0.0.1:7878`, must be
  loopback), the gateway on the next port, the template registry on the one after. Keep
  `-addr` stable for a data directory: VM configs store the gateway address and template
  references store the registry address.
- Sandboxes run detached. They keep running when Studio stops; on start Studio reconciles
  its catalog with microsandbox, and guest agents reconnect.
- msb keeps its sandboxes, images and snapshot index in its own SQLite database. Before any
  msb call opens it, Studio runs SQLite's quick check on it. A damaged one (a failed check,
  "file is not a database" or "database disk image is malformed") is moved aside with its
  `-wal` and `-shm` files as `msb.db.broken-<UTC time>` and never deleted. In its place goes
  a new database with the schema and every row that still reads, provided the migration list
  comes through whole; otherwise msb starts an empty one. A row the table's constraints refuse
  is left out, and `maintenance_lease` (msb's runtimes' coordination leases, which two writers
  damage first) starts empty. Then msb indexes the snapshots on disk again. `studio
  restore-msb-db` rebuilds the database from a moved-aside one later, with Studio stopped,
  moving the one in place aside as `msb.db.replaced-<UTC time>`. Once the SDK has opened the
  database, the doctor no longer opens it with Studio's own SQLite: SQLite's POSIX locks belong
  to the process, so closing a second handle would drop the SDK's locks. The `msb-db` doctor check, in `studio doctor` and Settings, fails for a damaged
  database or one a newer msb migrated (fix: `msb self downgrade <Studio's msb>`), and warns
  after a repair with what was kept and lost. Runtime calls that fail on the database answer
  503 with that fix instead of a bare SQLite error.
- Startup (`cmd/studio/main.go`): data dir and its lock `studio.lock` (held: ask the running
  Studio for a login link with the control credential, open it and exit), a check of msb's own database
  (`~/.microsandbox/db/msb.db`, `internal/msbdb`), doctor (warnings only), install the guest agent
  binaries, open the catalog and vault, make sure an environment exists, start the
  template registry, the runtime, the gateway and its resolvers, the agent channel hub,
  reconcile sandboxes, recover chats and build jobs, then serve the API and UI. Once it listens,
  Studio prints the login banner (the link as an OSC 8 hyperlink) and, when stderr is a terminal
  and `-no-browser` is unset, opens a second link in the browser (`rundll32
  url.dll,FileProtocolHandler`, `open`, or `xdg-open` with a display). On Windows it turns off
  the console's QuickEdit, whose selection blocks console writes, and logs to
  `logs/studio.log` (moved to `studio.log.1` past 10 MB at start), with only warnings on the
  console; the Windows autostart entry passes `-no-browser`.
- The Windows tray launcher (`cmd/studiow`, `studiow.exe` next to `studio.exe`, built with
  `-H windowsgui` and without cgo) never serves itself. It starts `studio.exe -no-browser` with
  a console that has no window (`CREATE_NO_WINDOW`, outside the tray's job), tells Studio's
  state from `GET /api/auth/status`, opens Studio with `studio login-url`, and Quit runs
  `studio quit`: `POST /api/auth/quit` with the control credential, then a wait until the data
  directory's lock is free. An update restarts `studio.exe` on its own, and replaces
  `studiow.exe` too when the archive has one; the running tray keeps its old binary until it
  starts again. A named mutex keeps one tray per user, and the autostart entry starts the tray
  when it is there. The icon and menu are plain Win32 (`Shell_NotifyIconW`).
- Subcommands: `studio` (serve), `openapi`, `login-url`, `tui` (the [terminal UI](tui.md)),
  `quit`, `doctor`, `version`, `autostart enable|disable|status`, `restore-msb-db`, and the sandbox commands of
  `internal/cli` (`ls`, `show`, `start`, `stop`, `suspend`, `resume`, `create`, `fork`, `rm`,
  `shell`, `ask`, `approvals`, `approve`, `deny`, `checkpoints`, `checkpoint`, `restore`,
  `envs`, `completion`; see [command line](cli.md)). The guest binary is `studio-agent` (see
  [guest agent](guest-agent.md)).

## Data locations

One data directory (`internal/paths`):

| OS | Default |
|---|---|
| Linux | `$XDG_DATA_HOME/sandbox-studio`, else `~/.local/share/sandbox-studio` |
| macOS | `~/Library/Application Support/Sandbox Studio` |
| Windows | `%LOCALAPPDATA%\Sandbox Studio` |

`SANDBOX_STUDIO_HOME` overrides it. Contents:

| Path | What |
|---|---|
| `studio.db` | SQLite catalog: everything per environment, including memory, encrypted secrets and CA keys |
| `studio.lock` | held by the running Studio, one per data directory |
| `logs/studio.log` | Studio's log, on Windows only |
| `vault.key` | master key file, only when no OS keychain is available |
| `guest/bin/` | the `studio-agent` binary, mounted read-only into every sandbox |
| `run/<id>.sock` | per-sandbox agent channel sockets (named pipes on Windows, see [guest-agent](guest-agent.md)) |
| `templates/` | template registry artifacts |
| `imports/` | sandbox import uploads |
| `git-staging/` | git review staging repos |
| `memory/` | llama.cpp libraries and the embedding model, downloaded on first use |
| `browser/` | browser broker state |
| `caddy/` | embedded Caddy storage |

VMs, images and snapshots live in microsandbox's own home (`~/.microsandbox`).

## Repository layout

```
cmd/studio/           host binary
cmd/studio-agent/     guest agent (linux/amd64 and arm64, CGO off)
internal/
  runtime/            microsandbox SDK wrapper (the only importer of the SDK)
  sandboxes/          sandbox lifecycle, checkpoints, fork, rebase, import/export
  resources/          resource limits
  templatespec/ templatebuild/ templateimage/ templateexport/ templateregistry/
  ocilayer/ upperlayer/ guestcapture/   template layer capture and validation
  gateway/ dnsproxy/ policy/ caddyrule/ ca/ secrets/   network and credentials
  agentchan/ agentproto/ agentbin/      host side and wire format of the agent channel
  guest/ agentcall/ agenthook/          guest agent internals
  harness/ teammates/ acp/ chat/        harness adapters, teammates, ACP chats
  claudeacp/                            ACP shim that runs Claude Code for subscription chats
  login/                                subscription logins in a system VM
  tasks/ heartbeat/                     teammates' task lists and scheduled check-ins
  netreview/                            AI reviewer of the assisted security level
  memory/ agentmem/                     memory store and its use in sessions
  browser/ gitreview/ integrations/     browser broker, git review
  preview/ webauth/ api/ events/ webui/ HTTP surface
  tui/                                  terminal UI client (studio tui)
  cli/                                  command line client (studio ls, ask, approve, …)
  store/                                SQLite catalog and migrations
  doctor/ autostart/ updates/ diskspace/ paths/ version/
  msbdb/                                check and repair of msb's own database
  e2e/                                  fakes of the e2e build (-tags e2e only)
web/                  Vite + React + TanStack Router + shadcn (Base UI); e2e tests in web/e2e,
                      browser tests against a real Studio in web/e2e-live
images/base/          base image (Dockerfile + rootfs)
images/browser/       browser VM image
site/                 user documentation (Astro Starlight)
spikes/               spike and qualification programs, not production code
docs/                 developer documentation
```

## Security model

| Threat | Mitigation |
|---|---|
| Agent sends data to unapproved hosts | Only DNS (to its own resolver) and TCP to public addresses leave the guest, and TCP goes through the gateway. Unknown names are held for approval. UDP and ICMP are blocked, so QUIC falls back to TCP. |
| Agent steals credentials | Secrets live only on the host. Guests see placeholders, substituted only for bound hosts. |
| Agent bypasses browser policy | The browser runs in its own VM, driven only by the broker. No CDP for agents; password values are redacted; credentials are filled by Studio. |
| Agent pushes to or acts on external services | Pushes go to a staging repo and wait for review. Browser actions and PRs are approvals. |
| Guest reaches host or private services | The fence allows public destinations only; the gateway dials public addresses only, except for the entries of an environment's local network list, whose private addresses it may dial, and the host ports the environment opened, which it dials on loopback for `host.studio.internal` (see [network](network.md#local-network)). Studio's own ports stay closed. Host services are virtual hosts the gateway serves, or the per-sandbox agent channel. |
| Local web attacker, DNS rebinding | Loopback listeners, host-bound session cookies, a Host check and a same-origin check on unsafe methods. See [API](api.md). |

## Testing and CI

- Go unit tests per package, all with fakes: no VMs, no real LLM APIs, no keychain.
- End-to-end tests: the Playwright suite in `web/e2e` runs against Studio built with `-tags e2e`,
  where `internal/e2e` replaces the VM runtime and the guest agent with fakes. See
  [frontend](frontend.md#end-to-end-tests).
- `internal/harness/opencode-plugin.test.mjs` tests the OpenCode plugin with `node --test`.
- Live qualifications are programs under `spikes/`; results are in [spikes](spikes.md).
- CI (`.github/workflows/ci.yml`): on Linux, macOS and Windows, `go vet`, `go test`,
  `go build` and the Linux guest agent build; on Linux and macOS the `install.sh` tests; on
  Linux also gofmt, golangci-lint, the OpenAPI schema check and shellcheck. The web job checks the generated API types, then runs
  typecheck, ESLint, Prettier and the build. The e2e job runs `make e2e` on Linux with
  Playwright's Chromium. `docs.yml` builds the site.
- Smoke (`.github/workflows/smoke.yml`, nightly and on demand): on Linux, macOS and Windows,
  build Studio, start it (it installs microsandbox), create a sandbox with `studio create`
  and delete it. Linux boots the base image built from the commit, the others the latest
  release's. GitHub's macOS runners have no nested virtualization, so the boot is skipped
  there with a warning. On Windows, Studio is started through WMI: the runner's job object
  doesn't allow breakaway, and msb starts each VM detached from its job. Linux then runs
  the browser tests of `web/e2e-live` against the same Studio.
- Releases: see [release](release.md).

## Conventions

- Single-line commit messages.
- `gofmt` for Go; ESLint and Prettier (`ts`/`tsx`/`js`/`jsx`) in `web/`.
- `make api` regenerates `web/openapi.json` and `web/src/lib/api/schema.gen.ts`; commit
  both.
- `make test` and `make lint` run the local checks.
- User-facing features get a page in `site/`; implementation notes go in `docs/`.
- Every change adds or updates e2e scenarios for what it changes; nothing merges with a red
  suite.
