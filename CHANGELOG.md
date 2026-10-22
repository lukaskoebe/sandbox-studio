# Changelog

## Unreleased

- **Chat:** a link to a server in the sandbox, like `http://localhost:5173/login`, is a button
  that opens that page in the port's preview.
- **Agents:** the `sandbox_info` tool, and `studio-agent info` in a terminal, tell an agent its
  sandbox, the ports listening in it with the links that open them, its security level and
  the network rules that cover it. `STUDIO_SANDBOX_ID` and `STUDIO_SANDBOX_NAME` are set in
  every sandbox.
- **Previews:** previews of sandbox ports are off until you turn on **Previews** under
  **Settings → General**. While they are off, the web hides the Previews menu and the chat's
  Preview tab, links to the sandbox's servers in a chat are plain text, and the details and
  the TUI list ports without their addresses. If you had sandboxes before, Home says so once.
- **Browser:** an agent shows you what it built with `browser_show`: it opens the page in its
  browser, under the browser's rules, and posts a card with the address and its note. **Watch**
  opens the teammate's Browser page and **Take the wheel** also hands you control; the agent
  carries on meanwhile.
- **Git:** when git in a sandbox wants credentials for a host no forge covers, Home shows a
  card. **Add forge** opens the Add forge dialog filled in from the repository's URL, **Not
  now** stops asking about the host for a day, and the agent reads in git's output that you
  have the card.
- **Updates:** Studio checks GitHub for a newer release once a day unless you turn **Updates**
  off under **Settings → General**; before, the check was off until you turned it on, and a
  choice you made stays. **Install automatically** also installs the release: Studio downloads
  it, checks it against `checksums.txt` and its GitHub build attestation, waits until no chat,
  template build or sign-in is running, backs up its database and restarts into it. If the
  new version doesn't start within two minutes, the old one and the database come back.

## v0.5.2 — 2026-10-22

Fixes from the first round of feedback: Studio on Windows no longer freezes after a click in
its window, a damaged microsandbox database keeps your sandboxes, and the web has fewer rough
edges.

- **Windows:** Studio turns off the console's QuickEdit, whose selection froze it after a
  click in the window, and logs to `%LOCALAPPDATA%\Sandbox Studio\logs\studio.log` with only
  warnings and errors on the console. The login link is clickable in Windows Terminal.
- **Start:** Studio opens its login link in the browser when started in a terminal
  (`-no-browser` turns that off; autostart passes it). Starting it again while it runs opens
  the running Studio and exits, instead of a second Studio on the same data. A page left on
  the login screen connects by itself once Studio answers again.
- **Start:** Studio refuses a database that a newer version migrated, untouched, instead of
  running on tables it doesn't know. Closing Studio's terminal shuts it down like Ctrl-C.
- **Runtime:** when microsandbox's database is damaged, the repair keeps the rows it can and
  leaves out msb's maintenance leases instead of giving up; `studio restore-msb-db` rebuilds it
  from the file a repair moved aside. A sandbox msb lost says so, and Studio warns about VMs
  that still run during a repair.
- **Runtime:** a pull refused because of an expired `docker login` says so and gives
  `docker logout` as the fix, and `studio doctor` mentions a saved login for the base image's
  registry.
- **Agents:** the instructions describe their machine: sudo and Docker, installing tools
  with mise, that a held connection waits for you and a refused one says so, that servers open
  in Studio's Previews, and that pushes go to review. mise trusts `/workspace` and installs
  Python prebuilt, and its downloads are on the curated list.
- **Network:** the security level is chosen from cards, in Settings → Network, the guided
  setup and a teammate's settings, each saying what connects without asking and what asks.
  A rule made from an approval keeps the note written under the card's Options, or one saying
  what was asked, by which sandbox and when. Network sits under Home in the sidebar with the
  count of approvals waiting.
- **Git review:** a push's review has tabs, Changes, Commits and Comments, above a footer that
  stays in view with Reject and Approve, the rest under More. Comment buttons show on touch
  screens.
- **Web:** a sandbox that is down shows Start or Resume in its header, its menu, its
  workspace and above its chats. A teammate's settings have one save bar and no longer run off
  the right edge. Approvals notify whenever Studio's window is not in front, and Settings →
  General has the button to allow notifications. Terminals follow the light and dark theme,
  with Always dark in their menu, and Claude Code's theme is auto.
