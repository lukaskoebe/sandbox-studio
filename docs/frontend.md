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
  to `/api/events` and invalidates the affected queries.
- Terminals: xterm.js. Editors: CodeMirror (YAML specs, Caddyfiles). Toasts: sonner.

## Routes

The environment is part of the URL, so tabs can show different environments. `/` redirects
to an environment.

| Route | Page |
|---|---|
| `/e/$env` | sandboxes: create, import |
| `/e/$env/sandboxes/$id` | one sandbox: agent view (chat or terminal, chosen on first open), terminals, previews, browser panel, checkpoints, connections, fork, rebase, export, suspend |
| `/e/$env/chats`, `/chats/new`, `/chats/$chat` | chats ([design](design/chat-mode.md)) |
| `/e/$env/builds` | template builds and templates |
| `/e/$env/network` | rules and pending network requests |
| `/e/$env/secrets` | secrets |
| `/e/$env/git` | forges and git review |
| `/e/$env/teammates`, `/e/$env/providers` | teammates and providers |
| `/e/$env/memory` | memory pages, facts, conflicts, search, sessions |
| `/e/$env/browser` | teammates' browsers: live view, takeover, history |
| `/e/$env/settings` | agents, runtime (doctor), start at login, updates |

The sidebar holds the environment switcher and the navigation. The approval stack
([approvals](approvals.md#realtime-and-ui)) is rendered by the root route on every page.

## Development

- `pnpm dev` serves on port 3000 and proxies `/api` (with WebSockets) to Studio on 7878,
  presenting Studio's origin; `pnpm dev:lan` listens on all interfaces.
- `/mockups` shows the two N4 visual directions with static fake data and no login. Dev
  builds only: elsewhere it renders not found. See [design/ui-style.md](design/ui-style.md).
- Checks: `pnpm typecheck`, `pnpm lint` (ESLint), `pnpm check` (Prettier on
  `ts`/`tsx`/`js`/`jsx`), `pnpm build`.
- After API changes run `make api` from the repository root.
