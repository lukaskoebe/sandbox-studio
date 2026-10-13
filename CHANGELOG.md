# Changelog

## v0.3.0 — 2026-10-14

Teammates you talk to in one place, a command line, subscription sign-ins, and network
security levels.

- **Teammate conversation:** a teammate's page opens on one thread: its chat turns with
  their tools, the facts it learned (with Forget), its tasks and its terminal sessions, the
  approvals it waits on, and the running turn live. The composer sends a message, hands over
  a task or teaches a fact; a rail shows what it is doing, knows and runs on.
- **Command line:** `studio ls`, `show`, `start`, `stop`, `suspend`, `resume`, `create`,
  `fork`, `rm`, `shell`, `ask`, `approvals`, `approve`, `deny`, `checkpoints`, `checkpoint`,
  `restore` and `envs` talk to the running Studio, with `--json`, exit codes for scripts and
  shell completion.
- **Terminal UI:** `studio tui` lists sandboxes live, starts and stops them, decides
  approvals and attaches to a sandbox's terminal in full screen.
- **Memory:** chats are remembered once they go idle, each session leaves a short summary
  that the next sessions see, and the memory tools say when to use them. The memory page
  shows quality numbers, suggestions to promote or accept, and exports and imports one
  markdown file per scope. Facts can hold in a context, and each sandbox has a read-only
  `~/memory`.
- **Teammate tasks and check-ins:** teammates keep a task list with MCP tools, and check in
  on open tasks on a schedule.
- **Subscription sign-ins:** Claude and ChatGPT subscriptions sign in through small system
  VMs; their tokens stay on the host. Claude subscription chats run through Claude Code, and
  `claude` in a terminal starts without asking how to sign in.
- **Security levels:** each environment and teammate has a level that decides requests no
  rule covers, with a curated list of development infrastructure and an AI reviewer at the
  assisted level, which Activity lets you revert.
- **Network rules and approvals:** rules record their origin, can expire, count their hits
  and are flagged when unused for 30 days. Approvals can allow once or for an hour, are
  grouped when they arrive together, and start at the teammate's scope and pattern level.
  Rules can name a request's method and path; proxy rules can send requests to another
  upstream. Private hosts (such as a company VPN) are reachable only through an explicit
  per-environment list, and deny rules answer lookups with NXDOMAIN.
- **Browser:** browsing is scoped per site with request-level approvals, a tracker list is
  denied, downloads go to the agent and to you, and lost replies and large screenshots no
  longer break a call.
- **Teammates:** emoji or generated avatars, copying a teammate to another environment,
  environment export and import, and agents are told which sandbox they are in.
- **Sandboxes:** live CPU and memory resize where the VM allows, uploads from the Files
  panel, SSH docs, Nix as a template tool, and an export disk guard.
- **Git:** GitLab as a forge, integration MCP tools for push and pull request status, and a
  note in the next chat when a push is rejected.
- **Look:** Home as one dashboard with inline approvals and an activity feed, polished
  sandbox and Settings pages, Settings at the bottom of the sidebar, and themed scrollbars.
- **Quality:** golangci-lint in CI, fuzz targets for the gateway's parsers and the Caddyfile
  compiler, and a memory retrieval eval.

## v0.2.0 — 2026-10-13

Shaped by feedback on v0.1: a new layout and look, a guided setup, and safer defaults for
git and secrets.

- **Guided setup:** a first start opens `/setup`, a stepper through the host check, work
  mode, AI provider, a default teammate (Iris), git, network and a first sandbox. Every step
  can be skipped, and Home shows a setup card until it is done or dismissed. The login link
  is printed as a banner at startup.
- **New layout:** a compact sidebar with sandboxes, their status and the approvals waiting
  on them. Teammates get their own pages (overview, chats, memory, browser, settings), and
  settings are split into general, providers, git, templates and runtime. Each sandbox has a
  dashboard (facts, uptime, ports, connections, history, checkpoints, chats, terminals) and a
  separate workspace with one side panel at a time. Old addresses redirect.
- **Workbench look:** denser tables and lists, monospace identifiers with copy buttons,
  status dots with words, an accent color per area, and a light/dark/system theme toggle in
  the sidebar footer. Home shows a stat strip, a sandbox table, the family tree, approvals
  and teammates.
- **Network:** rules, requests and activity are tabs with their own URLs; rules are grouped
  by domain with filters kept in the URL.