- **Web:** chats close their stream in hidden tabs, so background tabs no longer hold up every
  request, and the change stream reopens after Studio restarts. A sandbox's status says it may
  be out of date only after 15 s without fresh data. A rule's note shows in full on hover in
  the rules table and in Activity, and a secret Studio manages links to where it is changed.
  A sandbox's History and checkpoints sheet is wide enough for its history line.
- **Site:** a Docs link on the landing page, screenshots that open full size, a comparison
  with other ways to sandbox an agent, and the videos' cursors click where they point. The
  screenshots show this release, and the sandbox, checkpoint and chat pages describe the
  sandbox's header and options menu instead of the dashboard v0.5.1 replaced.

## v0.5.1 — 2026-10-21

v0.5.0 was tagged but never released; this is that release, with the live smoke test
updated for the new sandbox workspace.

A calmer Studio, built around your teammates: the web's sidebar and the TUI's list are one tree
of teammates and what they work in, Home is what needs you, a sandbox opens on its chats and
terminals, and Settings is a page. One decision pushes a branch and opens its pull request,
and review feedback can go to a new chat.

- **Git review:** one decision pushes a branch and opens its pull request. The review of a
  push to a branch other than the default one offers "Also open a pull request", ticked for a
  new branch, with a title you can edit; unticked, approving only pushes, and the decided
  review keeps an **Open pull request** button for later. A push to a branch that already has
  an open pull request says it updates it and no longer proposes a second one that the forge
  would refuse. In the TUI's review, `p` turns the pull request on or off and `o` opens it
  after the push. Clients that don't send the choice still get the separate `git.pr`
  approval.
- **Git review:** review feedback can go to a new chat in the push's sandbox, the default for
  a teammate's sandbox; it starts only when there is a note or a line comment to send.
- **Web:** the sidebar is one tree: each teammate with its sandbox and that sandbox's chats,
  terminals and waiting reviews, then the sandboxes without a teammate, with New chat on top
  and a menu for a new teammate, a sandbox without one, or an import.
- **Web:** Home shows what needs you (the approvals waiting, decided on the spot), the chats
  working now and the latest network activity, in place of the sandbox table.
- **Web:** a sandbox opens on its workspace; the dashboard is gone. Its chats, terminals and
  the git reviews waiting on it are tabs, and **+** starts a chat with its teammate, the
  teammate in a terminal, or a shell. The status pill in the header opens its details
  (template, resources, ID, ports, start, stop and resize), **Checkpoint** saves one, and
  ⋯ › History and checkpoints shows where it came from and its checkpoints.
- **Web:** Settings is a page instead of a dialog, with a searchable nav in three groups:
  this environment (General, Network, Secrets, Shared memory, Activity), accounts (AI
  providers, Git forges) and this computer (Templates, Runtime, Ports and LAN, Updates). The
  Network, Git, Secrets and Memory pages moved into it: the network rules and security level
  are Settings → Network, the host ports and local network list are Settings → Ports and LAN,
  and Activity lists every decision (network, pushes, tool permissions, browser, memory) with
  a filter by kind. Requests waiting for you are on Home. The old addresses and `?settings=`
  links lead to the section that replaced them.
- **Web:** a teammate's page has a smaller header with its role, harness and presence, and
  says there what keeps it from working. The rail shows its computer, the sandbox it works
  in, with a menu to fork it or add another (two or more are listed as Sandboxes), its last
  chats, what it knows and its check-ins; the Chats tab is gone, its "All" link leads to the
  list. Its Settings tab is a column of cards that say what each part is set to and open to
  edit it.
- **TUI:** the list is teammate-first, as the web's sidebar: it starts on Home's row, which
  says what needs you, then each teammate with its computer and that sandbox's sessions
  (several sandboxes are listed each under it), then the sandboxes without a teammate. A push
  waiting for review is a session of its sandbox. On Home's row the pane shows the cards
  waiting (`tab` the next, `y` `a` `n` on an agent's permission), the sessions working now and
  what was decided today, and `u` undoes a network decision's rule for a minute. A teammate's
  row shows its sandboxes, sessions, tasks, what it knows and its network level, with `c`
  chat, `T` agent, `t` shell, `+` another sandbox and `f` fork. The header's tabs are Home,
  Activity and Settings; `,` opens Settings, the web's sections with what each is set to.
  Activity lists every decision, and `←/→` narrows it to network, git, agents or memory. In a
  git review, `r` requests changes with a note that goes to a new chat.
- **Web:** the work mode setting is gone, from Settings and from the guided setup, which now
  has six steps. A sandbox's workspace opens its latest chat when no tab is picked; every
  session still opens in the terminal with one click. `GET` and `PUT /api/settings/ui` are
  removed.
- **Providers:** an OpenAI-compatible endpoint's model field suggests the models the
  endpoint lists, once its base URL and key are entered; any other name can still be typed.
  Studio asks the endpoint itself (`POST …/providers/models`), so the key never leaves it.
- **Teammates:** "Use my git identity" in a teammate's Git identity section has its sandboxes
  commit as you: the `user.name` and `user.email` of your git config on the Studio host, read
  when a session starts. The form shows the identity it would use; without one, the
  teammate's own git name and email stay in use. Environment files keep the choice.
- **Git:** the setup's first sandbox step lists the repositories of your forges (Forgejo,
  GitHub, GitLab, or a folder on this computer) to search and tick, and clones the ticked
  ones into the sandbox's `/workspace` once it runs, through Studio's git remote, so their
  pushes come to you for review. `GET …/forges/{id}/repositories` lists them and
  `POST …/sandboxes/{id}/clone` clones one.
