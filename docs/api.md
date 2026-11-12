# API, auth and storage

## HTTP API

- `net/http` routing and `log/slog`. Operations are declared with
  [huma](https://github.com/danielgtaylor/huma) in `internal/api`, which produces the
  OpenAPI 3.1 document.
- `make api` writes it to `web/openapi.json` (`studio openapi`) and generates
  `web/src/lib/api/schema.gen.ts` with `openapi-typescript`. CI fails if either is stale.
- Almost everything is under `/api/environments/{env}/...`. Global routes: environments,
  `/api/approvals`, `/api/events`, `/api/health`, `/api/provider-kinds`, `/api/setup`
  (the guided setup, for the environment named in `?env=`) and `/api/system/*`
  (doctor, autostart, updates, the previews setting, the host's git identity, its folders for
  a local forge).
- Streaming endpoints are plain handlers outside huma (`api.go`, `exports.go`, `uploads.go`): SSE at
  `/api/events` for change notices (see [approvals](approvals.md#realtime-and-ui)) and at
  `…/chats/{chat}/stream` for a chat; WebSockets (`coder/websocket`) for terminal attach and
  the browser live view; sandbox export download and import upload; a file upload into a
  sandbox's workspace (`uploads.go`, see [guest agent](guest-agent.md#uploads)).
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
  a host-bound, signed `studio_session` cookie (HttpOnly, SameSite=Strict, 30 days). In a
  terminal Studio also opens a second link in the browser (`-no-browser` turns that off).
- The web app's login gate polls `/api/auth/status` every three seconds while its tab is
  visible, so a page opened while Studio was down, or left on the gate during a restart,
  connects by itself with the cookie it has.
- `studio login-url` asks the running server for a fresh link, authenticating with a control
  token derived from the install key; only the short-lived ticket reaches the browser.
- Previews: opening one issues a one-minute ticket that sets a separate host-scoped
  `studio_preview` cookie for that preview host (eight hours, SameSite=Lax). Studio's
  cookies are stripped before proxying to the guest, and guest responses can't set Studio
  cookies or cookies for sibling hosts.
- The chat's preview panel frames the preview cross-site, so its login (`embed=1`) sets
  `studio_preview` as SameSite=None, Secure and Partitioned, which only frames under
  Studio's site get. Chromium sends a framed page's service worker no partitioned cookie,
  neither for its script nor for its fetches, so the same login also sets
  `studio_preview_frame`: SameSite=None and not partitioned (a browser that blocks
  third-party cookies drops it, and the worker stays out). It counts only for requests
  with `Sec-Fetch-Site: same-origin` or `Sec-Fetch-Mode: navigate`, which the preview's own
  pages and worker send and another site's requests can't (browsers set those headers);
  its signature is of its own kind, so it can't stand in for `studio_preview`.
- The desktop app's webview keeps no cookie for a framed preview at all: WebKitGTK's libsoup
  refuses SameSite=None without Secure and Secure over http, `*.localhost` included, and
  macOS's WKWebView blocks third-party cookies. So the app's panel asks for `keyed=1` and frames
  `<port>-<id>-<key>.localhost`, whose first label ends in the preview's key: 128 bits of a MAC
  of the plain host and port under the install key (`internal/webauth/keyed.go`). The gate lets
  in whatever comes to that host with the right key, no cookie needed, and refuses a wrong one.
  The key stays the same for a preview, so its origin and storage do too; it is as good as a
  login for that one preview, for whoever learns the address. Such a preview's responses get
  `Referrer-Policy: same-origin` unless the app sets one, so the host doesn't go to other sites.
- A module service worker's script and imports come without cookies; they carry a capability
  in their URL instead, which the gate accepts only on the browser's own worker fetches →
  [guest agent](guest-agent.md#terminals-and-previews).
- While the panel shows a preview, it logs in again once the login is an hour old, from a
  hidden frame and with a new ticket (`renew=1`): the login sets both cookies and answers with
  an empty page instead of the preview. An app left open keeps working past the eight hours,
  its service worker's fetches included, without being reloaded. The panel checks every
  minute, which catches up after the computer slept, and when its tab shows again.
- An app behind HTTP basic auth: a framed page's challenge (`401` with a `Basic`
  `WWW-Authenticate`, `Sec-Fetch-Dest: iframe`) reaches the browser as a page without the
  challenge, which posts its realm and path to the frame's parent, so the browser shows no
  dialog over all of Studio (`internal/preview/basicauth.go`). The panel asks in its own dialog
  over the preview, sends the sign-in with `PUT …/previews/{port}/basic-auth`, and opens the
  page again. Studio keeps it in memory only, until it stops, and adds it to that preview's
  requests that carry no `Authorization` of their own; one the app refuses is dropped and asked
  again (`DELETE` forgets it). A preview in a tab of its own keeps the browser's dialog.
- The panel's address bar: Studio can't read a framed preview's address across origins, so the
  script every preview page loads first (`/__studio/preview-sw.js`, `internal/webauth`) also
  carries a bridge to the panel. It posts only to Studio's page: the frame's parent, on a
  loopback host at the preview's port, and the frame's ancestor where the browser names it. It
  reports the path as a page loads and on pushState, replaceState, popstate and hashchange,
  says when a page starts to leave (the loading bar), and hands Studio a click on a link to
  another site instead of following it: Studio opens it in a new tab, or, for `localhost:<port>`
  or another preview host of the same sandbox, switches the panel to that port. It takes back,
  forward and go-to-a-path commands from that page only, the path on its own origin. A page
  without the bridge (not HTML, or before Studio's script) still opens a typed path, with a new
  login on it.
- The Vite dev server (`pnpm dev`, `pnpm dev:lan`) proxies `/api` with Studio's origin and
  goes through the same login exchange; it gets no key or bypass.

## Storage

- SQLite through `modernc.org/sqlite` (pure Go, with FTS5), one file `studio.db` with WAL,
  foreign keys on and a 5 s busy timeout.
- Migrations are embedded SQL files, `internal/store/migrations/NNN_name.sql`, applied in
  order, each in its own transaction (foreign keys off during the migration and checked
  before commit, as SQLite's table-rebuild procedure needs). `PRAGMA user_version` counts the
  applied ones. A database with more than this binary knows, which a newer Studio migrated, is
  refused (`store.ErrNewerSchema`) and left untouched, so an older binary never runs on tables
  it doesn't know, and a downgrade or an updater's rollback needs the backup from before.
- Every query on environment-owned data takes an environment ID, so rows can't leak across
  environments by accident.
- Memory keeps its own tables in the same database (`internal/memory`).
- The template build worker holds a nonblocking OS file lock derived from the database
  path (Linux, macOS, Windows), so two processes never recover or claim the same build
  queue.
