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
| `/e/$env` | Home, one narrow column: what needs you (the approvals waiting, decided on the spot), the chats working now, and earlier: decisions and chats started, each linking to its page or thread, with All chats and All activity. The setup card shows until the setup is complete or dismissed, and an environment with neither a teammate nor a sandbox offers to make the first |
| `/setup` | the guided setup: host check, AI provider, teammate, git, network level and a first sandbox. Each step can be skipped, and the setup resumes at the first step without an outcome, or at `?step=`; it works on the environment last opened, or the first. The teammate's Edit and the sandbox's Open sandbox add `?from=setup`, and those pages then link back to their step |
| `/e/$env/sandboxes/$id` | a computer without a teammate: its screen at full width (below). `?tab=` (`terminal`, `preview`, `changes`), `?term=` and `?review=` pick what it shows. A teammate's computer redirects to the teammate's page with `?computer=` |
| `/e/$env/sandboxes/$id/work` | the old workspace: redirects to the screen, on the tab its `?terminal=`, `?session=`, `?review=` or `?panel=` named |
| `/e/$env/chats`, `/chats/new`, `/chats/$chat` | chats ([design](design/chat-mode.md)): all of them, linked from Home. A chat is a thread on its teammate's page, so `/chats/$chat` redirects there with `?thread=`, and is left only for a chat the list doesn't know, such as one just deleted. `/chats/new` takes `?teammate=` or `?sandbox=` to preselect a sandbox, and opens the thread it creates |
| `/e/$env/settings/<section>` | Settings (below) |
| `/e/$env/teammates` | teammates |
| `/e/$env/teammates/$id` | the teammate and its computer side by side ([design](design/teammate-computer.md)), below |
| `/e/$env/teammates/$id/$view` | the old tabs: `chats` opens the teammate's home, `tasks`, `memory` and `settings` (with its `?open=`) the profile's sheet, `browser` the screen's Browser tab |
| `/e/$env/git/reviews/$id` | one git push review: the diff, commits and comments in tabs, decided there while it waits, its outcome after |
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
| | Network (`network`) | an intro, the security level, the network rules (filters in the URL: `q`, `scope`, `action`, `origin`, `unused`; sorted by registrable domain, with a heading for a domain with several rules), the curated list and the tracker list ([network](network.md)) |
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

