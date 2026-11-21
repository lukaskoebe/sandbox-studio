---
title: Command line
description: Drive computers, ask a teammate and answer approvals from a shell or a script.
---

`studio <command>` talks to the Studio that is already running on this machine. It covers the
daily loop for scripts and quick work: list and start computers, ask a teammate, answer
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
| `studio ls` | Lists the computers. |
| `studio show <sandbox>` | Shows one computer: status, teammate, template, resources. |
| `studio start <sandbox>` | Starts a computer, or resumes it when it is suspended. |
| `studio stop <sandbox>` | Stops a computer. Its disks stay. |
| `studio suspend <sandbox>`, `studio resume <sandbox>` | Suspends a computer with its memory, and resumes it where it was. |
| `studio create <name>` | Creates a computer and waits until it runs. `--teammate Finn` gives it an owner, `--template node@3` starts from a template, `--cpus` and `--memory` (in GiB) size it, and `--no-wait` returns once Studio has accepted it. A template sets the resources, so `--cpus` and `--memory` don't go with `--template`. |
| `studio fork <sandbox> <name>` | Forks a computer into a new one with a copy of its disks, and waits until it runs, unless `--no-wait`. |
| `studio rm <sandbox>` | Deletes a computer and its disks. Asks for its name first. |
| `studio shell <sandbox> [terminal]` | Opens the computer's main terminal in yours, or the one you name, such as an agent's (`claude`). `Ctrl-]` detaches; the terminal keeps running. When an approval of the computer arrives, the title says so and the bell rings, and `Ctrl-]` asks about it instead: for a network request, `o` allows it once, `y` allows it and `n` denies it; other requests are answered in `studio tui` or the browser. `enter` returns to the terminal, and `q` detaches. |
| `studio ask <sandbox> <prompt>` | Asks the computer's teammate and prints the answer as it comes. `--quiet` prints only the answer, without the tool calls on standard error. |
| `studio approvals` | Lists what is waiting for you, in every environment. |
| `studio approve <id>`, `studio deny <id>` | Decides an approval. `--once` answers a network request without a rule, `--scope sandbox`, `teammate` or `environment` sets the rule's scope, and `--sandboxes` lets a browser request's rule cover the computers too. A computer's write to a host of the curated list gets a rule for writes like it, such as `POST /repos/*`, as on its card. |
| `studio checkpoints <sandbox>` | Lists a computer's checkpoints. |
| `studio checkpoint <sandbox> [name]` | Saves a checkpoint. Without a name, it is named after the time. |
| `studio restore <sandbox> <checkpoint>` | Puts a computer back to a checkpoint. Asks first. Studio refuses it for now, because the current runtime cannot restore checkpoints. |
| `studio envs` | Lists the environments. |

`studio help` (or `studio -h`, `studio --help`) lists every command, Studio's own included,
and its flags. `studio help <command>` or `studio <command> -h` shows a command's arguments
and flags. `<sandbox>` is a computer's name or ID.

## Studio's own commands

Unlike the commands above, these work without a running Studio, except `login-url`, `tui` and `quit`.

