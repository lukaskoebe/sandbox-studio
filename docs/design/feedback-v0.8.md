# Feedback from the first days of v0.8.0

What users reported after v0.8.0 that needs more than a fix. The fixes themselves are in the
changelog's Unreleased section. Each item says what happens today, what to build and roughly
how big it is (S: a day, M: a few days, L: a week or more). The UI items have a mockup in the
"Studio feedback mockups" artifact, under the letter named here.

Two items have design docs of their own:
- [pull request reviews by a teammate](forge-reviews.md);
- [memory that links](memory-links.md).

## Teammate status shows terminal work

**Mockup A.** A teammate is *working* while a chat turn runs, *waiting* while an approval
is pending, and otherwise *idle* (`web/src/hooks/use-teammate-presence.ts`). Terminals don't
count. So Ada shows Idle while opencode works in one of her terminals, or while `make test`
runs there.

The guest lists terminal sessions with tmux's `list-sessions`. Each session reports its name,
the agent harness Studio started in it, and when it last printed.

**Built** on `feedback-v0.8`:

- The guest adds `#{pane_current_command}`, `#{pane_pid}` and `#{pane_current_path}` to the
  session format, and reads the foreground command line and its start from `/proc` (see the
  next item). It also sends its own clock with the answer, and the host shifts the times by
  the difference, so a guest whose clock is off doesn't look idle or working when it isn't.
- The host labels each session and says what it does (`internal/sandboxes/terminals.go`):
  *working* while a harness runs and printed in the last 20 s, *busy* while another program
  than the shell runs, *idle* otherwise. A harness is `claude`, `codex`, `opencode`,
  `cursor-agent`, `gemini` or `aider` by program name, the script an interpreter such as
  `node` runs, or the session's harness mark.
- `GET /api/environments/{env}/terminals` lists the terminals of the running sandboxes. While
  a UI tab has the event stream open, the host asks every guest every 10 s, gives each 5 s to
  answer, and publishes a `terminals` event when a label, program, state or directory
  changes. The list answers from what it saw in the last 15 s.
- Presence, below *waiting* and *working* in priority:
  - **Working in a terminal:** the same green dot as a chat; the header says "Working ·
    opencode in a terminal", or "opencode in api" when the session has a name of its own, and
    links to that session.
  - **Busy:** a quieter ring, not a dot; the header says "Idle · npm run dev running" and the
    sidebar row names the program. A server that runs for hours doesn't look like work.
- The terminal's session tabs show the same marks on each session (see below).

## Terminal sessions as tabs

**Mockup G.** Terminal sessions sit behind a small pill in the terminal's top right corner
(`web/src/components/computer/terminals.tsx`), which opens a menu. Users don't find it, and
nothing on the Terminal tab says there is more than one session.

- A strip of session tabs under the screen's bezel, while the Terminal tab is shown. Each tab
  has an icon (a terminal, or the robot for a harness), its label, a mark from the status
  above, and × on hover. The last one is **+** for a new shell.
- The strip is 24px high with 11.5px type, so it takes one terminal line. The keyboard hints
  live in the tooltips of **+** and of each tab, not in the strip. The bezel stays as it is.
- The Terminal tab itself counts sessions when there is more than one: "Terminal 4".
- Labels. A new shell is named `2`, `3` and so on today (`fresh()` in `terminals.tsx`), which
  says nothing. The tab shows, in this order:
  1. the name the user gave it;
  2. the command in the foreground, cut at 24 characters: `npm run dev`, `vim coupon.tsx`;
  3. the base name of the shell's directory: `webshop`, `api`.

  A harness session shows the harness's name. When two labels match, the later one gets " 2".
  The tooltip has the whole command, its directory and when it started. The tmux name stays
  the key in the URL's `term=` and the API, so a label can change while a program runs without
  breaking links.
- The guest adds `#{pane_current_path}` and `#{@studio_title}` to the session format, and
  reads the foreground command line from `/proc`: the pane's shell, its terminal's foreground
  process group (`tpgid` in `/proc/<pid>/stat`), then that process's `cmdline`. The status
  above needs only the program's name from `#{pane_current_command}`; a label needs its
  arguments too.