- **Git:** the Add forge dialog's "Short name" is now "Name in Studio", at the end of the
  form, and fills itself in from the host as you type the URL (`github`, `gitlab`, `example`
  for `git.example.org`, `local` for a folder). It says what it is for, with the remote URL
  sandboxes will use; most people never touch it.
- **Git:** a folder on this computer can be picked with **Browse…** in the Add forge dialog:
  it lists the host's folders from your home directory, marks the git repositories, and
  fills in the one you choose (`GET /api/system/folders`).
- **Sandboxes:** tab completion works in the sandbox shell, for git's commands, branches and
  remotes too: the base image has `bash-completion`, which its terminals load. Templates
  built on the new image have it as well.

## v0.4.2 — 2026-10-19

v0.4.1 was tagged but never released; this is that release, with typing after a mouse
selection in the terminal fixed.

Agents that get on with the work, a steadier chat, markdown in the TUI, a browser that stays
the user's while they drive it, and a microsandbox database that repairs itself.

- **Teammates:** the default soul has a teammate work on its own in its sandbox: it reads,
  runs, edits and installs what the work needs without asking, and checks with the user only
  before something outside the sandbox that can't be undone, such as a force-push or sending
  something. Teammates that kept the old default get the new one. Claude chats run in
  `bypassPermissions` mode, as in the terminal; questions such as AskUserQuestion still come
  to the user.
- **Chat:** a sent message shows in the chat at once and goes back into the box if it
  couldn't be sent. A new chat started from a sandbox's workspace opens in its chat tab instead
  of falling back to the terminal. Stopping a message while its agent starts now
  holds, tool calls still open when a turn ends show as stopped, and a prompt that waits in
  the queue says why it couldn't go. An agent idle for 30 minutes stops, and a fifth chat in a
  sandbox takes the place of the one idle longest; the next message resumes it. Large
  prompts with attachments get through to Claude.
- **Chat:** markdown tables render in the web chat, with their alignment and pipes inside
  code spans.
- **Terminal UI:** chats render markdown as the web does: headings, bold, italics, inline
  code, links with their address, nested lists, quotes, rules, code blocks, and tables
  boxed to the terminal's width.
- **Browser:** while the user drives the browser, agents can neither use it nor see it, and
  their paused calls tell them the user still has control. The new `browser_request_control`
  tool asks for it back: a notification, and a Handback card on the Browser page and in the
  TUI with Hand back and Keep driving. After a handback agents hear how long the user drove
  and where the page is now; the pages the user visited stay private unless the teammate's
  new **Tell agents where you browsed** setting is on.
- **Browser:** a long tab label stays inside its tab.
- **Terminal:** images pasted or dropped into the web terminal go into the sandbox, and their
  path into the terminal, so an agent such as Claude Code can read a screenshot. A mouse
  selection is copied when you let go and stays where it was, instead of clearing and
  jumping to the bottom; a click clears it, and typing goes to the shell as before.
- **Memory:** a one-off answer, such as "defer" to a question about one fix, no longer
  becomes a standing preference. Each fact records how far it reaches (general, a project or
  the task) and the question it answered. The embedding model says why it can't start, in
  the log and in `studio doctor`; on NixOS it needs `programs.nix-ld` with libstdc++ and
  libgomp, and `STUDIO_LLAMA_LIB` loads a system llama.cpp instead.
- **Sandboxes:** Studio checks microsandbox's own database when it starts. A damaged one is
  moved aside, never deleted, and rebuilt from the rows that still read, and the checkpoints
  on disk are indexed again. `studio doctor` fails for a damaged database and for one a newer
  msb migrated, with the fix, and errors from the database say what to do instead of a bare
  SQLite message.

