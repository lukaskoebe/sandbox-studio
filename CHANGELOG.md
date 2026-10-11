# Changelog

## v0.1.1 — 2026-10-12

- Mobile terminals have touch controls for Ctrl, Esc, Tab, arrows and the keyboard.
- Browser takeover accepts mobile text input, including composed text and paste, and offers
  touch keyboard controls.
- Teammate sandboxes can show their current browser in a resizable side panel, with a
  full-width view on small screens and a toggle to hide it without stopping the browser.
- Copy buttons work on the local HTTP development server, including when accessed over the LAN.
- Memory hooks load in both OpenCode V1 and V2, fixing the plugin error in upgraded VMs.
- The browser has an address bar and guidance for a blank start page.
- Dev-server previews handle IPv6 loopback listeners and closed idle connections.
- Long network approval patterns stay inside their cards, and the guest agent's directory
  is traversable by the sandbox user.

## v0.1.0 — 2026-10-11

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
- **Chat mode:** chats with teammates over the Agent Client Protocol, with permission
  requests and questions, per-turn changes and undo, a file browser, an embedded preview,
  and a handover to the terminal and back. Onboarding asks for Chat or Terminal.
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
