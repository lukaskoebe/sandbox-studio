# Frontend

`web/` is a single-page app: Vite, React, TanStack Router (file routes, no Start), TanStack
Query and shadcn on Base UI. `vite build` writes to `internal/webui/static/app`, which the Go
binary embeds (`//go:embed`) and serves at `/`.

## Stack

- shadcn preset: `base-mira` style on Base UI (`@base-ui/react`), Phosphor icons, Inter,
  Tailwind v4, base color stone (`components.json`). Generated components live in
  `src/components/ui`.
- Routing: `@tanstack/router-plugin` with `autoCodeSplitting`; the generated tree is
  `src/routeTree.gen.ts`.
- Data: `openapi-fetch` and `openapi-react-query` over the types generated from Studio's
  OpenAPI document (`src/lib/api`, see [API](api.md)). `src/hooks/use-events.ts` subscribes
  to `/api/events` and invalidates the affected queries. Lineage views don't poll: every
  sandbox start, stop, suspend, resume, rebase, checkpoint change and history change
  publishes on the `sandboxes` topic. A VM that stops on its own publishes nothing, so its
  lineage status updates at the next change or reload. Each new entry in a teammate's browser
  action log publishes on the `browser` topic, so the activity shows a step as it lands.
- Terminals: xterm.js, attached to tmux in the guest (mouse on). A drag selects in tmux, which
  copies with OSC 52; the terminal writes any OSC 52 clipboard to the browser's and refuses
  reads. Right-click opens Studio's Copy/Paste menu and never reaches tmux.
- Editors: CodeMirror (YAML specs, Caddyfiles). Toasts: sonner.
- Installable app: `public/manifest.webmanifest` (served as `application/manifest+json`)
  with the icons in `public/` (`icon.svg` is the source; the PNGs and `favicon.ico` are
  rendered from it, the maskable one without the rounded corners). There is no service
  worker: Chromium installs without one, and a cache would only serve a stale UI after an
  update. The pending-approval count goes on the app's icon with the Badging API
  (`approval-stack.tsx`), next to the tab title's. `e2e/install.spec.ts` asks Chromium for
  installability errors.
- Theme: the design tokens are in `src/styles.css` (the Workbench theme, see
  [design/ui-style.md](design/ui-style.md)). `src/lib/theme.ts` applies the system, light or
  dark choice, stored in the browser as `studio.theme`. Shared pieces such as `SectionHeader`,
  `Ident`, `KindChip` and `StatusDot` are in `src/components`.

## Routes

The environment is part of the URL, so tabs can show different environments. `/` redirects
to an environment.

