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
  copies with OSC 52; the terminal writes an OSC 52 clipboard to the browser's only within 2 s
  of the user's own key or pointer press in that terminal, says so with a toast otherwise, and
  refuses reads. A program can still write while the user types in the same terminal. Right-click opens Studio's Copy/Paste menu and never reaches tmux.
- Editors: CodeMirror (YAML specs, Caddyfiles). What Files and the tool cards only show is
  highlighted by Lezer's parsers without an editor (`src/lib/highlight.ts`): each language's
  parser is its own chunk, loaded when a file of it first shows, and the tokens take the
  `tok-*` classes in `styles.css`. A tool card's diff over 600 lines highlights in a worker
  (`src/lib/highlight.worker.ts`), which sends its tokens back as numbers, and a line over 2000
  characters stays plain. Toasts: sonner.
- Diffs (the Changes panel, a review, a message's changes): `@pierre/diffs`, in
  `src/components/diff-view.tsx`, loaded when one first shows. It keeps only the rows in view, in
  a Shadow DOM, and highlights with Shiki in two workers that stay once started. Its theme's
  colours are `var(--code-*)`, so a change of style or scheme needs no new highlight, and the
  rest of the style reaches it through the `--diffs-*` variables on `diffs-container` in
  `styles.css`: code at 13px on 20px lines at 100%, in rem so the text size scales it, with the
  rows DiffView reserves (`itemMetrics`) worked out from the same text size. A line over 2000
  characters stays plain here too. A full-length diff of several
  files lists them (`FileOverview`), beside it from a container width of 48rem. In a review
  (`fill`) the diff is the pane's one scroll frame: it fills the pane, the review's head and
  facts come in as `header` (the library's `renderCodeViewHeader`) and scroll away above the
  first file, the overview is a column beside it or, narrower, part of that header, and a push's
  Commits menu swaps in one commit's diff rather than nesting it. Comments on a
  range use the library's line selection; the comment button, which lies over the numbers,
  drags a range itself, finding the line under the pointer in the diff's shadow root. `parseDiff` in
  `review-feedback.tsx` stays free of the library, since a comment's quote needs only the lines.
- Errors: a part that fails shows `ErrorNotice` (`src/components/error-boundary.tsx`) in its
  place and the rest of Studio stays. Each screen tab and each diff has its own
  `ErrorBoundary`, and a route that fails shows `RouteError` (the router's
  `defaultErrorComponent`; a page that draws the window bar draws it on its error page too). A
  chunk that doesn't load, as after an update when the page asks for its old version's files,
  offers only Reload, since trying again asks for the same file. The server answers a missing
  `/assets/` file with a 404, never `index.html`.
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
| `/e/$env` | Home, one narrow column: what needs you (the approvals waiting, decided on the spot), Lead's Today so far card, each sandbox as a line its sessions branch off as they open and rejoin as they close (chats, terminals, browser sessions and previews; `docs/design/session-lines.md`), and earlier: decisions and chats started, each linking to its page or thread, with All chats and All activity. The setup card shows until the setup is complete or dismissed, and an environment with neither a teammate nor a sandbox offers to make the first |
| `/setup` | the guided setup: host check, AI provider, teammate, git, network level and a first sandbox. Each step can be skipped, and the setup resumes at the first step without an outcome, or at `?step=`; it works on the environment last opened, or the first. The teammate's Edit and the sandbox's Open sandbox add `?from=setup`, and those pages then link back to their step |
| `/e/$env/sandboxes/$id` | a computer without a teammate: its screen at full width (below). `?tab=` (`terminal`, `preview`, `changes`), `?term=` and `?review=` pick what it shows. A teammate's computer redirects to the teammate's page with `?computer=` |
| `/e/$env/sandboxes/$id/work` | the old workspace: redirects to the screen, on the tab its `?terminal=`, `?session=`, `?review=` or `?panel=` named |
| `/e/$env/chats`, `/chats/new`, `/chats/$chat` | chats ([design](design/chat-mode.md)): all of them, linked from Home. A chat is a thread on its teammate's page, so `/chats/$chat` redirects there with `?thread=`, and is left only for a chat the list doesn't know, such as one just deleted. `/chats/new` takes `?teammate=` or `?sandbox=` to preselect a sandbox, and opens the thread it creates |
| `/e/$env/settings/<section>` | Settings (below) |
| `/e/$env/lead`, `/lead/$day`, `/lead/settings` | [Lead](lead.md): today (written or so far), a written day, its settings |
| `/e/$env/teammates` | teammates |
| `/e/$env/teammates/$id` | the teammate and its computer side by side ([design](design/teammate-computer.md)), below |
| `/e/$env/teammates/$id/$view` | the old tabs: `chats` opens the teammate's home, `settings` (with its `?open=`) the settings page, `tasks` and `memory` their sheets, `browser` the screen's Browser tab |
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
| This environment | General (`general`) | start at login, "Open setup" (`/setup`) once Home's setup card is gone, "Show tips again" ([onboarding](#onboarding)), the environment's name, the environment file (export and import), and deleting the environment after its name is typed |
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

Requests waiting for a decision are on Home and in the top bar's approvals list, not in Settings.

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

The sidebar (`components/app-sidebar.tsx`) has the environment switcher, Search (the palette,
⌘K or Ctrl+K: `components/command-palette.tsx`) and Home, then Teammates, each with one word for
what it is doing (working, no computer, stopped …) or the count of what waits on it, and under
it its threads (`lib/threads.ts`): every one that runs, waits or is in the terminal, its agents
working in a terminal with no chat (marked `>_`), and the two latest others of the last day,
each with its state and, for a teammate with more than one computer, the computer; then "All N
threads" to its home. On hover a teammate shows `>_`, a terminal on the computer last used for
it, and +, a new thread: its home, with the composer focused (`lib/new-thread.ts`; ⌘K's
"New thread with X" does the same). Your computers lists the sandboxes without a teammate, each with its
status dot; Environment has Computers (every computer, with their count), Network, Secrets,
Memory and Activity; the footer has Settings, the version and the theme switch. Teammates' +
adds a teammate; Your computers' + makes a computer without one or imports one. A teammate is
waiting on you while one of its approvals is pending (a browser action or credential, a memory
share, or a tool permission in one of its chats), and working while one of its chats runs. The
Settings page names the environment in its header.

The palette finds teammates, each with its settings, tasks, memory and (with a computer) its
browser, and the rows of its settings (but Delete) by what they hold; computers with their tabs
(Terminal and Details, and while one runs Preview, Files and Changes); threads; Home and the
Environment's pages; Settings' sections; and actions. A teammate's settings row or a computer's
tab is a part: the name of its teammate or computer only narrows it, so "ada" lists Ada and its
places, "ada soul" Ada's Soul row and "docs changes" the docs computer's Changes tab. A word
matches a title as a run of letters, else as its letters in order close together ("actvty"),
else, from six letters on, one typo away at a word's start ("netwrok"); the keys and the place
only by a word's start, since what they matched isn't shown.

Above every page a row of breadcrumbs (`components/breadcrumbs.tsx`) says where it is: Home,
Environment › a page, Settings › a section, Computers › a computer of the user's own, or a
teammate › the open thread followed, in a thread, by its state (`lib/threads.ts`'s, with "needs
you" while it waits), what the page puts in the row's status slot (`components/top-bar-slot.tsx`),
and on its desk and in a thread a chip for the computer it shows ("on ● docs-site"). The thread's
crumb is its title, whose menu has its options (Rename…, Copy link, Continue in the terminal,
Delete thread…), which the thread page publishes through `lib/thread-actions.ts`, then the
teammate's other threads. On a narrow window the teammate's crumb and the chips keep their icons
and dots, so the title stays in view. Each part with a caret opens its neighbours; the chip's menu has the
teammate's computers, Give it another computer and All computers. The teammate's crumb is two
buttons when it isn't the last part: its name goes to its home and its caret opens the menu. What the computer's tabs show
is on the computer, not in the row.

Environment › Computers (`components/computers-page.tsx`) lists every computer with its forks
under their base: state, who uses it, the thread on it now (a chat that runs or waits, else the
teammate's agent in a terminal, else the latest chat), its disk and when it was last used,
and the computer's ⋯. All, Running, Yours and Bases filter it; Import and New computer add one,
and Rebase all appears while some run an older base image. A row opens a teammate's computer on
its desk and the user's own on its page, which has its terminal sessions in a panel beside the
screen, folded until opened.

The approvals button ([approvals](approvals.md#realtime-and-ui)) sits in the top bar on every
page; the root route mounts `ApprovalNotices` for the notifications and the page title.

The root route also maps Alt+← and Alt+→, and ⌘[ and ⌘] on a Mac, to Back and Forward
(`hooks/use-history-keys.ts`): the desktop app's webview has no such keys, and a browser that
has them gets the key handled once. They stay with a text field (word moves), the terminal (its
shell) and the live view of a browser you drive (the remote page). Back stops at Studio's first
page; there the key is the browser's again.

Home and every Settings section are a `ColumnPage` (`components/column-page.tsx`): the title
row and the page in one centred column, without a rule under the header.

### A teammate and its computer

The teammate's page (`components/teammate/`) has three states ([design](design/teammate-computer.md)):
its home, which takes the page with its computers beside the threads and the screen folded to a
rail (none on a phone); its desk, a computer open with the home narrowed to a panel beside it (`tab=` with no
thread); and a thread, the conversation on the left and the screen to the right. Everything it
shows is in the URL's search, so Back, Forward and links work: `thread`, `computer`, `tab`,
`term`, `review`, `fold`, `full`, `profile` (`settings`, or the `tasks` or `memory` sheet over
them; the old `card` opens `settings`), `open` (the open settings card) and `from=setup`. Esc
closes a sheet, the settings or a menu first, then leaves the thread. The page has no header of
its own, nor has a thread: the crumb row's teammate is its only name, and its menu switches
teammate and opens the Threads and the Settings; the thread's title, state and options are the
row's too.

The rim between the home or the conversation and the screen drags. From the home's rail a little
drag opens the desk; past the panel's minimum the screen takes the page (`full`), and past its
maximum the screen folds to the rail again (`fold`), so the same rim reaches all three. Review
on a push waiting for review (`opensEnlarged` in `git-review.tsx`) opens it with `full` set, so
the diff has the width; Esc or the layout switch brings the conversation back.
Double-click resets the split; the arrows move it, and past either end fold or expand.

- Home: its status goes into the top bar's status slot (`<name> now`): working on N chats, else
  its presence word and since when, with what keeps it from working
  (`hooks/use-teammate-presence.ts`); with nothing on the Now card it names a harness or
  program in a terminal and links to it. Each thing shows once: the **Now** card is there only
  while something waits for you (the waiting rows: Answer opens the card in place, Review opens
  a push on Changes) or its agents work in terminals (each with Open) or it browses. Below it
  the threads as cards, newest day first (title, a chip for another computer, the last answer,
  and what waits in it in words, "Asks to run make test", else the number of requests and its
  state; New while it has changes this browser hasn't seen: `lib/chat-seen.ts`, revs in
  localStorage), the small events between them (facts it
  learned, with Forget; tasks; terminal work; check-ins). One composer starts a thread on the
  computer on the screen (or another it picks), adds a task, or teaches a fact. Beside the
  threads on a wide page, above them on a narrow one, its computers as stacked cards: state, a
  fork's base, and Terminal, Browser and Files, each opening the desk on that tab; then a quiet
  "+ Another computer". A computer that is off says Off and has a
  green Start (Resume when suspended), and its Terminal and Files start it as they open; its
  browser button waits until it runs. The composer's line says "off, it starts as you type".
  The folded screen's rail has the same start as a power button (`FoldedStart`). A narrow
  page shows the first two and All N computers.
- The desk: the home in brief beside the open computer: what waits,
  the threads with their state, its agents in terminals, its computers, and the composer.
  Expand gives the home the page again, Fold leaves the computer the page.
- A thread: Back to the teammate, its title and state, and ⋯ to continue it in the agent's
  terminal or delete it; then the chat and its composer. Its changes and terminal show on the
  screen.
- The settings: **Settings** in the crumb's teammate menu opens them as a page, in place of the
  home or the thread, as a step in the history, so the browser's back, Esc and the "← name Esc"
  link above the title return to the thread or home it was opened from; opened from a link,
  the link goes to the home. Its sections replace that step rather than adding their own.
  Until 2026-11-19 a Conversation button in the header went to the home. Its header names
  the teammate with its role, agent, model and presence word. Until 2026-11-17 a Profile card opened from the same menu, with the open tasks, what it
  knows, its computers and the settings that matter day to day; everything on it but the
  identity line and New task was already here or on the home, so those two moved here and the
  card went. The page uses rows, an icon, the label, what it is
  set to and a caret, each opening to its fields underneath, under the card's section labels:
  Profile (identity, agent, git identity, approvals, soul), Knowledge (memory: its pages,
  facts, facts this week and last dream, Open its memory, Add a fact, Dream now, and the
  switches for learning from chats and dreaming every night; tasks: open and scheduled, the
  first four, Open its tasks; `components/teammate/knowledge.tsx`), Routine and access
  (check-ins, new hosts, network rules: its own and its computers', the environment's folded,
  each linking to Settings → Network, browser, secrets: its own, its sandboxes' and the
  environment's it inherits, with which replaces which; Add secret preselects the teammate's
  scope; copy to another environment), then delete. Memory or tasks opened from a row show a
  back arrow to that row. A setting has
  the same name on the card and in the sheet. `?open=` names the open row, which a row of the
  card opens and scrolls to (`components/disclosure-row.tsx`). One bar, "Unsaved changes · Discard · Save",
  saves the changes of every row, check-ins included, and shows only while there are some;
  leaving with changes, or closing the sheet, asks first (`components/save-bar.tsx`). Switches and selects that save
  as they change (check-ins on or off, network level, browser) stay as they are.

The screen (`components/computer/screen.tsx`) is the same for a teammate's computer and for one
without a teammate, which has it at full width. The breadcrumb row names the computer and
switches it (on a teammate's page its "on …" chip, whose menu has the owner's computers with
forks under the one they came from, Give … another computer and All computers), so the bezel
has no switcher of its own. The bezel has the tabs Terminal, Browser (a teammate's own browser), Preview (while previews are on), Files
and Changes, each with a dot while something on it waits for you; power (Stop, Start or Resume);
and ⋯. On a teammate's page the layout switch at the right of the breadcrumb row
(`components/teammate/layout-switch.tsx`, put there through `components/top-bar-slot.tsx`)
gives the page to the home or the conversation, to both, or to the computer; a narrow window
offers only the two alone. Under the bezel the display:

- Terminal: the computer's tmux sessions as a strip of tabs (`components/computer/terminals.tsx`),
  with + for a new shell and the Terminal tab counting them when there is more than one. A tab
  is named by the name it was given (double-click), the harness or program in the foreground,
  or its directory, and has a robot for a harness, a green dot while one works and a ring
  while another program runs. Alt+Shift+T, Alt+Shift+W and Alt+Shift+[ and ] open, close and
  switch sessions from the terminal; closing one a program runs in asks first. The URL names
  the one in view (`term`). The teammate's harness opens in one from a thread's Continue in
  the terminal.
- Browser: the teammate's browser window (tabs, toolbar, live view; [browser](browser.md))
  with its Activity and Sites beside it, or over it on a narrow screen, from the toolbar's
  clock or the site info's All sites. Its ⋯ menu opens Chrome's DevTools on the live page.
  They dock under the view behind a rim that drags up and down (`ResizeHandle` with
  `side="bottom"`; `top` and `bottom` drag rows as `left` and `right` drag columns), or
  open in a window from the menu or the panel's header (`components/browser-devtools.tsx`;
  [DevTools](browser.md#devtools)). In a browser other than Chromium the panel gives the
  address instead.
- Preview: the framed app with a port chip, Open in a new tab and Open fresh.
- Files: the computer's files (`chat/files-panel.tsx`), marking those git reports changed and
  those the thread's request changed. It opens at `/workspace`; the path's root goes to `/tmp`
  or `/`, the whole computer, and Go to a path (or a click beside the path) takes any path
  typed, a folder or a file. A place in `/workspace` is written relative to it and every other
  one absolute, in the URL too. Uploads and mentions stay in `/workspace`. `/` focuses Go to
  file from anywhere in the tab, also when nothing has the focus, as after a click on a
  button in WebKit, where buttons don't take it.
- Changes: git's view of the computer, read-only (`computer/changes.tsx`, `git-changes.tsx`),
  as a rail with the one picked beside it. The rail lists the pushes waiting for review first;
  then each repository of `/workspace` with its branch, ↑ ahead and ↓ behind its upstream, and
  its Staged, Not staged and Untracked files, as flat rows; then This thread, what the thread's
  requests changed. Beside it shows a push's review, a change's diff with its area and Open in
  Files, or the thread's changes. The pick is in the URL: `?review=` for a push, `?change=` for
  a change (its area and path, as `unstaged:main.go`) or `thread`; with none, the first change
  shows. Below a container width of 42rem the rail comes first and a pick takes its place,
  with a way back. The tab counts the changed files. git status is asked every 10 seconds while
  Files or Changes shows, every 30 otherwise, and when a request ends. Files keeps its `?file=`
  while another tab shows. Changes stays mounted once opened, hidden while another tab shows,
  like the terminals. A push or pull request review keeps what the reader wrote but hasn't
  sent and where in the diff they were (the file at the top of the view and how far
  into it, reported by the diff view's `onPlace` a moment after scrolling stops) in
  localStorage under `studio.review.<approval ID>` (`lib/review-drafts.ts`), so a folded
  screen, another computer or a reload finds them again. Line comments sit on the diff's
  lines, so a draft saved against a different diff keeps only the note and where it goes.
  Deciding drops the entry; nothing is saved for a decided review, and entries older than a
  month, or past the newest hundred, go. The files the reader marked viewed are kept per
  branch rather than per review, under `studio.viewed.<forge>/<owner>/<repo>#<branch>`
  (`lib/viewed-files.ts`), each path with an FNV hash of its patch's lines, so the branch's
  next push finds them: a file whose patch hashes differently says "Changed since you viewed
  it" and counts as unviewed. The diff view folds a viewed file to its header (CodeView's
  `collapsed`), shows the mark in each header and in the list of files, and reports the count
  to the bar's `ViewedCount`; **Next unviewed** goes to the next file after the one at the
  top. Branches untouched for two months, or past the newest two hundred, go. There is no
  Comments tab: the bar's `CommentNav` counts the line comments, lists them to jump to and
  steps through them, the decision row says what goes with the decision and opens the note
  (Add a note), and the diff view takes the review's keys while it shows (`keys`, a document
  keydown that skips text fields, terminals, dialogs and menus): j/k files, n/p comments, v
  mark viewed and go on, c comment on the line in focus, ? the list (`reviewKeys`). n and p go
  on from the comment last gone to while it shows, and otherwise from the top of the view;
  where a line is comes from the rows drawn in each file's shadow root, as CodeView has no
  API for a line's position.

The computer's ⋯ (`components/computer/computer-menu.tsx`) has its name and ID, template, size
and uptime, CPU and memory meters while it runs (polled every 5 seconds), Checkpoint now,
Checkpoints (lineage and checkpoints in a sheet), Fork, Merge into its source for a fork,
Connections (a sheet), Resources, Rebase, Export, Another computer, Suspend or Resume, and
Delete. Beside the computer's name, a count of its waiting connections
(`components/waiting-chip.tsx`) opens their hosts, with Allow all, Deny all and Review individually,
which is the top of Connections (`components/waiting-requests.tsx`). A computer that is off shows dark glass with Start and the checkpoint it keeps, and a
teammate's keeps its Browser tab, as the browser has a VM of its own; one being created shows
its progress. Folded, the screen is a strip of tab icons, and a tab opens the screen beside the
conversation. Given the page, the screen hides the conversation, which stays mounted behind it,
so a draft and the scroll are kept; Esc or the switch gives it back, and meanwhile its approvals
are in the top bar's list. Below 1024px the folded screen is that strip, and opens over the
conversation.

Review on a git approval's card or row, on Home or in the top bar's list, opens it on its
computer's Changes tab (`useOpenGitReview` in `components/git-review.tsx`): beside the open
thread when the computer is that teammate's, else through the computer's address, which leads a
teammate's computer to its desk. The review's own page (`/e/$env/git/reviews/$id`) stays for
decided pushes, the TUI and a computer that is gone. Sheets remain for detail opened from
outside its context: a memory conflict (from the top bar's approvals list and the Conflicts tab
of Settings → Shared memory) and a build's log (from Settings → Templates). Each of those sheets
has "Open as page", which leads to the item's own page above; Home's activity, Settings →
Activity, Settings → Templates and the memory conflicts tab link there too. A page for an item
that doesn't exist says so, and one already decided shows the outcome without the buttons. The
chat page of a chat the list doesn't know keeps a resizable side panel for its changes and files
(`components/side-panel.tsx`), its width in local storage (`studio.panel.chat`).

A chat's log folds each run of steps between the agent's messages (tool calls, thoughts, and
permissions and questions once answered) into one row (`chat/step-runs.ts` groups them,
`chat/step-run.tsx` draws them): how long it worked, from the item before it to its last step,
its step count, a chip per file its tool calls' diffs touched with the lines added and removed,
and how many failed. The row opens to the steps as lines (`ToolCallCard`'s `line`, a settled
ask as a line opening to its card); a chip opens Changes at that file once the turn has ended
(`onShowChanges(turn, file)` through the page to `ChangesPanel`'s `file`, which opens it and
scrolls to it; an agent's absolute path matches a workspace path by its end). The run that is
the open turn's latest work is open, with a ticking clock, its newest step as a card and all
but the newest four merged into one line past five; the user's open or closed choice is kept
per run in the log by its first item's ID. What stays outside: the agent's messages, plans,
markers, an ask that waits, a run that is a single thought (which folds already), and a failed
tool call that ended a finished turn with no message after it, shown open. **Show every step**
in Settings → General (`/api/system/chat-steps`, Studio-wide, `lib/chat-steps-setting.ts`) turns
the folding off.

A composer keeps what was typed but not sent under its `draft` key (a chat, a new chat's
sandbox, a teammate's home), so leaving and coming back finds it again: the text, mentions and
files in memory (`lib/drafts.ts`), the text and mentions also in session storage for a reload.
A thread's messages skip rendering off screen (`content-visibility: auto`) only in browsers
with scroll anchoring: the stand-in height becomes the real one as a message scrolls in above
the reading place, and without anchoring, as in Safari and the macOS app, the thread jumped.

Links from when a sandbox had a dashboard or a workspace (`/e/$env/sandboxes/$id` and `/work`,
with `?terminal=`, `?session=`, `?chat=` or `?panel=`) land on the screen with the matching
tab, on the teammate's page for a teammate's computer.

## Onboarding

Once the setup is over, Home shows **Get to know Studio** under the setup card
(`components/onboarding-card.tsx`): a first task for a teammate, and five features (network
approval, pushes and pull requests, the browser, previews and secrets), each ticked off once it
happened or its tip was closed. A first task is one of two starters, or the user's own,
for the first sandbox with a teammate (a running one first); **Start with** saves it as the
draft of a new chat on that sandbox and opens `/chats/new?sandbox=`, where it can be edited
before it is sent. The Secrets row links to Settings → Secrets. **Not now** puts the card away;
once all five are done it stays for a day.

Each feature explains itself the first time it happens with a tip beside its control
(`components/feature-tip.tsx`, a non-modal popover): the first held request's card and the
first push's or pull request's card, wherever approvals show, the teammate browser's **Take
the wheel** while the browser runs, the Preview tab while the sandbox listens on a port, and
**Add secret** on Settings → Secrets. One tip shows at a time (`lib/onboarding.ts` keeps them
in line); **Got it** closes it for good, **Stop tips** closes all of them, and Settings →
General's **Show tips again** brings back the tips and the card for what is left. Opening the
Preview tab counts as seeing previews.

The progress is the global `onboarding.v1` setting, read and replaced with `GET` and `PUT
/api/onboarding?env=` ([API](api.md)). Whether a feature happened is read from the
environment (a held connection or request, a `git.push` or `git.pr` approval, a browser action, a secret
that is neither a provider's key nor a forge's token); previews leave no record, so only their
tip ticks them off. The first read marks what already happened as seen, so someone who used
Studio before the onboarding existed gets no tips for it. The e2e login puts the card and
the tips away; `e2e/onboarding.spec.ts` brings them back.

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
