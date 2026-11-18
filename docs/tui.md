# Terminal UI

`studio tui` is a keyboard client of the running Studio, in the same binary. It covers the
daily loop: answer requests of every kind from Home, work in a thread with a teammate beside
its computer, review git pushes, see, create, fork and suspend computers, save and restore their
checkpoints, see their open ports, follow the network Activity, and type into any of a
computer's terminals, an agent's among them. It is laid out as the web is: a sidebar, and a
thread beside its computer, with the computer's tabs. It uses the web's words: a teammate works
in threads on its computers, and what waits for you are requests. Everything else stays in the
browser, and the TUI prints the URL where it applies. Plain `studio` is still the server;
`studio tui` only ever connects to one.

The user page is [Terminal UI](../site/src/content/docs/tui.mdx). The design of the frame is in
[design/tui-frame.md](design/tui-frame.md), and that of the visual look in
[design/tui-visual.md](design/tui-visual.md).

## Why a terminal client

Studio is a browser app on a loopback-only server. Using it from another machine means a
tunnel or the LAN Vite setup, people who live in a terminal still switched to a browser to
answer an approval, and over plain SSH (a dev box, a homelab server, a cloud VM) nothing
worked without forwarding a port. The TUI runs *on* the host, so nothing new listens on the
network.

It covers the daily loop rather than parity with the web: it is a client of the same HTTP
API and adds no second way to change state. Settings, providers, souls, the template editor,
lineage, memory, the rules editor and the browser stay in the web app; the TUI prints their
URL.

- **Library.** Charm's Bubble Tea v2 with Lip Gloss and Bubbles (`charm.land/*/v2`, MIT,
  works on Windows terminals). `tview` has heavier widgets and less control over the look; a
  hand-rolled renderer on `golang.org/x/term` needs no dependency but much more code. There
  is no glamour dependency: `markdown.go` renders chat text itself, reading markdown as the
  web's renderer does.
- **`studio tui`, not `studio`.** Plain `studio` stays the server even in an interactive
  terminal with a server already running, so scripts and autostart never get a UI.
- **The web's frame, one column at 80×24.** A sidebar, a thread and its computer side by side
  on a wide terminal, and one of them at a time on a narrow one or over a slow link, so the
  same places and keys work at every size.

## Starting it

```sh
studio            # the server, in one terminal, or at login (studio autostart enable)
studio tui        # in another terminal, on the same machine
```

`studio tui` needs an interactive terminal and a Studio on its loopback address,
`127.0.0.1:7878` by default. For another address, put the flag before the subcommand:
`studio -addr 127.0.0.1:9000 tui`. Without one it says so, prints the start command and exits with
status 1. It doesn't start Studio itself.

## The frame

Every screen has a crumb row and a rule on top, and a rule, the key bar and the notice line at
the bottom. At 80×24 the body between them is 19 rows.

