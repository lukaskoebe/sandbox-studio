# Sandbox Studio v2 — Plan

Status: accepted, M0 in progress · 2026-10-09

Sandbox Studio v2 is a full rewrite of v1 (`~/git/sandbox-studio-old`). It runs coding
agents as persistent "team members" (personas) inside microVM sandboxes, with every
outward-facing action — network access, git pushes, browser actions, credential use —
mediated by the host and approvable from a web UI.

## Decisions log

| Date | Decision |
|---|---|
| 2026-10-08 | Go backend; microsandbox runtime; React + shadcn preset + TanStack Router (no Start); Linux, macOS and Windows from the start |
| 2026-10-08 | Support OpenCode, Claude Code and Codex through harness adapters |
| 2026-10-08 | Personas with private memory, plus explicit shared memory; gbrain-style consolidation and contradiction handling |
| 2026-10-09 | **Environments**: isolated top-level containers (e.g. "work", "private") that share no rules, secrets, providers, personas or memory; switched with a dropdown (§6.0) |
| 2026-10-09 | **Subscription logins** (Claude Pro/Max, ChatGPT/Codex) are required, not only API keys (S8 is high priority) |
| 2026-10-09 | **Utility model**: Claude → `haiku` alias (currently `claude-haiku-4-5`); OpenAI/Codex → `gpt-6-luna`; custom OpenAI-compatible endpoint → the single model chosen during onboarding (§6.6) |
| 2026-10-09 | Open source under **Apache 2.0** at `github.com/lukaskoebe/sandbox-studio`; images at `ghcr.io/lukaskoebe/` |
| 2026-10-09 | Test hardware: an Apple Silicon Mac is available; **no Windows machine**. Windows gets CI builds and unit tests; VM-level tests need a cloud VM with nested virtualization or community testers |

---

## 1. Goals and non-goals

**Goals**

