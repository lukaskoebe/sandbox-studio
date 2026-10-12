# API, auth and storage

## HTTP API

- `net/http` routing and `log/slog`. Operations are declared with
  [huma](https://github.com/danielgtaylor/huma) in `internal/api`, which produces the
  OpenAPI 3.1 document.
- `make api` writes it to `web/openapi.json` (`studio openapi`) and generates
  `web/src/lib/api/schema.gen.ts` with `openapi-typescript`. CI fails if either is stale.
- Almost everything is under `/api/environments/{env}/...`. Global routes: environments,
  `/api/approvals`, `/api/events`, `/api/health`, `/api/provider-kinds`, `/api/settings/ui`
  and `/api/system/*` (doctor, autostart, updates).
- Streaming endpoints are plain handlers outside huma (`api.go`, `exports.go`): SSE at
  `/api/events` for change notices (see [approvals](approvals.md#realtime-and-ui)) and at
  `…/chats/{chat}/stream` for a chat; WebSockets (`coder/websocket`) for terminal attach and
  the browser live view; sandbox export download and import upload.
- The handler chain is `api.Guard(auth.Middleware(preview.Route(…, mux)))`; the embedded UI
  (`internal/webui`) is served at `/`.
- The e2e build (`-tags e2e`) adds a control API under `/api/e2e/` for the fake runtime. It
  isn't in the OpenAPI document; see [frontend](frontend.md#end-to-end-tests).

## Auth

Studio listens on loopback only. `internal/webauth` and `internal/api/guard.go` protect it
from other local users and from web pages:

- `Guard` rejects a Host that isn't a loopback name (DNS rebinding) and unsafe methods whose
  Origin is another site (e.g. a form on a preview page).
- At start Studio logs a one-time login link, and on a color terminal also prints it in a
  framed box (not when `NO_COLOR` is set or stderr is not a terminal). The ticket is in the
  URL fragment, removed before the app loads data, and exchanged once within ten minutes for
  a host-bound, signed `studio_session` cookie (HttpOnly, SameSite=Strict, 30 days).
- `studio login-url` asks the running server for a fresh link, authenticating with a control
  token derived from the install key; only the short-lived ticket reaches the browser.
- Previews: opening one issues a one-minute ticket that sets a separate host-scoped
  `studio_preview` cookie for that preview host (eight hours, SameSite=Lax). Studio's
  cookies are stripped before proxying to the guest, and guest responses can't set Studio
  cookies or cookies for sibling hosts.
- The Vite dev server (`pnpm dev`, `pnpm dev:lan`) proxies `/api` with Studio's origin and
  goes through the same login exchange; it gets no key or bypass.

## Storage

- SQLite through `modernc.org/sqlite` (pure Go, with FTS5), one file `studio.db` with WAL,
  foreign keys on and a 5 s busy timeout.
- Migrations are embedded SQL files, `internal/store/migrations/NNN_name.sql`, applied in
  order, each in its own transaction (foreign keys off during the migration and checked
  before commit, as SQLite's table-rebuild procedure needs).
- Every query on environment-owned data takes an environment ID, so rows can't leak across
  environments by accident.
- Memory keeps its own tables in the same database (`internal/memory`).
- The template build worker holds a nonblocking OS file lock derived from the database
  path (Linux, macOS, Windows), so two processes never recover or claim the same build
  queue.
