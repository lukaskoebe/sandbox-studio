---
title: Command line
description: Drive sandboxes, ask a teammate and answer approvals from a shell or a script.
---

`studio <command>` talks to the Studio that is already running on this machine. It covers the
daily loop for scripts and quick work: list and start sandboxes, ask a teammate, answer
approvals, save checkpoints. For a keyboard UI, use the [terminal UI](../tui/); for everything
else, the browser.

```sh
studio ls
NAME          STATUS   TEAMMATE  TEMPLATE  UPTIME
api           running  Finn      go@2      2h 14m
docs-site     stopped  Iris      node@3    –

studio ask api "Why does the login test fail on CI?"
chat q3k9w1m2a7b4c
› Run pnpm exec playwright test e2e/login.spec.ts
The session cookie is set after the first navigation in fixtures.ts, so a fast page…
```

## Commands

| Command | Does |
|---|---|
| `studio ls` | Lists the sandboxes. |
| `studio show <sandbox>` | Shows one sandbox: status, teammate, template, resources. |
| `studio start <sandbox>` | Starts a sandbox, or resumes it when it is suspended. |
| `studio stop <sandbox>` | Stops a sandbox. Its disks stay. |
| `studio suspend <sandbox>`, `studio resume <sandbox>` | Suspends a sandbox with its memory, and resumes it where it was. |
| `studio create <name>` | Creates a sandbox and waits until it runs. `--teammate Finn` gives it an owner, `--template node@3` starts from a template, `--cpus` and `--memory` (in GiB) size it, and `--no-wait` returns once Studio has accepted it. |
| `studio fork <sandbox> <name>` | Forks a sandbox into a new one with a copy of its disks, and waits until it runs, unless `--no-wait`. |
| `studio rm <sandbox>` | Deletes a sandbox and its disks. Asks for its name first. |
| `studio shell <sandbox> [terminal]` | Opens the sandbox's main terminal in yours, or the one you name, such as an agent's (`claude`). `Ctrl-]` detaches; the terminal keeps running. When an approval of the sandbox arrives, the title says so and the bell rings, and `Ctrl-]` asks about it instead: `o` allows it once, `y` allows it, `n` denies it, and `enter` returns to the terminal. |
| `studio ask <sandbox> <prompt>` | Asks the sandbox's teammate and prints the answer as it comes. |
| `studio approvals` | Lists what is waiting for you, in every environment. |
| `studio approve <id>`, `studio deny <id>` | Decides an approval. |
| `studio checkpoints <sandbox>` | Lists a sandbox's checkpoints. |
| `studio checkpoint <sandbox> [name]` | Saves a checkpoint. Without a name, it is named after the time. |
| `studio restore <sandbox> <checkpoint>` | Puts a sandbox back to a checkpoint. Asks first. Studio refuses it for now, because the current runtime cannot restore checkpoints. |
| `studio envs` | Lists the environments. |

`studio help` (or `studio -h`, `studio --help`) lists every command, Studio's own included,
and its flags. `studio help <command>` or `studio <command> -h` shows a command's arguments
and flags. Sandboxes are named by name or ID.

## Studio's own commands

Unlike the commands above, these work without a running Studio, except `login-url`, `tui` and `quit`.

| Command | Does |
|---|---|
| `studio` | Starts Studio, prints a one-time login link and, in a terminal, opens it in the browser; `-no-browser` leaves it printed. If Studio already runs with the same data directory, it opens that one instead and exits. |
| `studio doctor` | Checks that this machine can run Studio and says how to fix what can't. Exits 1 if a check fails. |
| `studio login-url` | Prints a fresh one-time login link for the running Studio. |
| `studio tui` | Opens the [terminal UI](../tui/). |
| `studio quit` | Stops the running Studio, as Ctrl-C in its window does, and waits until it has. Sandboxes keep running. |
| `studio autostart enable`, `disable`, `status` | Starts Studio when you log in, or stops doing so. It takes effect at the next login. |
| `studio restore-msb-db [-replace] [file]` | Rebuilds microsandbox's database from one Studio moved aside as damaged. Quit Studio first. See [Troubleshooting](../troubleshooting/#a-sandbox-says-microsandbox-lost-its-vm). |
| `studio version` | Prints Studio's version and its base image. |

## Asking a teammate

`studio ask` starts a chat with the teammate that owns the sandbox, the same chat you would
start in the browser: it shows up there with every tool call. The answer goes to standard
output as it streams in; the tool calls and anything the teammate waits for go to standard
error. So this keeps only the answer:

```sh
studio ask api "Summarize what changed on this branch" > summary.md
git diff | studio ask api -          # the prompt from standard input
studio ask api --chat q3k9w1m2a7b4c "And the tests?"   # continue the chat
```

When the teammate needs your approval mid-answer, `studio ask` names it with its ID; decide it
with `studio approve <id>` in another terminal, or in the browser. Ctrl-C stops following the
answer, not the teammate.

## Approvals

```sh
studio approvals
ID             KIND     FROM  REQUEST                 AGE
k2j4h6g8f0d1s  network  api   registry.npmjs.org:443  12s

studio approve k2j4h6g8f0d1s            # a rule, with the scope the teammate's cards preselect
studio approve k2j4h6g8f0d1s --once     # this connection only
studio deny k2j4h6g8f0d1s --scope environment
studio approve k2j4h6g8f0d1s --sandboxes  # a browser request: the sandboxes may connect too
```

A browser request's rule covers only the browsers, as on the web. `--sandboxes` makes it
cover the sandboxes' own connections too.

## Environments

Commands work in the first environment, as the browser opens. Pick another with
`--env <name>`, or for a whole shell with `export STUDIO_ENV=<name>`.

## Scripts

Every command takes `--json` and prints Studio's own objects. The exit status is 0 when the
command did what it says, 1 when it failed (an `ask` whose turn ended in an error included),
2 for a wrong command line, and 130 after Ctrl-C.

`studio rm` and `studio restore` ask for the sandbox's name in a terminal. In a script they
refuse unless you add `--yes`.

## Completion

```sh
source <(studio completion bash)     # in ~/.bashrc
source <(studio completion zsh)      # in ~/.zshrc
studio completion fish | source      # in ~/.config/fish/config.fish
```

Completion offers commands, flags, sandbox names, environments and approval IDs. The names come
from the running Studio; without one, you get commands and flags.

## Another address

Like `studio tui`, the commands connect to `127.0.0.1:7878`. For a Studio on another port, put
the flag before the command: `studio -addr 127.0.0.1:9000 ls`. They connect to this machine
only and sign in as you, so run them as the user Studio runs as.