The **crumb row** says where you are: `Home`, `Iris`, `Iris › Fix the login form`, `Iris ›
desk` (a teammate's computer with no thread beside it), a computer of yours by its name, or a full screen's
name (`Activity`, `Settings`, `Computers`, `Keys`…). It starts with `≡` while the sidebar isn't
drawn. On the right it has the computer on show as a chip, `● blub ▾` (its status dot, and `▾`
when the teammate has more than one), what waits for you, `▲ 4` (`▲ 4 waiting for you` on
Home), and from 100 columns the clock. A click on `≡` opens the sidebar, on the chip shows the
teammate's next computer, and on what waits goes Home.

The **columns** depend on the width only, never on the look:

| Width | Sidebar | Main | Beside it |
|---|---|---|---|
| below 100 | a drawer over the body, on `\` | the whole width | nothing; `tab` shows the computer in the thread's place, and back |
| 100–119 | 30 cells | the rest | nothing; `tab` flips as below 100 |
| from 120, a thread or a teammate's home | 30 cells | 42% of the rest, from 40 to 60 cells | the computer, in the rest |
| from 120, a thread that is an agent in a terminal | 30 cells | the terminal, in the wider share | the computer's Details, in the narrower |
| from 120, Home, a computer of yours, or a full screen | 30 cells | the rest | nothing |
| full screen (`alt+z`) | none | the pane with the keys, on the whole screen | nothing |

At 140 columns a thread has 30, 45 and 63 cells. Below 100 columns, `\` slides the sidebar
over the body's left, the body left as it is behind it; the sidebar then has the keys, and `\`,
`esc` or `enter` on a row close it. `alt+z` puts the pane that has the keys on the whole
screen, and back; a resize keeps it there. A terminal in full screen keeps one status line,
`● blub › claude ▲    ctrl+] keys back · alt+z`, and a result shows as the toast.

**Who has the keys** is always in view:

1. **A terminal.** Its tab's rule and the bottom rule turn into the accent's heavy `━`. Every
   key goes to the terminal, `q` and `ctrl+c` included, except `ctrl+]` (the keys back to
   Studio), `alt+1` to `alt+9` (another of the computer's terminals), `alt+z` (full screen) and
   `alt+a` (to the [banner](#the-approval-banner)'s buttons).
2. **The composer.** Text goes to the draft. `enter` sends it, `ctrl+j` starts a new line,
   `esc` puts it aside. `/` at its start lists [commands](#commands-and-files-in-the-composer)
   and `@` the computer's files.
3. **Studio.** The sidebar, the main column or the computer beside it has the cursor. The split
   left of that column is the accent's `┃`; the others are a dim `│`. `\` moves the cursor to the
   sidebar and back, and `tab` between a thread and its computer.

**Places.** The frame shows a place, as the web's pages are: Home, a teammate's home, a thread
beside its computer, or a computer on its own. `enter` on a row of the sidebar opens its place.
`esc` goes back through the places opened before, skipping the ones that went, and last to
Home; `q` does too, except on Home and in the sidebar, where it quits. The other screens
(Activity, Settings, Computers, New computer, Checkpoints, Environments, help) cover the frame
and return to it with `esc`. A push of a computer the environment on show doesn't have is a
place of its own, its review alone.

## Screens and keys

Keys are the same on every screen and cannot be changed. The key bar shows the ones that apply,
and `?` lists the keys of the place it is pressed in. `n` never means new: it says no to a
request, and `+` makes something new. `ctrl+k` opens [Search Studio](#search-studio) from every
screen, the composer included; only a terminal with the keys and a line the notice asks for keep
it.

| Screen | Shows | Keys |
|---|---|---|
| **Home** | [Home's list](#home): what waits for you, what works and what is quiet, with a reading pane for the row under the cursor | `↑/↓` `j/k` move over the rows · `tab` the next request · the row's own answers, such as `y` `n` `o` on a network request · `enter` open the row · `pgup/pgdn` scroll the reading pane · `u` undo · `+` new computer · `/` search · `l` Activity · `,` Settings · `e` environment · `\` the sidebar · `?` help · `q` quit |
| **Search Studio** (from `ctrl+k`, or `/` outside the composer and Activity) | [anything by name](#search-studio), grouped by kind, over the place | type to search · `↑/↓` move · `enter` open · `>` actions only · `@` teammates only · `/` the files of the computer in view · `esc` or `ctrl+k` close |
| **The sidebar** (from `\`, or beside the place from 100 columns) | Home, TEAMMATES with their [threads](#threads), YOUR COMPUTERS, and ENVIRONMENT | `↑/↓` `j/k` move · `enter` open the row's place · `+` a new thread with the teammate under the cursor, or a new computer · `→`/`←` show or hide a teammate's older threads · `tab` the next thread that waits · `u` undo · `/` search · `l` `,` `e` · `\` or `esc` back to the place · `?` · `q` quit |
| **A teammate's home** (from its row) | [who it is](#a-teammates-home), its computers, tasks and what it knows, over a composer | `enter` write to it · `+` another computer · `t` its computer's terminal · `c` its changes · `m` its next computer · `tab` the computer · `esc` back |
| **A thread** (from its row, or a request's) | the [transcript](#thread), streaming, a request it waits on as a card, and the composer | `p` or `enter` write (`/` commands, `@` files) · `↑/↓` the tool calls · `x` stop · `y` `a` `n` its permission · the card's keys · `+` a new thread · `t` the terminal · `c` the changes · `m` the next computer · `tab` the computer · `esc` back |
| **A computer** (beside a thread or a teammate's home, from `tab`, or on its own) | [its tabs](#the-computer): Terminal, Changes and Details | `1`–`5` a tab · on Terminal `enter` type in, `alt+1`–`alt+9` another terminal, `+` a new shell, `T` the agent · on Changes `j/k` the rail's row, `↑/↓` scroll, `enter` fold a repository, `v` side by side, and a push's [review keys](#reviewing-a-push) · on Details `s` start, stop or resume, `z` suspend, `f` fork, `h` checkpoints, `d` delete, `l` its Activity, `t` the terminal, `T` the agent · `m` the next computer · `tab` back to the thread · `esc` back |
| **Computers** (from ENVIRONMENT › Computers) | every computer of the environment, the teammates' and then yours, one row each with its owner, template, requests and status. From 100 columns, the dashboard of the one under the cursor beside the list | `↑/↓` move · `enter` the computer · `t` its terminal · `s` `z` `f` `h` `d` as on Details · `+` a new computer · `esc` back, the cursor where it was |
| **Checkpoints** (from `h`) | the computer's checkpoints, newest first: name, auto or manual, state, age | `n` save one · `r` restore the one under the cursor (asks first) · `↑/↓` move · `esc` back |
| **New computer** (from `+` or `+ New computer`) | a teammate, a template, a name; then the create's steps. A fork's progress shows here too | see below |
| **A review** (from `enter` on a push of another environment's computer, or of one that went) | the push's facts, commits and diff, or the pull request | the [review keys](#reviewing-a-push) · `↑/↓` `j/k` scroll · `esc` back |
| **Activity** (from `l`, or ENVIRONMENT › Activity; Network opens it on network) | connections of the environment's computers and every decision, newest first | `←/→` the kind: all, network, git, agents, memory · `enter` the entry and its rule · `/` filter by computer · `r` print the URL of Settings → Network · `esc` back |
| **Settings** (from `,`) | the web's Settings sections in their three groups, each with what it is set to | `↑/↓` move · `enter` Activity opens here, the others print their address in the browser · `l` Activity · `/` search · `esc` back |
| **Environments** (from `e`) | the environments; `enter` switches | `↑/↓` · `enter` choose · `esc` cancel |

What waits counts the requests of every environment, so requests that arrived together count
once. When a new request arrives, the terminal rings its bell once.

A confirmation (delete a computer, restore a checkpoint) shows its choices in the notice line,
`y Delete  [n Keep]`, with Keep focused: `enter` keeps, `y` or `←` then `enter` goes ahead.

### The sidebar

The sidebar has the rows of the web's sidebar, in its sections. From 100 columns it is on the
left of every place; narrower, `\` opens it as a drawer on the row of the place on show. The
place has the keys at first: `\` gives them to the sidebar, and `\` or `esc` gives them back,
with the cursor on the row of the place on show again.

- **Home**: `▲ n` for what waits for you. It opens [Home](#home).
- **TEAMMATES**: each teammate, its name and its role, `▲ n` for the requests that wait on it;
  under it, [its threads](#threads). It opens [its home](#a-teammates-home). Then `+ New
  teammate`, which prints the address of the web's teammates page: a teammate needs a provider,
  a model and a soul, which stay in the browser.
- **YOUR COMPUTERS**: the computers without a teammate, then `+ New computer`. A computer opens
  on its own. A teammate's computers are on the Computers screen and beside its threads.
- **ENVIRONMENT**: Computers with their count, which opens [the Computers
  screen](#screens-and-keys); Network with the network requests waiting, which opens Activity
  on network; Secrets and Memory, which print their page's address; Activity; and Settings with
  the version.

Its first two rows are `◆ Studio` with the environment, which a click opens the picker on, and
`/ Search   ctrl+k`, which a click opens [Search Studio](#search-studio) on.

### Search Studio

`ctrl+k` finds anything of Studio by name, as `⌘K` does on the web, with the same words and the
same order. `/` opens it too, except where `/` is text or a filter of its own: the composer and
Activity. It is a box over the body, at most 64 cells wide and 16 rows high, below the crumb
row; below 44 columns it takes the whole body.

```
╭─ Search Studio ──────────────────────────────────────────────╮
│ › log▏                                                       │
│──────────────────────────────────────────────────────────────│
│ THREADS                                                      │
│▸▲ Fix the login form                             Iris · blub │
│ ENVIRONMENT                                                  │
│ ◆ Activity                                       Environment │
│ SETTINGS                                                     │
│ , General                        Settings › This environment │
╰─ ↑↓ move · enter open · esc close ───────────────────────────╯
```

It finds, in this order:

- **Teammates**, by name and role. `enter` opens the teammate's home.
- **Computers**, by name, template and `base`, with their owner and status on the right. A
  computer opens on its own. Its tabs are hits too, `blub › Files`, but only for a word of their
  own: `blub` finds the computer, `blub files` its Files tab, which opens beside the thread
  when the computer is already in view. Files and Changes are there while the computer runs,
  Browser for a teammate's.
- **Threads**, every computer's, the newest first, with the teammate and the computer on the
  right. `enter` opens the thread as its row in the sidebar does.
- **Environment**: Home (`inbox`), Computers, Network, Secrets, Memory and Activity, as the
  sidebar has them, each with words that lead to it, such as `rules` for Network and `log` for
  Activity.
- **Settings**: the sections of the Settings screen. `enter` opens the screen on the section
  and prints its address in the browser.
- **Actions**: a new thread with each teammate, a new computer, a new teammate, switch
  environment, stop, start or resume the computer in view, the keys, and quit.

Each group lists 5 hits, 12 with a prefix. A word matches at its start, inside a word, with its
letters in order, or with one typo (`netwrok` finds Network); every word typed must match. With
nothing typed, it lists **Recent**, the last 6 hits it opened that weren't an action, a file
or a request, and **Waiting for you**, the first 5 requests of [Home's list](#home); `enter` on a request goes Home with the cursor on it.

Three prefixes keep a search to one kind: `>` actions, `@` teammates, and `/` the files of the
computer in view. `/` reads the computer's file list from Studio, as the web's `@` in the
composer does, and keeps it for 30 seconds. With nothing after `/` it lists the files that
changed and the top of the workspace; then names that start with what you typed come first,
then names and paths that have it, then the ones with its letters in order, and a slash looks
in that folder (`/web/src/`). `enter` on a file
opens it in the computer's [Files](#files) tab; on a folder, the folder. Home and a full screen
have no computer in view, and a computer that isn't running can't list its files; the box says
so.

`↑/↓` (or `ctrl+p`, `ctrl+n`, `tab`) move, and wrap; the wheel moves too. `enter` or a click
opens the hit, `esc`, `ctrl+k` or a click outside closes the box. A pasted text goes to the
query.

### Home

Home is one list of everything, sorted by how much it needs you, and a reading pane for the
row under the cursor. Its sections:

- **WAITING FOR YOU · n**: [the requests](#requests) waiting in every environment, newest
  first, grouped as on the web. A row has the request's kind (`±` a push, `⇅` the network, `?`
  an agent's permission, `▣` the browser, `◈` memory), who asked, what for, where and how long
  ago.
- **WORKING**: the threads that work and wait on nothing.
- **QUIET**: the threads done or ended in the last 24 hours (the five newest), each teammate's
  next scheduled run, and your own computers.
- **EARLIER TODAY**, under the list: what was decided today. It takes no cursor.

With nothing at all it says `✓ Nothing waits for you.`

The cursor starts on the top row, so the first request to arrive is under it. It stays on its
row by the row's ID while the list changes, and when its row goes, as after a decision, it is
on the row that took its place, the next request. `tab` moves to the next request, around the
end. `↑/↓` and `j/k` move a row.

The row's own keys answer it, as [its card's](#requests) do: `y` `n` `o` `O` `1` `i` `p` `s`
`x` `w` on a network request, `A` `D` `r` `s` on a group, and on an agent's permission `y`
allows once, `a` always and `n` denies, as well as the numbers. The key bar shows the row's
keys first. A push can be read only in part in the reading pane, so `y` on it asks once more
on the notice line, `Push 2 commits to fix-login without opening the diff?  y Push  [n Keep]`:
a second `y` approves it, and any other key keeps it. A forced push takes `F`. `r` asks for
changes, `n` rejects and `i` ignores it, without asking.

`enter` opens the row: a push its [review](#git-review); an agent's permission its thread; a
network request the latest thread on its computer, or else the computer; a thread the thread;
a computer the computer; a scheduled run its teammate. The other kinds' `enter` is their
card's. `esc` comes back to Home with the cursor where it was.

The reading pane shows the row in full: a push's review (its facts, commits and diff), a
request's card with its choices as keycaps, a thread's transcript, a computer's dashboard, or
a scheduled run's task with its next and last run. `pgup/pgdn` scroll it. Below 90 columns of
Home it is under the list, at least six rows, and it is left out below 16 rows; from 90 columns
it is beside the list, which takes 42% of the width, from 46 to 60 cells.

A network decision that made a rule can be undone for a minute: the notice says `u undoes
it`, and `u` deletes the rule (`DELETE .../rules/{id}`). The request stays decided; the next
connection asks again. Decisions with no rule, once or ignore, and the other kinds have no
undo.

### Threads

Under each teammate, the sidebar lists its threads, indented, as the web's sidebar does
(`web/src/lib/threads.ts`):

- its agents in a terminal of one of its computers with no thread behind them, such as
  `claude` or `codex-2`;
- its threads, on any computer: every one that runs, waits for you or is continued in a
  terminal, the two latest others of the last 24 hours, and the one on show. A `+ n older` row
  shows the rest, and so do `→` on the teammate's row (`←` hides them again) and `enter` on the
  older row. A check-in's thread says so, and so does a scheduled task's run. A thread
  continued in a terminal is listed once, as its terminal.

A computer's plain terminals, `main` among them, are on its [Terminal tab](#terminal). A push
waiting for a review is a row of Home, not of the sidebar.

Each thread shows its agent's state:

| Mark | State | A thread | An agent in a terminal |
|---|---|---|---|
| `●` | working | a turn runs | the guest says its agent works |
| `▲` | waiting | one of its permission requests waits, or another request of its computer while it is its teammate's latest thread there | a request of another kind waits in its computer; requests don't say which process asked |
| `✓` | done | it worked, and hasn't been opened since | the same |
| `○` | idle | none of these | none of these |
| `·` | unknown | | the guest doesn't report the terminal's output |

A thread or agent that turns done says so, in the notice line or as a toast. `tab` in the
sidebar moves the cursor to the next waiting one, then to the next done one.

The TUI reads the environment's terminals (`GET /api/environments/{env}/terminals`) and its
threads (`GET /api/environments/{env}/chats`) in two calls: after every event of the events
stream, and every 10 seconds otherwise. A terminal comes with its label and its state,
`working`, `busy` or `idle`, as the guest reports it to the web. Done lives in the TUI's memory
only.

### A teammate's home

A teammate's home shows it as its web page does: its role and how it is (waiting for you,
working, done or idle), its COMPUTERS with each one's template, resources and uptime (`◂` marks
the one shown beside it), its TASKS (`GET .../teammates/{id}/tasks`, the open ones first), what
it KNOWS (`GET .../memory/facts?scope=teammate:{id}`, the newest fact and the count) and its
NETWORK level, its own or the environment's, and what its new rules apply to. They are read
every 10 seconds while the home is on show.

Under them is a composer, `› Write to Iris…`. `enter` opens it, and the first prompt starts a
thread on the computer shown (`POST .../sandboxes/{id}/chats`), which then opens. `+` opens New
computer with the teammate chosen, `t` goes to its computer's Terminal tab with the keys, `m`
shows its next computer and `tab` gives the computer the cursor. A teammate with no computer
says that `+` gives it one.

The computer shown is the one the web would show (`web/src/lib/computer.ts`): the one asked
for, else the one picked last with `m`, else one where a thread works, else the latest thread's,
else its first.

### Thread

A thread is opened from its row in the sidebar, from a request's row on Home, from a teammate's
home with its first prompt, or with `+`. An idle thread with nothing waiting opens with its
composer ready; `esc` closes the composer and a second `esc` goes back. Its computer is beside
it from 120 columns, and `tab` shows it in the thread's place below.

A chat loads its log (`GET .../chats/{chat}?last=40`), then follows its stream (`GET
.../chats/{chat}/stream?after=<rev>`), the SSE the web follows. Each event carries a chat, turn
or item row at a new rev; the TUI keeps the newest version of each row, so an event that
arrives late changes nothing. A lost stream resumes from the last rev after 2 seconds, and a
chat that is gone (404) says so.

As on the web, the log is a page: every turn's header, and the items of the last 40 turns
(no more than 500 items). The transcript starts with `↑ 12 earlier turns · scroll up to load
them`; once its top is on show (by `pgup`, `g`, the wheel, or a page that fits the screen) the
next page back loads (`?last=40&before=<from>`) and the view keeps its place. The stream's
items for a turn not loaded yet wait for their page. The transcript keeps each item's lines
until the item, the width, the look or its fold changes, so a long chat's frames stay cheap;
the item at work and the ask waiting on you are drawn fresh.

The transcript:

- Prompts are bold, after `›`. The agent's text is rendered as markdown (`markdown.go`, the
  same blocks and inline marks as `web/src/components/chat/markdown.tsx`): headings and
  `**bold**` are bold, `*italics*` italic, and a link shows its text with its address dim
  after it. Bullets become `•` (`◦` nested) and items wrap under their text; quotes get a
  `│` rule (`>` in the plain look). Code blocks drop their fences and get a dim `│` rule
  with their language (indented four spaces in the plain look); inline code takes the accent
  color (keeps its backticks in the plain look). Tables are boxed, each column as wide as its
  cells up to a fair share of the width, with cells wrapping inside; a table too narrow for
  its columns becomes `heading: cell` lines per row.
- Thoughts and tool calls are folded to one line: `▸`, the title, and the call's state on the
  right. `↑/↓` moves a cursor between them, `enter` (or `space`) expands one: a tool call's
  locations, its diffs (`-`/`+` lines) and text output, at most 40 lines. With no cursor the
  transcript follows the end; `↓` past the last fold returns there.
- Plans, permission requests and questions show as one line each. The first one that waits
  is answered in place, with its choices listed under it:
  - a permission request: `y` allows once, `a` always and `n` rejects, `1`–`9` pick an option,
    `i` ignores it. It is decided as its row on Home is. `←/→` move over its choices as
    buttons, and `space` or `enter` presses one.
  - a question: `i` skips it (`decline`). One with no field takes `1` for OK; a choice, or yes
    or no, takes `1`–`9`; text or a number takes `r`, which opens a line for the answer in the
    notice line. The answer goes to `POST .../chats/{chat}/items/{item}/answer` with
    `{action: "accept", content: {field: value}}`, as the web's form sends it. A question with
    several fields, or a list, is a form; its keys print the chat's URL instead.
- `pgup/pgdn` (or `b`/`space`) scroll a page, `g`/`G` jump to the start or the end.

Any other request the thread waits on, such as a network request of a tool the agent runs or
the push it made, is a card above the composer, as on Home's reading pane, and takes the card's
keys while the composer is closed; `y` on a push asks once more, and `enter` opens its review.
More than one says how many more, which Home answers.

The composer is a box at the bottom of the thread, in the chat's color, with the teammate's
name in its frame and its keys on the right; it grows with the prompt to six lines. While no
prompt is written it is one line on the keycaps' background: `› Write to Iris…  @ file  /
command` (the second half where it fits), `Sending…` or the draft put aside. `p` (or `enter` with no fold selected) opens it: `enter` sends, `ctrl+j`
(or `shift+enter` where the terminal reports it) starts a new line, `esc` puts the draft aside.
A prompt is sent with `POST .../chats/{chat}/prompt`; while a turn runs Studio answers 409, and
the draft stays. `x` stops the running turn (`POST .../chats/{chat}/cancel`). `+` starts a new
thread with the teammate: the composer opens, and the first prompt creates it (`POST
.../sandboxes/{id}/chats`). A chat continued in the terminal, or one whose computer was reset,
takes no prompt here.

`t` goes to the computer's Terminal tab with the keys, `c` to its Changes tab, and `m` shows
the teammate's next computer beside the thread.

A thread that is an agent in a terminal, such as `claude`, has no transcript: the main column
is that terminal, with the computer's terminals as chips over it, and beside it is the
computer's Details tab. Its keys are the [Terminal tab's](#terminal).

### Commands and files in the composer

The composer completes what you type in a box over it, of at most nine rows, as the browser's
composer does (`web/src/components/chat/composer.tsx`). `↑/↓` (or `ctrl+p`/`ctrl+n`) move in
it, a click picks a row, and `esc` closes it and keeps the text; it opens again once its `/` or
`@` is typed anew. A second `esc` puts the draft aside, as always.

**`/` at the start** of the draft lists commands, and what follows it filters them: those whose
name starts with it first, then those with its letters in order (`/chnges` finds `/changes`).
`enter` runs the one under the cursor and clears the draft, and `tab` completes its name. A
draft that starts with `/` and names no command is sent as text, so `/usr is full` reaches the
teammate. The commands go where a key would:

| Command | Does |
|---|---|
| `/term` | the computer's Terminal tab with the keys, as `t`; its hint names the terminals |
| `/files`, `/changes`, `/browser`, `/details` | that tab of the thread's computer, with the cursor |
| `/threads` | the sidebar, on this teammate's threads |
| `/new` | a new thread with the teammate, as `+` |
| `/stop` | stops the running turn, as `x` |
| `/start`, `/stop-computer` | starts (or resumes) the thread's computer, or stops it |
| `/home`, `/search`, `/keys` | Home, [Search Studio](#search-studio), the keys of this place |

A command is listed only where it can go: `/files` and `/changes` while the computer runs,
`/browser` on a teammate's computer, `/stop` while a turn runs, `/new` on a thread that exists.

**`@` at the start of a word** lists the files of the thread's computer, changed files first,
ranked as Search Studio's `/` ranks them (`GET .../sandboxes/{id}/files/index`, the same read,
kept 30 seconds), eight at a time. What follows the `@` filters them, and `:12` or `:12-20`
after it names lines. `enter` or `tab` on a file puts `@path ` in the draft and mentions the
file; on a folder it narrows the list to what is in it. The prompt sends the mentions its text
still names (`mentions: [{path, start, end}]`), and the agent gets a link to each, as from the
browser. A computer that doesn't run says to start it, and `enter` with nothing listed sends
the draft.

### The computer

The computer pane is the web's computer screen: a row of tabs, `Terminal`, `Files`, `Changes`,
`Browser` and `Details`, the active one bold over a heavy `━` in the rule, and the computer's
status dot and word on the right. It is beside a thread or a teammate's home from 120 columns,
in their place after `tab` below, and alone for a computer of yours or one opened from Home or
Computers. `1` to `5` pick a tab by its place in that row. Only a teammate's computer has a
browser, so a computer of yours has no Browser tab, and `4` says so; beside a thread that is an
agent in a terminal the thread is the terminal, and `1` says so. `m` shows the teammate's next
computer, and `tab` goes back to the thread.

#### Terminal

A line of the computer's terminals as chips, `main  claude ▲  2  + new shell        61×33`: the
one on show bold, an agent with a request waiting marked `▲`, and the terminal's size on the
right while it is connected. Under it is that terminal, live: the pane attaches it once the
tab has been on show for 150 ms, and lets it go when it is left; the tmux session keeps
running. It shows `main` at first.

`enter` (or a click on the terminal) gives it the keys; `ctrl+]` gives them back. `alt+1` to
`alt+9` show the computer's n-th terminal, and keep the keys where they are. `+` opens a new
shell and gives it the keys, named as the web's `+` names one (`main` when it is free, else the
smallest number from 2). `T` starts the teammate's agent in a new terminal (`POST
.../sandboxes/{id}/sessions`, the web's Start agent). A computer that isn't running says `Start
blub first: Details, then s.`

The pane sends its size when it attaches and when it changes. tmux sizes a session to its
latest client, so a terminal open in the web too follows whichever was resized last. Alt keys
need a terminal that sends them as `ESC` and the key; on macOS, set Option to act as Meta.

#### Files

The computer's files, to read, as the web's Files tab. The top line is the folder on show,
`/workspace/src/`, over its entries, folders first: a folder with its slash, a link with `→`, a
file with its size and, in a list of 44 cells or more, how long ago it changed. From a pane of 80
cells the list is 30 cells wide with the file opened beside it; narrower, the file shows in the
list's place. A file shows as its text with line numbers, as a picture on a terminal of 256
colors or more (PNG, JPEG and GIF; a WebP says it is a picture), or as a line that says why
neither: larger than 1 MiB, not text, or not readable by the computer's user.

`j` and `k` move through the list, and so do `↑` and `↓` unless a file is open beside it, which
they scroll; `pgup`/`pgdn` (or `b`/`space`), `g` and `G` scroll it too. `enter` or `→` opens a
folder or a file. `←` or `backspace` goes up a folder, with the cursor on the one it came from,
or, where the file shows alone, back to its folder. Studio doesn't follow links: `enter` on one
says so. `r` goes to the next root: `/workspace`, `/tmp`, then the whole computer (`/`). `o`
prints the tab's address in the browser, where files are edited and uploaded. A click picks an
entry and a double click opens it; the wheel moves the cursor over the list and scrolls a file.

The folder is read with `GET .../sandboxes/{id}/files?path&root` when the tab shows and every 10
seconds after; a file with `GET .../files/content?path&root` when it opens, and again when its
folder says it changed. A listing has at most 1,000 entries; a longer one says the browser lists
them. A stopped computer has no files to read: `Files are read while docs-site runs. Details,
then s starts it.` A computer whose agent is from an older Studio lists only `/workspace`, and
the other roots say `Restart the computer to browse outside /workspace: its agent is from an
older Studio.` A file's text is cleaned as the rest of Studio's answers are: escape sequences go,
and a tab is four spaces.

#### Changes

What git sees on the computer, the pushes that wait for you and the ones that went out, and
what the open thread changed, as the web's Changes tab (`web/src/components/computer/
changes.tsx`). The tab's label counts the changes of every repository: `Changes 5`. On the
left is the rail, in sections:

- **WAITING FOR YOU**: the computer's pushes, pull requests, forge reviews and comments that
  wait, oldest first: `Push to fix-login`.
- **PUSHED**: its pushes and pull requests allowed in the last day: `Push to fix-login · #12`,
  or `· failed`.
