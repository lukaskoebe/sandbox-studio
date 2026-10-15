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
- S1–S3 and S9 on macOS and Windows; arm64 guests; a Windows cloud VM →
  [spikes](docs/spikes.md#still-open)
- Checkpoint restore, blocked by microsandbox #1736 →
  [sandboxes](docs/sandboxes.md#checkpoints). The lineage timeline's Restore and "Fork from
  here" wait on it: the menu items are always disabled and there is no backend for either.
- Full-snapshot suspend instead of in-place pause (a restored VM can't get Studio's proxy
  and DNS) → [sandboxes](docs/sandboxes.md#suspend-and-resume)
- Live runs of what is built but only checked against fakes: CPU and memory resize
  ([sandboxes](docs/sandboxes.md#resources)), suspend and resume, and the SSH access
  commands ([sandboxes](docs/sandboxes.md#ssh-access))
- Template build qualifications: release base, cold-cache pulls, live exec export with
  network installs, oversized captures →
  [template builds](docs/template-builds.md#current-qualification-boundary)
- Signed and notarized releases (the workflow exists, never exercised) →
  [release](docs/release.md)
- Run the smoke workflow before a release; `release.yml` doesn't today, so a release can
  ship without a real VM having booted it.

**Agents and providers**
- Subscription logins (Claude Pro/Max, ChatGPT/Codex) and CLI-based utility calls: built,
  pending the owner's accounts (spike S8) →
  [harnesses](docs/harnesses.md#subscription-logins-built-pending-live-verification)
- The chat adapters' assumptions against the pinned CLIs →
  [chat mode](docs/design/chat-mode.md) ("Unverified")
- Live hook verification for all three harnesses; spike S7
- An OpenCode session started before a rebase resumes after it (the home disk's acceptance
  check, not yet run live)

**Browser**
- The browser items pending a live run, ephemeral sessions included →
  [browser](docs/browser.md#verified-vs-pending-live)
- A Playwright run that clicks known targets in a real browser at several viewport sizes and
  zoom levels; the e2e suite checks the mapping against the fake only

**Git**
- Built-in pinned host keys for github.com, gitlab.com and bitbucket.org; today every SSH
  forge goes through scan-and-confirm → [git review](docs/git-review.md#ssh-forges)
- The host-agent acceptance check: an agent in a sandbox pushes to `git@github.com:…`
  through review, with the key only in the host agent

**Terminal UI**
- The PTY smoke test opens Home and Activity and quits, but doesn't answer an approval
- The site's terminal UI page shows a hand-copied text block, not one generated from the
  golden test
