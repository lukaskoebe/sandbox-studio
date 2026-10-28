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

- **Pull request reviews of any pull request**: a teammate can only review one it is handed,
  and can't list the pull requests of the repositories it reaches. It should list them, review
  any of them, and comment on pull requests of other people and agents, all through review →
  [forge reviews](docs/design/forge-reviews.md)

## Backlog

**Platforms and runtime**
- Checkpoint restore, blocked by microsandbox #1736, still open on 2026-10-28 and not in msb
  0.7.11 → [sandboxes](docs/sandboxes.md#checkpoints). The lineage timeline's Restore and
  "Fork from here" wait on it: the menu items are always disabled and there is no backend for
  either.
- Copy-on-write forks: a fork copies `/workspace` and the home as a zstd tar, one after the
  other, and loses the root and Docker disks. A snapshot-based fork waits on the same
  microsandbox #1736 → [feedback plan](docs/design/feedback-v0.8.md#copy-on-write-forks)
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
- The host-agent acceptance check: an agent in a sandbox pushes to `git@github.com:…`
  through review, with the key only in the host agent