## v0.4.0 — 2026-10-19

One look everywhere: the TUI's style in the web app, the docs and the videos. A TUI Home of
sandboxes and their sessions beside a live pane, approvals from a full-screen terminal,
settings as a dialog, scheduled tasks, and memory that catches up on the chats it missed.

- **Terminal UI:** Home lists each sandbox's sessions under it: its agents running in a
  terminal, its newest chats and its other terminals, each agent marked working, blocked or
  done. A number from 1 to 9 goes straight into a session, `Ctrl-]` (or `esc` in a chat) comes
  back, and `tab` finds the next one that waits for you. `T` starts the teammate's agent in a
  new terminal, `c` opens the newest chat and `C` a new one. The Chats screen is gone.
- **Terminal UI:** from 100 columns, Home is a sidebar of sandboxes with their sessions, each
  marked agent, chat, term, sched or check, beside a pane that shows the session under the
  cursor live: an agent's or a terminal's screen, or a chat. `enter`, a number or a click gives
  the pane the keys, `Ctrl-]` gives them back, `alt+1`–`alt+9` switch session and `alt+z` goes
  full screen. A blocked agent's pane shows its approval in a banner, and `y`, `o` or `n` in
  the sidebar decide a network request. A sandbox's header shows its dashboard, with CPU over
  2 minutes as an area chart and memory as a gauge and history. The 120-column detail pane and
  the 160-column network column are gone; the dashboard has both.
- **Terminal UI:** an approval waiting in a sandbox shows as a banner over each of its
  sessions, terminals and chats, with its choices as buttons. In the pane, `alt+a` answers it
  and gives the keys back to the agent; **More…** opens its card and returns to the session once
  it is decided. A terminal attached full screen sets its title and rings the bell when an
  approval arrives, and `Ctrl-]` goes straight to it, then attaches again.
- **Terminal UI:** `←`/`→` move over the buttons and toggles of approval cards, the banner, a
  chat's permission and question choices and the delete and restore confirmations, and `space`
  or `enter` presses one. Confirmations start on Keep.
- **Terminal UI:** the chat's prompt is a box at the bottom of the chat, in its color, instead
  of the line above the key bar.
- **Terminal UI:** `g` and the Git tab open a Git screen that lists the pushes and pull
  requests waiting for review, then the decided ones; before, the tab opened the approvals.
- **Website:** the landing page takes the TUI's look: monospace headings, a teal accent and
  the TUI's Home among the code samples.
- **Web app, docs and videos:** the same look as the TUI and the landing page. Text is set in
  Instrument Sans, and titles, keys and identifiers in JetBrains Mono, both bundled. The grays
  are cooler, buttons and links are teal (green now only means running or ok), keys are drawn as
  keycaps, and terminals use the TUI's colors. The cube of the app icon, the favicon, the logo
  and the TUI's `◆` is teal too, and the docs' screenshots show the new look.
- **Command line:** `studio shell <sandbox> [terminal]` opens any of a sandbox's terminals,
  such as an agent's.
- **Web app:** Settings is a dialog over the page you are on instead of a page of its own,
  with its sections down the side. `?settings=<section>` opens it, so a section can be linked,
  and the old `/settings/…` addresses open it over Home.
- **Web app:** form dialogs such as the git forge, provider, secret, teammate and task dialogs
  are wider on large screens and set short fields side by side, so they are no longer tall and
  narrow.
- **Command line:** `studio shell` answers approvals too. When the sandbox asks for something,
  the terminal's title says so and the bell rings; `Ctrl-]` then shows the request instead of
  detaching, and `o`, `y` or `n` allows it once, allows it or denies it before the terminal is
  attached again. `enter` goes back to the terminal and `q` detaches.
- **Memory:** a chat whose turns weren't read into memory is read later instead of being
  skipped. A failed call is tried again after 15 minutes and an hour, a run over the daily
  budget waits for the next day, and every 15 minutes a sweep catches up on up to 4 chats that
  went idle while Studio was stopped or busy, one at a time. A stopped utility VM isn't started
  while the host has less than 1.5 GiB of memory free, and one extraction reads at most a chat's
  newest 500 items.
