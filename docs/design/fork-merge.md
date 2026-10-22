# Merging a fork back

A fork (`internal/sandboxes/transfer.go`) is a copy of a sandbox's `/workspace` and
`/home/agent`. **Merge into <source>** brings what changed in the fork back into the sandbox it
was forked from, without touching that sandbox until the user accepts the result.

Three sandboxes take part:

- the **source**, which the fork was made from and the merge goes into;
- the **fork**, whose changes are merged;
- the **scratch** sandbox, a fresh fork of the source named `<source>-merge-<n>`, where the
  merge is done, conflicts are resolved and tests can run.

## The base

Right after a fork boots, Studio runs `studio-agent merge-base` in it. That records the fork's
state, which is the source's at fork time, in `~/.local/state/studio/merge/base/`:

- a manifest of `/workspace` and `/home/agent`: path, type, mode, size, mtime and SHA-256 of
  every file Studio would merge (see [What is merged](#what-is-merged));
- the content of the text files up to 1 MiB, content-addressed, at most 512 MiB in all, for
  three-way merges;
- each git repository's branches and their commits;
- the tool installs and the harness session files there are.

The manifest names the fork's ID. A fork of a fork inherits its source's base in the copied
home and overwrites it with its own; a base whose ID isn't the fork's counts as none.

Forks made before Studio kept a base have none. Their merge treats every file as added on both
sides: identical files merge, and every other difference is a conflict.

## What is merged

| Where | How the fork's change is found | How it is merged |
|---|---|---|
| Files of a git work tree (`git ls-files -co --exclude-standard`) | the fork's working tree against its `HEAD`: uncommitted changes only | three-way, with the fork's `HEAD` version as the base |
| Commits of a git repository | branches that are new or moved since the base | `git bundle` of the new commits; fetched into the source's repository as `refs/heads/fork/<fork>/<branch>`, with a suffix if that exists. Nothing is overwritten |
| Other files in `/workspace` and `/home/agent` | against the base | one side changed: take it; both, text: `git merge-file`; both, binary or a type change: a conflict |
| Tool installs: `~/.local/share/mise/installs/<tool>/<version>`, `~/.cargo/bin/*`, `~/go/bin/*`, `~/.rustup/toolchains/*` | installs the base doesn't list | copied whole if the source lacks them; never file by file |
| Harness sessions: `~/.claude/projects`, `~/.codex/sessions` | against the base | union by file; a file both sides changed keeps the source's |

Left out: `.git` directories (their content travels as bundles), ignored files of a work tree,
caches (`~/.cache`, `~/.npm/_cacache`, `~/go/pkg`, `~/.cargo/registry`, `~/.cargo/git`,
`~/.rustup/downloads`, `~/.local/share/mise/downloads`, `~/.local/share/pnpm/store`,
`~/.bun/install/cache`), Studio's own state (`~/.local/state/studio`) and the files Studio
writes into every sandbox's home (harness configs, credentials, `~/.config/git/config`,
`~/memory`). Sockets, FIFOs and devices are skipped. A file over 64 MiB is compared by size and
mtime only, and can't be merged three-way.

## Steps

1. **Prepare.** Studio forks the source into the scratch sandbox and records its starting state
   there (`merge-base --start`), with the same rules as the base.
2. **Merge.** `studio-agent merge-export` in the fork writes an archive of its changes: the
   changed files, the base versions of the text files among them, the bundles and the new
   installs. Studio streams it through memory into `studio-agent merge-apply` in the scratch
   sandbox, which merges it into the scratch sandbox's trees and answers with the report. The
   archive is staged under `/workspace/.studio-merge/`, which git ignores (it holds a
   `.gitignore` of `*`).
3. **Conflicts.** A conflicting text file gets conflict markers; every conflict leaves the
   three versions in `/workspace/.studio-merge/conflicts/<tree>/<path>.{base,ours,theirs}`. The
   user, or an agent in a chat on the scratch sandbox, resolves them there and can run the
   tests. **Check again** (`merge-status`) counts what is left: a text file is resolved once it
   has no conflict markers, any other once its `.theirs` file is deleted.
4. **Accept.** Studio checkpoints the source, then `merge-replay-export` in the scratch sandbox
   writes everything that changed there since step 1, compared against its starting state, and
   `merge-replay` in the source applies it. A path the source changed since step 1 is a
   conflict: the replay checks every path first and applies nothing if one conflicts. Branches
   the scratch sandbox added or moved are bundled and set in the source's repository if the
   source's branch is still where it was. Then the scratch sandbox is deleted.
5. **Abort** deletes the scratch sandbox. The source was never touched.

All the guest steps run as the agent user, never as root, so a symlink in a sandbox can't make
them write outside what that user may write. Archives pass through the host in memory and are
never stored there.

## Records

A `merges` row tracks each merge: source, fork, scratch sandbox, state (`preparing`, `ready`,
`conflicts`, `failed`, `accepted`, `aborted`), the report and the checkpoint taken before
Accept. Accepting records a `merge` event in the source's lineage, naming the fork.

## Later

- Merging several forks at once.
- A `.studio-merge.toml` with per-path rules, such as "last writer wins" for lockfiles.
