# Sessions on Home (design)

Status: built (2026-10-18); [tui.md](../tui.md#sessions) describes it as it is. Where the
build differs from the text:

- A click selects a session's row and a double click enters it, as on every other row, so a
  click can still show a sandbox's detail pane without taking over the screen.
- Blocked sessions read "blocked", without the reason, so their row fits the wide layout's
  list. The approvals say why.
- The toast is for done only. A new approval already rings the bell.
- Sandbox rows don't repeat their sessions' most urgent state, since the sessions are listed
  right under them. The header counts working and blocked sessions.
- `studio shell <sandbox> [terminal]` attaches a terminal by its name from a shell.
- From 100 columns a session opens in a pane beside the list instead of taking the screen,
  and the terminal preview of section 6 is that pane, live
  ([tui-home-panes.md](tui-home-panes.md)).

The TUI should put the agents one key away. Today they sit several screens deep, and an agent
that runs in a terminal can't be reached from the TUI at all. This concept flattens Home into
one list of sandboxes and everything that can be entered in them. It takes what fits from
herdr, a terminal multiplexer built for coding agents.

## 1. Today

What reaching an agent takes, after moving to the sandbox:

| To | Keys | Then |
|---|---|---|
| a chat | `c`, move to the chat, `enter`, `p` | `esc` closes the composer, `esc` returns to Chats, `esc` returns to Home |
| an agent in a terminal (one the web started, such as `claude`) | `t` attaches tmux `main`, then tmux's `ctrl-b s`, a move, `enter` | it depends on tmux's keys, which the TUI never mentions |
| a second chat in another sandbox | `esc esc esc`, a move, `c`, a move, `enter` | |

Nothing says which agent works, which one waits for you, or which one finished, except the
approvals count in the header. The detail pane lists a sandbox's terminals (`TERMINALS main ·
claude · 2`), but you can't enter them.

## 2. What herdr does

herdr runs in the terminal you already have and keeps sessions alive after you detach
([bitdoze review](https://www.bitdoze.com/herdr-agent-multiplexer/),
[linuxlinks](https://www.linuxlinks.com/herdr-terminal-based-agent-multiplexer/),
[tecmint](https://tecmint.com/herdr-run-ai-coding-agents-in-linux-terminal)). How it's built:

- **Its model.** Workspaces (one per project) hold tabs, tabs hold panes, and panes are
  real terminals. It detects an agent from the process in a pane.
- **The sidebar.** It lists every agent with its state: working, blocked, done, idle or
  unknown. A tab and a workspace show their most urgent state. A done agent stays marked
  until you look at it ([agents](https://herdr.dev/docs/agents/)).
- **Blocked is strict.** It means an approval, a question or a permission prompt on screen.
  Detection reads the bottom of the screen, or uses a harness's own hooks.
- **Keys.** It's mouse-first: a click on an agent in the sidebar switches to it. A prefix key
  (`ctrl+b`) leads to splits, tabs, detach (`q`) and the bindings (`?`). Number keys `1..9`
  jump to an agent, a tab or a workspace ([configuration](https://herdr.dev/docs/configuration/)).
- **Toasts.** It shows one when an agent changes state, except in the tab on show.

What Studio takes:

- one list of everything you can enter, with each agent's state;
- jumping straight in with a number or a click;
- one key to leave, which lands back where you were;
- done stays marked until seen;
- a sandbox shows the most urgent state of its sessions.

What Studio leaves out:

- **Panes and tabs inside the TUI.** Showing terminals side by side would need a terminal
  emulator in the TUI. tmux in the sandbox already keeps the sessions. An attached terminal
  keeps the whole screen, as now.
- **A prefix key.** The attached terminal is passed through raw, so one detach key (`Ctrl-]`)
  is all it reserves. Inside it, tmux's `ctrl-b` stays tmux's.
- **Keys you can change.** The TUI's keys stay fixed.

## 3. The new Home

Under each sandbox, Home lists its sessions, indented. A session is anything you can enter:

- **agent terminals:** tmux sessions with a harness (`@studio_harness`), such as `claude`
  or `codex-2`;
- **chats:** the newest three of the sandbox's teammate in that sandbox, then a `+ 4 older`
  row that shows the rest. A chat continued in the terminal is listed once, as its terminal;
- **plain terminals:** `main` and any other tmux session.

```
Sandbox Studio · Default                     2 working · 1 blocked · ● 3 approvals
────────────────────────────────────────────────────────────────────────────────
  ● Running   blub             Iris      node-22+python-… up 2h 14m      ✓ done
 1   ✓ claude                  agent · done 2m ago
 2   ○ Fix the login form      chat · 3m ago
 3   ○ main                    terminal