- **Sandbox creation and fork** run in the background with step-by-step progress.
- **Lineage:** each sandbox records where it came from and what happened to it (created,
  forked, rebased, checkpointed, restored, deleted); the dashboard shows its history and
  Home the family tree, both updated live. Templates are named by their stack, such as
  `node-22+python-3.13@2`.
- **Persistent home directory:** new sandboxes get a 4 GiB home disk that is kept across
  rebases and is part of forks, checkpoints and exports. Template toolchains moved to
  `/opt/mise`. Exports from v0.1 still import.
- **Git over the host's SSH agent:** forges can use your SSH agent instead of a stored key,
  with a hint to use a dedicated key and a warning when signing is slow.
- **Scoped secrets:** a secret can belong to an environment, a teammate or a single
  sandbox; the narrowest one wins.
- **Template editor:** the template spec editor has YAML highlighting, completion from the
  spec vocabulary, validation as you type and an example template.
- **Agent browser:** a teammate can open an ephemeral session that starts clean and is
  discarded when closed, next to its usual one. Clicks in the live view land where you
  click.
- **New chat** preselects the teammate or sandbox it was started from; a chat started in a
  workspace opens there.
- **End-to-end tests:** a Playwright suite against a fake runtime runs in CI and nightly.
- **Base image:** the image build fails if the mise download fails.

## v0.1.1 — 2026-10-12

- Mobile terminals have touch controls for Ctrl, Esc, Tab, arrows and the keyboard.
- Browser takeover accepts mobile text input, including composed text and paste, and offers
  touch keyboard controls.
- Teammate sandboxes can show their current browser in a resizable side panel, with a
  full-width view on small screens and a toggle to hide it without stopping the browser.
- Copy buttons work on the local HTTP development server, including when accessed over the LAN.
- Memory hooks load in both OpenCode V1 and V2, fixing the plugin error in upgraded VMs.
- Stopping a chat immediately after sending a message preserves prompt/cancel ordering.
- The browser has an address bar and guidance for a blank start page.
- Dev-server previews handle IPv6 loopback listeners and closed idle connections.
- Long network approval patterns stay inside their cards, and the guest agent's directory
  is traversable by the sandbox user.

## v0.1.0 — 2026-10-11

The first public release.

- **Sandboxes:** microVMs on microsandbox with persistent workspace and Docker disks, Docker
  inside, browser terminals (tmux) and port previews, disk checkpoints, suspend and resume,
  fork, rebase onto a new template, and export/import as a `.studio-sandbox` file.
- **Templates:** YAML specs (resources, Node/Python/Go versions, apt packages, a setup
  script) built into cached, content-addressed templates.
- **Environments:** isolated sets of rules, secrets, providers, teammates and memory, with a
  switcher in the UI.
- **Network gateway:** all sandbox TCP egress goes through Studio; `allow`, `proxy`,
  `caddy` and `deny` rules per environment, teammate or sandbox; unknown hosts are held
  until approved in the approvals inbox; per-environment TLS CA; connection log.
- **Secrets:** an encrypted vault on the host; guests see placeholders that the gateway
  substitutes only for bound hosts.
- **Teammates:** named agents with a role, soul, git identity and a harness (OpenCode,
  Claude Code or Codex) on Anthropic, OpenAI or OpenAI-compatible API keys. Subscription
  logins are not supported yet.
- **Chat mode:** chats with teammates over the Agent Client Protocol, with permission
  requests and questions, per-turn changes and undo, a file browser, an embedded preview,
  and a handover to the terminal and back. Onboarding asks for Chat or Terminal.
- **Memory:** per-teammate and shared memory with hybrid search on local embeddings,
  automatic recall and capture through harness hooks, consolidation and contradiction
  handling.
- **Browser broker:** a Chromium VM per teammate driven only by Studio, with approvals,
  redaction, credential fill, history and a live view with takeover.
- **Git review:** a virtual git remote; pushes are staged and reviewed before Studio pushes
  them upstream. GitHub, Forgejo/Gitea, HTTPS and SSH forges; pull request proposals on
  GitHub and Forgejo.
- **Packaging:** `studio doctor`, opt-in start at login and update check, `install.sh`,
  release builds for Linux (amd64, arm64), macOS (Apple Silicon) and Windows (amd64,
  preview) with checksums and build provenance attestations.
