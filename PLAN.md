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
- Live CPU and memory resize of a running VM, once an msb release offers it: msb 0.7.7 saves
  every change for the next start, which was verified live on 2026-10-17 →
  [sandboxes](docs/sandboxes.md#resources)
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
  runner before. Find the CPU it fails on; llama.cpp's win-cpu-x64 build picks a backend
  per CPU → [release](docs/release.md)

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
- An embedded preview's frame cookie lasts 8 hours like the session; after that an app shell its
  service worker cached loads, but its fetches get 403 until the page is reloaded through Studio
- Safari's handling of `*.localhost` previews as secure contexts is unverified
- Stealth mode in a browser VM: Debian Chromium's version in the user agent, and a switch
  restarting the browser under a connected live view → [browser](docs/browser.md#stealth-mode)
- A stronger stealth engine than Chromium's stealth mode: Obscura keeps no profile, CA or live
  view, so Camoufox (Firefox) would mean replacing agent-browser → [browser](docs/browser.md#stealth-mode)

**Git**
- The host-agent acceptance check: an agent in a sandbox pushes to `git@github.com:…`
  through review, with the key only in the host agent
