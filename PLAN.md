# Sandbox Studio — Plan

Status: in progress · 2026-10-12

The v2 plan is implemented (v0.1.0 and v0.1.1). How it works now is in the
[developer documentation](docs/README.md). This plan covers the next round, driven by user
feedback on v0.1.x. Principles stay as they were ([architecture](docs/architecture.md)): one
opinionated way per feature, the host decides, secrets never enter a sandbox, ask instead
of failing.

Items are grouped into milestones in the order they should land. Each item names what
changes, where, and when it counts as done. Sections marked **Concept** are designs to agree
on before anything is built.

## Decisions

| # | Question | Decision (2026-10-12) |
|---|---|---|
| D1 | New dev dependencies `@playwright/test` (e2e) and `@codemirror/lang-yaml` (spec editor) | Approved |
| D2 | Resizable panels: shadcn `Resizable` or our own splitter | Our own splitter as a shared component, no new dependency |
| D3 | Visual direction for the redesign (N4) | A, "Workbench" (2026-10-13), with the accent colors toned down slightly |
| D4 | Default teammate | "Iris", generalist engineer: curious, careful and direct; short soul |
| D5 | Network security levels (Concept C1) | Open: concept to review, not built yet |
| D6 | Host ssh-agent by default (reverses "never use the host agent") | Approved. When signing waits on a host confirmation (hardware key, password manager), show a dismissible hint to set up a dedicated Studio key |

## N1 — Safety net: end-to-end tests

Every later milestone restructures the UI, so the tests come first.

- **Fake runtime for UI tests.** A `-tags e2e` build of `studio` swaps `internal/runtime` and
  the guest channel for in-process fakes. Sandboxes "boot" in milliseconds, terminals echo,
  ports and connections are scripted, and the gateway, approvals, secrets, rules, git review and
  chats run for real against them. This is fast and runs on every CI push.
- **Playwright suite** in `web/e2e` (D1) against that binary, one Studio per worker with its
  own `SANDBOX_STUDIO_HOME`, `-addr` and an invalid D-Bus address (no keychain). First
  scenarios:
  - first start: login link, onboarding, provider, teammate, sandbox
  - sandbox lifecycle: create, terminal input/output, stop, start, delete
  - network: a held connection raises the approval card; Allow creates a rule; the card
    disappears in a second tab
  - secrets: create, placeholder shown in the guest env, value never in any API response
  - git review: push to the virtual remote, approve, upstream receives it (local bare repo)
  - chat: a scripted ACP fake answers, asks for a permission, gets it
  - browser: live view frames render, a click maps to the right page coordinates (also
    covers N2's mouse fix)
  - approval card and other overflow checks at 390 px and 1440 px
- **Real-VM smoke test on CI.** GitHub's Linux runners expose KVM (to verify; it needs a
  udev rule). A nightly and pre-release job runs the existing `integration` test plus a
  short Playwright run against a real Studio: create a sandbox, run
  `docker run hello-world`, approve `registry.npmjs.org`, open a Vite preview.
- Done when CI runs the fake suite on every push and the KVM job nightly, and the
  `CONTRIBUTING.md` checks include `pnpm e2e`.

## N2 — Quick fixes

- **Browser view mouse offset.** `components/browser-live.tsx` maps clicks with the canvas
  rect and the frame metadata's `deviceWidth/Height`. It ignores `offsetTop` and
  `pageScaleFactor`, maps against new metadata while the previous frame is still painted,
  and measures the rect after the mobile keyboard reflows the layout. Fix: keep the
  metadata with the frame it belongs to; map through the drawn frame's own size;
  apply `offsetTop` and scale; switch to pointer events with pointer capture (touch, and a
  release outside the canvas); and on touch, focus the hidden input only after the tap is
  sent. A Playwright test clicks known targets at several viewport sizes and zoom levels.
- **Login link banner.** At startup Studio prints the link as a framed, colored block on
  stderr when it is a terminal and `NO_COLOR` is unset. In every case it also keeps the slog
  line, for autostart logs (`cmd/studio/banner.go`, `x/term`).
- **Equal overview cards.** `h-full` on the card and a fixed card layout (N3 replaces the
  content).