| Command | Does |
|---|---|
| `studio` | Starts Studio, prints a one-time login link and, in a terminal, opens it in the browser; `-no-browser` leaves it printed. If Studio already runs with the same data directory, it opens that one instead and exits. |
| `studio doctor` | Checks that this machine can run Studio and says how to fix what can't: virtualization (KVM, Hypervisor.framework or Windows Hypervisor Platform), the microsandbox runtime, the base image, the data directory, free disk space, the port, the SSH agent, the memory model, microsandbox's database, the sandbox VMs, leftover VMs and the sandboxes' storage. Exits 1 if a check fails. **Settings → Runtime** shows the same checks. |
| `studio login-url` | Prints a fresh one-time login link for the running Studio. |
| `studio tui` | Opens the [terminal UI](../tui/). |
| `studio app` | Opens Studio in [a window of its own](../getting-started/#studio-in-a-window-of-its-own), starting Studio if it isn't running. Without the app or a display it opens the browser. |
| `studio quit` | Stops the running Studio, as Ctrl-C in its window does, and waits until it has. Computers keep running. **Quit Studio…** in the Studio menu, which the version line at the foot of the sidebar opens, does the same from the browser. |
| `studio restart` | Stops the running Studio and starts this binary in its place. The installer runs it. Computers keep running. |
| `studio uninstall [-keep-data] [-keep-microsandbox] [-yes]` | Removes Studio, its data and its computers, and microsandbox unless that has sandboxes of its own. It lists what goes and asks you to type `uninstall`. It also removes the start-at-login entry, the line in your SSH config and, on Linux, the launcher entry. `-keep-data` keeps the data and computers for installing again, `-keep-microsandbox` keeps microsandbox and its images but not Studio's VMs, and `-yes` doesn't ask, for scripts. |
| `studio autostart enable`, `disable`, `status` | Starts Studio when you log in, or stops doing so. It takes effect at the next login. |
| `studio ssh enable`, `disable`, `status` | Lets any SSH client reach the computers as `<name>.studio`, or stops doing so. See [Connect over SSH](../sandboxes/#connect-over-ssh). Studio's SSH config runs `studio ssh-proxy`, which needs Studio running; you don't run it yourself. |
| `studio restore-msb-db [-replace] [file]` | Rebuilds microsandbox's database from a copy: one of Studio's own, kept every few minutes while it changes, or one a repair moved aside. Without a file it lists what each copy knows and picks the one that knows the most of your computers. Quit Studio and stop any computer's VM still running first; if the database in place knows computers, it needs `-replace`. See [Troubleshooting](../troubleshooting/#a-computer-says-microsandbox-lost-its-vm). |
| `studio version` | Prints Studio's version and its base image. |

## Asking a teammate

`studio ask` starts a chat with the teammate that owns the computer, the same chat you would
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
studio approve k2j4h6g8f0d1s --sandboxes  # a browser request: the computers may connect too
```

A browser request's rule covers only the browsers, as on the web. `--sandboxes` makes it
cover the computers' own connections too.

## Inside a computer

On a computer, the `studio` command runs Studio's tools for its teammate. Agents use it, and you can too, in a terminal or over SSH:

| Command | What it does |
| --- | --- |
| `studio info` | Says which computer this is, its teammate, the ports it serves with their preview links, and its network level and rules. |
| `studio memory search`, `get`, `page`, `remember`, `share`, `correct`, `forget` | Searches and changes the teammate's [memory](../memory/); `~/memory` holds it as files. |
| `studio task list`, `create`, `status`, `note` | Reads and changes the teammate's tasks. |
| `studio git pushes`, `git repos` | Lists the computer's pushes and what became of them, and the forges' repositories. |
| `studio pr list`, `read`, `status`, `create`, `review`, `review-status`, `checks`, `comment` | Works with pull requests; see [Git review](../git-review/#pull-requests-on-the-computer). `~/forge` holds them as files, and `studio forge sync` writes it again. |
| `studio browser …` | Drives the teammate's [browser](../browser/); `studio browser --help` lists its commands. |
| `studio wait <approval>` | Waits for your answer to something that needs it, and says what became of it. |
| `studio call <tool> '<json>'` | Calls any of Studio's tools by name. |

Flags come from each tool's arguments, and `--<flag>-file` reads one from a file. `--json` prints JSON, `--wait` waits for an approval instead of returning, and `-o` writes a screenshot or other output to a file. A pull request command takes the forge, repository and branch from the checkout it runs in. The command exits 0 when done, 1 when refused or failed, 2 for a wrong command line, and 75 when it waits for your approval; `studio pr checks` exits 8 while a check runs.

## Environments

Commands work in the first environment, as the browser opens. Pick another with
`--env <name>`, or for a whole shell with `export STUDIO_ENV=<name>`.

## Scripts

Every command but `completion` takes `--json` and prints Studio's own objects. The exit status is 0 when the
command did what it says, 1 when it failed (an `ask` whose turn ended in an error included),
2 for a wrong command line, and 130 after Ctrl-C.

`studio rm` and `studio restore` ask for the computer's name in a terminal. In a script they
refuse unless you add `--yes`.

## Completion

```sh
source <(studio completion bash)     # in ~/.bashrc
source <(studio completion zsh)      # in ~/.zshrc
studio completion fish | source      # in ~/.config/fish/config.fish
```

Completion offers commands, flags, computer names, teammate names after `--teammate`, environments and approval IDs. The names come
from the running Studio; without one, you get commands and flags.

## Another address

Like `studio tui`, the commands connect to `127.0.0.1:7878`. For a Studio on another port, put
the flag before the command: `studio -addr 127.0.0.1:9000 ls`. They connect to this machine
only and sign in as you, so run them as the user Studio runs as.