- **REPOSITORIES**: each repository under `/workspace`, `▾ web · fix-login ↑2` and its count,
  then its changes under dim `staged`, `not staged` and `untracked`: `M src/login.tsx +1 −1`
  (`A` added, `M` modified, `D` deleted, `R` renamed, `!` conflicted). A repository after the
  first starts folded, `enter` on its header folds or opens it, and one without changes says
  `clean`.
- **THIS THREAD**: beside a thread, its requests that changed files, newest first, by their
  prompt.

The rest of the pane shows the row under the cursor: a push's [review](#reviewing-a-push), a
forge review or comment as its card, a repository's branch, upstream and changes, a change's
diff from its first hunk under its path and area, or the files a request changed with their
diffs. At first the cursor is on the first push that waits, else the first change, else the
thread's newest request. `j/k` move it, and the cursor stays on its row as git is read again.
`↑/↓` scroll, `pgup/pgdn` (or `b`/`space`) scroll a page, `g`/`G` jump to the start or the end,
and `[`/`]` to the previous or the next file. From a pane of 70 cells the rail is beside the
diff; narrower it is one line over it, `‹ M src/login.tsx +1 −1 · 3 of 7 ›`. From a pane of
160 cells the diff is side by side, which `v` toggles: at 180 columns that is after `alt+z`.