- **Scheduled tasks:** a task can get a schedule in its dialog: once at a day and time, on
  some weekdays at a time, or every few hours inside a window. At each time the teammate works
  on that task in a new chat, marked as scheduled, in its latest sandbox or the one you pick. A
  busy teammate makes the run wait up to 30 minutes; a time Studio was off for runs if it is less
  than an hour late and is logged as missed otherwise. The task list shows each schedule with its
  next and last run, **Run now** starts one at once, and Activity lists the runs. Check-ins leave
  scheduled tasks out. Only the user sets schedules.
- **Fix:** the TUI no longer crashes when its terminal gets only a few rows, such as a phone
  over SSH turned on its side while Home's pane shows a terminal.
- **Fix:** `studio tui` and `studio login-url` read the Studio's key without opening the
  catalog for writing, so a newer binary no longer migrates the catalog of the Studio it talks to.

## v0.3.5 — 2026-10-17

A terminal UI that looks like Studio and decides every approval, Codex memory that works
in chats, and releases checked on real VMs before they go out.

- **Terminal UI:** on color terminals, `studio tui` has the web's look: area colors and status
  dots, tabs, approvals as cards with their choices as keys, a shaped chat, CPU and memory
  sparklines, and an Activity strip. Wide terminals show the selected sandbox beside the
  list, with a picture of its teammate's browser, and from 160 columns its network as it
  happens. The git review gets a file list, side by side diffs and the changed words
  highlighted. The mouse works: click a row, a tab or a key. What an action did, or an error,
  shows at the top right for 3 seconds. `STUDIO_TUI=plain` and `NO_COLOR` keep the plain look.
- **Terminal UI:** every kind of approval can be decided there: browser actions and
  credentials, memory shares and conflicts, and an agent's permissions and questions. New are
  checkpoints, fork, suspend and a running sandbox's ports. Allowing a browser request covers
  only the browsers, as on the web (`s` picks who; `--sandboxes` in the CLI).
- **Codex:** its memory hooks run in chats too; Codex skipped them silently there because
  they weren't trusted. Its chat-titling requests no longer get the chat's memory.
- **Agents:** an agent started in a terminal is recognized as an agent session, with its chat.
  Claude's compaction summaries and slash-command lines no longer show up in what memory
  learns from a chat.
- **Browser:** a teammate's browser activity updates as each step lands, not every 10
  seconds. `GET .../browser/frame` returns the page's latest picture as a JPEG.
- **Command line:** `studio checkpoint <sandbox>` without a name works; it failed with
  "validation failed". The checkpoint is named after the time, as on the web.
- **Sandboxes:** resize, suspend and resume, SSH access and scheduled checkpoints were run on
  real VMs. A resize of a running sandbox applies at its next start: the microsandbox
  version Studio uses can't resize a running VM. The SSH steps in the docs now set
  `User agent`, which the login needs.
- **Git:** the SSH host keys GitHub, GitLab and Bitbucket publish are built in, so adding one
  of those forges needs no fingerprint check, and any other key for those hosts is refused.
- **Releases:** each release is published only after Studio built from it boots a sandbox on
  Linux and Windows, runs the live browser tests there, and embeds with the real model on
  every host.
- **Docs:** the site and its landing page are redesigned, with overview videos.

## v0.3.4 — 2026-10-16

A browser that asks far less, messages you can send while the agent works, and sandboxes that
can reach this computer and the local network when you let them.

- **Browser:** allowing a page also allows its site for the browser session, so the page's
  own scripts and images no longer ask one after the other; a page that is still loading
  counts as its site. Reports Chromium sends sites on its own, such as Network Error Logging,
  are refused without asking. A rule from a browser request covers only the browsers, not the
  sandboxes, unless you choose **Browsers and sandboxes**; rules get an **Only in the
  browser** switch.
- **Approvals:** a network approval's **Options** ask two things: **Who** (the teammate's
  browser, all browsers, or browsers and sandboxes; for a sandbox, its scope and ports) and
  **For** (always, 1 hour, this session or once). The buttons say how long they last, such
  as **Allow for 1 hour**. When Studio rejects a decision, the message says which field
  failed, and suggests restarting a Studio that is older than the page.
- **Chats:** a message sent while the agent works waits above the composer, where you can
  edit or remove it, and goes as the next turn; Enter during a turn steers the next one.
  Only chats with replies you haven't seen are marked in green, a working agent pulses
  quietly, and open chat tabs survive a reload.
- **Network:** sandboxes can reach open ports of this computer as `host.studio.internal`, and
  addresses on the **Local network** list as `192-168-1-20.lan.studio.internal`; both are off
  by default. The Rules page flags a rule no sandbox can reach, with the reason.
- **Git review:** a **Folder on this computer** forge serves repositories from a folder such
  as `~/git` with no token; an approved push goes in as `git push` to the folder would.

