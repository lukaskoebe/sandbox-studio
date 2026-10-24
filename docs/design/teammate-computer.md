# A teammate and their computer (design)

Status: building (2026-10-24), on branch `h-teammate-computer`. Chosen from the hierarchy mockups
(round 3, mock H, `bin/hierarchy-ui/h-computer.html`; write-up in
`bin/hierarchy-ui/notes/round3-h.md`).

The teammate's page had five tabs (Conversation, Tasks, Memory, Browser, Settings) and a rail, and
each of the teammate's sandboxes had a page of its own with chats, terminals and reviews as tabs,
three side panels and a status bar. A chat had a third page. Three levels for one teammate, and the
sidebar mirrored them as a tree: teammate, sandbox, session.

H makes the sandbox a device instead of a level. A teammate's page has two parts: the
conversation with them on the left, and their computer on the right, drawn as one screen with a
bezel. Chats are threads inside the conversation. Tasks, memory and settings are in a profile
that opens from the name. A plain sandbox is the same screen at full width.

## Two levels

There are two: **teammate (or plain computer) → thread**. Everything else is a state of the same
page, kept in the URL as search params so Back, Forward and links work:

| Param | Values | Default |
|---|---|---|
| `thread` | a chat ID | none: the teammate's home |
| `computer` | a sandbox ID of the teammate | the thread's sandbox, else the one worked in last, else the first |
| `tab` | `terminal`, `browser`, `preview`, `changes` | `terminal` (`browser` while the teammate drives it and nothing else was picked) |
| `term` | a terminal session name | `main` |
| `fold` | `1` | the screen is shown |
| `profile` | `card`, `tasks`, `memory`, `settings` (`open=` a settings card) | closed |
| `review` | an approval ID, on `changes` | the oldest waiting push of the computer |

Esc closes the profile or a menu first, then leaves the thread.

A plain computer's page, `/sandboxes/$id`, takes the screen's params (`tab`, `term`, `review`),
and `from=setup` for the way back to the guided setup. The address of a teammate's computer
redirects to the teammate's page with `computer=`, and the old workspace address,
`/sandboxes/$id/work`, to the screen on the tab its link named.

## Where every piece goes

| Today | In H |
|---|---|
| Teammate header: avatar, name, role · harness · presence, Wake, Readiness, ⋯ | The name and role open the profile; one presence word on the right. Readiness problems show as a line in the profile and above the composer. Wake is the bezel's power button. The teammate ⋯ menu (pause, delete …) moves to the profile's foot. |
| Tabs Conversation / Tasks / Memory / Browser / Settings | Conversation is the page. Browser is a screen tab. Tasks, Memory and Settings are profile sections. `/teammates/$id/{chats,tasks,memory,settings,browser}` redirect to the matching params. |
| The conversation's flat turn list | Home lists the chats as thread cards grouped by day (title, an "on docs-site" chip for another computer, the last answer, the number of turns and the state), with the small events between them: learned facts, tasks, terminal summaries, check-ins. Opening a card opens the thread. |
| Rail: Now, Computer card(s), Earlier chats, Knows, Check-ins | Now is the presence word and the working card. The computer *is* the screen. Earlier chats are the cards. Knows and Check-ins are in the profile. |
| Composer modes Message / Task / Teach, New chat, sandbox picker | One composer. At home it starts a new thread on the computer shown; in a thread it replies (or queues while a turn runs). Teach and New task live in the profile. |
| Waiting for you (approval cards) | A compact row each at the end of home: the kind's tile, what is asked, where and how long ago. Answer opens the card in place; Review opens a push on Changes and a memory conflict in its sheet. Inline in the thread that asked, too. |
| Sandbox page header: owner / name, state pill, Wake, Checkpoint, ⋯ | Bezel: the computer switcher with a status dot (forks indented under their source), the tabs, power, ⋯ and fold. |
| Sandbox ⋯ menu, details pill, Checkpoint button, status bar | The computer menu (⋯): name, template, size and uptime; CPU and memory meters; Checkpoint now; History and checkpoints; Fork; Merge into …; Rebase; Resources; Connections; Export; Another computer; Suspend or Resume; Delete. Ports are a chip on Preview; the ID and generation are in History. |
| Workspace tab strip (chat, terminal and review tabs) | Chats are threads. Terminal sessions sit behind a `main ▾` chip on the Terminal tab (new shell, the teammate's harness in a terminal, close). Reviews are on Changes. |
| Side panels Browser / Connections / Files | Browser is a tab. Files and changes are on Changes, under any waiting push. Connections opens from ⋯ as a side sheet. |
| Previews dropdown | The Preview tab: the framed app with a port chip, Open in a new tab, Open fresh. Hidden while previews are off. |
| Chat page `/chats/$chat` | Redirects a teammate's chat to `?thread=`. Its header actions (open in terminal, delete) move to the thread header's ⋯. |
| Merge banner | Above the display, on the screen of the fork. |
| Sidebar tree: teammate → sandbox → sessions, New chat, All chats | Home; Teammates (avatar, name, one status word or waiting count); Your computers (plain sandboxes with a status dot); footer Settings, version, theme. New chat becomes the composer; All chats stays reachable from Home. |

Iris without a computer gets a dashed screen, "Iris has no computer yet", with *Give Iris a
computer*. A stopped computer shows a dark glass screen with Start (and the checkpoint it keeps).
A computer that is creating shows its progress on the screen.

## Components

- `components/computer/screen.tsx`: the bezel, tabs and display; hosts `terminal.tsx`,
  `browser-window.tsx`, `chat/preview-panel.tsx`, `git-review.tsx` and the changes and files of
  `chat/workspace-panel.tsx`.
- `components/computer/computer-menu.tsx`: from `sandbox-options-menu.tsx`, `sandbox-details.tsx`
  and `sandbox-usage.tsx`.
- `components/computer/terminals.tsx`: the session state and the chip, from `sandbox-workspace.tsx`.
- `components/computer/changes.tsx`: the Changes tab, a waiting push's review over the files.
- `components/teammate/page.tsx`, `home.tsx` (cards, events, waiting, composer), `thread.tsx`,
  `header.tsx` and `profile.tsx` with its `tasks.tsx`, `memory.tsx` and `settings.tsx`.
- `components/column-page.tsx`: the title row and centred column of Home and every settings
  section, without a rule under the header, as here.
- `sandbox-workspace.tsx`, `sandbox-page.tsx`, `sandbox-usage.tsx`, `browser-panel.tsx`,
  `teammate-conversation.tsx` and the teammate subroutes are gone; `teammates/$id/$view`
  redirects the old tabs.

## Risks

- The terminal and the live browser sit in a column about 55 % wide that can fold: both must
  refit on resize (xterm's fit addon runs on a ResizeObserver; the browser view scales).
- Terminals stay mounted while another tab is shown, so switching keeps scrollback and the
  connection; switching computers remounts them.
- e2e specs that walk the old tabs and the sandbox page change with it.

## Phases

Phases 1 to 3 are done.

1. The screen, the computer menu and the teammate page with search params; redirects.
2. The plain sandbox page as a full-width screen; the sidebar.
3. Settings and Home brought to the same look.
4. e2e specs, `docs/frontend.md`, the user docs and the changelog.