Git is read with `GET .../sandboxes/{id}/git` every 5 seconds while the tab shows, every 20
seconds while only its label does, and at once when a request of the open thread ends. PUSHED
comes from `GET .../approvals?status=approved`, read every third time while the tab shows. A
change's diff is read when the cursor comes to it (`GET .../git/diff?repo&path&area`, and
`from` for a rename) and again once git says the change moved on; a request's files come from
`GET .../chats/{chat}/turns/{turn}/changes`. A stopped computer has no git to read: `Git's
changes are read while docs-site runs. Details, then s starts it.` A computer whose agent is
from an older Studio says `Restart the computer to see its git changes`.

**Comments on lines.** `c` puts a cursor on the line of a diff at the top of main, reversed,
in the accent while the computer has the keys. `↑/↓` move it from line to line of the files,
past the hunks' heads, and the other scroll keys bring it along with the page. `enter` asks for
the comment in the notice line, and `esc` (or `c` again) lets go of the cursor. Side by side, a
row with a removed line beside an added one takes the comment on the added line; `v` shows the
diff as it comes, where the removed line takes its own. Where the comment goes depends on the
row:

- **On a push that waits**, the comments collect: each shows dim under its line,
  `╰ Why did this go?`, and the notice counts them. `enter` on a line with a comment edits it,
  and an empty comment takes it back. `r` sends them with what to change (see
  [Reviewing a push](#reviewing-a-push)), and `L` leaves them on the push without deciding
  it, for any agent on the computer to read (`POST .../approvals/{id}/feedback` with
  `{note}` alone): `Left 2 comments on fix-login for Finn to read.` A decided push takes no
  comments, nor does a pull request, whose comments are written on the forge.
- **On a change or a request**, the comment goes into the thread's composer, on a line of its
  own: `@web/src/login.tsx:13 Is this the only button?`. The path is the file's in
  `/workspace`, the repository's path first, and the composer gets the keys. The prompt sends
  the file as a mention (`mentions: [{path, start}]`), as the web's `@` does, while its text
  still names it. A removed line is no line of the file any more, so its comment mentions the
  file alone and says which line it was:
  ``@web/src/login.tsx (removed line 13, `<div onClick={submit}>Sign in</div>`)``. Without a
  thread on the computer beside the tab, `c` says to open one.

#### Browser

The teammate's browser, to look at. The top line is the page's address, `No page yet` before
the first; under it who drove it last (`api-refactor drove it last.`, or `You're driving.` while
you drive it in the web) and how many pages agents opened in sessions of their own, then the web
address where it is driven. A page a rule blocked names the rule (`The page didn't load: a rule
for *.example blocks intranet.example. Settings, then Network edits it.`), and a page that asks
for a username and password says so. On a terminal of 256 colors or more the page follows in
half blocks, as wide as the pane and as tall as its rows let it, each cell two pixels of the
browser's latest frame. A browser that isn't running says what it is doing, in the web's words
(`Asleep`, `Waking up…`, `Going to sleep…`, `Stopped unexpectedly`, `No browser yet`), and
where the web wakes it.

The tab has no keys of its own: the browser is driven, and woken, in the web. It reads `GET
.../teammates/{id}/browser`, and while the browser runs `GET .../browser/frame`, every 3 seconds
while the tab shows, as Home's reading pane does.

#### Details

What the computer is and does: its status, teammate, template, resources, uptime, requests, ID,
and its open ports. From 20 rows in the visual look it is the dashboard: CPU over the last 2
minutes as an area chart with its mean and peak, memory as a gauge and a one-row history, the
first request waiting as a card, the history line from its lineage, its newest connections and
its teammate's browser.

`s` starts, stops or resumes it, `z` suspends it, `f` forks it, `h` opens its checkpoints, `d`
deletes it (after asking), `l` opens its Activity and `t` its Terminal tab with the keys. On a
teammate's computer, `T` starts the teammate's agent, as on the Terminal tab.

The ports (`GET .../sandboxes/{id}/ports`) are listed with each one's preview address,
`http://{port}-{id}.localhost:{studio port}/`, while the previews setting (`GET
/api/system/previews`) is on; otherwise only the port. Where the terminal shows links, the
address is one (OSC 8). Studio announces no change to the ports, so the tab reads them again
every three seconds while it is on show, as the web's Ports region does. It lists six and counts
the rest.

The previews need a preview login. The web opens a preview through a one-time link
(`POST .../previews/{port}/open`), which sets a preview cookie for eight hours. The TUI doesn't
print those links: each is a one-time token, too long for an 80-column line, and only good
in a browser anyway. A preview opened once from the web's Ports region opens from the TUI's
address after that.

### The approval banner

When a request waits in a computer, its Terminal tab, and a thread that is an agent in a
terminal, get a box over the terminal's top rows that says what waits, such as `api-refactor
wants proxy.golang.org:443 · 1 more`. A thread's own requests are cards in it instead. The box
carries its choices as buttons: `o Allow once`, `y Allow`, `n Deny` and `a More…` for a single
network request, `a Review it` for any other kind or a group.

- **While Studio has the keys**, the letters decide it, and `←/→` move over the buttons and
  `space` or `enter` presses one. `enter` keeps typing into the terminal until an arrow is
  pressed.
- **While the terminal has the keys**, they go to the agent, so the box says `alt+a answers
  it`. `alt+a` moves the keys to the banner's buttons. After a decision, or `esc`, the keys go
  back to the agent, so answering takes `alt+a`, then `y`, and typing goes on.
- **`a`** (More…, Review it) goes to Home with the cursor on that request, where the pattern,
  the scope and the other choices are. Once it is decided there, the TUI goes back to the
  terminal, with the keys where they were. `esc` there goes back without deciding.

`studio shell <sandbox> [terminal]` hands the terminal over raw, so it can't draw a box over
the computer's screen. Instead, while it is attached, it checks the computer's pending
approvals every 2 seconds. A new one sets the terminal's title to `▲ api-refactor wants
proxy.golang.org:443 · Ctrl-] answers it` and rings the bell once. The title is written only
after 150 ms without output, so it doesn't land inside an escape sequence the computer is
writing, and the old title is pushed (`CSI 22;0 t`) and popped (`CSI 23;0 t`) around it, in
terminals that keep a title stack. Its `Ctrl-]` then asks about the approval in place (`Shell`,
`askApproval` in `internal/tui/shell.go`): it clears the screen and shows the request with `o`
allow once, `y` allow (the teammate's default pattern and scope, as the card would start) and
`n` deny, or for another kind says to answer it in the TUI or the browser. `enter` goes back to
the terminal, and `q`, `Ctrl-]` or `Ctrl-C` detach. After a decision the terminal is attached
again, which redraws it. With nothing waiting, `Ctrl-]` detaches as before.

### Requests

Each request is a row of [Home](#home), and its card is in the reading pane, or above the
composer of the thread it waits on. Requests
from one computer that arrive within three seconds of each other form one row and one card,
and a browser page's requests to one site form one, as on the web. A card's choices start the
way the web card starts them: the pattern level and the scope its teammate last chose.

| Card | Keys |
|---|---|
| Network request | `y` allow · `n` deny (each makes a rule) · `o` allow once · `O` deny once (no rule) · `1` allow for 1 hour · `i` ignore · `p` cycle the host pattern, then a browser request's method and path · `s` scope: environment, the teammate's computers or only this computer; for a browser request, who: its browser, all browsers, or browsers and computers · `x` any port instead of the default ports · `w` allow the navigation's site for this browser session |
| Group | `A` allow all · `D` deny all · `r` review one by one · `s` scope, or who for a browser page's requests |
| Git push or pull request | `enter` opens its review in the computer's Changes tab · `y` asks once more, then approves · `F` force-pushes · `r` requests changes · `n` reject · `i` ignore |
| Browser action | `y` allow · `n` deny · `i` ignore · `r` toggles whether the decision holds for every such action on the origin (the web's "always") |
| Browser credential | `y` fill · `n` deny · `i` ignore |
| Memory share | `y` share with every teammate · `n` decline · `i` ignore |
| Memory conflict | `1` keep fact A · `2` keep fact B · `i` later (it stays open on the memory page) · `enter` prints the memory page's URL, for keep both or an edit |
| Agent permission | `1`–`9` pick one of the agent's options, numbered as listed · `i` ignore |
| Any other kind | `enter` prints the URL of the page that decides it |

The decisions go to the same endpoint as the web card (`POST
/api/environments/{env}/approvals/{id}/decide`), with the same bodies:

- a browser action: `{action, scope: "teammate", remember}`, with `remember` left out for an
  ignore;
- a browser credential: `{action, scope: "teammate"}`;
- a memory share, and a memory conflict put off: `{action, scope: "environment"}`;
- an agent permission: `{action, scope: "environment", remember}`. Each option maps to an action
  and `remember` by its kind (allow once, allow always, reject once, reject always), and Studio
  maps them back to the option, as it does for the web's buttons.

Keeping one fact of a memory conflict resolves the conflict itself, as the web's conflict sheet
does: `POST .../memory/conflicts/{id}/resolve` with `{resolution: "keep_a" | "keep_b"}`. Studio
then closes the approval. Keeping both facts, or editing one, needs a text editor for two
facts, so it stays in the browser.

### Reviewing a push

`enter` on a push's row on Home, or on its card in a thread, opens the push in its computer's
[Changes tab](#changes): beside the thread when the thread is on that computer, else on the
computer's own. It shows what the web's review shows (`web/src/components/git-review.tsx`),
from the approval's `git` detail: the repository, computer and teammate, the branch (and whether
it is new), the change with its file count and line totals, the commits, and the unified diff,
colored as on the web (`+` green, `-` red, hunks in the accent). A pull request shows its
branches, title and body instead. It scrolls as the tab does. A push of a computer that the
environment on show doesn't have, or that went, opens a review place: the review alone, where
`j/k` scroll too, and from 120 columns a file list sits beside it.

`y` approves and pushes (or opens the pull request), `n` rejects, `i` ignores. A forced push
shows **Rewrites history** with the commits it drops, and `F` (force-push) approves it instead
of `y`, which only says so. Each decides with
`{action, scope: "sandbox"}`, as the web does. The Changes tab stays, the push going to PUSHED
once it is out; a review place goes back. On a
push to a branch other than the default one, `p` says whether approving opens a pull request
too, and `o` opens it after a push without one. `r` requests changes: it asks for a note,
rejects the push with it, and for a teammate's push sends it to a new thread on the computer
(`POST .../approvals/{id}/feedback` with `{note, newChat: true}`), as the web's Request
changes does. The [comments on lines](#changes) left on the push go with the note, after it,
as the web writes them, and the note may then be empty:

```
Comments on the diff:
- src/login.tsx:13 (removed line), on `<div onClick={submit}>Sign in</div>`:
  Why did this go?
