# Developer documentation

How Sandbox Studio is built. User guides are on the docs site (`site/`); build and
contribution steps are in [CONTRIBUTING.md](../CONTRIBUTING.md) and the site's developer
guide.

Start with [architecture](architecture.md): goals, principles, concepts, process model,
data locations, repository layout, security model, testing and conventions.

## Components

| Page | Covers | Code |
|---|---|---|
| [Sandboxes and runtime](sandboxes.md) | VM naming, mounts, boot, resources, checkpoints, suspend, restore gate, base image | `internal/runtime`, `internal/sandboxes` |
| [Guest agent and channel](guest-agent.md) | vsock/yamux channel, stream kinds, subcommands, terminals, previews, CA trust in guests | `cmd/studio-agent`, `internal/guest`, `internal/agentchan` |
| [Network, secrets and CA](network.md) | fence, DNS, gateway pipeline, rules, hold-and-ask, interception, Caddy rules, vault, CA, connection log | `internal/gateway`, `internal/policy`, `internal/secrets` |
| [Approvals](approvals.md) | kinds, decisions, events, the approval stack | `internal/policy`, `internal/integrations`, `internal/events` |
| [API, auth and storage](api.md) | huma/OpenAPI, streaming, login and preview auth, SQLite and migrations | `internal/api`, `internal/webauth`, `internal/store` |
| [Frontend](frontend.md) | stack, routes, development | `web/` |
| [Terminal UI](tui.md) | `studio tui`: screens, keys, sign-in, terminal attach | `internal/tui` |
| [Command line](cli.md) | `studio ls`, `ask`, `approve` and the other commands: design, exit statuses, completion | `internal/cli` |
| [Template builds](template-builds.md) | specs, build worker, registry, instances | `internal/templatebuild`, `internal/templateregistry` |
| [Rebase and fork](rebase.md) | workspace and home copy between VMs | `internal/sandboxes` |
| [Export and import](export-import.md) | `.studio-sandbox` files | `internal/sandboxes` |
| [Teammates and providers](teammates.md) | provider kinds, teammates, ownership | `internal/teammates` |
| [Logins on this computer](host-logins.md) | offering providers from the host's Claude Code, Codex and opencode logins, and what Studio reads of them | `internal/hostlogins` |
| [Harnesses and agent sessions](harnesses.md) | adapters, files, hooks, versions, chats over ACP | `internal/harness`, `internal/chat` |
| [Memory](memory.md) | store, search, embeddings, hooks and tools, extraction, consolidation | `internal/memory`, `internal/agentmem` |
| [Browser broker](browser.md) | browser VM, tools, policy, redaction, credentials, live view | `internal/browser` |
| [Git review](git-review.md) | forges, virtual remote, staged pushes, PRs, integration interface | `internal/gitreview` |
| [The Studio app](app.md) | `studio app`: the Wails window, tray and notifications, the page's messages, building | `cmd/studio-app`, `internal/launcher` |
| [Release](release.md) | cutting a release | `.github/workflows/release.yml` |
| [Updates](updates.md) | the update check and automatic installs: verification, swap, restart, rollback | `internal/updates` |

## Background

- [Decisions and history](decisions.md): decisions log, what changed from v1, milestones.
- [Spikes](spikes.md): M0 spikes and later live qualifications, with what is still open.
- Design docs: [chat mode](design/chat-mode.md), [git remotes](design/git-remotes.md), [sessions on Home](design/tui-sessions.md), [scheduled tasks](design/scheduled-tasks.md).
- [PLAN.md](../PLAN.md): the backlog, what is still open.