▸ ● Running   api-refactor     Finn      go@2             up 12m     ▲ blocked
 4   ▲ Port the parser to go   chat · waits on a permission
 5   ● codex                   agent · working
 6   ○ main                    terminal
  ○ Stopped   docs-site        Iris      node@3
 7   ○ Update the deploy notes chat · 1d ago
     + 2 older
────────────────────────────────────────────────────────────────────────────────
1-9 enter  enter open  t terminal  c chat  tab next waiting  a approvals  ? help
```

The cursor moves over every row. The first nine sessions get the numbers `1` to `9`, top to
bottom, shown in the gutter. A stopped sandbox has no terminals, but its chats are listed and
can be read.

**States**, the same names as herdr's:

| State | Chat | Agent terminal |
|---|---|---|
| ● working | a turn runs | the terminal printed in the last 3 seconds |
| ▲ blocked | a `chat.permission` approval for it waits (its subject is `chat/<chat>/…`) | an approval of another kind waits in its sandbox. Approvals don't name the process, so this is the sandbox's best guess |
| ✓ done | it was working, and you haven't entered it since | the same |
| ○ idle | none of the above | none of the above |
| · unknown | | the guest predates the activity field |

Plain terminals show no state. A sandbox row shows its most urgent session state, blocked
before done before working, after its own status. The header counts working and blocked
sessions across the environment, before the approvals. "Done" lives in the TUI's memory, so a
new `studio tui` starts with none.

In the visual look the states take the palette (working in the accent with the spinner,
blocked in warning, done in success). A session that turns blocked or done while you're
elsewhere brings a toast, unless it is the one on show.

## 4. Entering and leaving

| On | `enter` or its number | |
|---|---|---|
| a terminal row | attaches that tmux session, whatever its name | `Ctrl-]` returns to Home, with the cursor on the same row |
| a chat row | opens the chat; an idle chat opens with the composer ready | `esc` closes the composer, a second `esc` returns to Home |
| `+ n older` | shows the sandbox's older chats in place | |
| a sandbox row | the sandbox's detail, as now | `esc` |

Switching sessions takes two keys: `Ctrl-]` (or `esc` in a chat), then the other session's
number. `tab` moves the cursor to the next session that waits for you, blocked first, then
done. A click on a session row enters it.

The keys on a sandbox row stay what they were, with three changes:

- `c` enters the sandbox's newest chat, or opens the composer for a new one when it has none.
- `C` always starts a new chat.
- `T` starts the teammate's harness in a new terminal (`POST .../sessions`, the web's "Start
  agent") and attaches it.

The Chats screen goes. Its list is now part of Home, and `n` inside a chat still starts a new
one. Number keys on Home don't clash with anything: Home had no digit keys.

## 5. Data

- **Terminals.** Home reads `GET .../sandboxes/{id}/terminals` for every running sandbox,
  every 3 seconds while it's on show, in place of the selected one only. The guest adds
  `#{window_activity}` to its `list-sessions` format. That's the time of the session window's
  latest output, which tmux updates whenever a pane prints. It reaches the API as
  `activity` (Unix seconds). It's left out for guests that don't send it, which makes the
  state unknown. The guest binary is mounted from Studio, so a sandbox picks the field up
  on its next start.
- **Chats.** Home reads `GET .../sandboxes/{id}/chats` for every sandbox with a teammate, once
  on load and again on each `chats` event. It no longer reads them only when `c` is pressed.
- **Approvals.** These are already loaded for the header. Blocked comes from them.

## 6. Later

- **A status line while attached.** The bottom row would show the session, the others that
  wait, the approvals and `Ctrl-] back`. The sandbox would get one row less, and the line
  would be redrawn after each chunk of output. While Bubble Tea runs the attachment, it
  doesn't run Update, so the attachment would need its own feed of states.
- **Blocked read from the screen**, as herdr does, for each harness's permission and question
  prompts. Each harness's wording has to be checked in a VM first.
- **A preview of a terminal** in the detail pane, from `tmux capture-pane`.