- Double-click renames a session: it sets the `@studio_title` option, not the tmux name.
- Keyboard: Alt+Shift+T opens a shell, Alt+Shift+[ and Alt+Shift+] switch sessions while
  the terminal has focus, and Alt+Shift+W closes one, after a confirmation if a program other
  than the shell runs. The mockup had Ctrl+Shift+T, Ctrl+Shift+W and Ctrl+PgUp/PgDn, but
  Chrome keeps those for its own tabs and a page never sees them outside an installed app.
- On a phone the strip scrolls sideways.

**Built** on `feedback-v0.8`, with the status item. The rename is `PUT
/api/environments/{env}/sandboxes/{id}/terminals/{name}/title`, at most 40 characters; an empty
title goes back to the label Studio picks.

## One memory panel

**Mockup H.** The memory sheet (`web/src/components/memory-tabs.tsx`) has five tabs, and
each one looks different:

| Tab | What it is today |
|---|---|
| Pages | a list beside raw compiled truth in a monospace block |
| Facts | a table with six columns and badges in four styles |
| Conflicts | a run log, then cards with a colored rule |
| Search | cards with a four-digit score |
| Sessions | a list of its own |

Above the tabs there are two lines of numbers, and Export and Import on a row of their own.
Under each tab a header repeats the tab's name. Users find it hard to read.

The proposal is one layout and one row:

- **One header line:** "3 facts · 2 pages · core 1 200 / 4 000", then **Dream now** and a ⋯
  menu with Export, Import, the day's extraction cost and "How memory works".
- **Search on top, always.** It filters what the panel shows instead of being a tab of its
  own. The score and its explanation move into a hover card.
- **Sections in one list:** Needs you (conflicts, suggestions and, later, lint findings),
  Core, Pages, Facts, Sessions. The first two hide when empty.
- **One memory row** everywhere: the text in the body font at body size, and one quiet line
  under it with the kind, where it came from and when. A status shows only when it isn't
  *active*. Actions sit in a ⋯ that appears on hover, with Edit first.
- **Pages render as markdown**, with `[[links]]` clickable, **Linked from** under each page
  ([memory that links](memory-links.md)), and the timeline as a quiet list.

M. It reuses the queries the panel has; it is mostly layout.

**Built** on `feedback-v0.8` (`web/src/components/memory-panel.tsx`, replacing
`memory-tabs.tsx` and `memory-suggestions.tsx`), with mockup I for a page. Changes from the
mockup: the search box filters as you type and runs the ranked search on Enter, with the
`why` as the hit's meta line rather than a hover card; lint findings have a section of their
own, **Loose ends**, under Needs you, three at first, since an older memory can have many;
retracted and replaced facts hide behind a toggle; a page also lists the facts it records.
The ⋯ menus are always visible, since a hover-only menu can't be reached on a phone.

## Long chats load in pages

**Mockup B.** A chat loads its whole log (`getChat`), and the thread renders every item.
Long chats are slow to open and heavy to scroll.

- `GET …/chats/{chat}?last=40` answers with every turn's header (its prompt and outcome,
  which are small) and the items of the last 40 turns. `?before=<seq>` pages back. The SSE
  stream stays as it is (`after=rev`).
- The thread shows a **Load earlier** row above the first loaded turn. It also loads when the
  row scrolls into view. `preserveScrollOnPrepend` on the message scroller keeps the reading
  position.