The sidebar has the environment switcher and Home, then Teammates, each with one word for what
it is doing (working, no computer, stopped …) or the count of what waits on it, then Your
computers, the sandboxes without a teammate, each with its status dot, and a footer with
Settings, the version and the theme switch. Teammates' + adds a teammate; Your computers' +
makes a computer without one or imports one. A teammate is waiting on you while one of its
approvals is pending (a browser action or credential, a memory share, or a tool permission in
one of its chats), and working while one of its chats runs. The Settings page names the
environment in its header.
The approval stack ([approvals](approvals.md#realtime-and-ui)) is rendered by the root route
on every page.

Home and every Settings section are a `ColumnPage` (`components/column-page.tsx`): the title
row and the page in one centred column, without a rule under the header.

### A teammate and its computer

The teammate's page (`components/teammate/`) has the conversation on the left and the screen of
the computer it works on to the right ([design](design/teammate-computer.md)). Everything it
shows is in the URL's search, so Back, Forward and links work: `thread`, `computer`, `tab`,
`term`, `review`, `fold`, `full`, `profile` (`card`, `tasks`, `memory`, `settings`), `open` (the open
settings card) and `from=setup`. Esc closes the profile or a menu first, then leaves the thread.

- Home: the header (avatar, name, role and harness, what keeps it from working, its presence
  word), the threads as cards grouped by day (title, a chip for another computer, the last
  answer, the number of requests, its state, and New while it has changes this browser hasn't
  seen: `lib/chat-seen.ts`, revs in localStorage), the small events between them (facts it
  learned, with Forget; tasks; terminal work; check-ins), what it browses now, and what waits
  for you, a row each, whose Answer opens the card in place and Review opens a push on Changes.
  One composer starts a thread on the computer on the screen, adds a task, or teaches a fact.
- A thread: Back to the teammate, its title and state, and ⋯ to continue it in the agent's
  terminal or delete it; then the chat and its composer. Its changes and terminal show on the
  screen.
- The profile: the name opens a card with the open tasks, what it knows, its computers and its
  settings; each opens as a sheet. The settings sheet has one card per part, closed, with what
  it is set to: identity, agent, git identity, approvals, soul, check-ins, network level,
  browser, secrets (its own, its sandboxes' and the environment's it inherits, with which
  replaces which; Add secret preselects the teammate's scope) and delete. `?open=` names the
  open card (`components/disclosure-card.tsx`). One bar, "Unsaved changes · Discard · Save",
  saves the changes of every card, check-ins included, and shows only while there are some;
  leaving with changes, or closing the sheet, asks first (`components/save-bar.tsx`). Switches and selects that save
  as they change (check-ins on or off, network level, browser) stay as they are.

The screen (`components/computer/screen.tsx`) is the same for a teammate's computer and for one
without a teammate, which has it at full width. Its bezel has the computer's name, a switcher
between the owner's computers with forks under the one they came from and Another computer;
the tabs Terminal, Browser (a teammate's own browser), Preview (while previews are on) and
Changes, each with a dot while something on it waits for you; power (Stop, Start or Resume);
⋯; enlarge (a teammate's page on a wide screen) and fold. Under it the display:

- Terminal: the computer's tmux sessions behind a chip (a robot for an agent's): switch, a new
  shell, close. The URL names the one in view (`term`). The teammate's harness opens in one
  from a thread's Continue in the terminal.
- Browser: the teammate's browser window (tabs, toolbar, live view; [browser](browser.md))
  with its Activity and Sites beside it, or over it on a narrow screen, from the toolbar's
  clock or the site info's All sites.
- Preview: the framed app with a port chip, Open in a new tab and Open fresh.
- Changes: a waiting push's review over the files and changes of the open thread.

The computer's ⋯ (`components/computer/computer-menu.tsx`) has its name and ID, template, size
and uptime, CPU and memory meters while it runs (polled every 5 seconds), Checkpoint now,
Checkpoints (lineage and checkpoints in a sheet), Fork, Merge into its source for a fork,
Connections (a sheet), Resources, Rebase, Export, Another computer, Suspend or Resume, and
Delete. A computer that is off shows dark glass with Start and the checkpoint it keeps, and a
teammate's keeps its Browser tab, as the browser has a VM of its own; one being created shows
its progress. Folded, the screen is a strip of tab icons. Enlarged, it takes the page, and the
conversation is a strip with the teammate's face, a dot while something waits for you, and the
way back; the conversation stays mounted behind it, so a draft and the scroll are kept, and Esc
or the bezel's Shrink gives it back. Folding and enlarging undo each other. Below 1024px it
is that strip, and opens over the conversation.

Sheets remain for detail opened from outside its context: the git push review (from the
approval stack), a memory conflict (from the approval stack and the Conflicts tab of Settings →
Shared memory) and a build's log (from Settings → Templates). Each of those sheets has "Open as
page", which leads to the item's own page above; Home's activity, Settings → Activity, Settings
→ Templates and the memory conflicts tab link there too. A page for an item that doesn't exist
says so, and one already decided shows the outcome without the buttons. The chat page of a chat
the list doesn't know keeps a resizable side panel for its changes and files
(`components/side-panel.tsx`), its width in local storage (`studio.panel.chat`).

Links from when a sandbox had a dashboard or a workspace (`/e/$env/sandboxes/$id` and `/work`,
with `?terminal=`, `?session=`, `?chat=` or `?panel=`) land on the screen with the matching
tab, on the teammate's page for a teammate's computer.

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
its preview, with previews turned on. It puts the level and the previews setting back and
deletes the sandbox at the end. The smoke workflow runs it on Linux after its own steps, and
uploads traces, screenshots and videos when it fails.
