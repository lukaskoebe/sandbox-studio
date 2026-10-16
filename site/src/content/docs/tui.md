---
title: Terminal UI
description: Create and check sandboxes, answer approvals, review git pushes, chat with a teammate and open a sandbox's terminal, from the terminal on the Studio machine or over SSH.
---

`studio tui` is a keyboard client of the Studio that is already running on this machine. It
shows the sandboxes of an environment and creates new ones, answers approvals and reviews git
pushes, chats with a sandbox's teammate, follows the network Activity, and opens a sandbox's
terminal in full screen. Everything else stays in the browser, and the TUI prints the address
of the page where you need it.

```
Sandbox Studio · Default                                            ● 1 approval
────────────────────────────────────────────────────────────────────────────────
▸ ● Running   blub             Iris      node-22+python-… up 2h 14m
  ● Running   api-refactor     Finn      go@2             up 12m     1 approval
  ○ Stopped   docs-site        Iris      node@3
  ◐ Booting   demo             —         base















────────────────────────────────────────────────────────────────────────────────
enter open  n new  s start/stop  a approvals  t terminal  c chat  ? help  q quit
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
| `n` | create a new sandbox |
| `t` | open the sandbox's terminal (it must be running) |
| `s` | start, stop or resume the sandbox |
| `d` | delete the sandbox; the TUI asks first |
| `c` | the sandbox's chats with its teammate |
| `a` | approvals, from every environment |
| `l` | Activity: connections and decisions |
| `e` | switch environment |
| `/` | filter by name, teammate or template |
| `?` | help |
| `q` | quit |

`esc` or `q` goes back from every other screen. The header counts the approvals waiting in
every environment, and the terminal rings its bell once when a new one arrives.

## New sandbox

`n` asks whose sandbox it is (a teammate, or none), what it starts from (the base image or one
of the environment's templates), and its name, which is also its hostname: lowercase letters,
digits and dashes. `enter` goes on and `esc` goes back a step. Studio then creates the sandbox,
and the TUI shows the steps as they happen, the image pull with its progress, as the browser
does. Once the sandbox is ready it opens. If the create fails, you see the step and the reason,
and `d` deletes the sandbox so you can try again. `esc` leaves the create to finish on its own.

## Approvals

Requests from one sandbox that come in close together are one card, as they are on the web.
A card lets you:

- **Allow or deny** a network request. `y` and `n` make a rule for the host pattern and the
  scope you chose; `o` and `O` answer only this connection, with no rule; `1` allows it for
  an hour; `i` ignores it. When the browser opens a new site, `w` allows the site for the
  rest of that browser session, with no rule.
- **Choose what the rule covers:** `p` changes the host pattern (for a browser request, also
  its method and path), `s` the scope (the environment, your teammate's sandboxes, or this
  sandbox), and `x` any port instead of the default ports.
- **Decide a group:** `A` allows every host, `D` denies every host, and `r` reviews them one by
  one.
- **Review a git push:** `enter` opens the push in a pager: the repository, branch and change,
  the commits, and the diff with added lines in green and removed ones in red. Scroll with the
  arrows, `pgup`/`pgdn` (or `b`/`space`), `g` and `G`. Then `y` approves and pushes it, `n`
  rejects it, and `i` ignores it. A pull request shows its title and description, and `y`
  opens it.

Browser actions, memory and chat permissions are decided in the browser. On their card,
`enter` prints the address of the page to open.

## Chat

`c` lists the chats of the sandbox's teammate in that sandbox, newest first; `enter` opens one
and `n` starts a new one. A sandbox needs a teammate to chat with.

```
Sandbox Studio · Default                                            ● 1 approval
────────────────────────────────────────────────────────────────────────────────
Fix the login form · blub                                                  Ready
› The login form doesn't submit on enter. Can you fix it?

  ▸ Thought it through
  ▸ Read src/login.tsx                                                      done
  ▸ Edit src/login.tsx                                                      done

Fixed

The form used a `div` as its button, so enter did nothing. It is a real submit
button now:

    <button type="submit">Sign in</button>

- Enter submits the form.
- The button is focusable.



────────────────────────────────────────────────────────────────────────────────
p prompt  ↑/↓ tools  pgup scroll  n new chat  esc/q back  ? help
```

The transcript streams as the agent works. Thoughts and tool calls are folded to one line:
`↑` and `↓` move between them, and `enter` expands one to show its files, its diff or its
output. `p` writes a prompt at the bottom: `enter` sends it, `ctrl+j` starts a new line, and
`esc` puts it aside. `x` stops the turn that runs. Headings are bold and code is indented;
there is no other formatting. When the agent asks for a permission or a question, the
transcript says so, and you answer it in the browser.

## Activity

`l` shows the network log of the environment, newest first: each connection a sandbox made,
with its verdict, and each decision on a request, with how it was made. On a sandbox, `l`
shows only that sandbox; elsewhere `/` filters by sandbox name. `enter` opens an entry with the
rule that decided it: its host pattern, action, ports, which sandboxes it covers, where it came
from, its hits and when it expires. Without a rule, it says what decided instead, such as the
curated list or a once decision. The log refreshes every two seconds. Rules are edited in the
browser; `r` prints the address.

## Terminal

`t` opens the sandbox's main terminal, the one the browser opens first. The sandbox's screen
takes over your terminal, and your keys go to it, as in any other terminal. Resizing your
window resizes the sandbox's. Press `Ctrl-]` to go back to the TUI. If the sandbox ends the
terminal, you see that, and the next key returns you to the TUI.

## Stays in the browser

Settings, providers, teammates, templates, the rules editor, the network page, memory, the
browser, secrets and the git settings are in the browser, and so are answers to chat
permissions and questions. The TUI prints their addresses where it has a key for them, and `?`
shows where the browser is.