- Linux, macOS (Apple Silicon) and Windows 11 hosts from the first release.
- One Go binary that serves the API and an embedded React UI.
- [microsandbox](https://docs.microsandbox.dev) as the VM runtime; Docker works inside sandboxes.
- Keep and improve v1's highlights:
  - ad-hoc network approval popups
  - secret injection
  - the Caddy rule editor
  - persistent workspaces with snapshots
  - persistent agent state
- Personas: named agents with their own identity, memory and settings. Each persona can
  run OpenCode, Claude Code or Codex.
- Memory that agents actually use:
  - hybrid search with local embeddings, configured out of the box
  - per-persona memory plus a shared team memory
  - consolidation and explicit handling of contradictions
- An agent-controlled browser whose actions are approved in the UI, with live view and takeover.
- A single approvals inbox in which every permission request pops up automatically.

**Non-goals for the first release**

- Cloud/multi-user hosting.
- Messaging-channel integrations (Slack, etc.).
- A third-party plugin runtime.
- Migrating v1 data. v2 is a fresh install; v1 keeps working side by side.
- Intel Macs. microsandbox does not support them.

## 2. Principles

1. **One way to do each thing.** Each feature has one opinionated path. Alternatives wait
   until someone needs them. v1's many ways to install and connect gbrain are the cautionary
   tale.
2. **The host decides; the guest is untrusted.** Agents have root in their sandbox. All
   policy lives on the host:
   - the gateway
   - the approvals inbox
   - the secrets vault
   - the browser broker
3. **Secrets never enter a sandbox.** Guests see placeholders, and the gateway substitutes
   real values on the way out.
4. **Ask, don't fail.** A blocked action becomes an approval request that can succeed once
   approved. Examples: a new domain, a git push, a browser click.
5. **Automatic over voluntary.** Memory recall and capture happen through harness hooks at
   fixed moments. Agents are not relied on to remember to call memory tools.
6. **Declarative where it matters.** Sandboxes are created from a spec file. Templates are
   cached snapshots of a built spec.

## 3. What carries over from v1

| v1 | v2 |
|---|---|
| NixOS/QEMU VMs, Python manager | microsandbox microVMs, Go manager |
| DNS → host gateway, per-VM Caddy, pending-domain list | microsandbox's fixed outer policy, plus all egress going to the Studio SOCKS5 gateway. Hold-and-ask approvals. Embedded Caddy for custom rules. |
| `{env.NAME}` secrets in rules; global and per-VM rule scopes | **Environments** (isolated rule/secret/provider/persona sets with a switcher); secrets referenced as `{secret.NAME}`; guest-side placeholders substituted only for bound hosts |
| Custom disk snapshots, live snapshots, suspend/continue | Native microsandbox disk snapshots, full (memory) snapshots and forks |
| Nix-built guest image | One CI-built OCI base image, plus a per-sandbox spec built with mise and apt into a cached template snapshot |
| OpenCode only, app-state profiles | OpenCode, Claude Code and Codex through harness adapters; personas |
| gbrain on the host with Postgres/Bun/tokens | Built-in memory: SQLite, local embeddings, consolidation |
| Named tmux browser terminals | Same, over the guest agent channel |
| No git approvals | Git review: pushes are held until approved |
| — | Browser broker with approvals and live view |

**Dropped:**
- the gbrain install, Postgres and token-renewal machinery
- OpenCode release-pinning tooling (the base image pins harness versions instead)
- the libvirt probe
- Landlock worker confinement (VM isolation plus host-side policy replaces it)
- favicon discovery (it can come back later)

## 4. Concepts

- **Environment.** The top-level isolation boundary, e.g. "work" or "private". It owns:
  - its providers and logins
  - its secrets and rules
  - its personas (and so their sandboxes and private memory)
  - its shared memory
  - its approvals

  Nothing is shared between environments. The UI shows one environment at a time and has a
  switcher dropdown.
- **Persona.** A named agent identity, treated like a team member. It has:
  - a name, avatar and role description
  - a "soul": personality and working rules, as markdown
  - a harness (OpenCode, Claude Code or Codex), a provider and a model
  - a git identity
  - default network scope and approval auto-rules
  - a private memory scope

  A persona owns one or more sandboxes.
- **Sandbox.** A microVM "machine" that belongs to one persona. It has:
  - a workspace disk
  - a Docker data disk
  - harness state (sessions and history)
  - the spec it was built from

  It persists across stop and start.
- **Spec / Template.** A YAML sandbox spec covering resources, tools, apt packages and a
  setup script. A *template* is the disk snapshot that results from building a spec on the
  base image. It is cached by `hash(spec, base image digest)`.
- **Session.** One agent conversation by a persona in one of its sandboxes. It runs as a
  native TUI in tmux; programmatic sessions come later via ACP.
- **Gateway.** The host-side SOCKS5 and TLS proxy that sees every outbound connection from
  every sandbox and applies rules.
- **Rule.** A network policy entry scoped to environment, persona or sandbox. Kinds are
  `allow`, `proxy`, `caddy` and `deny`.
- **Secret.** A named credential in an environment's vault. It is bound to the hosts allowed
  to receive it.
- **Provider.** An environment's LLM access. Kinds:
  - Claude subscription login
  - ChatGPT/Codex subscription login
  - Anthropic API key
  - OpenAI API key
  - custom OpenAI-compatible endpoint

  The provider kind determines which harnesses can use it and which utility model memory
  jobs use.
- **Approval.** A typed permission request shown in the inbox. Kinds:
  - `network.domain`
  - `network.request`
  - `git.push`
  - `browser.action`
  - `browser.credential`
  - `memory.conflict`
  - `memory.share`

  Decisions are allow-once, allow-by-pattern (which creates a rule), deny-once and
  deny-by-pattern.
- **Memory.** Facts, pages and sources in two scopes: `persona:<id>` (private, shared by all
  of that persona's sessions) and `shared` (team-wide, written explicitly).

## 5. Architecture

```
React SPA (embedded) ──HTTP / SSE / WS──┐
┌──────────────────────────── sandbox-studio (one Go binary) ────────────────────────┐
│ API · approvals inbox · secrets vault · personas · specs/templates                 │
│ gateway: SOCKS5 in → identify sandbox → SNI/Host/DNS name → rules →                │
│          passthrough | TLS terminate (Studio CA) + inject/substitute | Caddy | deny│
│ memory: SQLite (FTS5 + vectors) · llama.cpp embeddings (yzma) · consolidation jobs │
│ sandbox manager (microsandbox Go SDK) · browser broker · git review                │
└──────┬───────────────────────────────────────────────┬─────────────────────────────┘
       │ all TCP egress (SOCKS5, user = sandbox id)    │ vsock host socket per sandbox
 ┌─────┴──────────────┐   ┌──────────────┐      ┌──────┴────────────────────────┐
 │ sandbox (persona A)│   │ sandbox (B)  │      │ studio-agent (in every guest): │
 │ harness TUI, tmux, │   │ …            │      │ terminals, previews, hooks,    │
 │ dockerd, workspace │   │              │      │ MCP server (memory/browser/…)  │
 └────────────────────┘   └──────────────┘      └───────────────────────────────┘
                                   ┌────────────────────────────┐
                                   │ browser VM: Chromium +     │ ← controlled only by
                                   │ agent-browser (no agent    │   the Studio broker
                                   │ access to CDP)             │
                                   └────────────────────────────┘
```

**Process model.** Studio runs as a normal user process; there is no system service.
- **Runtime:** it starts microsandbox through the Go SDK. The SDK embeds an FFI library;
  `EnsureRuntime` installs `msb` and `libkrunfw` into `~/.microsandbox`.
- **Shutdown:** sandboxes run detached and keep running when Studio quits or restarts; on
  start, Studio reconciles its records with microsandbox and the guest agents reconnect.
  Stopping a sandbox is explicit.
- **Long paths:** `MSB_HOME` must stay short because Unix socket paths are limited to 108
  bytes. The spike hit this limit.

**Data locations** follow per-OS conventions (`os.UserConfigDir` / `os.UserCacheDir`):
- `studio.db`: the SQLite catalog and memory
- `secrets/`
- `ca/`: the Studio TLS CA
- `models/`: the embedding GGUF
- `llama/`: the llama.cpp libraries
- `images/`: base-image digests

## 6. Component design

### 6.0 Environments

- Every row in the catalog (personas, sandboxes, rules, secrets, providers, memory, approvals,
  integration settings) carries an `environment_id`. Queries are always environment-scoped;
  the store layer requires an environment in its API, so a cross-environment read cannot
  happen by accident.
- **Gateway lookups** go sandbox → persona → environment, and only that environment's rules
  and secrets are considered. The rule scope formerly called "global" is now the
  environment scope.
- **UI:**
  - an environment switcher in the sidebar header
  - the selected environment is part of the URL (`/e/$env/...`), so tabs can show different
    environments
  - approvals from other environments still raise a notification and show a per-environment
    badge in the switcher, so nothing waits unseen
- Each environment has its own Studio CA certificate, so certificates and placeholders never
  validate across environments.
- **Onboarding** creates the first environment: name, provider (login or key), model (for
  custom endpoints), first persona, first sandbox.
- Export and import work per environment. Copying a persona to another environment copies
  its settings but never its memory or secrets.

### 6.1 Sandbox runtime (microsandbox)

- Use the Go SDK (`github.com/superradcompany/microsandbox/sdk/go`).
  - It needs CGO, so release builds run natively per OS.
  - Wrap it in `internal/runtime` so the rest of the code never imports the SDK directly.
- Stable Studio sandbox IDs map to versioned msb sandbox names (`sbx-<id>-<gen>`).
  - A restore or rebase creates generation `n+1` and retires `n`. The UI keeps one identity.
- Storage per sandbox:
  - Root: the template's layered root (overlay); persists across stop and start.
  - `/workspace`: an owned **disk** volume, included in snapshots and forks.
  - `/var/lib/docker`: an owned disk volume, so Docker overlay storage isn't nested on the
    root's overlay. This was verified in the spike.
  - `/opt/studio/studio-agent`: a read-only file mount of the Linux guest-agent binary
    shipped inside Studio, so updating Studio updates every guest.
- Init: none. microsandbox's agentd stays PID 1, which is what lets it freeze every guest
  process for full snapshots, forks and live disk snapshots; with an init handoff (systemd)
  msb 0.7.7 refuses all three. After each VM boot Studio runs `studio-agent boot`, which
  starts a supervisor for containerd, dockerd and the agent (restarting them with backoff).
  Before a stop Studio runs `studio-agent shutdown`, because msb gives the guest two seconds
  before it kills every process. Sandboxes created with systemd keep it; `boot` is a no-op
  there.
- Snapshot features, mapped to UX:
  - **Checkpoint:** a disk snapshot in snapshot group `sbx-<id>`.
    Captures root, workspace and Docker disks. Stop the sandbox before capture: the
    current microsandbox guest-flush path cannot freeze our systemd PID-1 workloads.
    Studio does not bypass that flush check for live disk captures.
    This is a filesystem checkpoint, not application-level database consistency or
    a saved process session. Stop applications first when their own recovery requires it.
  - **Suspend / Resume:** a full snapshot, then stop; restore resumes the processes,
    including agent TUIs.
  - **Fork:** a running sandbox becomes a new sandbox, with copy-on-write memory.
  - **Restore checkpoint:** a new generation from the snapshot.
    Currently gated off with microsandbox 0.7.7: disk restore loses the systemd init
    configuration ([upstream #1676](https://github.com/superradcompany/microsandbox/issues/1676)).
    Do not enable until the runtime preserves init and a real restore/restart passes.
    Requires the sandbox to be stopped. Studio restores and stops a replacement VM,
    records the new generation, then removes the old VM. A durable operation record
    lets startup finish cleanup or discard an uncommitted replacement after interruption;
    unresolved cleanup blocks further lifecycle changes instead of risking both VMs running.
  - **Export/import:** a `.msb` archive.
- Resource changes go through `modify`: live within the `max_*` limits set at boot, otherwise
  on the next start.

### 6.2 Base image, specs and templates

- **Base image** `ghcr.io/lukaskoebe/sandbox-studio-base:<ver>`:
  - Multi-arch: amd64 and arm64.
  - Built by GitHub Actions with buildx and pinned by digest in each Studio release.
  - Contents:
    - Debian stable, docker-ce, git, tmux, mise (no init system; see 6.1)
    - node (for the harnesses), ripgrep, jq, curl
    - pinned **OpenCode, Claude Code and Codex**
  - Optional extra: Nix (single-user, `/nix` on the root) for projects that use flakes or devenv.
- **Spec** (`spec.yaml`):

  ```yaml
  resources: { cpus: 4, memory: 8G, max_memory: 16G, workspace: 40G, docker: 30G }
  tools: { node: "22", python: "3.13", go: "1.25" }   # mise
  apt: [postgresql-client]
  setup: |
    corepack enable
  ```

- **Template build:**
  1. Boot the base image with gateway networking, using a "template-build" scope that
     includes a curated, user-approved registry rule set.
  2. Run `apt`, `mise install` and `setup`.
  3. Stop and take a disk snapshot.

  Templates are cached by hash. Specs can be edited in the UI as YAML, and the same file can
  be exported or imported.
- **Rebase:** moving an existing sandbox to a new template keeps its workspace. The mechanism
  depends on spike S5 (named disk volume vs. copying the workspace).

### 6.3 Guest agent (`studio-agent`)

A small static Linux Go binary (in `cmd/studio-agent`), mounted into every sandbox.

- **Channel:**
  - It dials the host over vsock (microsandbox host sockets: a Unix socket on
    Linux/macOS, a named pipe on Windows).
  - It runs one multiplexed session (yamux) per sandbox, and the host can open streams back.
  - The socket is per sandbox, so the sandbox's identity is implicit.
- **Services:**
  - **Terminals:** PTYs attached to tmux sessions.
  - **Port forwarding** for app previews: `https://<port>-<sandbox>.localhost:<studio-port>`.
  - **File browse and upload.**
  - **Health and status.**
  - **Hook handler:** `studio-agent hook <event> --harness <name>` reads the harness's hook
    JSON on stdin, calls Studio and prints the harness-specific response.
  - **MCP server:** `studio-agent mcp` (stdio) exposes memory, browser, approvals and git
    tools. All three harnesses support MCP.

### 6.4 Network gateway, rules and secrets

**Outer fence.** This is microsandbox's own policy, fixed at sandbox creation:
- Deny the host group and metadata endpoints.
- Allow public destinations, plus private ones where needed (see the open question below).
- Pin DNS resolvers.
- Send everything to the Studio SOCKS5 gateway, with username = sandbox id and a
  per-sandbox password supplied through an env var.
- UDP other than DNS is denied, so QUIC falls back to TCP.

**Gateway pipeline, per connection:**
1. Identify the sandbox from its SOCKS5 credentials.
2. Determine the name: TLS SNI → HTTP Host → the Studio DNS resolver's IP-to-name map
   (spike S2).
3. Match rules of the sandbox's environment, most specific scope first (sandbox, then
   persona, then environment; an environment-level `deny` always wins, as in v1). Rules can carry port lists; the default is 80 and 443.
4. Act on the rule kind:
   - `allow`: dial the destination IP and splice the bytes through.
   - `proxy`: terminate TLS with the Studio CA. Optionally:
     - inject headers (`{secret.NAME}`)
     - substitute secret placeholders
     - override the upstream, Host, SNI or upstream CA
     - apply request-level approval patterns (method + path → `network.request` approval)
   - `caddy`: terminate TLS and hand the plaintext request to an **embedded Caddy**,
     configured from the rule's Caddyfile snippet (the v1 editor). Caddy runs as a library:
     no listeners, no admin endpoint, its storage in Studio's data dir. Each rule is the
     routes of one site, compiled against an allowlist of matchers, handlers and
     `reverse_proxy` options. That allowlist:
     - refuses `{env.*}`, `{file.*}`, `{system.*}`, `{$VAR}` and `import`, so a rule can't
       read the host
     - requires fixed upstreams, dialed through the gateway's public-only dialer
     - refuses TLS options and forward proxies

     Secrets are written `{secret.NAME}` (v1 used `{env.NAME}`) and may only appear in
     `header_up` values. Caddy never sees them: it gets a marker that Studio's transport fills
     in on the way out, for HTTPS upstreams the secret is bound to. Responses are masked as
     for `proxy`. WebSockets aren't passed through Caddy rules.
     When a response needs secret masking, Studio masks all nonempty values, including
     overlapping occurrences, suppresses trailers, and refuses protocol upgrades or
     content encodings its upstream transport has not decoded. This masks literal values;
     it does not detect an upstream transforming or encoding a credential in its content.
   - `deny`: close the connection. For HTTP or intercepted TLS, return a readable 403 that
     tells the agent what to do.
   - No match: **hold and ask.**
     - Create a `network.domain` approval, buffer the ClientHello and hold the connection up
       to 60 s.
     - If approved, continue with the new rule. If time runs out, close (or return a 403
       "pending approval, retry later").
     - Repeated attempts merge into one request with a counter.

**CA trust.**
- Every environment has its own CA. Studio pushes it to the guest agent whenever the agent
  connects, together with the secrets' placeholders (again whenever secrets change). The
  agent adds the CA to the system trust store and writes both to `/etc/sandbox-studio`.
  The base image points `SSL_CERT_FILE`, `NODE_EXTRA_CA_CERTS`, `REQUESTS_CA_BUNDLE` and
  `GIT_SSL_CAINFO` at the system bundle.
- Containers and `docker build` steps inside the guest trust it with no extra flags. The image
  shadows `runc` on `PATH` with the agent, which adds a read-only mount of
  `/etc/sandbox-studio` at `/dev/sandbox-studio` (a tmpfs, so it never lands in an image)
  and the same variables plus `CURL_CA_BUNDLE`, unless the container already sets them.
  Images that set their own (e.g. `curlimages/curl`) need `-e CURL_CA_BUNDLE=/dev/sandbox-studio/ca-bundle.crt`.

**Secrets.**
- Values are encrypted in `studio.db` with a master key held in the OS keychain:
  - macOS Keychain
  - Windows Credential Manager
  - Linux Secret Service, falling back to a 0600 key file on headless Linux
- Each secret has bindings (`hosts: [...]`, at least one, never `*`). Sandboxes see its name
  as an env var holding a placeholder (`OPENAI_API_KEY=studio-<random>`). The gateway
  substitutes the placeholder only in requests to bound hosts and blocks (and logs) attempts
  to send it anywhere else.
- **LLM keys and harness auth use this mechanism**, so real provider keys never enter a
  sandbox.

**Observability.** A per-sandbox connection log (name, port, verdict, rule, bytes) is
shown in the UI and kept for a bounded period.

### 6.5 Approvals inbox

- One table, `approvals`, with:
  - `kind`, `subject` (persona/sandbox/session), `payload` (typed per kind), `status`
  - `created_at`, `expires_at`, `decision`, `decided_by`, `rule_created`
- Real-time:
  - An SSE stream, `/api/events`, pushes new approvals to every open tab.
  - A toast or popup, plus an OS notification through the browser Notification API.
  - A global badge.
- Each approval kind has a renderer:
  - domain + attempts
  - HTTP request preview
  - git diff
  - browser action plus a screenshot with the target highlighted
  - memory conflict side by side
- **Pattern rules.** "Always allow" creates a rule whose matcher is pre-filled from the
  request and can be edited before saving. Examples: `*.npmjs.org`, `POST
  /repos/*/pulls`, `browser.click on github.com/* where label~"Merge"`.
- Auto-decided requests are logged too, so the inbox doubles as an audit log.

### 6.6 Personas, providers and harness adapters

**Persona settings.**
- Identity and soul (markdown).
- Harness: `opencode | claude | codex`.
- Provider (one of the environment's) and model.
- Git identity, e.g. `Ada (agent) <ada@agents.invalid>`.
- Default rule scope and approval auto-patterns.
- Memory scope.

**Providers and logins.**

| Provider kind | Harnesses | How credentials reach the agent | Utility model |
|---|---|---|---|
| Claude subscription | Claude Code (and OpenCode only if its Anthropic login is supported; verify) | `claude setup-token` run in a system sandbox during onboarding; the resulting long-lived OAuth token is stored as a secret; guests get `CLAUDE_CODE_OAUTH_TOKEN=<placeholder>` and the gateway substitutes it on `api.anthropic.com` | `haiku` alias, via `claude -p --model haiku` in a system sandbox |
| ChatGPT / Codex subscription | Codex | `codex login` (device-code flow) in a system sandbox; Studio holds `auth.json` and owns token refresh. How the guest authenticates without holding the refresh token is spike S8; fallback is a guest-side `auth.json`, flagged in the UI as weaker isolation | `gpt-6-luna`, via `codex exec -m gpt-6-luna` in a system sandbox |
| Anthropic API key | Claude Code, OpenCode | placeholder for `ANTHROPIC_API_KEY` | `claude-haiku-4-5` via the Messages API |
| OpenAI API key | Codex, OpenCode | placeholder for `OPENAI_API_KEY` | `gpt-6-luna` via the Responses API |
| Custom OpenAI-compatible | OpenCode, Codex (custom `model_providers`) | placeholder key; base URL in harness config | the single model chosen in onboarding, via Chat Completions |

- **Why subscription utility calls go through the CLIs:** subscription logins are meant for
  the vendors' own clients. Running `claude -p` / `codex exec` headless in a dedicated
  system sandbox keeps that usage inside the official clients. Memory jobs batch many items
  per call to amortize the CLI overhead, and use the CLIs' structured-output modes (verify
  flags in S8).
- The utility model is derived from the environment's provider and is not a separate
  setting. API-key providers get a default daily spend cap (shown in the UI); subscription
  providers are capped by number of calls per day instead.

**Adapter interface** (`internal/harness`):

```go
type Harness interface {
    Name() string
    // Config files to write into the guest home: instructions block, MCP server
    // registration, hooks, provider/model, auto-approve mode.
    Render(p Persona, s Sandbox, providers Providers) []GuestFile
    TUICommand(session SessionOpts) []string       // launched in tmux
    ACPCommand() []string                          // programmatic sessions (later)
    StateDirs() []string                           // persisted on the sandbox
    ParseHook(event string, stdin []byte) (HookEvent, error)
    RenderHookResponse(HookResult) []byte
}
```

**Capability matrix.** Cells marked (verify) are covered by spike S7.

| | OpenCode | Claude Code | Codex |
|---|---|---|---|
| Instructions | `AGENTS.md` | `CLAUDE.md` importing `@AGENTS.md` | `AGENTS.md` |
| MCP | `opencode.json` `mcp` | managed MCP config | `config.toml` `[mcp_servers]` |
| Hooks | JS plugin that calls `studio-agent hook` (verify event names for the pinned version) | managed settings hooks | system-layer `hooks.json` (managed, so no trust prompt) |
| Context at session start / after compaction | plugin (verify) | `SessionStart` `additionalContext` | `SessionStart` `additionalContext` (sources incl. `compact`) |
| Context per prompt | plugin `chat.message` / system transform (verify) | `UserPromptSubmit` | `UserPromptSubmit` |
| Before compaction | `experimental.session.compacting` | `PreCompact` | `PreCompact` |
| Session end | `session.idle` | `SessionEnd` / `Stop` | `SessionEnd` / `Stop` |
| Programmatic (later) | `opencode acp` | `claude-agent-acp` adapter | `codex-acp` adapter |
| Auth via gateway placeholder | provider key | `ANTHROPIC_API_KEY` / `CLAUDE_CODE_OAUTH_TOKEN` (verify) | `OPENAI_API_KEY`; ChatGPT login (verify, S8) |
| Inside-sandbox permissions | allow all | bypass permissions | full auto |

- **Inside-sandbox permissions.** Agents run without in-sandbox permission prompts; the VM
  is the boundary. Outward actions go through Studio approvals.
- **Instructions.** Persona instructions are delivered as SessionStart context together with
  core memory, plus a small managed block in the instructions file that points to the memory
  tools. Nothing persona-specific lives in the workspace repository.

### 6.7 Memory

**Scopes.**
- `persona:<id>` is private and shared by every session of that persona across all its
  sandboxes.
- `shared` is team-wide. Every persona can read it; writes are explicit (a `share` tool or
  promotion). Every write records its author persona.
- When a persona fact conflicts with a shared fact, the result is a `memory.conflict`. Until
  it is resolved, the shared fact wins at recall, with a note.

**Data model** (SQLite as the source of truth; inspired by gbrain's compiled-truth and
timeline split and its takes-vs-facts distinction):

- `sources`: where information came from.
  - Kind: `user` (stated in chat or edited in the UI) | `verified` (the agent ran a check and
    cites the command and result) | `document` (repo or file import) | `inferred` (the
    agent's own conclusion) | `consolidation`.
  - Plus session ref, persona, timestamp and a short evidence quote. Full transcripts are
    **not** stored.
  - Trust tier ranking: `user > verified > document > inferred`.
- `facts`: atomic claims.
  - Fields: `scope`, `kind` (preference | decision | fact | procedure | event),
    `entity_ids`, optional normalized `attribute` (e.g. `deploy.command`), `text`,
    `observed_at`, `valid_from`, `valid_until`, `confidence`, `tier`, `support_count`.
  - Status: `active | superseded | disputed | retracted`.
  - Plus `supersedes`.
- `pages`: entity and topic pages (person, project/repo, topic, procedure, persona-self).
  - **Compiled truth** above the line: a regenerated synthesis.
  - **Timeline** below: append-only dated entries linking facts and sources.
  - `always_load` marks *core* pages. Their total is capped (about 4k chars per scope);
    writes over the budget are refused with guidance.
- `conflicts`: fact A, fact B, verdict, status, resolution.
- `chunks` and `embeddings` over pages and facts for retrieval.

**Write paths.**
1. **Automatic extraction.** On `PreCompact` and `SessionEnd`/`Stop` (debounced), the hook
   sends the transcript segment since the last checkpoint. Studio then:
   - runs the *utility model* to extract candidate facts, each with kind, tier, entities and
     an evidence quote
   - deduplicates them by embedding similarity plus attribute key
   - writes them to the persona scope

   This removes the dependence on agents remembering to write.
2. **Explicit tools:**
   - `remember(text, kind, entity?, scope)`
   - `share(fact_id | text)`, which writes to shared
   - `correct(fact_id, text)`
   - `forget(fact_id)`

   The managed instructions tell agents when to use them, e.g. for user-stated preferences
   and verified procedures.
3. **The user** edits pages and facts in the UI (tier `user`).

**Read paths.** Placement follows gbrain's finding that boundaries beat per-message
retrieval.
1. **Session start** (also on resume and after compaction), a budgeted *context pack*:
   - persona soul
   - core pages (persona and shared)
   - the project page for the current repo (keyed by git remote)
   - shared-memory changes since this persona's last session
   - open conflicts touching those entities
2. **Per prompt:** local hybrid search over the prompt, injected only when hits clear a
   relevance threshold, capped at about 500 tokens. Every injection is logged.
3. **Tools:** `memory_search` and `memory_get`, for when the agent wants more.
4. **Browsable view:** a read-only rendered markdown view at `~/memory/{persona,shared}`
   that agents can `rg` through.

**Retrieval.**
- BM25 (FTS5) and vector search, fused with reciprocal-rank fusion.
- Boosts by tier and by recency, with decay depending on kind (preferences decay slowly,
  events quickly).
- `superseded` facts are excluded unless asked for; `disputed` ones are returned with a
  marker.
- Vectors are scanned brute-force in memory as int8. That is fine up to roughly 100k chunks;
  an ANN index is a later concern.

**Embeddings.**
- llama.cpp loaded from Go through [yzma](https://github.com/hybridgroup/yzma) (purego, no
  extra cgo), with prebuilt per-OS libraries (Metal on macOS, CPU elsewhere).
- The model is embeddinggemma-300m in GGUF form, downloaded with checksum verification when
  memory is first activated. There is one model and no model picker.
- The embedding model and version are recorded per vector. Changing the model triggers a
  background re-embed.

**Consolidation ("dream") job.** Runs nightly, after N new facts, or on demand, per scope:
1. Group new facts by entity and attribute.
2. Merge duplicates (raising `support_count` and linking sources).
3. **Conflict detection.** An LLM judge with temporal reasoning (modeled on gbrain's
   contradiction probe) returns one of:
   - `no_conflict`
   - `duplicate`
   - `temporal_supersession`: the value changed over time
   - `contradiction`
   - `context_dependent`: both are true in different contexts

   The judge is given dates and tiers. It must establish the same entity and the same
   attribute, and two different times, before it may call something a supersession.
4. **Resolution policy:**
   - Temporal supersession where the newer fact has an equal or higher tier: set
     `valid_until` on the old fact automatically. This is logged and reversible.
   - Contradiction, or a newer fact with a lower tier: open a `memory.conflict` approval
     showing both facts with their sources. Both stay retrievable, marked `disputed`.
   - Context-dependent: rewrite both facts with explicit qualifiers and link them.
5. Regenerate the compiled truth of the pages it touched, and append timeline entries.
6. Suggest promotions from persona to shared memory (facts about the user, shared infra or
   shared projects) as `memory.share` approvals.
7. Suggest core-page candidates.

Judge results are cached by `(fact pair hash, model, prompt version)`. A daily cost budget
applies, and its usage is shown in the UI.

**Multiple data sources.** Every input — sessions from different personas, imported
documents or repos, user edits, and later external connectors — enters as a `source` with a
tier. Consolidation is source-aware, so a disagreement between a user statement and an
agent inference is never silently resolved in favor of the most recent one.

**Utility model.** One provider and model setting used for extraction, judging and
synthesis, defaulting to the cheapest model of the configured provider. Calls go straight
from Studio, not through a sandbox.

**Memory UI:**
- persona and shared views of pages, facts and timeline
- the conflicts queue
- a per-session log of injections and writes
- "why was this recalled"
- quality metrics: injections per session, follow-up `memory_get` rate, writes per session,
  open conflicts

**Portability.** Markdown export and import of pages with facts in frontmatter, so v2
memory can be moved to gbrain or qmd later.

### 6.8 Browser broker

- **Isolation.** The browser runs in a separate microVM: Debian Chromium (arm64 and amd64)
  with agent-browser. Its egress goes through the same gateway, so domain approvals apply.
  Only Studio can reach it, over the browser VM's studio-agent channel. **Agents never get
  CDP.**
- **Tools.** Agents get MCP tools from `studio-agent mcp`:
  - `open`, `snapshot`, `click`, `fill`, `type`, `press`, `select`, `scroll`, `get_text`
  - `screenshot`, `wait`, `back`
  - `fill_credential(ref, name)`

  Studio maps each call to an agent-browser command executed in the browser VM.
- **Policy before execution:**
  - Deny `eval`/`addscript`/`setcontent`, network routes, HAR export, cookie and storage
    reads, and `get value` on password-like fields.
  - Downloads and uploads require approval.
  - Everything else goes through approval patterns, matched on action, URL, element role
    and label. Examples: auto-allow `snapshot`, `scroll` and `get_text`; ask for
    `click`/`fill` on new origins.
- **Output filtering.** Snapshots have values blanked for `input[type=password]`,
  `autocomplete=current-password|new-password|one-time-code|cc-*`, and fields marked
  sensitive by rule.
- **Credentials.** `fill_credential` opens a `browser.credential` approval. On approval,
  Studio fills the value from the vault directly. The agent sees only the credential's name.
- **Live view and takeover.**
  - agent-browser's WebSocket screencast (JPEG frames) is relayed to the UI.
  - User mouse and keyboard events are forwarded as input events, so the user types
    passwords directly without them passing through the agent.
  - The UI shows an "agent paused while user drives" toggle.
- **History.** An action log with a screenshot per action and per-session replay. rrweb is
  deliberately not used: it needs page injection, breaks under strict CSP, misses canvas and
  cross-origin frames, and makes input forwarding awkward.

### 6.9 Git review and the integration interface

- **Built-in "git review" integration:**
  - Agents' remotes point at `https://git.studio.internal/<forge>/<owner>/<repo>.git`, which
    the gateway serves virtually.
  - **Fetch:** proxied to the real forge with a read token injected.
  - **Push:** received into a host-side staging repo (go-git) and answered with
    `remote: queued for review`. A `git.push` approval shows commits and the diff.
  - **Approve:** Studio pushes upstream with the real credentials.
  - **Reject:** the agent is told on its next fetch, via a ref note and an MCP notification.
- **Forgejo adapter.** An optional "open PR" proposal after a push (Forgejo API, approved in
  the inbox). The GitHub and GitLab adapters share the interface.
- **Integration interface.** Compiled-in Go modules, enabled or disabled in settings:

  ```go
  type Integration interface {
      ID() string
      Routes() []gateway.VirtualHost              // e.g. git.studio.internal
      ApprovalKinds() []approvals.KindSpec        // payload schema + renderer id
      MCPTools() []mcp.Tool                       // exposed through studio-agent
      Jobs() []jobs.Spec
  }
  ```

  A third-party plugin runtime (OCI image per plugin in its own microVM) is revisited after
  two or three integrations exist.

### 6.10 Terminals, previews and files

- Terminals: xterm.js ↔ WebSocket ↔ Studio ↔ studio-agent ↔ tmux. Sessions are named,
  survive UI reloads and are restored after a full-snapshot resume.
- Previews: `<port>-<sandbox>.localhost` routed to the guest port through the agent channel;
  a list of detected listening ports.
- SSH: `msb ssh serve` for VS Code Remote (documented, not wrapped).

### 6.11 Frontend

- Created with `pnpm dlx shadcn@latest init --preset b1a0At9rU --template start`, then
  converted to router-only (verified in the scratch build):
  - remove `@tanstack/react-start`; add `@tanstack/router-plugin`
  - add `index.html` and `src/main.tsx` (`RouterProvider`)
  - the root route renders `<Outlet/>`
  - `vite.config.ts` uses `tanstackRouter({ target: "react", autoCodeSplitting: true })`
- Stack:
  - **Preset:** base-mira style on Base UI, Phosphor icons, Inter, Tailwind v4.
  - **Data:** TanStack Query, with types generated from the API's OpenAPI spec.
  - **Terminals and editors:** xterm.js for terminals, CodeMirror for YAML and Caddy editing.
- Routes:
  - `/` overview: team, running sandboxes, inbox
  - `/inbox`
  - `/personas/$id`: identity, sandboxes, sessions, memory, settings
  - `/sandboxes/$id`: terminals, previews, snapshots, network log, spec
  - `/memory`: shared memory, conflicts
  - `/network`: rules, secrets, activity
  - `/browser/$session`: live view and history
  - `/settings`: providers, utility model, integrations, runtime doctor
- Built `dist/` is embedded with `//go:embed`. In development, Vite proxies `/api` to the Go
  server.

### 6.12 API and realtime

- Go 1.25, `net/http` routing and `log/slog`.
- API types are defined with [huma](https://github.com/danielgtaylor/huma) → OpenAPI 3.1 →
  `openapi-typescript` for the client.
- Realtime and streaming:
  - SSE `/api/events` for approvals, status and logs.
  - WebSockets (`coder/websocket`) for terminals and the browser stream.
- Auth: the server binds to `127.0.0.1` with a per-install token in an HttpOnly cookie, set
  through a one-time URL opened at launch. This protects against other local users and
  against DNS-rebinding sites.
  - The launch token is carried in a URL fragment, removed before loading application
    data, and exchanged once within ten minutes. `studio login-url` obtains a fresh link
    from the running server using a separate control credential from the local install key.
  - Studio sessions last thirty days. Preview launch tickets last one minute and establish
    separate eight-hour host-scoped cookies, stripped before proxying to a guest. Guest
    responses cannot set Studio authentication cookies or cookies for sibling hosts.
  - The LAN dev UI uses the same exchange and cookie checks through Vite's origin-checking
    proxy; it does not receive the install key or a privileged proxy bypass.

### 6.13 Storage and configuration

- SQLite: `modernc.org/sqlite` (pure Go, includes FTS5). Migrations are embedded SQL files.
- One declarative export/import (`studio-export.yaml`) covering:
  - personas
  - specs
  - rules (secrets by name only)
  - providers (credentials excluded)
  - integrations
  - approval patterns

  Memory is exported separately as markdown.

### 6.14 Security model summary

| Threat | Mitigation |
|---|---|
| Agent exfiltrates data to unapproved hosts | Every TCP connection goes through the gateway; unknown names are held for approval; UDP denied; DNS pinned (Studio resolver per S2) |
| Agent steals credentials | Secrets live only on the host; guests see placeholders; substitution only for bound hosts |
| Agent bypasses the browser policy | The browser runs in a separate VM; the agent has no CDP access; password values are redacted; credentials are filled by Studio |
| Agent pushes or acts on external services | Git pushes are staged for review; request-level approvals for write APIs |
| Guest reaches host services | The outer fence denies the host group; host services are reachable only over the per-sandbox vsock channel |
| Local web attacker / DNS rebinding | Loopback bind, token cookie, Host header check |

## 7. Repository layout

```
sandbox-studio/
  cmd/studio/            # host binary (main)
  cmd/studio-agent/      # guest agent (linux/amd64 + linux/arm64, CGO off)
  internal/
    runtime/             # microsandbox wrapper, generations, snapshots
    spec/                # spec parsing, template builds, cache
    gateway/             # socks5, sni/host sniffing, dns, rules engine, tls mitm, caddy
    secrets/             # vault, keychain, placeholders
    approvals/           # inbox, patterns, SSE fan-out
    persona/
    harness/             # opencode, claude, codex adapters
    memory/              # store, embed (yzma), search, extract, consolidate
    browser/             # broker, policy, redaction, stream relay
    integrations/gitreview/
    agentchan/           # vsock/yamux host side
    api/                 # huma handlers, auth
    store/               # sqlite, migrations
  web/                   # Vite + React + TanStack Router + shadcn
  images/base/           # Dockerfile for the base image
  images/browser/        # Dockerfile for the browser VM image
  docs/
  PLAN.md
```

## 8. Milestones

Each milestone ends with a working, demoable build on Linux. The macOS and Windows checks
are part of the acceptance criteria.

**M0 — Foundations and spikes** (results: `docs/spikes.md`)
- Repo skeleton, CI (lint and test on 3 OSes), the release-build matrix.
- Spikes S1–S8 (section 9).
- Accepted when: the spike report is written; the gateway and Docker-in-sandbox demo runs on
  Linux, macOS and Windows.

**M1 — Sandboxes and terminals**
- Base image v0, sandbox create/start/stop/delete, guest agent channel, web terminals
  (tmux), previews.
- UI shell with the preset.
- Accepted when: from the UI, you can create a sandbox, open two terminals, run
  `docker run hello-world` and open a dev-server preview.

**M2 — Gateway, approvals and secrets (v1 parity)**
- SOCKS5 gateway, rules (`allow`/`proxy`/`caddy`/`deny`) with scopes, hold-and-ask, the
  inbox with SSE popups and pattern rules.
- Secrets vault, placeholders and injection, the CA in guests (including Docker), the
  connection log, the Caddy editor.
- Accepted when:
  - `npm install` in a fresh sandbox triggers popups; approving lets the same install finish
    without a retry
  - a secret bound to `api.example` never appears in the guest
  - a v1-style Caddy rule works

**M3 — Specs, templates, persistence**
- Spec YAML, template builds and cache, workspace and Docker disks, checkpoints, suspend and
  resume (full snapshot), fork, restore, rebase, export and import.
- Accepted when: a suspend/resume brings a running agent TUI back mid-session; a fork creates
  an independent copy; a spec change rebuilds the template and rebases while keeping the
  workspace.

**M4 — Personas and harnesses**
- Persona CRUD, providers (Anthropic, OpenAI, OpenAI-compatible) with gateway-injected
  keys, the OpenCode, Claude Code and Codex adapters, git identity per persona, sessions in
  tmux.
- Accepted when: the same persona can run each of the three harnesses, each reaching its
  provider without a real key in the guest.

**M5 — Memory core**
- Store, yzma embeddings, hybrid search, hooks (session start, per prompt, pre-compaction,
  end) for all three harnesses, MCP memory tools, automatic extraction, the memory UI.
- Accepted when: a preference stated in one session of persona A is recalled automatically
  in a new session of A on another harness and in another sandbox, and is not visible to
  persona B.

**M6 — Shared memory, consolidation, contradictions**
- Shared scope, `share`/promotion approvals, the dream job (dedupe, judge, supersession,
  conflicts, compiled truth), the conflicts UI, core pages, the cost budget.
- Accepted when: contradictory facts from two sources produce a conflict item; resolving it
  changes what is recalled; a time-based change is superseded automatically with a visible
  timeline entry.

**M7 — Browser broker**
- Browser VM image, broker tools, policy and patterns, redaction, credential fill, live view
  with takeover, action history.
- Accepted when: an agent logs into a test app using `fill_credential` without ever seeing
  the password; the user approves clicks from the popup; the user can take over the live
  view.

**M8 — Git review and Forgejo**
- Virtual git remote, staged pushes, diff review, approve or reject, Forgejo PR proposals.
- Accepted when: an agent push shows up as a reviewable diff; approval pushes to a test
  Forgejo instance.

**M9 — Packaging and release**
- Signed macOS build (notarized) and Windows build, first-run doctor (KVM/HVF/WHP checks
  via msb doctor), autostart option, update check, docs.

ACP-driven chat sessions and persona "tasks"/heartbeats (OpenClaw-style proactive work) come
after M9, or slot in after M5 if wanted earlier.

## 9. Spikes and risks (M0)

| # | Question | Done when |
|---|---|---|
| S1 | Gateway and Docker-in-sandbox on **macOS and Windows** (already verified on Linux: sandbox boot about 1 s, nested container egress is attributed to the right sandbox, SNI deny works) | Same spike program passes on both OSes |
| S2 | Per-sandbox Studio DNS resolver (loopback nameserver accepted? rebind protection?) for DNS-level deny and IP→name for non-TLS ports | ssh to `github.com:22` is matched by name |
| S3 | vsock host-socket channel with yamux, both directions, on the 3 OSes (Unix socket vs named pipe) | Terminal and port-forward over the channel |
| S4 | CA injection: guest trust store, language runtimes, containers in nested Docker | `curl`, `node`, `python`, `git` and a container all trust MITM'd hosts |
| S5 | Named disk volumes in snapshots and forks vs owned volumes; best rebase mechanism | Decision recorded |
| S6 | Hold-and-ask: client timeouts for npm, pip, git, curl, docker pull while held | Defaults chosen per client |
| S7 | Harness hooks: exact OpenCode plugin events for the pinned version; Claude Code managed settings; Codex system-layer managed hooks without trust prompts | Context injection proven in all three |
| S8 | **(high priority)** Subscription auth: Claude Code OAuth token via placeholder; Codex ChatGPT login (refresh handled by Studio vs guest-side `auth.json`); headless structured output from `claude -p` and `codex exec` for utility jobs | A login-based persona runs in each harness without the long-lived credential in the guest (or the fallback is documented) |
| S9 | yzma + embeddinggemma on 3 OSes: latency and memory | Under 50 ms per short query on CPU |

Other risks:

- **Windows support in microsandbox is preview, and we have no Windows machine.** Windows
  gets CI builds and unit tests on every commit. VM-level checks (S1, S3) run on a cloud
  Windows VM with nested virtualization when we reach them, and the release notes mark
  Windows as preview until they pass.
- **CGO cross-builds.** Use native runners per OS/arch; no cross-compiling.
- **Hosted CI probably can't run VMs on macOS or Windows.** GitHub-hosted macOS and Windows
  runners are not expected to support nested virtualization (to be verified in M0), so VM
  integration tests run on Linux CI (which has KVM). macOS and Windows likely need real
  hardware or self-hosted runners.
- **Harness churn.** Config formats and hooks change often. Pin harness versions in the base
  image and give each adapter contract tests against recorded hook payloads.
- **microsandbox is pre-1.0 (0.7.x).** Pin the SDK version, wrap it in `internal/runtime`
  and upgrade deliberately.

## 10. Testing, CI and release

- **Go:** unit tests per package. Integration tests (`-tags integration`) start real
  sandboxes on Linux CI with KVM.
- **Gateway:** table tests with recorded ClientHellos and HTTP requests; a fuzz test for the
  SNI parser.
- **Harness adapters:** contract tests against recorded hook stdin/stdout fixtures per
  harness version.
- **Memory:** a fixture corpus with expected recall and conflict verdicts (eval script,
  tracked over time).
- **Frontend:** Vitest for logic; Playwright e2e against a real Studio on Linux CI.
- **Release:** GitHub Actions matrix (linux amd64/arm64, darwin arm64, windows amd64)
  builds `studio` with the embedded `web/dist` and the `studio-agent` binaries. The base and
  browser images are built and pushed by digest. macOS builds are signed and notarized;
  Windows builds are signed.

## 11. Conventions

- Single-line commit messages.
- `gofmt`/`golangci-lint`; `prettier`/`eslint` for `web/`.
- Each milestone closes with a short `docs/` page for the user-facing feature.

## 12. Open questions

1. **Signing.** Is there an Apple Developer account for notarization, and a Windows
   code-signing certificate, or do we ship unsigned at first?
2. **Shared-memory writes.** Visible immediately (with a feed and revert), or only after
   approval? Planned default: immediate, with feed and revert.
3. **Private networks.** Should sandboxes be able to reach private-network hosts (e.g.
   company services over VPN) through `proxy` rules? This determines the outer fence's
   private-range allowance. Planned default: allowed only through explicit `proxy` rules.