## v0.3.3 — 2026-10-15

Scheduled checkpoints, review feedback that reaches the agent, a browser that opens what its
sandbox serves, and fixes from feedback.

- **Checkpoints:** a sandbox can take checkpoints on a schedule, every 1, 4, 12 or 24 hours.
  It keeps the newest 3 and the last of each day for a week, and never deletes yours. Off by
  default; checkpoints can now be deleted oldest first.
- **Git review:** comment on lines of the diff, and send your review straight to the agent:
  as a prompt to one of the sandbox's chats (queued while a turn runs) or pasted into an
  agent's terminal. Pushes and pull requests have their own **Git** page in the sidebar,
  with the count waiting for review. An SSH forge's address takes a clone URL such as
  `git@github.com:owner/repo.git`.
- **Browser:** a teammate's browser opens what its sandbox serves at `localhost`, and clicks
  on the live view land where the pointer is (they drifted down toward the bottom of the
  page). After you hand the browser back, the teammate is told how long you drove, the pages
  you were on and where the page is now. A new **Stealth mode** in a teammate's settings
  presents its browser as a desktop Chrome for sites that turn automated browsers away.
  Chromium no longer asks about Google's services.
- **Previews:** an app's service worker works in the chat's preview panel; its script and
  fetches got 403 there.
- **Memory:** embeddings work again; every chunk failed with "invalid memory handle" and
  search ran on keywords alone.
- **Sidebar:** a running sandbox's terminals are listed under it, and a link can name the
  workspace tab to show.
- **Terminal:** Ctrl+L clears a terminal that is scrolled up.
- **Teammates:** without a provider, adding a teammate says why it can't and leads to the
  providers.
- **App:** Studio can be installed as an app, with its own icon and the number of approvals
  waiting on it.
- **Command line:** `studio -h`, `--help` and `help` list Studio's commands, and every command
  answers `-h`.
- **Sandboxes:** a second Studio on the machine can start sandboxes; DNS ports another
  program holds are skipped.

## v0.3.2 — 2026-10-15

A browser that works like a browser, a finished terminal UI, and fixes from feedback.

- **Browser:** a teammate's browser is one window with tabs (its profile, then each throwaway
  session), Back, Forward, Reload, an address bar and Downloads. Typing an address or going
  back takes the wheel; one line says who drives. The agent's questions show on the page,
  and a sleeping browser wakes when you open a page. The teammate's Browser tab has its
  Activity (every step by session, replayed from screenshots) and Sites (Ask first, Allowed
  or Blocked per site, hidden fields) beside it; the same window opens beside a sandbox and
  beside the teammate's conversation, whose thread shows a live card while it browses.
  Throwaway sessions end with the chat that opened them, when their sandbox stops, or with
  their tab's close button.
- **Approvals:** an approval shows in one place on the page; the floating stack leaves out
  cards a conversation, a chat, the browser, Home or a review page already shows.
- **Previews:** a preview's service worker can no longer catch Studio's login, and **Open
  fresh** clears a preview's caches, storage and service workers for an app that left a
  worker behind.
- **Terminal:** selecting text copies it, right-click opens Copy and Paste instead of the
  browser's menu, and Ctrl+Shift+C copies; a sandbox can no longer read your clipboard.
- **Claude subscription sign-in:** a step-by-step dialog, and the code you paste goes into
  Claude Code for you instead of having to be typed into the terminal.
- **Terminal UI:** `studio tui` has chat with streamed turns, Activity, git push review with
  the diff in a pager, and new sandboxes with their progress.
- **Sandboxes:** the workspace status line shows CPU and memory use; rebase runs in the
  background with the progress view and names the step that failed.
- **Pages:** git push reviews, memory conflicts and build logs have pages of their own to
  link to. Secrets are grouped by scope, and a teammate's settings list its secrets.
- **Git:** upstream fetches are rate-limited per staging mirror, so agent-signed fetches
  don't ask on every push.
- **Windows:** a sandbox's agent channel keeps accepting after a client hangs up, and closing
  it no longer hangs when the client hangs up at the same moment.
- **Tests:** the e2e suite drives git push review, chats and the browser with fakes, and the
  smoke workflow runs a browser test against a real VM.

## v0.3.1 — 2026-10-14

Fixes creating sandboxes on Windows.

- **Windows:** Studio serves each sandbox's agent channel on a named pipe that only your user
  can open. v0.3.0 used a socket file there, which microsandbox rejects on Windows, so every
  sandbox failed to create with "vsock host path must be a local Windows named pipe".
