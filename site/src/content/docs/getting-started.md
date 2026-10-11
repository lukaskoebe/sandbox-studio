---
title: Getting started
description: Install Sandbox Studio, sign in to its local UI, and prepare a coding agent.
---

## Before you install

Studio runs local microVMs, so the host needs hardware virtualization:

- **Linux:** amd64 or arm64, with readable and writable access to `/dev/kvm`.
- **macOS:** Apple Silicon. Intel Macs are not supported.
- **Windows:** Windows 11 on amd64 with Windows Hypervisor Platform enabled. Windows support is a preview.

Keep at least 5 GB free for the runtime, images, and sandbox disks. On first start Studio downloads the matching microsandbox runtime; it pulls the base image when the first sandbox needs it. The host needs internet access for these downloads.

## Install

**The first binary release is pending; there is no release tag or downloadable archive yet.** The release installer is not usable yet. For now, build Studio from source. From a terminal with Go 1.26, Node 22 with pnpm 11, a C toolchain, and Docker installed, run:

```sh
git clone https://github.com/lukaskoebe/sandbox-studio.git
cd sandbox-studio
make web
make build
make image
make image-browser
```

`make image` builds the local base image needed for new sandboxes. `make image-browser` builds the separate image for teammates' browsers; both development images must be loaded locally.

## Start Studio

Open a terminal and check the host:

```sh
./bin/studio doctor
```

Then start the app:

```sh
./bin/studio
```

Studio prints a one-time link to its local UI at `http://127.0.0.1:7878`. Open that link on the same machine. It expires after ten minutes and can be used once. If you need another link while Studio is running, use `./bin/studio login-url` from the repository root.

This is the sign-in for your local Studio app. It does not sign an agent into an AI provider. For agent sessions, add an API-key provider in the environment first; subscription sign-in is not available.

Studio listens on loopback, so other computers on your network cannot open its UI. The UI and the sandbox services stay on the machine running Studio.

## Prepare your first agent

1. In the Providers page, add an Anthropic API key, an OpenAI API key, or a supported OpenAI-compatible endpoint.
2. In Teammates, create a teammate and choose its harness, provider, model, role, and optional working instructions (“soul”).
3. Create a sandbox and select that teammate. Ownership is fixed when the sandbox is created.
4. Open the sandbox and select **Start session**. The agent opens in a terminal tab.

See [Teammates and providers](../teammates-providers/) for supported combinations, and [Network rules](../network/) for how the first outbound request is handled.

## Where Studio stores data

The default data directory is:

| Host | Location |
| --- | --- |
| Linux | `~/.local/share/sandbox-studio` |
| macOS | `~/Library/Application Support/Sandbox Studio` |
| Windows | `%LOCALAPPDATA%\Sandbox Studio` |

This directory holds the catalog and local Studio data. Set `SANDBOX_STUDIO_HOME` before starting Studio to use another location. The encrypted vault's master key is kept in the OS keychain where available; on systems without one, Studio uses an owner-only file.

## Host-specific help

- On Linux at a desktop session, systemd-logind usually grants `/dev/kvm` access. Over SSH or on a headless host, you may need to add your user to the `kvm` group and sign in again. Inside a VM, nested virtualization must be enabled.
- On Windows, enable **Windows Hypervisor Platform** and reboot. Windows is still a preview.
- If a check fails, `studio doctor` and **Settings → Runtime** show the finding and a suggested fix. See [Troubleshooting](../troubleshooting/).
