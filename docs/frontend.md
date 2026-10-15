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
  lineage status updates at the next change or reload.
- Terminals: xterm.js, attached to tmux in the guest (mouse on). A drag selects in tmux, which
  copies with OSC 52; the terminal writes any OSC 52 clipboard to the browser's and refuses
  reads. Right-click opens Studio's Copy/Paste menu and never reaches tmux.
- Editors: CodeMirror (YAML specs, Caddyfiles). Toasts: sonner.
- Theme: the design tokens are in `src/styles.css` (the Workbench theme, see
  [design/ui-style.md](design/ui-style.md)). `src/lib/theme.ts` applies the system, light or
  dark choice, stored in the browser as `studio.theme`. Shared pieces such as `SectionHeader`,
  `Ident`, `KindChip` and `StatusDot` are in `src/components`.

## Routes

The environment is part of the URL, so tabs can show different environments. `/` redirects
to an environment.

| Route | Page |
|---|---|
| `/e/$env` | Home: the environment at a glance (the sandbox table with the approvals waiting on each, the family tree, the approvals and teammates), create, import, and the setup card until the setup is complete or dismissed |
| `/setup` | the guided setup (PLAN N5): host check, work mode, AI provider, teammate, git, network and a first sandbox. Each step can be skipped, and the setup resumes at the first step without an outcome; it works on the environment last opened, or the first |
| `/e/$env/sandboxes/$id` | one sandbox's dashboard, laid out like Home: a facts band (status, owner, template, resources, age) with start, stop, resize and the workspace; chats, terminal sessions (each opens in the workspace), history (lineage timeline) and checkpoints; ports and recent connections at the side |
| `/e/$env/sandboxes/$id/work` | its workspace: terminal tabs and chats in one strip, the picked one in full, and one side panel. `?terminal=` and `?chat=` pick a tab; `?panel=` names the open panel (browser, connections or files) |
| `/e/$env/chats`, `/chats/new`, `/chats/$chat` | chats ([design](design/chat-mode.md)). `/chats/new` takes `?teammate=` or `?sandbox=` to preselect a sandbox; `?origin=workspace` (the workspace's New chat) opens the chat it creates in `/sandboxes/$id/work?chat=` instead of the chat page |
| `/e/$env/network/rules`, `/requests`, `/activity` | network rules (filters in the URL: search, scope, action, origin, unused; grouped by registrable domain), requests waiting for a decision, decided requests; `/e/$env/network` opens requests when any wait, else rules |
| `/e/$env/secrets` | secrets, in a section per scope: the environment's, then each teammate's, then each sandbox's, each with an add button that preselects its scope |
| `/e/$env/memory` | shared memory: pages, facts, conflicts, search, sessions |
| `/e/$env/teammates` | teammates |
| `/e/$env/teammates/$id` | one teammate's overview: agent, identity, soul, sandboxes, scoped secrets |
| `/e/$env/teammates/$id/tasks` | the teammate's task list, and the log of its check-ins |
| `/e/$env/teammates/$id/chats` | the teammate's chats |
| `/e/$env/teammates/$id/memory` | the teammate's private memory, with the tabs of shared memory |
| `/e/$env/teammates/$id/browser` | the teammate's browser: live view, history, rules |
| `/e/$env/teammates/$id/settings` | edit the teammate, its check-ins, network level and secrets (its own, its sandboxes' and the environment's it inherits, with which replaces which; Add secret preselects the teammate's scope), or delete it |
| `/e/$env/settings` | redirects to `general` |
| `/e/$env/settings/general` | work mode, start at login, updates (the whole installation), and "Open setup" (`/setup`) once Home's setup card is gone |
| `/e/$env/settings/providers` | AI providers of the environment |
| `/e/$env/settings/git` | forges and git push review of the environment |
| `/e/$env/settings/templates` | template builds and templates of the environment |
| `/e/$env/settings/runtime` | host checks (doctor) |
| `/e/$env/git/reviews/$id` | one git push review: the commits and diff, decided there while it waits, its outcome after |
| `/e/$env/memory/conflicts/$id` | one memory conflict: both sides, resolved there while it is open, its verdict after |
| `/e/$env/builds/$id` | one template build: its log, and "Use spec" and "Create sandbox" once it is done |

On the first load of a browser session, `/e/$env` redirects to `/setup` while the setup is
neither completed nor dismissed and the environment has no sandbox yet; the check runs once
per session (`studio.setup.routed` in session storage). The setup's progress is stored on the
server as the `setup.v1` setting and read with `GET /api/setup?env=` ([API](api.md)). A step
reads done when its condition already holds (the host check passes, a work mode is chosen,
the environment has a provider, a teammate, a forge or a sandbox); the setup is complete once
every step is done or skipped.

The old addresses `/e/$env/git`, `/builds`, `/providers` and `/browser` redirect to the
settings pages and teammate browsers above. `/browser?teammate=<id>` opens that teammate's
browser.

The sidebar holds the environment switcher, the navigation (Home, Chats, Network, Secrets,
Memory, Settings), the teammates and the sandboxes. A sandbox row has its status dot (the word
is its tooltip) and the count of approvals waiting on it. A teammate's dot shows its presence:
waiting for you while one of its approvals is pending (a browser action or credential, a
memory share, or a tool permission in one of its chats), working while one of its chats is
running, and idle otherwise. The Settings pages for an environment name it in their header.
The approval stack ([approvals](approvals.md#realtime-and-ui)) is rendered by the root route
on every page.

Both sandbox pages share a header: the sandbox's name and ID, its status, the page switch
(Dashboard, Workspace) and the options menu (fork, rebase, export, suspend or resume, stop,
delete). The dashboard's start and stop buttons and the workspace's tabs are the everyday
path; the menu holds the rest. The workspace ends in a status line: state, ID, template,
listening ports, approvals waiting, CPU and memory use while the sandbox runs (`cpu 12% · mem
1.2/4 GiB`, polled every 5 seconds, hidden below the large breakpoint) and generation.

Side panels (`components/side-panel.tsx`) hold what you watch next to the terminal or chat:
the teammate browser, connections, and files with changes. The chat page uses the same panel
for its workspace. One panel is open at a time, and on the workspace the URL names it. A
panel is resizable from its left edge on wide screens (1024px and up) and covers the screen
below that. Its width is remembered per page in local storage (`studio.panel.sandbox`,
`studio.panel.chat`). Sheets remain only for detail opened from outside its context: the
git push review, memory conflicts and the build log of an approval card. Each of those sheets
has "Open as page", which leads to the item's own page above; Home's activity, Settings → Git,
Settings → Templates and the memory conflicts tab link there too. A page for an item that
doesn't exist says so, and one already decided shows the outcome without the buttons.

Links from before the dashboard and workspace split that carry workspace parameters
(`?terminal=`, `?session=`, `?chat=`) redirect the dashboard to `/work`.

## Development

- `pnpm dev` serves on port 3000 and proxies `/api` (with WebSockets) to Studio on 7878,
  presenting Studio's origin; `pnpm dev:lan` listens on all interfaces.
- `/mockups` shows the two N4 visual directions with static fake data and no login: A is the
  app's theme now, B is kept for comparison. Dev builds only: elsewhere it renders not found.
  See [design/ui-style.md](design/ui-style.md).
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
