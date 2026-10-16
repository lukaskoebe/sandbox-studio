# Sandbox Studio

Sandbox Studio runs coding agents (OpenCode, Claude Code and Codex) as persistent teammates,
each working in its own [microsandbox](https://docs.microsandbox.dev) microVM. The agents
have root inside their sandbox, but everything that leaves it goes through Studio on the
host: network connections, git pushes, browser actions and credentials. You review and
approve those from a local web UI, a terminal UI or the command line.

**📖 Documentation: [lukaskoebe.github.io/sandbox-studio](https://lukaskoebe.github.io/sandbox-studio/)**

> **Status: early.** Expect rough edges and breaking changes between 0.x releases. Windows
> support is a preview.

## Features

- **A sandbox per teammate.** Persistent Linux workspaces with Docker inside, terminals,
  port previews, disk checkpoints, suspend and resume, fork, and export/import. Templates
  are built from a short YAML spec.
- **Network rules and approvals.** Every TCP connection from a sandbox, Docker included,
  goes through Studio's gateway. Rules (`allow`, `proxy`, `caddy`, `deny`) apply per
  environment, teammate or sandbox; a connection to an unknown host waits for your answer.
- **Secrets never enter sandboxes.** Keys stay encrypted on the host. Guests see
  placeholders, which the gateway swaps for the real value only for the hosts a secret is
  bound to.
- **Git review.** Sandboxes hold no forge credentials. Each push waits as a diff until you
  approve it; Studio then pushes to GitHub, GitLab, Forgejo/Gitea/Codeberg, any HTTPS or SSH git
  host, or a folder on your computer.
- **Browser.** Agents drive a real Chromium in a separate microVM. Actions on new sites ask
  first, password fields are redacted, and you can watch live and take over.
- **Teammates and memory.** Named agents with a role, a harness, a provider and a git
  identity, with memory across sessions and a shared team memory. Providers are API keys,
  OpenAI-compatible endpoints, or a Claude Pro/Max or ChatGPT subscription.
- **Chat or terminal.** Talk to a teammate in a chat and undo its changes per turn, or run
  the agent in a terminal.
- **Environments** such as "work" and "private" share no rules, secrets, providers,
  teammates or memory.

## Install

Linux (amd64/arm64 with KVM), macOS (Apple Silicon) or Windows 11 (preview, amd64), with
about 5 GB of free disk.

```sh
curl -fsSL https://raw.githubusercontent.com/lukaskoebe/sandbox-studio/main/install.sh | sh
studio doctor   # check that this host can run Studio
studio          # prints a one-time login link for http://127.0.0.1:7878
```

The installer verifies the release's checksums and installs `studio` to `~/.local/bin`
without sudo. On Windows, or to install by hand, download an archive from
[Releases](https://github.com/lukaskoebe/sandbox-studio/releases). See
[Getting started](https://lukaskoebe.github.io/sandbox-studio/getting-started/) for
platform notes and the guided setup.

## Security model

Studio assumes the agent in a sandbox is untrusted; all policy lives on the host. A sandbox
reaches the network only through Studio's gateway (TCP to public addresses, DNS through
Studio's resolver; UDP and ICMP are blocked). Credentials stay on the host, git pushes and
unknown hosts wait for approval, and the browser runs in its own VM. The UI listens on
loopback only and signs in with a one-time link. Report vulnerabilities privately as
described in [SECURITY.md](SECURITY.md).

## Building from source

Requirements: Go 1.26, Node 22 with pnpm 11, a C toolchain (the microsandbox SDK needs CGO),
and Docker for the images.

```sh
make web            # build the UI into the Go binary's embed directory
make build          # bin/studio, with both Linux guest agents embedded
make image          # build the base image and load it into microsandbox
make image-browser  # build and load the image for teammates' browsers
./bin/studio
```

For UI work, run `go run ./cmd/studio` and `pnpm dev` in `web/` (Vite proxies `/api`).
[CONTRIBUTING.md](CONTRIBUTING.md) lists the checks to run before sending a change,
[docs/](docs/README.md) explains how Studio is built, and [docs/release.md](docs/release.md)
how releases are cut.

## License

Apache License 2.0; see [LICENSE](LICENSE).
