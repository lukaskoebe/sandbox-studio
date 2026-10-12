# Sandbox Studio v2 plan

The original v2 plan (2026-10-08 to 2026-10-11) is implemented. What was built is described
in the developer docs: start at [docs/README.md](docs/README.md). Decisions, the v1
comparison and milestones are in [docs/decisions.md](docs/decisions.md); spikes and risks in
[docs/spikes.md](docs/spikes.md). The full plan text is in git history (before this file was
replaced).

## Still open from the v2 plan

One line per item, with where it was in the plan and where it is tracked now.

- Subscription logins (Claude Pro/Max, ChatGPT/Codex), CLI-based utility calls with a daily cap; spike S8 (§4, §6.6, §9) → [harnesses](docs/harnesses.md#verified-vs-pending)
- Live hook verification for all three harnesses and confirming `gpt-6-luna`; spike S7 (§6.6, §9) → [harnesses](docs/harnesses.md#verified-vs-pending)
- S1–S3 and S9 on macOS and Windows; arm64 guests; a Windows cloud VM (§8 M0, §9) → [spikes](docs/spikes.md#still-open)
- Checkpoint restore, blocked by microsandbox #1736 (§6.1) → [sandboxes](docs/sandboxes.md#checkpoints)
- Full-snapshot suspend instead of in-place pause (§6.1) → [sandboxes](docs/sandboxes.md)
- Live CPU/memory resize of a running sandbox (§6.1)
- Template build qualifications: release base, cold-cache pulls, live exec export with network installs, oversized captures (§6.2) → [template builds](docs/template-builds.md#current-qualification-boundary)
- Nix as an optional extra in the base image (§6.2)
- File upload into the guest (§6.3, §6.10)
- DNS-level deny (§6.4)
- Per-rule upstream, Host, SNI and CA overrides; request-level `network.request` approvals (§4, §6.4)
- Allow-once/deny-once decisions; HTTP request and browser-action pattern rules (§4, §6.5) → [approvals](docs/approvals.md)
- Auto-decided requests logged as an audit trail in the inbox (§6.5)
- Onboarding wizard beyond choosing chat or terminal (§6.0)
- Per-environment export/import (`studio-export.yaml`) and copying a teammate to another environment (§6.0)
- Teammate default rule scope and approval auto-patterns; avatar field (§4, §6.6)
- Memory: `context_dependent` judge verdict, dream promotion suggestions and core-page candidates, browsable `~/memory`, quality metrics UI, markdown export/import (§6.7) → [memory](docs/memory.md)
- Browser downloads handed to the agent, and the browser items pending a live run (§6.8) → [browser](docs/browser.md#verified-vs-pending-live)
- Integration interface: MCP tools and jobs; notifying the agent over MCP when a push is rejected (§6.9) → [git review](docs/git-review.md)
- GitLab merge requests (§6.9) → [git review](docs/git-review.md)
- SSH into sandboxes (`msb ssh serve`) documented (§6.10)
- Signed and notarized releases (§8 M9, §12) → [release](docs/release.md)
- Teammate tasks and heartbeats (§8)
- Testing: VM integration tests in CI on Linux with KVM, Playwright and Vitest, SNI fuzz test, memory eval corpus, recorded hook fixtures for all adapters (§10) → [architecture](docs/architecture.md#testing-and-ci)
- golangci-lint (§11)
- Access to private networks through explicit rules; only public addresses are allowed today (§12) → [network](docs/network.md)
