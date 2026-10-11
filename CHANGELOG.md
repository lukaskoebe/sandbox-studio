# Changelog

## v0.1.0 — Unreleased

The first public release.

- **Sandboxes:** microVMs on microsandbox with persistent workspace and Docker disks, Docker
  inside, browser terminals (tmux) and port previews, disk checkpoints, suspend and resume,
  fork, rebase onto a new template, and export/import as a `.studio-sandbox` file.
- **Templates:** YAML specs (resources, Node/Python/Go versions, apt packages, a setup
  script) built into cached, content-addressed templates.
- **Environments:** isolated sets of rules, secrets, providers, teammates and memory, with a
  switcher in the UI.
- **Network gateway:** all sandbox TCP egress goes through Studio; `allow`, `proxy`,
  `caddy` and `deny` rules per environment, teammate or sandbox; unknown hosts are held
  until approved in the approvals inbox; per-environment TLS CA; connection log.
- **Secrets:** an encrypted vault on the host; guests see placeholders that the gateway
  substitutes only for bound hosts.
- **Teammates:** named agents with a role, soul, git identity and a harness (OpenCode,
  Claude Code or Codex) on Anthropic, OpenAI or OpenAI-compatible API keys. Subscription
  logins are not supported yet.
- **Memory:** per-teammate and shared memory with hybrid search on local embeddings,
  automatic recall and capture through harness hooks, consolidation and contradiction
  handling.
- **Browser broker:** a Chromium VM per teammate driven only by Studio, with approvals,
  redaction, credential fill, history and a live view with takeover.
- **Git review:** a virtual git remote; pushes are staged and reviewed before Studio pushes
  them upstream. GitHub, Forgejo/Gitea, HTTPS and SSH forges; pull request proposals on
  GitHub and Forgejo.
- **Packaging:** `studio doctor`, opt-in start at login and update check, `install.sh`,
  release builds for Linux (amd64, arm64), macOS (Apple Silicon) and Windows (amd64,
  preview) with checksums and build provenance attestations.
