# Sandbox Studio

Sandbox Studio runs coding agents (OpenCode, Claude Code and Codex) as persistent teammates,
each working in its own [microsandbox](https://docs.microsandbox.dev) microVM. The agents
have root inside their sandbox, but everything that leaves it goes through Studio on the
host: network connections, git pushes, browser actions and credentials. You review and
approve those from a local web UI. Studio is a single binary that serves the API and the UI
on `127.0.0.1`.

> **Status: early.** v0.1 is the first public release; expect rough edges and breaking
> changes between 0.x releases. Windows support is a preview. See [PLAN.md](PLAN.md) for the
> design and roadmap.

## Features

- **Teammates.** Named agents with a role, a "soul" (personality and working rules), a
  harness (OpenCode, Claude Code or Codex), a provider, a model and a git identity. Each
  owns its sandboxes. Providers are Anthropic and OpenAI API keys or an OpenAI-compatible
  endpoint; subscription logins (Claude Pro/Max, ChatGPT) are not supported yet.
- **Memory.** Each teammate remembers across sessions, sandboxes and harnesses, and there
  is a shared team memory. Recall and capture run through harness hooks. Consolidation
  merges duplicates and turns contradictions into items you resolve. Embeddings run
  locally, with nothing to configure ([docs/memory.md](docs/memory.md)).
- **Network rules and approvals.** Every TCP connection from a sandbox, including from
  Docker containers inside it, goes through Studio's gateway. Rules (`allow`, `proxy`,
  `caddy`, `deny`) apply per environment, teammate or sandbox. A connection to an unknown
  host is held while an approval pops up in the UI, and continues once you allow it. Custom
  rules can be written as Caddy config.
- **Secrets never enter sandboxes.** Secrets and API keys stay on the host, encrypted with a
  master key kept in the OS keychain (or in an owner-only file when there is none). Guests
  only see placeholders, which the gateway replaces with the real value on the way out, and
  only for the hosts a secret is bound to.
- **Git review.** Sandboxes hold no forge credentials. They push to a virtual remote that
  Studio serves; each push is held as a reviewable diff until you approve it, and Studio
  then pushes it upstream. Forges: GitHub and Forgejo/Gitea/Codeberg (both with pull
  request proposals), any HTTPS git host, and plain SSH remotes with a pinned host key
  ([docs/git-review.md](docs/git-review.md)).
- **Templates.** Describe a sandbox in a short YAML spec (resources, Node/Python/Go
  versions, apt packages, a setup script); Studio builds it into a cached template
  ([docs/template-builds.md](docs/template-builds.md)). Rebase moves an existing sandbox to a
  new template and keeps its workspace.
- **Browser.** Agents drive a real Chromium in a separate microVM through tools Studio
  offers; they never get the debugging port. Actions are approved in the UI, password
  fields are redacted, stored credentials are filled in by Studio, and you can watch live and
  take over ([docs/browser.md](docs/browser.md)).
- **Sandboxes.** Persistent workspaces, Docker inside, terminals and port previews in the
  browser, disk checkpoints, suspend and resume, fork, and export/import.
- **Environments** such as "work" and "private" share no rules, secrets, providers,
  teammates or memory.
- Chat mode for non-developers is in development.

## Requirements

- **Linux** on amd64 or arm64 with KVM: `/dev/kvm` must be readable and writable by your
  user.
- **macOS** on Apple Silicon (Hypervisor.framework). Intel Macs are not supported.
- **Windows 11** on amd64 with the Windows Hypervisor Platform (preview).
- Network access on first start: Studio downloads the microsandbox runtime and the base
  image. Keep at least 5 GB of disk free for images and sandboxes.

## Install

On Linux and macOS:

```sh
curl -fsSL https://raw.githubusercontent.com/lukaskoebe/sandbox-studio/main/install.sh | sh
```

To inspect the script first:

```sh
curl -fsSLo install.sh https://raw.githubusercontent.com/lukaskoebe/sandbox-studio/main/install.sh
less install.sh
sh install.sh
```

The script downloads the latest release for your platform, checks it against the release's
`checksums.txt` (and with `gh attestation verify` when the GitHub CLI is installed and
logged in), and installs `studio` to `~/.local/bin` without sudo. It then checks KVM or
Hypervisor.framework access and tells you what to fix; it never installs other software.
Set `SANDBOX_STUDIO_VERSION=v0.1.0` to pick a release and `SANDBOX_STUDIO_INSTALL_DIR` to
change the directory.

**Windows**, or installing by hand: download the archive for your platform from
[Releases](https://github.com/lukaskoebe/sandbox-studio/releases) (Linux amd64/arm64,
macOS arm64, Windows amd64). Verify it with `sha256sum -c checksums.txt --ignore-missing`
(on Windows: `Get-FileHash <file>` and compare with `checksums.txt`) or
`gh attestation verify <archive> --repo lukaskoebe/sandbox-studio`. Each archive contains a
single `studio` executable; put it anywhere on your `PATH`.

