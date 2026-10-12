# Contributing

Issues and pull requests are welcome. For anything larger than a fix, open an issue first:
Studio has one opinionated way to do each thing (see [principles](docs/architecture.md#principles)), so a new
option or alternative needs a good reason. Report security problems privately, as described
in [SECURITY.md](SECURITY.md).

## Setup

Go 1.26, Node 22 with pnpm 11, a C toolchain (the microsandbox SDK needs CGO) and, for the
base image, Docker. See "Building from source" in [README.md](README.md).

## Checks

Run these before sending a change; CI runs them too.

```sh
gofmt -l .                                    # must print nothing
go vet -tags nobadger,nomysql,nopgx ./...
go vet -tags e2e,nobadger,nomysql,nopgx ./... # the e2e build, see below
go test -tags nobadger,nomysql,nopgx ./...
make api                                      # commit web/openapi.json and schema.gen.ts if they change
sh scripts/test-install.sh                    # when install.sh changes

cd web
pnpm typecheck && pnpm lint && pnpm check     # pnpm format fixes formatting
pnpm e2e                                      # needs bin/studio-e2e: make e2e-bin
```

The `nobadger,nomysql,nopgx` build tags leave out the storage backends of Caddy's PKI app,
which Studio doesn't use; release builds use them too. Tests use fakes and never start VMs.
The tests that boot the real base image need the `integration` tag, a microsandbox-capable
host, `make image` and `make agent`:

```sh
go test -tags integration -run TestSandboxLifecycle -v ./internal/sandboxes
```

## End-to-end tests

`make e2e` builds the web UI and a Studio with the `e2e` build tag, then runs the Playwright
suite in `web/e2e` with two workers, each with its own Studio. The e2e build keeps the real
gateway, approvals, secrets and stores, but replaces the VM runtime and the guest agent with
in-process fakes, so no VM or network access is needed. To use an installed Chromium instead
of Playwright's download, set `PW_CHROMIUM`:

```sh
PW_CHROMIUM=/usr/bin/chromium make e2e
```

The fakes and the scenarios are described in [frontend](docs/frontend.md#end-to-end-tests).
CI runs the suite on every push. The integration test runs nightly on a KVM runner
(`.github/workflows/nightly.yml`).

## Conventions

- Commit messages are a single line.
- User-facing behaviour is documented in `docs/`; update it with the change.
- No new dependencies without a discussion in the issue.