- **Copy pass.** Page intros become one short sentence, with a "Learn more" link to the docs
  site where needed. The 15 longest texts the survey found go first (Git, Secrets, Browser,
  Teammates, Network, Memory, rule dialog, export tooltip). The Git forge "Name" field
  becomes "Short name" with the hint "Used in sandbox remote URLs". Below it, a live preview
  shows the full rewritten URL for an example repository instead of the
  `<name>/<path>` placeholder sentence.
- **Network page tabs.** Rules, Requests and Activity become tabs (`/network/rules`,
  `/network/requests`, `/network/activity`). Rules get search, scope and action filters, and
  grouping by host suffix.

## N3 — Information architecture

**Sidebar.**
- Top: environment switcher.
- Nav: Home, Chats, Network, Secrets, Memory (shared), Settings.
- Two lists below:
  - **Teammates:** avatar, name, a dot for "working / waiting for you / idle".
  - **Sandboxes**, as today.
- Builds, Git, Providers and Browser leave the main nav (see below).

**Teammate pages** `/e/$env/teammates/$id`. Everything that belongs to one teammate lives
here:
- Overview: role, soul, harness/provider/model, git identity, and their sandboxes.
- Chats.
- Memory (private scope; shared memory stays top-level).
- Browser: live view, history, origin patterns. This replaces `/browser` and its teammate
  picker.
- Settings: what `TeammateDialog` edits today, as a page.

**Settings subpages** `/settings/...`:
- General: work mode, start at login, updates.
- AI providers.
- Git: forges and push review.
- Templates: builds and the spec editor.
- Runtime: host checks.

Environment-scoped subpages show the current environment's name.

**Sandbox page split.**
- **Dashboard** `/sandboxes/$id`:
  - status, owner, template, resources, uptime
  - open ports with preview links
  - recent connections
  - the lineage view (N9) with checkpoints
  - chats and sessions
  - lifecycle actions
- **Workspace** `/sandboxes/$id/work`: the terminal or chat, plus side panels. Its toolbar
  keeps only what you use while working:
  - session/chat switcher and "new"
  - preview menu
  - panel toggles: Browser, Connections, Files/Changes
- **Options menu** (⋯, on both pages): Fork, Rebase, Export, Suspend/Resume, Stop, Delete.
- Checkpoints move from the sheet to the dashboard.

**Panels instead of sheets.** One side-panel system (D2) for everything you want to watch next
to the terminal or chat: browser, connections, files/changes, preview. Panels are resizable
on desktop, take the full screen on phones, and their sizes are remembered per page.
The chat's fixed-width workspace aside becomes such a panel. Sheets remain only for
short-lived detail opened out of context: git push review, memory conflict and build log
from an approval card. Each also gets a full page it can link to.

Done when the e2e suite passes on the new routes, old URLs redirect, and no page uses a
Sheet for content you'd want next to a terminal.

## N4 — Visual identity

The UI is clean but reads as stock shadcn. Goal: recognizably "Sandbox Studio" without
getting louder.

- **Process.** A short style brief (`docs/design/ui-style.md`) and two directions mocked on
  three screens (Home, sandbox workspace, approval card) as static pages in the dev build.
  You pick one (D3); then it is rolled out page by page behind the e2e suite.
- **Direction A, "Workbench":**
  - denser, tool-like
  - monospace for identifiers (hosts, ports, sandbox and template IDs)
  - section headers with a small colored icon tile
  - lists instead of cards for collections
  - a slim status bar per sandbox (CPU, memory, ports, pending approvals)
- **Direction B, "Calm studio":**
  - more whitespace
  - a warm neutral palette around the existing green
  - larger page titles with one-line intros
  - teammates rendered as people (avatars, presence) across the app
  - cards only for things you act on
- **Color accents, used sparingly and consistently.** One hue per approval kind and its
  page:
  - network sky
  - git violet
  - browser amber
  - memory teal
  - chat/agent green

  They appear in nav icons, approval cards, kind chips and the lineage view, and nowhere
  else. Status keeps its own fixed set (running green, waiting amber, failed red).
- Dark mode gets an explicit toggle (system / light / dark).

## N5 — First start and onboarding

