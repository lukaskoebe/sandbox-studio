# Sandbox Studio — Plan

Status: the second round (N1–N11, C1, C2) shipped in v0.2.0 to v0.3.2, 2026-10-13 to
2026-10-15. What is left is the backlog below.

How Studio works now is in the [developer documentation](docs/README.md), and what was
decided and built, and why, in [decisions and history](docs/decisions.md). Principles stay
as they were ([architecture](docs/architecture.md)): one opinionated way per feature, the
host decides, secrets never enter a sandbox, ask instead of failing.

Each item names what is missing and where it is described. New work lands with its e2e
scenarios and the matching developer and user docs, as [CONTRIBUTING.md](CONTRIBUTING.md)
says.

## Backlog

**Platforms and runtime**
- S1–S3 on a real Mac (the macOS runners can't boot VMs); arm64 guests →
  [spikes](docs/spikes.md#still-open)
- Checkpoint restore, blocked by microsandbox #1736 →
  [sandboxes](docs/sandboxes.md#checkpoints). The lineage timeline's Restore and "Fork from
  here" wait on it: the menu items are always disabled and there is no backend for either.
- Scheduled checkpoints over a day on real time: a short live run with the times moved back
  took, pruned (oldest first, with force) and listed them on 2026-10-17 →
  [sandboxes](docs/sandboxes.md#scheduled-checkpoints)
- Full-snapshot suspend instead of in-place pause (a restored VM can't get Studio's proxy
  and DNS) → [sandboxes](docs/sandboxes.md#suspend-and-resume)
- Live CPU and memory resize of a running VM: msb 0.7.7 saved every change for the next
  start (verified live on 2026-10-17). msb 0.7.8 applies one live when the VM has headroom,
  which Studio doesn't boot with yet; then a "Restart to apply" for the rest →
  [feedback plan](docs/design/feedback-v0.8.md#cpu-and-memory-at-runtime)
- VS Code Remote-SSH through the SSH host config; the `msb ssh` commands themselves were
  verified live on 2026-10-17 → [sandboxes](docs/sandboxes.md#ssh-access)
- Template build qualifications: release base, cold-cache pulls, live exec export with
  network installs, oversized captures →
  [template builds](docs/template-builds.md#current-qualification-boundary)
- Signed and notarized releases (the workflow exists, never exercised) →
  [release](docs/release.md)
- Studio inside Studio needs nested virtualization, which microsandbox 0.7.7 turns off
  (microsandbox #1778). Check again once microsandbox #823 (an opt-in setting, with
  snapshots) is merged and released, then expose it as a template setting.
- Local embeddings crashed once on a Windows runner: in the v0.3.5 release's smoke run,
  `TestLlamaEmbeds` died after 6 s with no output, and passed on the re-run and on the
  runner before. It happened again in v0.8.0's dry run. Find the CPU it fails on; llama.cpp's win-cpu-x64 build picks a backend
  per CPU → [release](docs/release.md)
- Copy-on-write forks: today a fork copies `/workspace` and the home as a tar and loses the
  root disk; a snapshot-based fork waits on microsandbox #1736 →
  [feedback plan](docs/design/feedback-v0.8.md#copy-on-write-forks)
- Base images go out of date with no notice, and a sandbox without a template can't rebase
  onto the current default image →
  [feedback plan](docs/design/feedback-v0.8.md#base-images-go-out-of-date)
- zsh as the terminal shell → [feedback plan](docs/design/feedback-v0.8.md#zsh)
- A native app with Wails 3 (v3.0.0, out 2026-10-26), starting with a spike →
  [feedback plan](docs/design/feedback-v0.8.md#a-native-app-with-wails-3)
- The updater's follow-ups: exec in place on Unix, Nix-installed binaries, nix-ld →
  [feedback plan](docs/design/feedback-v0.8.md#the-updater)

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
- A teammate's status counts its terminals: a harness or a program running there →
  [feedback plan](docs/design/feedback-v0.8.md#teammate-status-shows-terminal-work)
- Memory that links: `[[links]]`, backlinks, an index page, a `~/memory` folder, page
  writes by teammates and lint → [memory links](docs/design/memory-links.md)

**Web UI**
- Terminal sessions as tabs → [feedback plan](docs/design/feedback-v0.8.md#terminal-sessions-as-tabs)
- One memory panel: one header, search on top, one row style, pages as markdown →
  [feedback plan](docs/design/feedback-v0.8.md#one-memory-panel)
- Long chats load in pages → [feedback plan](docs/design/feedback-v0.8.md#long-chats-load-in-pages)

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
- Pull request reviews by a teammate, Forgejo first → [forge reviews](docs/design/forge-reviews.md)
- Force pushes of feature branches through review →
  [feedback plan](docs/design/feedback-v0.8.md#force-pushes-through-review)