| Route | Page |
|---|---|
| `/e/$env` | Home: what needs you (the approvals waiting, decided on the spot), the chats working now, and earlier: the latest network activity. The setup card shows until the setup is complete or dismissed, and an environment with neither a teammate nor a sandbox offers to make the first |
| `/setup` | the guided setup: host check, AI provider, teammate, git, network level and a first sandbox. Each step can be skipped, and the setup resumes at the first step without an outcome, or at `?step=`; it works on the environment last opened, or the first. The teammate's Edit and the sandbox's Open workspace add `?from=setup`, and those pages then link back to their step |
| `/e/$env/sandboxes/$id` | redirects to its workspace, with its parameters |
| `/e/$env/sandboxes/$id/work` | the sandbox: its chats, terminals and the git reviews waiting on it as tabs in one strip, the picked one in full, and one side panel. `?terminal=`, `?chat=` and `?review=` pick a tab; a review that opens by itself waits for a click; `?panel=` names the open panel (browser, connections or files). Open chat tabs are kept in sessionStorage per sandbox; chats with changes this browser hasn't seen (`lib/chat-seen.ts`, revs in localStorage) are marked green, and only they: other counts use the quiet `CountPill` |
| `/e/$env/chats`, `/chats/new`, `/chats/$chat` | chats ([design](design/chat-mode.md)). `/chats/new` takes `?teammate=` or `?sandbox=` to preselect a sandbox; `?origin=workspace` (the workspace's New chat) opens the chat it creates in `/sandboxes/$id/work?chat=` instead of the chat page |
| `/e/$env/settings/<section>` | Settings (below) |
| `/e/$env/teammates` | teammates |
| `/e/$env/teammates/$id` | the teammate's conversation: its thread, the composer (message, task, teach) and a rail with what it does now, its computer (its one sandbox; "Sandboxes" once it has more, with a menu to fork it or add another), its last chats, what it knows and its check-ins. The header names its role, harness and presence, and says what keeps it from working. The teammate's tabs are Conversation, Tasks, Memory, Browser and Settings |
| `/e/$env/teammates/$id/tasks` | the teammate's task list, and the log of its check-ins |
| `/e/$env/teammates/$id/chats` | the teammate's chats, from the rail's "All N" |
| `/e/$env/teammates/$id/memory` | the teammate's private memory, with the tabs of shared memory |
| `/e/$env/teammates/$id/browser` | the teammate's browser: the browser window (tabs, toolbar, live view) with Activity and Sites beside it ([browser](browser.md#live-view-and-takeover)) |
| `/e/$env/teammates/$id/settings` | one card per part, closed, with what it is set to: identity, agent, git identity, approvals, soul, check-ins, network level, browser, secrets (its own, its sandboxes' and the environment's it inherits, with which replaces which; Add secret preselects the teammate's scope) and delete. `?open=` names the open card (`components/disclosure-card.tsx`). One bar, "Unsaved changes · Discard · Save", saves the changes of every card, check-ins included, and shows only while there are some; leaving the page with changes asks first (`components/save-bar.tsx`). Switches and selects that save as they change (check-ins on or off, network level, browser) stay as they are |
| `/e/$env/git/reviews/$id` | one git push review: the commits and diff, decided there while it waits, its outcome after |
| `/e/$env/memory/conflicts/$id` | one memory conflict: both sides, resolved there while it is open, its verdict after |
| `/e/$env/builds/$id` | one template build: its log, and "Use spec" and "Create sandbox" once it is done |

Settings is one page (`routes/e.$env.settings.tsx`, the sections in `components/settings/`):
a nav on the left with the sections grouped by what they belong to, and the section in view at
`/e/$env/settings/<section>`. A search above the nav filters the sections by name and keywords
(`components/settings/link.tsx`). On phones the nav is a row above the section that scrolls
sideways. Its sections:

| Group | Section | Shows |
|---|---|---|
| This environment | General (`general`) | start at login, "Open setup" (`/setup`) once Home's setup card is gone, and the environment file (export and import) |
| | Network (`network`) | an intro, the security level, the network rules (filters in the URL: `q`, `scope`, `action`, `origin`, `unused`; grouped by registrable domain) and the curated list ([network](network.md)) |
| | Secrets (`secrets`) | secrets, in a section per scope: the environment's, then each teammate's, then each sandbox's, each with an add button that preselects its scope |
| | Shared memory (`memory`) | shared memory: pages, facts, conflicts, search, sessions |
| | Activity (`activity`) | every decided approval of the environment (network requests, git pushes and pull requests, chat tool permissions, browser actions, memory), newest first, with chips for All, Network, Git, Chats, Browser and Memory kept in `?kind=`. Its columns: Decided, What (the kind and the host or a summary; a push links to its review page), From, Decision (with Revert for a reviewer's network decision) and Details (the reason, or a push's outcome) |
| Accounts | AI providers (`providers`) | AI providers of the environment |
| | Git forges (`forges`) | the git forges of the environment |
| This computer | Templates (`templates`) | template builds and templates of the environment |
| | Runtime (`runtime`) | host checks (doctor) |
| | Ports and LAN (`ports`) | this computer's ports open to sandboxes (`host.studio.internal`) and the local network list: private addresses, ranges and names, such as a company VPN's ([network](network.md)) |
| | Updates (`updates`) | the update check (the whole installation) |

Requests waiting for a decision are on Home and in the approval stack, not in Settings.

`/e/$env/settings` opens General, and `/e/$env/settings/git` opens Git forges. An old
`?settings=<section>` link on any page of an environment, from when Settings was a dialog,
redirects to that section (`git` to `forges`, `network` to `ports`).

On the first load of a browser session, `/e/$env` redirects to `/setup` while the setup is
neither completed nor dismissed and the environment has no sandbox yet; the check runs once
per session (`studio.setup.routed` in session storage). The setup's progress is stored on the
server as the `setup.v1` setting and read with `GET /api/setup?env=` ([API](api.md)). A step
reads done when its condition already holds (the host check passes, or the environment has
a provider, a teammate, a forge or a sandbox); the setup is complete once
every step is done or skipped.

The first sandbox step lists the repositories of the environment's forges that have a list
(Forgejo, GitHub, GitLab and folders on this computer) as a searchable checklist. The ones
picked are cloned into `/workspace` one after the other once the sandbox runs, each showing
how it went; while nothing was cloned yet, more can be picked there
([git review](git-review.md#cloning-into-a-sandbox)).

The old addresses redirect: `/e/$env/network` and `/network/rules` to Settings → Network
(the rules keep their filters), `/network/requests` to Home, `/network/activity` to Settings →
Activity with `?kind=network`, `/git` to Settings → Activity with `?kind=git`, and `/secrets`,
`/memory`, `/providers` and `/builds` to their sections. `/browser` redirects to the teammate
browsers above; `/browser?teammate=<id>` opens that teammate's browser.

The sidebar is one tree: the environment switcher, Home and Network (the rules, with the
count of network approvals waiting), then each teammate with its
sandbox, then the sandboxes without a teammate, and a footer with All chats, Settings, the version
and the theme switch. A teammate's row says where it works and what it is doing: waiting on you
while one of its approvals is pending (a browser action or credential, a memory share, or a
tool permission in one of its chats), working while one of its chats is running, and idle
otherwise. Under a sandbox are its chats, its terminals (tmux sessions, a robot for an
agent's) and the git reviews waiting on it; each opens its tab in the sandbox's workspace,
whose URL names the tab in view (`?terminal=`, `?chat=` or `?review=`). A teammate with more
than one sandbox lists them, each with its status dot (the word is its tooltip) and the count
of approvals waiting on it. New chat sits at the top, with a menu for a new teammate, a
sandbox without a teammate or an import. The Settings page names the environment in its
header.
The approval stack ([approvals](approvals.md#realtime-and-ui)) is rendered by the root route
on every page.

The sandbox's header names its teammate and the sandbox, and has a pill with its status, CPU
and memory. The pill opens its details (`components/sandbox-details.tsx`): teammate,
template, resources, uptime, age, ID and listening ports, with start, stop and resources.
Next to it are a Checkpoint button and the options menu: history and checkpoints (lineage
and checkpoints in a sheet), another sandbox for the teammate, fork, rebase, export, suspend
or resume, stop and delete. The tab strip's + starts a chat with the teammate, the
teammate's harness in a terminal, or a shell; a sandbox without a teammate has New terminal.
The workspace ends in a status line: state, ID, template,
listening ports, approvals waiting, CPU and memory use while the sandbox runs (`cpu 12% · mem
1.2/4 GiB`, polled every 5 seconds, hidden below the large breakpoint) and generation.

Side panels (`components/side-panel.tsx`, our own splitter rather than a resizable-panels
dependency) hold what you watch next to the terminal or chat:
the teammate browser, connections, and files with changes. The chat page uses the same panel
for its workspace. One panel is open at a time, and on the workspace the URL names it. A
panel is resizable from its left edge on wide screens (1024px and up) and covers the screen
below that. Its width is remembered per page in local storage (`studio.panel.sandbox`,
`studio.panel.chat`). Sheets remain only for detail opened from outside its context: the
git push review (from the approval stack), a memory conflict (from the approval stack and
the Conflicts tab of Settings → Shared memory) and a build's log (from Settings → Templates).
Each of those sheets has "Open as page", which leads to the item's own page above; Home's
activity, Settings → Activity, Settings → Templates and the memory conflicts tab link there too. A page for an item that
doesn't exist says so, and one already decided shows the outcome without the buttons.

Links from when a sandbox had a dashboard (`/e/$env/sandboxes/$id`, with or without
`?terminal=`, `?session=` or `?chat=`) redirect to `/work` with their parameters.

## Development

- `pnpm dev` serves on port 3000 and proxies `/api` (with WebSockets) to Studio on 7878,
  presenting Studio's origin; `pnpm dev:lan` listens on all interfaces.
- Checks: `pnpm typecheck`, `pnpm lint` (ESLint), `pnpm check` (Prettier on
  `ts`/`tsx`/`js`/`jsx`), `pnpm build`, `pnpm e2e` (see below).
- After API changes run `make api` from the repository root.

## End-to-end tests

`web/e2e` holds the Playwright suite (`@playwright/test`, configured in
`web/playwright.config.ts`). It drives `bin/studio-e2e`, a Studio built with `-tags e2e`
(`make e2e-bin`, after `make web`). Each worker starts its own Studio with a fresh data
directory on a loopback port in 18400–18999; the gateway and template registry take the next
two. The D-Bus address points at a socket that doesn't exist, so the vault key goes to a file
in the data directory and never to the OS keychain.

The `e2e` build replaces the VM runtime and the guest agent with the fakes in
[`internal/e2e`](../internal/e2e):

- Sandboxes boot in milliseconds. The fake guest dials the sandbox's agent socket and speaks
  the real channel protocol: hello, config (recorded, so tests can read the placeholders),
  terminals that echo input, sessions, listening ports and TCP for previews.
- `POST /api/e2e/sandboxes/{id}/connect` makes the fake guest request a host through the real
  gateway, so network approvals appear. Every upstream host answers "hello from <host>".
- A teammate's browser VM boots like a sandbox, and its fake guest answers agent-browser
  commands from a small page model (`internal/e2e/browser.go`): every http(s) page is a sign-in
  form whose button goes to `/welcome`, each session has its own history, and screenshots and
  live-view frames are small JPEGs coloured by the URL. `POST /api/e2e/sandboxes/{id}/calls
  {method, params, wait}` makes a sandbox's agent call a tool (such as `browser_click`) through
  the real channel and broker, so browser approvals and the action log fill as they would;
  `GET /api/e2e/teammates/{id}/browser` reads the browser's sessions and the live view's input.
- Git push review reaches a fake forge. `PUT /api/e2e/upstream/{host}/{path}` (re)creates a
  bare repository with one commit on main, and `GET` lists its branches. The e2e build hands
  the git remote an HTTP client that sends every forge request to these repositories (git's
  smart HTTP, served by go-git), whatever the forge's host. `POST /api/e2e/sandboxes/{id}/push`
  makes the fake guest clone a repository through the gateway, as git in the sandbox would
  (`https://git.studio.internal/<forge>/<path>.git` over the SOCKS5 proxy, trusting Studio's
  CA), commit a file and push a branch; it answers with the commit and git's output.
- A chat's agent is a scripted ACP agent. The fake guest serves the `acp` stream with
  `acptest.Agent`, and every prompt plays the same turn: a message, a tool call that asks for
  permission (allow or reject), then the tool call's end and a closing message once answered.
  `GET /api/e2e/sandboxes/{id}` lists each agent's chat, prompts and permission outcomes.
- `/api/e2e/` exists only in the e2e build, behind the same login as the API. The gateway,
  approvals, rules, secrets, git review and the catalog are the real ones. The per-sandbox
  DNS resolvers are not started, because they listen on fixed ports; the fake guest names
  hosts in SOCKS5 instead.

Tests locate elements by role and label, and use `data-testid` only where a control has no
accessible name (approval cards).

`web/e2e-live` is a second, small suite against a real Studio that is already running and
boots real VMs. It has its own config, and reaches Studio through a login link:

```sh
cd web
STUDIO_LOGIN_URL="$(studio login-url)" pnpm exec playwright test -c e2e-live/playwright.config.ts
```

Its one scenario creates a sandbox in the UI, runs `docker run hello-world` in its terminal
(noted and left out when the image has no Docker), sets the level to Locked so that
`registry.npmjs.org` gets an approval card and allows it, then starts Vite from npm and opens
its preview. It puts the level back to Guarded and deletes the sandbox at the end. The smoke
workflow runs it on Linux after its own steps, and uploads traces, screenshots and videos
when it fails.
