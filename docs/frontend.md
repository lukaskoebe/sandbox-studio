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
- Terminals: xterm.js. Editors: CodeMirror (YAML specs, Caddyfiles). Toasts: sonner.
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
| `/e/$env/sandboxes/$id` | one sandbox's dashboard: status, owner, resources, ports, recent connections, checkpoints, chats and terminal sessions (each opens in the workspace), and its history (lineage timeline) |
| `/e/$env/sandboxes/$id/work` | its workspace: terminal tabs and chats in one strip, the picked one in full, and one side panel. `?terminal=` and `?chat=` pick a tab; `?panel=` names the open panel (browser, connections or files) |
| `/e/$env/chats`, `/chats/new`, `/chats/$chat` | chats ([design](design/chat-mode.md)). `/chats/new` takes `?teammate=` or `?sandbox=` to preselect a sandbox; `?origin=workspace` (the workspace's New chat) opens the chat it creates in `/sandboxes/$id/work?chat=` instead of the chat page |
| `/e/$env/network/rules`, `/requests`, `/activity` | network rules (filters in the URL, grouped by registrable domain), requests waiting for a decision, decided requests; `/e/$env/network` opens requests when any wait, else rules |
| `/e/$env/secrets` | secrets |
| `/e/$env/memory` | shared memory: pages, facts, conflicts, search, sessions |
| `/e/$env/teammates` | teammates |
| `/e/$env/teammates/$id` | one teammate's overview: agent, identity, soul, sandboxes, scoped secrets |
| `/e/$env/teammates/$id/chats` | the teammate's chats |
| `/e/$env/teammates/$id/memory` | the teammate's private memory, with the tabs of shared memory |
| `/e/$env/teammates/$id/browser` | the teammate's browser: live view, history, rules |
| `/e/$env/teammates/$id/settings` | edit the teammate, or delete it |
| `/e/$env/settings` | redirects to `general` |
| `/e/$env/settings/general` | work mode, start at login, updates (the whole installation) |
| `/e/$env/settings/providers` | AI providers of the environment |
| `/e/$env/settings/git` | forges and git push review of the environment |
| `/e/$env/settings/templates` | template builds and templates of the environment |
| `/e/$env/settings/runtime` | host checks (doctor) |

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
listening ports, approvals waiting and generation.

Side panels (`components/side-panel.tsx`) hold what you watch next to the terminal or chat:
the teammate browser, connections, and files with changes. The chat page uses the same panel
for its workspace. One panel is open at a time, and on the workspace the URL names it. A
panel is resizable from its left edge on wide screens (1024px and up) and covers the screen
below that. Its width is remembered per page in local storage (`studio.panel.sandbox`,
`studio.panel.chat`). Sheets remain only for detail opened from outside its context: the
git push review, memory conflicts and the build log of an approval card.

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
- `/api/e2e/` exists only in the e2e build, behind the same login as the API. The gateway,
  approvals, rules, secrets, git review and the catalog are the real ones. The per-sandbox
  DNS resolvers are not started, because they listen on fixed ports; the fake guest names
  hosts in SOCKS5 instead.

Tests locate elements by role and label, and use `data-testid` only where a control has no
accessible name (approval cards). Scenarios that need more fakes (git review, chats and the
browser) are `test.fixme` in `web/e2e/todo.spec.ts`.