- **Doctor:** on Windows, `studio doctor` fails when Studio runs in a job object that keeps
  sandboxes from starting detached (they would fail with "Access is denied"). On a Mac
  without Hypervisor.framework it says so instead of reporting an error.
- **Smoke tests:** a nightly workflow builds Studio on Linux, macOS and Windows, starts it,
  and creates and deletes a sandbox. It replaces the old nightly integration test.

## v0.3.0 — 2026-10-14

Teammates you talk to in one place, a command line, subscription sign-ins, and network
security levels.

- **Teammate conversation:** a teammate's page opens on one thread: its chat turns with
  their tools, the facts it learned (with Forget), its tasks and its terminal sessions, the
  approvals it waits on, and the running turn live. The composer sends a message, hands over
  a task or teaches a fact; a rail shows what it is doing, knows and runs on.
- **Command line:** `studio ls`, `show`, `start`, `stop`, `suspend`, `resume`, `create`,
  `fork`, `rm`, `shell`, `ask`, `approvals`, `approve`, `deny`, `checkpoints`, `checkpoint`,
  `restore` and `envs` talk to the running Studio, with `--json`, exit codes for scripts and
  shell completion.
- **Terminal UI:** `studio tui` lists sandboxes live, starts and stops them, decides
  approvals and attaches to a sandbox's terminal in full screen.
- **Memory:** chats are remembered once they go idle, each session leaves a short summary
  that the next sessions see, and the memory tools say when to use them. The memory page
  shows quality numbers, suggestions to promote or accept, and exports and imports one
  markdown file per scope. Facts can hold in a context, and each sandbox has a read-only
  `~/memory`.
- **Teammate tasks and check-ins:** teammates keep a task list with MCP tools, and check in
  on open tasks on a schedule.
- **Subscription sign-ins:** Claude and ChatGPT subscriptions sign in through small system
  VMs; their tokens stay on the host. Claude subscription chats run through Claude Code, and
  `claude` in a terminal starts without asking how to sign in.
- **Security levels:** each environment and teammate has a level that decides requests no
  rule covers, with a curated list of development infrastructure and an AI reviewer at the
  assisted level, which Activity lets you revert.
- **Network rules and approvals:** rules record their origin, can expire, count their hits
  and are flagged when unused for 30 days. Approvals can allow once or for an hour, are
  grouped when they arrive together, and start at the teammate's scope and pattern level.
  Rules can name a request's method and path; proxy rules can send requests to another
  upstream. Private hosts (such as a company VPN) are reachable only through an explicit
  per-environment list, and deny rules answer lookups with NXDOMAIN.
- **Browser:** browsing is scoped per site with request-level approvals, a tracker list is
  denied, downloads go to the agent and to you, and lost replies and large screenshots no
  longer break a call.
- **Teammates:** emoji or generated avatars, copying a teammate to another environment,
  environment export and import, and agents are told which sandbox they are in.
- **Sandboxes:** live CPU and memory resize where the VM allows, uploads from the Files
  panel, SSH docs, Nix as a template tool, and an export disk guard.
- **Git:** GitLab as a forge, integration MCP tools for push and pull request status, and a
  note in the next chat when a push is rejected.
- **Look:** Home as one dashboard with inline approvals and an activity feed, polished
  sandbox and Settings pages, Settings at the bottom of the sidebar, and themed scrollbars.
- **Quality:** golangci-lint in CI, fuzz targets for the gateway's parsers and the Caddyfile
  compiler, and a memory retrieval eval.

## v0.2.0 — 2026-10-13

Shaped by feedback on v0.1: a new layout and look, a guided setup, and safer defaults for
git and secrets.

- **Guided setup:** a first start opens `/setup`, a stepper through the host check, work
  mode, AI provider, a default teammate (Iris), git, network and a first sandbox. Every step
  can be skipped, and Home shows a setup card until it is done or dismissed. The login link
  is printed as a banner at startup.
- **New layout:** a compact sidebar with sandboxes, their status and the approvals waiting
  on them. Teammates get their own pages (overview, chats, memory, browser, settings), and
  settings are split into general, providers, git, templates and runtime. Each sandbox has a
  dashboard (facts, uptime, ports, connections, history, checkpoints, chats, terminals) and a
  separate workspace with one side panel at a time. Old addresses redirect.
- **Workbench look:** denser tables and lists, monospace identifiers with copy buttons,
  status dots with words, an accent color per area, and a light/dark/system theme toggle in
  the sidebar footer. Home shows a stat strip, a sandbox table, the family tree, approvals
  and teammates.
