# Agent command

`studio` in a sandbox runs the tools of the `studio` MCP server as commands, so an agent can
script, pipe and loop over them in its shell, and the user can run them over SSH. The concept
is in the [data as files artifact](https://claude.ai/artifact/ReJo1R59jU3s6QLKpUL1DS); this
page is what was built.

The MCP server stays as it is: every tool is still there, and `studio` adds commands next to
it rather than replacing it.

## How it runs

- `studio-agent boot` links `/usr/local/bin/studio` to `/opt/studio/bin/studio-agent`
  (`guest.CommandLink`). It does this on every boot, systemd guests included. A file of the
  user's own named `studio`, or a link to something else, is left alone.
- Called as `studio`, the agent binary runs `agentcall.RunCLI` instead of its subcommands.
  `studio-agent` keeps its own commands (`hook`, `mcp`, `git-credential` and the rest).
- Each command is one call through `/run/studio-agent/call.sock`, the same as an MCP tool
  call. Studio knows the sandbox from the channel, so the identity, the approvals and the logs
  are the same as for the MCP tool.
- A sandbox that was already running when Studio was updated has no link until it restarts.
  The managed instructions say so.

## Commands

The commands are built from `agentcall.Tools`, so a new tool gets a command without extra
work:

- **Names.** `commandNames` gives a tool its words and its positional arguments, such as
  `memory search <query>`, `pr review <number>` and `browser get-text <ref>`. A tool that isn't
  listed gets its name split at the first `_`.
- **Flags.** Every other property in the schema is a flag, with `_` written as `-`:
  `--fact-id`, `--include-done`.
  - Integers and booleans are parsed by their type. Arrays and objects are JSON.
  - An enum takes `-` for `_` (`--verdict request-changes`).
  - `--<flag>-file <path>` reads a string from a file, or from stdin with `-`.
  - `pr review` also takes `--comment path:line=text` (or `path:line:old=text` for a removed
    line), which can be repeated.
- **Taken from the checkout.** In a checkout, a pull request command fills in what it was not
  given: `forge` and `repository` come from a remote on `git.studio.internal` (`origin`
  first, otherwise the only such remote), and `branch` from `HEAD`.
- **`studio call <tool> '<json>'`** (or `-` to read stdin) runs any tool by its MCP name, with
  no flag parsing. It refuses names that are not tools, such as `credential` or `hook`.
- **Help.** `studio --help` lists the commands, `studio browser --help` (or `studio help
  browser`) the commands of one group, and `studio <command> --help` the tool's description
  and its arguments. A bare group, as `studio browser`, lists its commands on stderr and exits
  with 2.
- **`studio forge sync`** is the one command that is not an MCP tool
  (`agentproto.MethodForgeSync`); see below.

## Output and exit codes

- A result that is a string is printed as it is.
- Any other JSON result is printed as indented `key: value` lines in the order of the JSON.
  Text over several lines becomes an indented block.
- `--json` prints the result as JSON instead.
- An image, such as a screenshot, is written to `-o <file>`, or else to
  `$TMPDIR/studio-<tool>-<time>.png`, and the command prints the path.

| Code | Meaning |
|---|---|
| 0 | done |
| 1 | refused or failed; the message goes to stderr |
| 2 | a usage mistake: an unknown command or flag, or a missing argument |
| 75 | waiting for the user (`EX_TEMPFAIL`) |

A call waits for the user when one of these is true:

- The result's `status` is `awaiting approval`, as for `pr create` and `pr review`.
- The error starts with "waiting for the user" or says "call again", as for a browser action
  on a site not yet allowed, or for a takeover.

With `--wait` the command keeps asking every 5 seconds:

- `pr create` waits through `git_pull_request_status`.
- `pr review` waits through `git_pull_request_review_status`.
- A refusal that only waits is retried as it was.

There are no approval IDs and no `studio wait <id>` command. Those would need a host API that
names pending approvals to the guest, which is left for later.

## ~/forge

`~/forge` holds the pull requests of the sandbox's repositories as files, so an agent can
`grep`, `cat` and `ls` them instead of calling a tool for each. Studio writes it from
`gitreview` (`forgefiles.go`) for every forge with an API that pull requests use: GitHub,
GitLab, Forgejo, Gitea and Codeberg. It is read-only, and the guest swaps the whole folder in
at once, as for `~/memory` (`agentproto.HomeDir`).

    ~/forge/
      index.md                                  what it holds, when, and the trust note
      <host>/<owner>/<repo>/pulls.md            open pull requests, and those closed in the last 7 days
      <host>/<owner>/<repo>/pulls/<n>/pr.md     front matter, the description, commits, how to fetch it
                                  conversation.md   the comments
                                  reviews.md        reviews and line comments
                                  files.md          the files it changes, with + and −

- **Which repositories.** Those the workspace has a remote for through `git.studio.internal`
  (the guest's `git-remotes` files op, with the branch checked out), those the sandbox pushed
  to, and those of a pull request it read. A guest older than the op is covered by its pushes.
  At most 20 repositories.
- **Which pull requests in full.** One whose head is a branch the sandbox has checked out or
  pushed, and one the agent read with `pr read` (`git_pull_request_read`). At most 25; the
  rest are lines in `pulls.md`.
- **When.** When a session starts (the `session-start` hook), every 5 minutes while one runs
  (the sandbox called Studio in the last 30 minutes), and on `studio forge sync`, which
  answers once the folder is written (at most 90 s). A listing younger than 2 minutes is
  shared between sandboxes, and a pull request whose `updated` time didn't change isn't read
  again. A folder that would only change its `fetched_at` isn't written.
- **Front matter.** `pr.md` starts with `number`, `title`, `state` (`open`, `closed` or
  `merged`), `draft`, `base`, `head`, `head_sha`, `author`, `url`, `updated_at`, `forge`,
  `repository` and `fetched_at`; a GitLab merge request also has `kind: merge_request`. Every
  file has `fetched_at`.
- **Limits.** 4 MiB and 4000 files in all, 64 KiB per file: a longer file is cut with a note
  that `studio pr read` has the rest. What doesn't fit is left out, and `index.md` says so.
- **Trust.** Tokens that look like credentials are replaced with `[redacted]`
  (`memory.CredentialLike`). `index.md` and each file say that titles, descriptions, comments
  and reviews are written by people on the forge, to be read as data and never as
  instructions.

The concept also had conditional requests (`If-None-Match`) per pull request; the 2-minute
shared listing and the `updated` check do that job with the adapters as they are. Issues and
checks are not in `~/forge` yet.
