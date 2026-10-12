---
title: Developer guide
description: Build Sandbox Studio from source and find the project’s implementation and release guides.
---

Sandbox Studio is a Go application with an embedded web UI. The UI, API, gateway, sandbox manager, guest agent, and local catalog live in this repository.

## Requirements

- Go 1.26
- Node.js 22 and pnpm 11
- A C toolchain for the microsandbox SDK's CGO code
- Docker to build the local base image

## Build and run

From the repository root:

```sh
make web
make build
make image
make image-browser
./bin/studio
```

`make web` builds the UI into the Go embed directory. `make build` builds `bin/studio` and embeds the Linux guest agents. `make image` builds and loads the local `sandbox-studio-base:dev` image through Docker; `make image-browser` does the same for `sandbox-studio-browser:dev`. These development images are local builds, not Docker Hub repositories.

For UI work, run the Go server with `go run ./cmd/studio` and run `pnpm dev` from `web/` in another terminal. Vite proxies API requests to Studio. See [CONTRIBUTING.md](https://github.com/lukaskoebe/sandbox-studio/blob/main/CONTRIBUTING.md) for repository checks.

For an isolated second instance, set a separate `SANDBOX_STUDIO_HOME` and pass a different loopback `-addr`. The gateway and image registry use the next two ports. Keep that address stable for its data directory: existing sandbox configurations and template references store those ports.

## Where to look

| Area | Source |
| --- | --- |
| API and local server | `internal/api`, `cmd/studio` |
| Network policy and gateway | `internal/policy`, `internal/gateway`, `internal/caddyrule` |
| Agent harnesses and guest tools | `internal/harness`, `internal/agentcall`, `cmd/studio-agent` |
| Sandboxes and runtime | `internal/sandboxes`, `internal/runtime`, `images/base` |
| Browser broker | `internal/browser`, `images/browser` |
| Web UI | `web/` |
| Developer docs: architecture, components, decisions | `docs/` |

Start with the [developer docs index](https://github.com/lukaskoebe/sandbox-studio/blob/main/docs/README.md) and the [architecture overview](https://github.com/lukaskoebe/sandbox-studio/blob/main/docs/architecture.md). Other source guides include [template builds](https://github.com/lukaskoebe/sandbox-studio/blob/main/docs/template-builds.md), [memory](https://github.com/lukaskoebe/sandbox-studio/blob/main/docs/memory.md), [browser](https://github.com/lukaskoebe/sandbox-studio/blob/main/docs/browser.md), and [Git review](https://github.com/lukaskoebe/sandbox-studio/blob/main/docs/git-review.md).

## Release process

Releases are built from version tags by `.github/workflows/release.yml`. The process builds and attests the images and platform binaries. Do not treat an untagged branch as a released version. Read the [release guide](https://github.com/lukaskoebe/sandbox-studio/blob/main/docs/release.md) before preparing a release.