- **Network:** rules, requests and activity are tabs with their own URLs; rules are grouped
  by domain with filters kept in the URL.
- **Sandbox creation and fork** run in the background with step-by-step progress.
- **Lineage:** each sandbox records where it came from and what happened to it (created,
  forked, rebased, checkpointed, restored, deleted); the dashboard shows its history and
  Home the family tree, both updated live. Templates are named by their stack, such as
  `node-22+python-3.13@2`.
- **Persistent home directory:** new sandboxes get a 4 GiB home disk that is kept across
  rebases and is part of forks, checkpoints and exports. Template toolchains moved to
  `/opt/mise`. Exports from v0.1 still import.
- **Git over the host's SSH agent:** forges can use your SSH agent instead of a stored key,
  with a hint to use a dedicated key and a warning when signing is slow.
- **Scoped secrets:** a secret can belong to an environment, a teammate or a single
  sandbox; the narrowest one wins.
- **Template editor:** the template spec editor has YAML highlighting, completion from the
  spec vocabulary, validation as you type and an example template.
- **Agent browser:** a teammate can open an ephemeral session that starts clean and is
  discarded when closed, next to its usual one. Clicks in the live view land where you
  click.
- **New chat** preselects the teammate or sandbox it was started from; a chat started in a
  workspace opens there.
- **End-to-end tests:** a Playwright suite against a fake runtime runs in CI and nightly.
- **Base image:** the image build fails if the mise download fails.

## v0.1.1 — 2026-10-12

- Mobile terminals have touch controls for Ctrl, Esc, Tab, arrows and the keyboard.
- Browser takeover accepts mobile text input, including composed text and paste, and offers
  touch keyboard controls.
- Teammate sandboxes can show their current browser in a resizable side panel, with a
  full-width view on small screens and a toggle to hide it without stopping the browser.
- Copy buttons work on the local HTTP development server, including when accessed over the LAN.
- Memory hooks load in both OpenCode V1 and V2, fixing the plugin error in upgraded VMs.
- Stopping a chat immediately after sending a message preserves prompt/cancel ordering.
- The browser has an address bar and guidance for a blank start page.
- Dev-server previews handle IPv6 loopback listeners and closed idle connections.
- Long network approval patterns stay inside their cards, and the guest agent's directory
  is traversable by the sandbox user.

## v0.1.0 — 2026-10-11

The first public release.

- **Sandboxes:** microVMs on microsandbox with persistent workspace and Docker disks, Docker
  inside, browser terminals (tmux) and port previews, disk checkpoints, suspend and resume,
  fork, rebase onto a new template, and export/import as a `.studio-sandbox` file.
- **Templates:** YAML specs (resources, Node/Python/Go versions, apt packages, a setup
  script) built into cached, content-addressed templates.
- **Environments:** isolated sets of rules, secrets, providers, teammates and memory, with a
  switcher in the UI.
- **Network gateway:** all sandbox TCP egress goes through Studio; `allow`, `proxy`,
  `caddy` and `deny` rules per environment, teammate or sandbox; unknown hosts are held
  until approved in the approvals inbox; per-environment TLS CA; connection log.
- **Secrets:** an encrypted vault on the host; guests see placeholders that the gateway
  substitutes only for bound hosts.
- **Teammates:** named agents with a role, soul, git identity and a harness (OpenCode,
  Claude Code or Codex) on Anthropic, OpenAI or OpenAI-compatible API keys. Subscription
  logins are not supported yet.
- **Chat mode:** chats with teammates over the Agent Client Protocol, with permission
  requests and questions, per-turn changes and undo, a file browser, an embedded preview,
  and a handover to the terminal and back. Onboarding asks for Chat or Terminal.
- **Memory:** per-teammate and shared memory with hybrid search on local embeddings,
  automatic recall and capture through harness hooks, consolidation and contradiction
  handling.
- **Browser broker:** a Chromium VM per teammate driven only by Studio, with approvals,
  redaction, credential fill, history and a live view with takeover.
- **Git review:** a virtual git remote; pushes are staged and reviewed before Studio pushes
  them upstream. GitHub, Forgejo/Gitea, HTTPS and SSH forges; pull request proposals on
  GitHub and Forgejo.
- **Packaging:** `studio doctor`, opt-in start at login and update check, `install.sh`,
  release builds for Linux (amd64, arm64), macOS (Apple Silicon) and Windows (amd64,
  preview) with checksums and build provenance attestations.
