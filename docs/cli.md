# Command line

`studio <command>` runs short commands against the running Studio: list and drive sandboxes, ask
a teammate, answer approvals, save and restore checkpoints. It is for scripts and for quick work
without a browser. `studio tui` is the interactive client; plain `studio` is the server. All
three are the same binary. The user page is [Command line](../site/src/content/docs/cli.md).

| Command | Does |
|---|---|
| `ls` | the sandboxes of the environment: name, status, teammate, template, uptime |
| `show <sandbox>` | one sandbox: status, error, teammate, template, resources, created |
| `start`, `stop`, `suspend`, `resume <sandbox>` | the lifecycle; `start` resumes a suspended sandbox. Studio answers once the VM is in its new state |
| `create <name>` | a new sandbox, with `--teammate`, `--template`, `--cpus`, `--memory` (GiB); waits until it runs, unless `--no-wait` |
| `fork <sandbox> <name>` | a fork with a copy of the disks; waits like `create` |
| `rm <sandbox>` | deletes the sandbox and its disks; asks first |
| `shell <sandbox>` | the sandbox's main terminal in this one, as in the TUI; `Ctrl-]` detaches |
| `ask <sandbox> <prompt>` | a chat with the sandbox's teammate, the answer printed as it streams; `-` reads the prompt from stdin, `--chat <id>` continues a chat |
| `approvals` | the pending approvals of every environment |
| `approve`, `deny <approval>` | decides one; a network request gets a rule with the teammate's usual scope (`--scope` to choose), or `--once` for this connection only |
| `checkpoints <sandbox>`, `checkpoint <sandbox> [name]` | lists or saves checkpoints |
| `restore <sandbox> <checkpoint>` | puts the sandbox back to a checkpoint, by name or ID; asks first |
| `envs` | the environments |
| `completion bash\|zsh\|fish` | the completion script |

Every command that talks to Studio takes `--env` (name or ID; default `$STUDIO_ENV`, else the
first environment, as the web UI and TUI open) and `--json`. Sandboxes are named by name or ID.
Flags go before or after the arguments; `--` ends them.

## Design

- **A client of the API, nothing more** (`internal/cli`). It signs in as the local owner the way
  the TUI does (docs/tui.md, "Sign-in"), uses the TUI's client (`tui.Client`, loopback only,
  no proxy, no redirects), and ends its session when it exits. It adds no endpoints, so it can
  do nothing the web UI can't, and it never reads the catalog beyond the install key.
- **Synchronous where Studio is.** v1's mutations returned job IDs and needed `--wait` and a
  `wait` command. v2's lifecycle calls answer once the VM is in its new state, so the commands
  just return. Only `create` and `fork` run in the background in Studio; they wait by default,
  polling the sandbox, and `--no-wait` returns at once. Ctrl-C stops the waiting, never the
  operation, and says so.
- **Teammates are first class.** `ask` is the v2 command v1 had no equivalent for: it starts
  (or continues) a real chat, which shows up in the web UI with its tool calls and approvals.
  The agent's text goes to stdout as it grows; tool calls (`› title`) and what the turn waits
  for go to stderr, so `studio ask api "…" > answer.md` keeps only the answer. Approvals the turn
  waits for are named with their `studio approve` ID. `--json` prints one object at the end:
  `chat`, `turn`, `answer`, `stopReason` and the end marker if any.
- **Exit statuses** follow v1: 0 done, 1 failed (including a turn that ended in error, was
  stopped or hit a limit, and a create that failed), 2 usage, 130 interrupted.
- **Output.** Tables for people, `--json` for scripts: the API's own objects, indented. Text
  from Studio or a sandbox has its control characters escaped (`\x1b`), so a sandbox can't
  drive the terminal; JSON keeps the strings and escapes them as JSON does.
- **Asking first.** `rm` and `restore` can't be undone. In a terminal they ask for the sandbox's
  name; without one they refuse unless `--yes`, so a script states it.
- **Completion** offers commands, flags, environment and teammate names for `--env` and
  `--teammate`, sandbox names and approval IDs. Names come from the running Studio through the
  hidden `studio __complete <words>` with a 1.5 s limit; without a Studio, completion offers
  commands and flags only and never starts one. New names (`create`, `fork`) get no
  suggestions.

## Not in the CLI

- **Running a command in a sandbox** (`exec`). The guest's run stream only runs an allowlist
  (`agentproto.RunCommands`); a general one would be a new guest capability, so for now
  `studio shell` is the way in.
- **Settings, rules, secrets, providers, memory, templates.** Those are configured in the web UI,
  where they are explained. The CLI covers the daily loop.
