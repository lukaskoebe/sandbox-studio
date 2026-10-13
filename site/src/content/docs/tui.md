---
title: Terminal UI
description: Answer approvals, check sandboxes and open a sandbox's terminal from the terminal, on the Studio machine or over SSH.
---

`studio tui` is a keyboard client of the Studio that is already running on this machine. It
shows the sandboxes of an environment, answers approvals, and opens a sandbox's terminal in
full screen. Everything else stays in the browser, and the TUI prints the address of the
page where you need it.

```
Sandbox Studio · Default                                            ● 1 approval
────────────────────────────────────────────────────────────────────────────────
▸ ● Running   blub             Iris      node-22+python-… up 2h 14m
  ● Running   api-refactor     Finn      go@2             up 12m     1 approval
  ○ Stopped   docs-site        Iris      node@3
  ◐ Booting   demo             —         base















────────────────────────────────────────────────────────────────────────────────
enter open  a approvals  t terminal  s start/stop  d delete  ? help  q quit
```

## Start it

Start Studio as usual, then, in another terminal on the same machine:

```sh
studio tui
```

If Studio isn't running, the TUI says so and shows the command that starts it. It needs an
interactive terminal, and it signs in as you without asking for anything: only your user can
read the data that makes this possible, so run it on the machine Studio runs on. Over SSH, run
it there.

## Keys

The keys are the same on every screen, and they can't be changed. The key bar at the bottom
shows the ones that apply where you are, and `?` lists them all.

| Key | On the sandbox list |
|---|---|
| `↑` `↓` or `j` `k` | move the cursor |
| `enter` | open the sandbox: its facts, and the same keys |
| `t` | open the sandbox's terminal (it must be running) |
| `s` | start, stop or resume the sandbox |
| `d` | delete the sandbox; the TUI asks first |
| `a` | approvals, from every environment |
| `e` | switch environment |
| `/` | filter by name, teammate or template |
| `n`, `c`, `l` | new sandbox, a sandbox's chats, Activity: print the browser address |
| `?` | help |
| `q` | quit |

The header counts the approvals waiting in every environment, and the terminal rings its bell
once when a new one arrives.

## Approvals

Requests from one sandbox that come in close together are one card, as they are on the web.
A card lets you:

- **Allow or deny** a network request. `y` and `n` make a rule for the host pattern and the
  scope you chose; `o` and `O` answer only this connection, with no rule; `1` allows it for
  an hour; `i` ignores it.
- **Choose what the rule covers:** `p` changes the host pattern (for a browser request, also
  its method and path), `s` the scope (the environment, your teammate's sandboxes, or this
  sandbox), and `x` any port instead of the default ports.
- **Decide a group:** `A` allows every host, `D` denies every host, and `r` reviews them one by
  one.

Git pushes, browser actions, memory and chat permissions are decided in the browser. On their
card, `enter` prints the address of the page to open.

## Terminal

`t` opens the sandbox's main terminal, the one the browser opens first. The sandbox's screen
takes over your terminal, and your keys go to it, as in any other terminal. Resizing your
window resizes the sandbox's. Press `Ctrl-]` to go back to the TUI. If the sandbox ends the
terminal, you see that, and the next key returns you to the TUI.

## Stays in the browser

Settings, providers, teammates, templates, rules, the network page, memory, the browser,
secrets, the git page with its diffs, and the form for a new sandbox are in the browser. The
TUI prints their addresses; `?` lists them. Chats and Activity are in the browser for now.