- **Guided setup** `/setup`, opened on first start and resumable from Home until done.
  - Steps, each skippable:
    1. Host check (doctor), with fixes for the common failures.
    2. Work mode (Chat / Terminal). This replaces today's blocking dialog.
    3. AI provider: subscription login or API key.
    4. Teammate: the default teammate prefilled, so one click accepts it.
    5. Git: detect the host SSH agent and the repository you want to work on (N6). Skip
       keeps git local.
    6. Network level (C1, once built; until then a short explanation of approvals).
    7. First sandbox: created with live progress, then you land in its chat or terminal.
  - Progress is stored server-side (`settings` key `setup.v1`).
  - Home shows a checklist card until setup is complete or dismissed.
- **Default teammate.** When an environment's first provider is added, Studio creates a
  teammate:
  - "Iris", generalist engineer (D4)
  - harness picked by provider kind: Claude subscription/API → Claude Code, ChatGPT
    subscription/OpenAI API → Codex, custom endpoint → OpenCode
  - the provider's default model
  - git identity `<Name> (agent) <name@agents.invalid>`

  You can rename or delete it like any other.
- **Sandbox creation progress.**
  - Create becomes asynchronous: the API returns the sandbox in state `creating`, and the
    page follows progress over SSE.
  - The runtime switches to the SDK's `CreateSandboxWithProgress`:
    - image pull: bytes, layer n/m
    - VM start
    - `studio-agent boot`
    - services up
    - agent connected
    - CA and placeholders configured
  - Studio publishes these phases on a new `sandboxes` SSE topic. The dashboard shows a
    step list with a progress bar for the pull, and failures name the step.
  - Fork, rebase and template instances use the same view.

## N6 — Git over the host SSH agent

- **Paste a repository URL to set up a forge.** `git@github.com:owner/repo.git` or an HTTPS
  URL fills kind, host, short name and the repository path. Users stop thinking about the
  virtual URL; the form shows it as a preview (N2 copy).