## First run

```sh
studio doctor   # check that this host can run Studio
studio          # prints a one-time login link for http://127.0.0.1:7878
```

On first start Studio installs the matching microsandbox runtime into `~/.microsandbox`, and
it pulls the base image the first time a sandbox needs it, so it needs network access. Open
the printed link; it expires after ten minutes and works once. Run `studio login-url` for a
new one while Studio is running. Data lives in the user data directory
(`~/.local/share/sandbox-studio` on Linux, `~/Library/Application Support/Sandbox Studio` or
`%LOCALAPPDATA%\Sandbox Studio`); set `SANDBOX_STUDIO_HOME` to move it.

**Doctor.** `studio doctor` checks the microsandbox runtime and `msb doctor`, hardware
virtualization, the base image, the data directory, free disk space (it warns below 5 GB) and
the loopback ports Studio listens on. Every finding comes with a fix. The same checks run at
startup, where problems are logged as warnings and Studio starts anyway, and on the
**Settings** page under **Runtime**.

**Settings** also has two options, both off by default:

- **Start at login** (or `studio autostart enable|disable|status`) adds a per-user systemd
  unit, LaunchAgent or `HKCU\...\Run` entry. It needs no administrator rights and starts the
  binary it was enabled from, so enable it again after moving the binary.
- **Check for updates** asks GitHub for the latest release at most once a day and shows when a
  newer one is available. Studio never downloads or installs updates itself.

### Platform notes

- **Linux** needs read and write access to `/dev/kvm`. On a desktop session at the machine
  itself, systemd-logind grants that through an ACL. Over SSH, or on a headless server, add
  yourself to the `kvm` group with `sudo usermod -aG kvm $USER`, then log out and back in.
  Inside a VM, enable nested virtualization.
- **macOS** needs Apple Silicon; it uses Hypervisor.framework. Intel Macs are not supported.
- **Windows** is a preview. Turn on the Windows Hypervisor Platform feature
  (`Enable-WindowsOptionalFeature -Online -FeatureName HypervisorPlatform` in an
  administrator PowerShell) and reboot.

## Using sandboxes

Open sandbox previews through the **Previews** menu. Each preview gets its own host-scoped
session, and Studio removes its authentication cookies before forwarding requests to the
sandbox. Preview links use `.localhost`, so they open on the machine running Studio.

Use **Checkpoints** to save a sandbox's root, workspace and Docker disks, while it runs or
while it is stopped. Checkpoints can depend on earlier ones; delete newer checkpoints before
their parents. Restore is currently disabled: microsandbox 0.7.7 restores a snapshot with its
default network, which would bypass Studio's gateway
([upstream #1736](https://github.com/superradcompany/microsandbox/issues/1736)).
Disk checkpoints do not preserve running processes, and one of a running sandbox is
crash-consistent. Quiesce applications first when they need application-level consistency;
**Suspend** pauses a running sandbox in place and **Resume** continues it where it was,
terminals included. A suspended sandbox keeps its memory and does not survive a host reboot.

## Security model

Studio assumes the agent in a sandbox is untrusted; all policy lives on the host.

- **Network:** a sandbox's only way out is TCP to public addresses through the Studio
  gateway, plus DNS to Studio's resolver. UDP and ICMP are blocked. Studio's own services are
  reachable only through the gateway and the sandbox's agent channel.
- **Credentials:** secrets, API keys and forge tokens stay on the host. Guests get
  placeholders; the gateway substitutes real values only for bound hosts, terminating TLS
  with a per-environment Studio CA that guests trust.
- **Actions:** git pushes are staged for review; unknown hosts, browser actions and
  credential fills wait for approval unless a rule or pattern you set allows them.
- **Browser:** Chromium runs in its own VM that only Studio can control; password values
  are redacted before the agent sees a page.
- **Local UI:** Studio listens on loopback only. A login link is valid once for ten
  minutes and sets an HttpOnly session cookie; requests are checked for the expected Host.

Report vulnerabilities privately as described in [SECURITY.md](SECURITY.md).

## Building from source

Requirements: Go 1.26, Node 22 with pnpm 11, a C toolchain (the microsandbox SDK needs CGO),
and Docker for the base image.

```sh
make web      # build the UI into the Go binary's embed directory
make build    # bin/studio, with both Linux guest agents embedded
make image    # build the base image with Docker and load it into microsandbox
./bin/studio  # prints a one-time login link for http://127.0.0.1:7878
```

A local build uses the `sandbox-studio-base:dev` image that `make image` loads. During UI
work, run `go run ./cmd/studio` and `pnpm dev` in `web/` (Vite proxies `/api`), and paste
the launch link into the UI's Connect screen. For the LAN development UI, run
`pnpm dev:lan`; authentication still goes through Studio. See
[CONTRIBUTING.md](CONTRIBUTING.md) for the checks to run before sending a change, and
[docs/release.md](docs/release.md) for how releases are cut.

## License

Apache License 2.0; see [LICENSE](LICENSE).