```

A review place takes no comments: they are written in the browser there.

### Activity

`l` on Home or in the sidebar shows the environment's log; on a computer's Details tab it
starts filtered to that computer. The log merges, as the web's Activity does:

- each computer's latest connections (`GET .../sandboxes/{id}/connections`), which Studio keeps
  in memory: host and port, the verdict (allowed, denied, failed, waiting, unanswered), and the
  rule that decided it;
- the decisions on its network requests (`GET .../approvals?status=all`, decided ones only),
  worded as on the web: Allowed, Denied, Allowed once, Ignored, Allowed · reviewer, the curated
  list or the level Open;
- the decisions of the other kinds: pushes and pull requests, agents' permissions and browser
  actions, memory shares and conflicts.

`←/→` narrows the log to a kind, as the web's filter does: all, network, git, agents or
memory. The header shows the kinds, the one on show highlighted.

Studio announces no change to the connection logs, so the screen reads them again every two
seconds while it is on show, as the web's connection panel does. `/` filters by computer name;
`esc` clears the filter before it leaves. `enter` opens an entry with the rule that decided it
(`GET .../rules`): its host pattern, action, ports, what it covers (the environment, a
teammate's computers or one computer), the requests it narrows to, its origin (an approval, the
curated list, the AI reviewer, by hand), hits, expiry, age and note. Without a rule it says why:
the curated list, the level, a once decision, an ignore. Rules are edited in the browser; `r`
prints where.

### New computer

`+` on Home or the Computers screen, or `+ New computer`, asks, one step at a time, what the web's create dialogs ask:

1. whose computer it is: a teammate of the environment, or none (skipped when there are none);
2. what it starts from: the base image or one of the environment's templates (`GET
   .../templates`);
3. its name, which is also its hostname: lowercase letters, digits and dashes, checked
   against the API's pattern before the create.

`enter` goes on, `esc` goes back a step. The create is `POST .../sandboxes` (or `POST
.../templates/{id}/sandboxes` with a template), which answers at once with the computer in state
`creating`. The progress screen then follows it through the `sandboxes` topic of the
event stream, as the web's progress does: the steps done, the one that runs (with the image
pull's bytes or layers), and those to come. Once the create is over it opens the computer. A
failed create shows the step that failed and why, and `d` deletes the computer. `esc` leaves
the create running on its own.

### Fork, suspend and checkpoints

`f` on a computer's Details tab or a row of the Computers screen asks for the fork's name in the notice line, starting from the web's
suggestion (the name, cut to 35 characters, with `-fork`). The name is checked against the
API's pattern before the fork. `POST .../sandboxes/{id}/fork` answers at once with the new
computer in state `creating`, and the progress screen follows it as it does a create.

`z` suspends a running computer (`POST .../sandboxes/{id}/suspend`); `s` resumes it.

`h` opens the computer's checkpoints (`GET .../sandboxes/{id}/checkpoints`). They are copies of
its disks, not of its running processes. `n` asks for a name in the notice line; an empty one
takes the web's suggestion from the time (`Checkpoint 2026-10-17 09:30:00 UTC`, with ` (2)` and
on when taken). It saves while the computer runs or is stopped. `r` restores the checkpoint under
the cursor after a `y`: only a stopped computer goes back, on a runtime that can restore
(`checkpointRestoreSupported`), and what changed since is lost. Deleting and renaming checkpoints,
and the schedule, stay in the browser.

## Sign-in

`studio tui` signs in as the local owner, the way `studio login-url` does:

1. It reads the install key from the catalog in the data directory and derives the control
   credential from it (`webauth.ControlToken`). Only a process that can read the catalog has it,
   so only the same OS user on the same machine can use the TUI. Over SSH, run it on the host.
2. It presents the control credential to `/api/auth/launch` and gets a one-time login ticket.
3. It exchanges the ticket at `/api/auth/exchange`, as the web login does, and keeps the
   resulting session cookie in memory. That session is the TUI's own; a browser never sees it.
4. On exit it calls `/api/auth/logout`, which clears the cookie.

No endpoint was added for this. Nothing listens on the network: the TUI reaches Studio on its
loopback address only, and refuses any other address.

Studio's sessions are signed cookies with no server-side list, as in the browser. Logout clears
the cookie in the client, but a copy of the cookie stays valid until its 30-day lifetime. A
revocable session table would be a change to the web sessions too; it is not part of the TUI.

## Terminal

The TUI draws a computer's terminals itself, at every width: on the Terminal tab, as a thread
that is an agent in a terminal, and in full screen. An emulator (`charmbracelet/x/vt`) is fed
by the same websocket as the web terminal (`.../terminals/{name}/attach?cols&rows`), and the
keys are encoded as a terminal would. A terminal is created when it doesn't exist, as `main` is
the first time. Only a running computer has terminals.

`studio shell <sandbox> [terminal]` hands the terminal over instead, on the same websocket:

- The screen, the keyboard and the terminal's size are handed over in raw mode, so the
  computer's full-screen programs work as they do in a terminal of their own. The size is sent
  whenever it changes, checked every 250 ms.
- `Ctrl-]` detaches. It is not sent to the computer. If an approval came while it was
  attached, `Ctrl-]` asks about it, as [the banner](#the-approval-banner) says.
- If the computer ends the terminal, Studio shows that and the next key detaches. That key is
  not sent to the computer either.

## Stays in the browser

Editing Settings, providers, templates and the template editor, rules, memory, lineage,
secrets and the git forges; creating or editing a teammate; deleting or renaming checkpoints
and their schedule; editing a computer's files and uploading to it; comments on a range of a
diff's lines, and in a review place; and driving the teammate's browser and its DevTools. Some answers stay there too, because they need more than a key:

- keeping both facts of a memory conflict, or editing one: two texts to edit;
- a chat question that is a form of several fields, or a list: a form to fill;
- a preview's first login: a one-time link for a browser.

Where a screen has a key for one, it prints `open in browser: <url>` in the notice line.
Settings (`,`) lists the web's sections with what each is set to, and `enter` prints the
address of one. `?` gives the browser's address for the rest.

## Look

There are two looks. The visual look is the default on a terminal with colors. The plain look
is used under `STUDIO_TUI=plain`, under `NO_COLOR`, or on a terminal without colors.

The plain look uses:

- the terminal's own foreground and background;
- one accent (ANSI cyan) for focus and status;
- dim text for secondary facts;
- green and red in diffs.

Every screen is laid out for 80×24.

The visual look carries the web's visual language ([design/ui-style.md](design/ui-style.md)
and [design/tui-visual.md](design/tui-visual.md)):

- **Colors.** One hue per area: network, git, browser, memory and chat. The status set is
  running, waiting, failed and stopped. The status dot is colored and the word never is;
  stopped is a hollow `○`. Truecolor terminals get the web's values, with light and dark
  variants chosen from the terminal's background. 256-color terminals get the nearest xterm
  colors. 16-color terminals get ANSI colors, so the user's palette still applies there.
  Focus stays the one cyan accent.
- **Crumb row and key bar.** The [crumb row](#the-frame) is the same in both looks; the
  visual one colors the chip's status dot and what waits. The key bar groups keys with a dim
  `│` between the groups and calls `?` "more". When the separators don't fit, it drops them.
- **Cards.** In Home's reading pane a request is a rounded card with its area's rule, a kind
  chip, and the choices as keycaps. `←/→` move over the card's toggles (pattern,
  scope, ports) and then its choices; the one focused is reversed, and `space` or `enter`
  steps a toggle or presses a choice. Nothing is focused until an arrow is pressed. In the
  plain look the focused one is in brackets.
- **Chat.** Prompts have a green rule and tool calls a glyph for their kind. A running call or
  turn spins. Code blocks have a dim rule and their language, and a plan is a checklist.
- **Wide layouts.** From 100 columns the sidebar is beside the place, and from 120 a thread's
  computer beside the thread ([the frame](#the-frame)), in both looks. From 90 columns of its
  own, Home's list has its reading pane beside it. A computer's dashboard has the history line
  from its lineage and its network: its connections newest first, with their verdict's dot, age, host and verdict,
  read from `GET .../sandboxes/{id}/connections` every 2 seconds while it shows (`dash.go`).
  From 120 columns a review place gains a file list with +/− bars, where `[` and `]` jump
  between files. When the computer's teammate's browser runs, the dashboard ends with its page's
  address and, on terminals with 256 colors or more, a picture of the page about 40×9 cells,
  drawn in half blocks (`▀`, two pixels a cell) from `GET .../browser/frame` every 3 seconds
  (`picture.go`). A picture that doesn't fit the pane's height is left out. From 160 columns of its pane a
  diff is side by side, and `v` toggles it. Inside a changed line, the words it changes are in reverse video. They come from a
  diff of the words of each removed line and the added line paired with it (`worddiff.go`),
  and lines that share no word, or have more than 300, keep their line's color only.
- **Live numbers.** From 100 columns, the Computers screen's rows have a CPU sparkline per
  running computer, and the dashboard charts CPU and memory over 2 minutes (`charts.go`). The
  sidebar, 30 cells wide, has no room for one. Details below 20 rows shows CPU and a memory
  bar. Usage is read every 2 seconds, while the frame or Computers is on show, for running
  computers. A runtime that
  reports no usage stops the reads. Activity has a strip of the busiest hosts, with 10-minute
  connection sparklines.
- **Mouse.** Bubble Tea's cell-motion mode is on, and holding shift keeps the terminal's own
  selection. A click selects a row: a computer, thread, place, card, log entry, checkpoint,
  environment, fold, file or change, and gives its column the cursor. A double click opens it as
  `enter` does. A click on a terminal gives it the keys. The wheel scrolls a review, the
  Changes tab and the transcript, and moves the cursor in lists and on the Changes rail. A click on the crumb row's `≡`, chip or what waits
  acts as [the frame](#the-frame) says, and a click on the sidebar's environment opens the
  picker. A click on a card's keycap or a key of the key bar presses
  that key; the bar's arrows and keys with a modifier take no clicks.
- **Motion.** The result of an action (a start, a stop, a decision, a detach) and the errors
  of background loads show as a toast at the top right for 3 seconds, in the running or the
  failed color, and clear the notice line's "Stopping…". The plain look keeps them on the
  notice line. A new request's card slides in from the top of the reading pane over about
  150 ms. Nothing else moves but the spinner and the booting shimmer.

No key changed between the looks. Only `[`, `]` and `v` in a diff are new.

## Code

The package is `internal/tui`. `cmd/studio/tui.go` is the subcommand.

- `client.go` is the typed client. It adds no endpoints; its results are the API's own Go
  types (`sandboxes.View`, `api.ApprovalView`, `store.Chat`, `chat.Log`, `gateway.Conn`,
  `store.Rule`, `browser.BrowserStatus`, `events.Event`). Changes arrive on `/api/events`; a
  burst of them refetches once, after 300 ms. `ChatStream` reads a chat's SSE stream into
  `ChatEvent`s. Every answer and chat event is cleaned as it is decoded (`sanitize.go`): a
  computer writes much of what the TUI draws (file names, diffs, an agent's answers, its tools'
  output), and none of it may talk to the terminal. Escape sequences are removed, a carriage
  return becomes a new line, other control characters show as their pictures (`␇`, `␡`), and
  invalid UTF-8 as `�`. Bytes, such as raw JSON, stay as they are; a question's choices are
  cleaned where they are drawn.
- `app.go`, `keys.go` and `view.go` are the Bubble Tea v2 model: state, messages, the key
  routing of the full screens, help, and the frame of each screen. The key bar is Bubbles'
  `help`, and the bindings are Bubbles' `key`, so the bar and the handlers come from one list.
- `place.go` is the places: what each one is, `open` and `back` with the history, the focus,
  and the computer a teammate's place shows. `frame.go` is the frame: `layoutFor`, the only
  function that decides the columns, the crumb row, the columns and their splits, the drawer and
  full screen. `route.go` sends Studio's keys to the sidebar, the main column or the computer,
  and answers the sidebar's.
- `inbox.go` is Home's list: its rows and sections, the cursor kept by the row's ID, the keys
  of a row (a push's second `y` among them), what `enter` opens, and the layout of the list and
  the reading pane. `reading.go` is the reading pane. `needs.go` keeps the undo and reads EARLIER
  TODAY.
- `approvals.go` ports the web's card grouping (`web/src/lib/approval-groups.ts`) and the
  decision choices of `web/src/components/approval-decision.tsx`. `kinds.go` decides the other
  kinds (browser, memory, agent permissions) and reads a question's schema into the one field
  the TUI answers. `pager.go` lays out a push's review and decides it, and is the review place.
- `thread.go` is a thread (its card above the composer and its keys) and a teammate's home,
  which `mate.go` reads. `chat.go` is the chat: the stream, the transcript's layout and folds.
  `composer.go` is the composer's box, and `complete.go` its `/` commands and `@` files.
- `computer.go` is the computer pane: its tabs and their keys. `tab_terminal.go` is the
  Terminal tab, with its chips and new shells, `tab_files.go` the Files tab, with its reads, the
  list and the file opened, `changes.go` the Changes tab, with git's reads, the rail and what it
  shows, `comments.go` its line cursor, the comments on a push and the note they make, and a
  comment on a change put into the composer with its mention, `tab_browser.go` the Browser tab, which draws `picture.go`'s reads at the pane's size,
  and `tab_details.go` the Details tab, which draws
  `detail.go`'s facts or `charts.go`'s dashboard. `homepane.go` is the live terminal's state and
  keys and the approval banner, and `pane.go` the terminal itself, an emulator between the
  attachment's websocket and the frame.
- `tree.go` is the sidebar: its sections and rows and the cursor on them. `sessions.go` is the
  threads under each teammate: the environment's two reads, `threadsOf` (the web's listing
  rule), `threadWaiting` (the requests a thread waits on) and their states. `places.go` is what
  an ENVIRONMENT row opens, `computers.go` the Computers screen and `settings.go` the Settings
  screen.
- `buttons.go` is the focus of the arrows over buttons, the confirmations, and the return to a
  place after a decision made on Home. `input.go` is the line editor the composer and the new
  computer's name use, and `linePrompt`, the one-line question of the notice line (a fork's
  name, a checkpoint's name, an answer).
- `activity.go` is the Activity log, its kinds and an entry's rule. `newsandbox.go` is the new
  computer's form, the fork, and the progress of both. `checkpoints.go` is the checkpoints
  screen, and `ports.go` the ports on Details.
- `style.go` holds the styles: the look, the palette of each color profile, and the fitting of
  styled text. `draw.go` has the visual look's pieces: dots, keycaps, card frames, sparklines,
  bars, the spinner and the grouped key bar. `cards.go` is the requests' cards, `live.go` the
  usage, lineage and spinner reads, `motion.go` the toasts and the cards' slide-in, `dash.go`
  the network column, and `picture.go` the browser's page and its picture in half blocks.
  `mouse.go` maps clicks and the wheel to what each row and key of the frame leads to. Each body
  records those rows and keycaps as it draws them, and the frame the key bar's keys and the
  crumb row's parts.
- `terminal.go` is the raw attachment `studio shell` uses: it bridges the websocket and reads
  the keys itself, which is what lets Ctrl-] return without a goroutine left reading the
  terminal. `shell.go` is `studio shell`, with its watch of the approvals.

The background work of the chat and Activity screens carries a generation, so a result, a
stream event or a tick of a screen the user has left is dropped.

Tests:

- Model tests drive `Update` with key and result messages.
- Golden views (`testdata/*.golden`) are the screens at 80×24 with `NO_COLOR`, which is the
  plain look. The `visual_*` goldens hold the visual look's 256-color ANSI at 80×24. The
  `wide_*` goldens hold its text at 100×30, 120×30, 140×40 and 180×50. All of them run with a
  fixed clock, profile and spinner frame. After an intended change,
  `go test ./internal/tui -run Golden -update` rewrites them; review the diff.
- `frame_test.go` checks `layoutFor` at each width, that `tab` and `\` move the cursor, that the
  drawer takes the keys and closes, that `esc` walks the history back to Home, that full screen
  survives a resize, that `m` goes round a teammate's computers, and that `?` lists the keys of
  each place.
- The client and the commands run against an in-process Studio: the API behind the same guards
  and an in-memory catalog, with a fake VM runtime, the chat service and a connection log. The
  test Studio never touches the real one. Where that Studio can't answer (an agent that is
  connected, a runtime that restores), a server that records the calls checks the paths and
  bodies the keys send; so does a prompt from a teammate's home, which creates the thread.
- A pseudo-terminal test runs the TUI as a child process: it shows Home, opens Activity, goes
  back, allows the waiting host with `y` on Home (the server then has its rule) and quits. A
  second one runs at 140×40, where the sidebar is beside Home, and opens Activity by clicking
  its key in the key bar. A third runs `studio shell`'s attachment on a real terminal and checks
  that unbuffered input reaches the computer and that Ctrl-] detaches. They are skipped on
  Windows.
- `tab_terminal_test.go` runs the Terminal tab and a thread that is an agent's terminal against
  a fake attachment, at 80 columns and wide: the chips, the keys it sends, Ctrl-], `alt+digit`,
  `+` attaching a new shell by its name, full screen, and the banner's keys.
- Model tests press the arrows over a card's toggles, the banner, a chat's picks and a
  confirmation, answer the banner with `alt+a` and check the keys go back, and check that a
  decision on Home returns to the terminal it came from. Others check that `y` on a push asks
  once more, that the cursor goes to the next request after a decision and keeps its request
  through a refresh, that `tab` wraps, and that a thread's card answers `y`.