- **Auth for SSH forges.**
  - **Use my SSH agent** is the default when `SSH_AUTH_SOCK` (Linux/macOS) or the OpenSSH
    agent pipe (Windows) is reachable.
  - **Studio key** (today's generated ed25519 key) stays as the alternative.
- **How the agent is used.**
  - Studio's host-side go-git transport (`internal/gitreview/ssh.go`) signs with
    `x/crypto/ssh/agent` instead of a vault key.
  - The agent socket is never forwarded into a sandbox. Only Studio uses it, for fetches
    and for approved pushes, so agents still go through push review.
  - Host keys stay pinned: built-in known keys for github.com, gitlab.com and bitbucket.org,
    and today's scan-and-confirm for the rest.
- **Trade-off (D6).** Fetches may prompt with hardware keys or 1Password-style confirmations.
  Studio rate-limits fetches per staging mirror, and the design doc's "never" becomes "only
  host-side, never forwarded".
- **Schema.** `forges.secret_id` becomes nullable for agent-auth forges (migration and
  trigger change).
- **Autostart.** A systemd or launchd start without the agent shows "SSH agent not
  available" on the forge and in the doctor.
- Done when an agent in a sandbox pushes to `git@github.com:…` through review, with the
  key only in the host agent.

## N7 — Persistent home directory

- **Today.** `/home/agent` lives on the template's root layer. It survives stop and start, but
  fork, rebase and export drop it, so OpenCode/Claude/Codex sessions and logins are lost.
- **Change: a third owned disk, `home`, mounted at `/home/agent`.**
  - About 4 GiB, separate from `/workspace` and its quota.
  - Seeded from the image's `/home/agent` on first boot.
  - Fork copies it, and rebase carries it over the same way as the workspace.
  - Export includes the harness state directories, from the existing `StateDirs()`.
- **Moving template tools out of home.** Template builds currently install mise toolchains
  under `/home/agent/.local/share/mise`, which the new disk would hide. They move to
  `/opt/mise` (the template layer). This bumps the template exporter version, so templates
  rebuild once.
- **Chats survive.** Chats no longer become orphaned on fork or rebase when the harness
  session still exists.
- Done when an OpenCode session started before a rebase resumes after it.

## N8 — Scoped secrets

- **Scope.** Secrets get the same scopes as rules: environment, teammate, sandbox. For the
  same name, the most specific scope wins. That way `GITHUB_TOKEN` can differ per teammate.
- **Schema.** Nullable `teammate_id` and `sandbox_id` columns with the same-environment
  triggers rules use.
- **Lookup.**
  - The vault's `Env` and `Bindings` filter by sandbox and owner, and the cache keys on
    the sandbox.
  - A secret change reconfigures only the affected sandboxes.
  - The browser's `fill_credential` lookup follows the same scope.
- **UI.** A scope selector like the rule dialog's. The table groups by scope, and teammate
  pages list their secrets.

## N9 — Lineage: forks, checkpoints and generations

**Concept.** Each sandbox's dashboard shows its history as a horizontal timeline, drawn like
a git graph:

- The line is the sandbox over time. Generations are segments, labeled with their template.
  A rebase is a template change on the line, and a restore jumps back to a checkpoint.
- Checkpoints are nodes. Clicking one offers Restore (when the runtime allows it; today it
  is gated by microsandbox #1736), Fork from here (same gate), Rename and Delete.
- Forks branch off to their child sandboxes and link to them. The parent shows as an
  incoming branch.
- A "now" node at the end holds the live state, with "Save checkpoint".

**Environment-wide view.** On Home, a compact family tree: templates → sandboxes → forks,
with status colors. It answers "where did this sandbox come from".

**Data.** This needs history the catalog doesn't keep today:
- a `forked_from` (sandbox, generation, time) column
- a `sandbox_events` table recording create, fork, rebase (from/to template), restore,
  checkpoint and delete (kept as a tombstone, so lineage survives deletion)

Drawn as plain SVG, with no graph library.

## N10 — Template editor

- **Editor.** CodeMirror with YAML (D1), the same theme as the Caddyfile editor. Autocomplete
  for top-level keys, resource units, tool names and versions, and apt package names
  from a small static list.
- **Validation.** `POST /builds/validate` runs the existing strict parser and returns errors
  with line and column (the parser walks `yaml.Node`, which has positions). The editor shows
  them as lint markers while you type.
- **Example.** One example template, "Web app": Node 22, Python 3.13, postgresql-client,
  `corepack enable`. It prefills a new build, replacing today's 512 MiB smoke-test example.

## N11 — Ephemeral browser sessions

- **What.** An agent can open a throwaway browser session:
  `browser_open(url, ephemeral: true)`. A new tool, `browser_close`, ends it.
- **How.** The session runs in the teammate's existing browser VM as a second agent-browser
  session with a temporary profile under `/tmp` (tmpfs). No cookies or storage reach the
  persistent profile on `/workspace`. It ends on close, after 10 minutes idle, when the
  owning chat or session ends, or when the VM stops. No second VM is needed.
- **Approvals.** The same per-origin approval rules apply as for the persistent profile;
  being ephemeral changes only storage. Credentials can still be filled through
  `fill_credential` approvals.
- **Live view.** Shows a session switcher: Profile / Ephemeral (n).
- **First step.** Verify agent-browser runs two sessions with different profiles in one
  VM; if it can't, fall back to an ephemeral browser VM per request.

## C1 — Network rules and security levels (Concept)

**Problem.** Browsing raises one approval per third-party host: trackers, CDNs, consent
banners and image hosts (seen in testing: googletagmanager, usercentrics, licdn and
wikimedia thumbs for two pages). Package installs do the same on a smaller scale. The rules
list grows into hundreds of one-off hosts nobody reviews.

**1. A security level per environment, overridable per teammate.** A four-step slider. It
sets what happens when no rule matches; explicit rules always win, and secrets only ever
reach bound hosts at every level.

| Level | No rule matches → | For whom |
|---|---|---|
| **Locked** | ask for everything (today) | sensitive work |
| **Guarded** (default) | allow a curated, versioned list of development infrastructure (package registries, toolchain downloads, container registries, forge read access, major CDNs); ask for the rest | most users |
| **Assisted** | like Guarded, then an AI reviewer decides allow / ask / deny for the rest; the decision and reason appear in Activity and can be reverted with one click | people who find popups tiring |
| **Open** | allow public egress and log it; deny lists still apply | throwaway experiments |

- **The AI reviewer.** It runs on the environment's utility model and sees:
  - the host
  - the method and path, where TLS is intercepted
  - the sandbox's current task: the last user prompt in its chat
  - which page initiated a browser request

  It never sees secrets or bodies, cannot allow requests that carry a placeholder, and asks
  when unsure. Its prompt treats hosts and paths as untrusted data. Its decisions are cached
  per host and task.
- **The curated list** ships with Studio as data. It is shown read-only in Rules, and
  individual entries can be turned off.

**2. Site-scoped browsing approvals.** The browser VM already trusts the environment CA.
If the gateway terminated TLS for all of the browser VM's traffic (today it only does that
for `proxy` rules), it could read `Sec-Fetch-Site`, `Sec-Fetch-Dest` and `Referer` there.
The broker also knows each tab's top-level URL. So:
- Approving a page approves the site, not a host. Subresource requests whose top-level
  document is that site are allowed for the session.
- Top-level navigations to new sites still ask, or are judged by the level.
- A built-in tracker deny list (EasyPrivacy-style domains, shipped as data) removes most of
  the noise before anything asks.

**3. Grouped approvals.** Requests that arrive together, from the same sandbox and the same
page within a few seconds, become one card: "docs.example.com wants 9 hosts". It offers
Allow all for this site, Review individually, or Deny. This replaces "+4 more".

**4. Rules that stay manageable.**
- Rules record their origin: manual, approval, curated or reviewer.
- Rules can expire: "Allow for 1 hour" sits next to "Always".
- Unused rules (no hits in 30 days) are flagged for cleanup.
- Rules group by registrable domain, with search and filters (N2's tabs).

**Order once agreed.** Grouped approvals and rule hygiene first (no new risk). Then
site-scoped browsing with the tracker list. Then the levels with the curated list, and
Assisted last.

## Backlog: still open from the v2 plan

Not scheduled in the milestones above. Items the milestones absorb are left out:
- the onboarding wizard, now N5
- Playwright, Vitest and KVM tests in CI, now N1
- auto-decided requests as an audit trail, now C1's Activity and rule origins

**Platforms and runtime**
- S1–S3 and S9 on macOS and Windows; arm64 guests; a Windows cloud VM →
  [spikes](docs/spikes.md#still-open)
- Checkpoint restore, blocked by microsandbox #1736 →
  [sandboxes](docs/sandboxes.md#checkpoints) (N9's "Restore" and "Fork from here" wait on it)
- Full-snapshot suspend instead of in-place pause; live CPU/memory resize
- Template build qualifications: release base, cold-cache pulls, live exec export with
  network installs, oversized captures →
  [template builds](docs/template-builds.md#current-qualification-boundary)
- Nix as an optional extra in the base image
- File upload into the guest; documented SSH access (`msb ssh serve`)
- Signed and notarized releases (the workflow exists, never exercised) →
  [release](docs/release.md)

**Agents and providers**
- Subscription logins (Claude Pro/Max, ChatGPT/Codex) and CLI-based utility calls; spike S8
  → [harnesses](docs/harnesses.md#verified-vs-pending)
- Live hook verification for all three harnesses; spike S7
- Teammate default rule scope, approval auto-patterns and an avatar field
- Teammate tasks and heartbeats (proactive work)
- Per-environment export/import and copying a teammate to another environment

**Network and approvals**
- DNS-level deny; per-rule upstream, Host, SNI and CA overrides
- Request-level `network.request` approvals; allow-once/deny-once decisions
- Access to private networks through explicit rules (public addresses only today)

**Memory**
- `context_dependent` judge verdict, dream promotion suggestions and core-page candidates
- Browsable `~/memory`, quality metrics UI, markdown export/import

**Browser and git**
- Browser downloads handed to the agent; the browser items pending a live run →
  [browser](docs/browser.md#verified-vs-pending-live)
- Integration interface MCP tools and jobs; telling the agent over MCP when a push is
  rejected; GitLab merge requests → [git review](docs/git-review.md)

**Quality**
- SNI parser fuzz test, memory eval corpus, recorded hook fixtures for all adapters,
  golangci-lint

## Testing and conventions

- Every milestone adds e2e scenarios for what it changes; nothing merges with a red suite.
- Developer docs (`docs/`) and the user docs site are updated in the same change as the
  behaviour.
- Single-line commits.