- The turn list (the Changes tab's turn picker) uses the headers, so it lists every turn
  without loading them.

**Built** on `feedback-v0.8`. A page also stops at 500 items, unless its last turn alone has
more, since one agent turn can have hundreds of tool calls. Each turn's header carries the
start of its message (`prompt`), which names it in the turn picker and the footers. The row
sits above the scroller's content rather than in it: the scroller takes every child of its
content for a message and keeps the reading place only when the first one moves down. It
loads by itself once the reader scrolls, not while the thread opens at its end.

## Copy-on-write forks

**No mockup.** A fork is not copy-on-write today. `Manager.fork`
(`internal/sandboxes/transfer.go`) creates a new VM from the template's image, or the current
default image, and streams `/workspace` and `/home/agent` across as a gzip tar. The root disk
isn't copied: apt packages, `/etc` changes and tools from `mise use -g` (in `/opt/mise`) are
missing in the fork, and the Docker disk starts empty. The fork dialog and the docs now say
so.

msb 0.7.8 has `Sandbox.Fork`. Its child shares the parent's disks through qcow2 backing files,
or copies them with a reflink where the file system has one. The child can't be given Studio's
proxy login, DNS or vsock channel, though (spike S5, microsandbox #1736), which is the same
gap that blocks checkpoint restore.

- **S, now:** zstd instead of gzip, and the workspace and home copied at once. A large
  workspace forks in roughly half the time.

  **Built** on `feedback-v0.8` ([rebase](../rebase.md#workspace-and-home-copy)). On a 24 MB
  package, zstd exported 1.7 times as fast as gzip and unpacked 2.5 times as fast; the home
  no longer waits for the workspace. Export files stay gzip.
- **M, once #1736 ships:** fork through `Snapshot.Create` and a restore with the child's own
  egress settings, keeping `checkEgress` (`internal/runtime/checkpoints.go`). It copies the
  root disk and Docker disk too, at copy-on-write cost.
- Not recommended: Studio managing qcow2 images itself, beside msb.

## CPU and memory at runtime

**Mockup D.** Resizing exists: `PUT …/resources` → `Manager.Resize` asks msb for a dry
run, applies what can change live and saves the rest for the next start. It never restarts
the sandbox. In practice everything waits for the next start. msb 0.7.7 changed nothing
live, and Studio boots VMs with no headroom: the memory ceiling is the memory size, and the
CPU maximum is the CPU count.

msb 0.7.8 applies a change live when the VM reports that it can resize CPUs or memory and the
new value fits under the ceiling. It uses virtio-mem for memory, and there is no balloon.

- **S:** boot with headroom: a CPU maximum of the host's cores (at most 16) and a memory
  ceiling of twice the size, capped by the host's memory. Then verify a live change on msb
  0.7.8, and update [sandboxes](../sandboxes.md#resources), which still describes 0.7.7.
- **S, UI:** the Resources dialog says, per resource, what applies now and what waits for the
  next start, and offers **Save and restart** beside Save. The sandbox's details then show
  "8 GiB · 12 GiB after restart". The meters keep dividing by what the VM has now.

## Base images go out of date

**Mockup C.** Each sandbox keeps the base image it was created from. Only `/opt/studio` (the
guest agent and the tools) updates itself. The harness and ACP versions pinned in the image,
Debian's security updates, the home disk (exporter v2) and the network rules' guest side
arrive only in new sandboxes. Errors such as "image predates chats"
(`internal/guest/acp.go`) and "created before network rules existed" are the visible part.

Rebase moves a sandbox onto another template's image, keeping `/workspace` and the home.
Today it is in the computer menu only. Its API requires a template, so a sandbox without one
can't move to the current default image. Nothing tells the user a sandbox is behind.

- **S:** record the base image's digest on each sandbox and on each template build. The view
  gets `baseOutdated` when it differs from the release's `version.BaseImage` (or the
  template's latest build).
- **M:** rebase onto the default image (an empty template ID), and a template's **Rebuild on
  the current base**, after which its sandboxes are offered a rebase.
- **M, notices:**
  - a dot on the computer menu and a line in it: "Built on an older base. Rebase…";
  - one line on Home after an update that changed the base: "3 sandboxes use the old base";
  - release notes that say which features need a new base.
- **S:** the "predates" errors name Rebase. The rebase dialog lists what is lost: Docker data,
  software outside `/workspace` and the home, and running terminals.

## Force pushes through review

**Mockup F.** The mirror (`internal/gitreview/receive.go`) takes only branches. It refuses
deletes, and a push whose old value isn't the forge's tip ("stale info"). A new branch takes
any history; a fast-forward is queued for review; a branch still waiting for review can be
replaced if the new push still builds on the forge's tip. Rewriting a branch that is already
on the forge is refused. The agent's memory fact and its first guess, that such a push would
be queued, were wrong. The managed instructions now say the rule and the refusal says what to
do instead.

What users want is the rebase-and-force-push of a feature branch:

- A non-fast-forward push of a branch other than the default branch is queued as a `git.push`
  marked *forced*.
- Its review lists the commits it drops, alongside the ones it adds. Approving it takes an
  explicit **Force-push** button, not the usual Approve.
- The default branch stays fast-forward only.
- On approval Studio pushes with a lease on the tip it showed, so a forge that moved meanwhile
  refuses it, as `pushUpstream` already does for normal pushes.

M.

## zsh

**No mockup.** bash is the login shell (`images/base/Dockerfile`), and the guest starts
terminals with `SHELL=/bin/bash`. zsh would miss three things:

- the profile scripts (`studio-mise.sh`, `studio.sh`, `sandbox-studio-env.sh`), because
  Debian's zsh doesn't read `/etc/profile`;
- mise's activation, which `studio-mise.sh` does only for bash;
- bash-completion.

- **S:** zsh in the image; `/etc/zsh/zprofile` with `emulate sh -c '. /etc/profile'`;
  `mise activate zsh` in `/etc/zsh/zshrc`; a `~/.zshrc` skeleton.
- **M:** a "Terminal shell" setting (bash or zsh) in Settings → General, passed in the guest's
  config: tmux's `default-shell`, with bash as the fallback. Agent harness sessions stay bash.
- **Today, by hand:** `sudo apt install zsh`, `set -g default-shell /usr/bin/zsh` in
  `~/.tmux.conf`, and the zprofile and zshrc lines above.

## A native app with Wails 3

**No mockup.** Studio runs as a local server, and the browser is its window. Wails v3.0.0
came out on 2026-10-26. It needs Go 1.26, which Studio already uses, and on Linux GTK4 with
WebKitGTK 6.0 (GTK3 with `-tags gtk3` until v3.1). It has a tray, native notifications with
actions and single-instance locking.

- **Spike first (M):** a Wails window that loads `http://127.0.0.1:<port>` from the
  running Studio, rather than embedding the UI through Wails' asset server. The UI stays the
  same, and cookies, the SSE stream and `*.localhost` previews behave as in a browser. Check
  that WebKitGTK and WebView2 resolve `*.localhost`, open pop-ups (OAuth, previews) in the
  system browser, and keep the login cookie across restarts.
- **Then (L):**
  - the tray replaces `studiow.exe` on Windows and the autostart entries elsewhere;
  - one binary with `studio` (headless, unchanged) and `studio app`;
  - native notifications instead of the browser's;
  - signing and notarization, which the release workflow has never run.
- The browser stays supported, and the server stays the product. The app is a window and a
  tray around it.

## The updater

**No mockup.** After installing an update, a Studio started in a terminal stopped. That
has two possible causes, and both are fixed:

- **Mistaken for a service.** Studio took itself for a systemd service, because the terminal
  had inherited `INVOCATION_ID` from the service that started it (gnome-terminal-server, a
  UWSM session). It exited for systemd to restart it, and nothing did. A service is now
  recognized by `SYSTEMD_EXEC_PID` too. Confirmed on the NixOS machine: the install record of
  its update to v0.7.0 says `"supervised": true`, and a shell there has `INVOCATION_ID` set.
- **A silent restart.** Studio restarted itself detached and gave the terminal its prompt
  back, which looks the same. It now logs where it went and that `studio quit` stops it.

Then, on branch `feedback-v0.8`:

- On Linux and macOS, Studio becomes the new binary in place (`updates.Restart`). It keeps the
  PID, the terminal and Ctrl-C, and rolls itself back if it can't run, stops before serving
  or doesn't serve in two minutes.
- A binary under `/nix/store` says to update through Nix (`cannotInstall`).
- A new binary that doesn't run with ENOENT on NixOS names `programs.nix-ld`, as
  `internal/memory/llama.go` does for llama.cpp.

## Smaller

- `TestLlamaEmbeds` crashed on the Windows runner again, in v0.8.0's dry run (PLAN.md has the
  first time, in v0.3.5).
