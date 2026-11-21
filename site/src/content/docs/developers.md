---
title: Developer guide
description: Build Sandbox Studio from source and find the project’s implementation and release guides.
---

Sandbox Studio is a Go application with an embedded web UI. The UI, API, gateway, computer manager, guest agent, and local catalog live in this repository; the code calls a computer a sandbox.

## Requirements

- Go 1.26
- Node.js 22 and pnpm 11
- A C toolchain for the microsandbox SDK's CGO code
- Docker to build the local base image, and microsandbox's `msb` on your `PATH` to load it
- On Linux, for the desktop app: the GTK3 and WebKitGTK 4.1 development packages (`libgtk-3-dev libwebkit2gtk-4.1-dev`)

## Build and run

From the repository root:

```sh
make web
make build
make image
make image-browser
./bin/studio
```

`make web` builds the UI into the Go embed directory. `make build` builds `bin/studio` and embeds the Linux guest agents. `make app` builds `bin/studio-app`, the desktop app. `make image` builds and loads the local `sandbox-studio-base:dev` image through Docker; `make image-browser` does the same for `sandbox-studio-browser:dev`. These development images are local builds, not Docker Hub repositories.

For UI work, run `make agent` once, so the guest agents are there to embed, then `make dev`, which runs the Go server and Vite together; Vite serves on port 3000 and proxies API requests to Studio, and `STUDIO_URL` points it at another one.

:::caution
A development build migrates the database of the data directory it opens, and the release you have installed then refuses it as being from a newer version of Studio. Don't point a development build at the data of the Studio you use: give it a `SANDBOX_STUDIO_HOME` of its own.
:::

For an isolated second instance, set a separate `SANDBOX_STUDIO_HOME` and `MSB_HOME`, and pass a different loopback `-addr`; without its own `MSB_HOME`, Studio refuses to start while another one uses microsandbox's home. The gateway and image registry use the next two ports. Keep that address stable for its data directory: existing computers' configurations and template references store those ports.

## Where to look

| Area | Source |
| --- | --- |
| API and local server | `internal/api`, `cmd/studio` |
| Network policy and gateway | `internal/policy`, `internal/gateway`, `internal/caddyrule` |
| Agent harnesses and guest tools | `internal/harness`, `internal/agentcall`, `cmd/studio-agent` |
| CLI and terminal UI | `internal/cli`, `internal/tui` |
| Desktop app | `cmd/studio-app`, `cmd/studiow` (Windows) |
| DNS, secrets and credentials | `internal/dnsproxy`, `internal/secrets`, `internal/credentials` |
| Git review | `internal/gitreview` |
| Memory | `internal/memory`, `internal/agentmem` |
| Computers and runtime | `internal/sandboxes`, `internal/runtime`, `images/base` |
| Browser broker | `internal/browser`, `images/browser` |
| Web UI | `web/` |
| Tests | `internal/e2e`, `web/e2e`, `web/e2e-live` |
| These docs and their screenshots | `site/`, `site/scripts/screenshots.mjs` |
| Developer docs: architecture, components, decisions | `docs/` |

Start with the [developer docs index](https://github.com/lukaskoebe/sandbox-studio/blob/main/docs/README.md) and the [architecture overview](https://github.com/lukaskoebe/sandbox-studio/blob/main/docs/architecture.md). Other source guides include [template builds](https://github.com/lukaskoebe/sandbox-studio/blob/main/docs/template-builds.md), [memory](https://github.com/lukaskoebe/sandbox-studio/blob/main/docs/memory.md), [browser](https://github.com/lukaskoebe/sandbox-studio/blob/main/docs/browser.md), [Git review](https://github.com/lukaskoebe/sandbox-studio/blob/main/docs/git-review.md), [network](https://github.com/lukaskoebe/sandbox-studio/blob/main/docs/network.md), [approvals](https://github.com/lukaskoebe/sandbox-studio/blob/main/docs/approvals.md), [the agent CLI](https://github.com/lukaskoebe/sandbox-studio/blob/main/docs/agent-cli.md), [the CLI](https://github.com/lukaskoebe/sandbox-studio/blob/main/docs/cli.md), [the terminal UI](https://github.com/lukaskoebe/sandbox-studio/blob/main/docs/tui.md) and [the frontend](https://github.com/lukaskoebe/sandbox-studio/blob/main/docs/frontend.md), which covers the e2e fakes.

## Checks

`make check` runs the formatters, linters and tests, and `make e2e` the browser tests against a Studio with fake computers. After changing the API, `make api` regenerates `web/openapi.json` and the UI's types. [CONTRIBUTING.md](https://github.com/lukaskoebe/sandbox-studio/blob/main/CONTRIBUTING.md) lists the rest, such as the smoke workflow and the fuzz targets.

## Release process

Releases are built from version tags by `.github/workflows/release.yml`. A release branch (`release/v1.2.3`) runs the same build and smoke tests without publishing, and the tag is pushed only once that run and ci pass on the branch's head. Before that, `make check` runs on main, and `make e2e` when the UI changed. After a release, the screenshots workflow retakes these docs' screenshots. The process builds and attests the images and platform binaries. Do not treat an untagged branch as a released version. Read the [release guide](https://github.com/lukaskoebe/sandbox-studio/blob/main/docs/release.md) before preparing a release.
