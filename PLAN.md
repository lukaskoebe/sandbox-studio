# Sandbox Studio — Plan

Status: v0.10.0 shipped on 2026-10-27, with the v0.8 and v0.9 feedback rounds (terminal tabs,
zsh, live resize, base image notices and rebases onto the current base, long chats in pages,
one memory panel and linked memory pages, force pushes and pull request reviews through
review, the Studio app, @ mentions, the Files tree). What is left is below: what comes next,
then the backlog.

How Studio works now is in the [developer documentation](docs/README.md), and what was
decided and built, and why, in [decisions and history](docs/decisions.md). Principles stay
as they were ([architecture](docs/architecture.md)): one opinionated way per feature, the
host decides, secrets never enter a sandbox, ask instead of failing.

Each item names what is missing and where it is described. New work lands with its e2e
scenarios and the matching developer and user docs, as [CONTRIBUTING.md](CONTRIBUTING.md)
says.

## Next

The feedback round of 2026-11-12. Its obvious fixes are in the changelog's next release: a new
environment opening at once, renaming environments, `/tmp` in Files, terminal links, rebases keeping
the tools and optionally Docker, git's setup on computers without a teammate, the diff view's
scrolling, the last computer remembered, and the catalog's fix text on every lifecycle action.
Since then: the `git_pull_request_create` tool, and pushes to a checked-out branch of a local
repository, which update its files. Why Docker's images went on a rebase: they live on the Docker
disk, which a new generation got empty from its template; **Keep Docker's images** now copies them.

To build, in this order:

1. **A teammate per copy.** A base serves several teammates, so **New computer** keeps the teammate
   field when **Start from** names a base, with **No teammate** among its choices, and the fork
   request takes an optional `teammateId` (absent: the source's; empty: none). Changing a computer's
   owner later is `PATCH …/sandboxes/{id}` with `teammateId`: the owner trigger
   (`sandboxes_teammate_owner_update`) allows it for a stopped computer, Studio rewrites the managed
   home files on the next connect, dismisses credential cards waiting for the old owner and
   publishes the change, since teammate rules follow the owner. The dialogs say that the home is
   copied as it is, the old teammate's harness history and logins included.
2. **Previews for module service workers.** Chrome fetches a module worker's script without
   cookies, so the preview gate (`internal/webauth`) refuses it with 403. The preview page gets a
   script that appends a per-preview capability to `navigator.serviceWorker.register` URLs; the gate
   accepts it only with `Service-Worker: script` and `Sec-Fetch-Dest: serviceworker`, and strips it
   before proxying. Check whether a module worker's static imports need it too.
3. **Deleting an environment**, after typing its name: Studio stops and removes its computers,
   disks, templates, CA, vault entries and terminals; the store cascades the rest. The last
   environment can't be deleted, nor one with an operation running.
4. **Faster Docker copies.** A fork or rebase copying 9 GB of Docker data took up to 15 minutes,
   against about 2 for `docker save` and `docker load` over SSH. Measure where the time goes with a
   1–2 GB image first (this host has 6 GB of disk free): the zstd level, the import's writeback
   every 32 MiB, the size pre-walk and the exec stdin chunks. Then tune the copy, or copy the stopped
   source's Docker disk image on the host instead, which a Docker fork already requires.
5. **The agent settings**: the profile card's small menu and the sheet look unrelated, and the menu
   is hard to find. Mocks first, as asked, for you to pick from.

Needs your call:

- **An AI provider for a computer without a teammate.** A chat needs a teammate today (its soul,
  memory and rules). Either a teammate-less chat on a provider you pick, with no memory, or keep
  pointing to "assign a teammate". The first is the smaller change; I'd wait for a second ask.
- **Stealth mode with the host's Chrome user agent.** Only the user agent would change: the
  platform, fonts, WebGL and client hints still say Linux, and a mismatch is easier to spot than a
  plain Linux Chrome. I'd at most match the host's Chrome version, not its OS.
- **Wails' self-update tutorial**: no. It is for Wails v2 and replaces the binary in place; Studio's
  updater already checks attestations, backs up, rolls back and updates the companions →
  [updates](docs/updates.md).
- **"could not stop sandbox: file is not a database"**: microsandbox's catalog was damaged. Studio
  now names the fix (restart Studio, which repairs it) for every lifecycle action. The cause is
  unknown; the startup log of the next restart and any `~/.microsandbox/db/msb.db.broken-*` would
  tell.

A fresh sandbox per task from a prepared base shipped in v0.17.0 with these decisions: JetBrains and
VS Code hosts stay out of the curated list (a base's home carries the backend, so it downloads once,
and read-only hosts break tools with their own certificate store), the default home stays 4 GiB (a
base's home can be resized and forks inherit it), and rules aren't copied (a teammate-scoped rule
already reaches every fork). Add rule copying only if that falls short.

## Backlog

**Platforms and runtime**
- Checkpoint restore, blocked by microsandbox #1736, still open on 2026-10-28 and not in msb
  0.7.11 → [sandboxes](docs/sandboxes.md#checkpoints). The lineage timeline's Restore and
  "Fork from here" wait on it: the menu items are always disabled and there is no backend for
  either.
- Copy-on-write forks: a fork copies `/workspace` and the home as a zstd tar, one after the
  other, and loses the root and Docker disks. A snapshot-based fork waits on the same
  microsandbox #1736 → [feedback plan](docs/design/feedback-v0.8.md#copy-on-write-forks). Once
  snapshots are copy-on-write, accepting a fork merge should take a checkpoint of the source again
  → [decisions](docs/decisions.md).
- Full-snapshot suspend instead of in-place pause (a restored VM can't get Studio's proxy
  and DNS, #1736 again) → [sandboxes](docs/sandboxes.md#suspend-and-resume)
- Scheduled checkpoints over a day on real time: a short live run with the times moved back
  took, pruned (oldest first, with force) and listed them on 2026-10-17 →
  [sandboxes](docs/sandboxes.md#scheduled-checkpoints)
- Template build qualifications: release base, cold-cache pulls, live exec export with
  network installs, oversized captures →
  [template builds](docs/template-builds.md#current-qualification-boundary)
- SSH access with the clients themselves: VS Code Remote-SSH, Zed and JetBrains Gateway
  against a sandbox on the release base image (`ssh`, `sftp` and the proxy were run on a
  minimal image). msb 0.7.8's SFTP server closes its channel without an exit status, so
  `scp` exits 1 after copying; report it upstream →
  [sandboxes](docs/sandboxes.md#ssh-access)
- Signed and notarized releases (the workflow exists, never exercised) →
  [release](docs/release.md)
- Studio inside Studio needs nested virtualization, which microsandbox 0.7.7 turns off
  (microsandbox #1778). Check again once microsandbox #823 (an opt-in setting, with
  snapshots) is merged and released, then expose it as a template setting.
- Local embeddings crashed once on a Windows runner: in the v0.3.5 release's smoke run,
  `TestLlamaEmbeds` died after 6 s with no output, and passed on the re-run and on the
  runner before. It happened again in v0.8.0's dry run. Find the CPU it fails on; llama.cpp's
  win-cpu-x64 build picks a backend per CPU → [release](docs/release.md)

**Agents and providers**
- Subscription logins and CLI-based utility calls (spike S8): the Claude sign-in works; a
  Claude session and chat on it, the ChatGPT/Codex login and the refresh are still to be run →
  [harnesses](docs/harnesses.md#subscription-logins-built-pending-live-verification)
- The chat adapters' assumptions against the pinned CLIs →
  [chat mode](docs/design/chat-mode.md) ("Unverified")
- What spike S7 left: OpenCode's V2 hook delivery (the pinned 1.18.35 uses V1), and the
  `gpt-6-luna` utility model's ID and price, which need an OpenAI key →
  [harnesses](docs/harnesses.md#verified-vs-pending)
- An OpenCode session started before a rebase resumes after it (the home disk's acceptance
  check, not yet run live)

**Browser**
- The browser items pending a live run, ephemeral sessions included →
  [browser](docs/browser.md#verified-vs-pending-live)
- The teammate browser keeps one profile, so a service worker an app registered on
  `localhost:PORT` stays and can answer for another app on that port later → [browser](docs/browser.md)
- Safari's handling of `*.localhost` previews as secure contexts is unverified
- Stealth mode in a browser VM: Debian Chromium's version in the user agent, and a switch
  restarting the browser under a connected live view → [browser](docs/browser.md#stealth-mode)
- A stronger stealth engine than Chromium's stealth mode: Obscura keeps no profile, CA or live
  view, so Camoufox (Firefox) would mean replacing agent-browser → [browser](docs/browser.md#stealth-mode)

**Git**
- Diffs, after moving to [`@pierre/diffs`](https://diffs.com)
  ([why and what it measured](docs/decisions.md#decisions-log)): a keyboard way to comment on a line
  (the button shows on hover and the line number takes a click or tap, neither from the keyboard);
  try it on a real touch screen, in the desktop app's WebKit and in Firefox; ship fewer than its
  roughly 300 grammars if the 12.6 MB it adds matters. Its conflict primitive could serve fork merges.
- The host-agent acceptance check: an agent in a sandbox pushes to `git@github.com:…`
  through review, with the key only in the host agent
- Pull request reviews, what is left: teammates' own forge accounts (a verdict on the user's
  own pull request posts as a comment until then), review requests on a forge becoming
  tasks, and GitLab merge requests → [forge reviews](docs/design/forge-reviews.md#phases)
- The pull request tools against a real Forgejo and GitHub: the list's `draft` field on
  Forgejo, and Forgejo's `position`/`original_position` of a review's line comments, were
  read from the API docs, not a live forge
