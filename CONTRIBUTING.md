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
golangci-lint run                             # the linters CI runs; see .golangci.yml
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
Real VMs are booted by the smoke workflow (`.github/workflows/smoke.yml`), nightly and on
demand: it builds Studio on Linux, macOS and Windows, starts it, and creates and deletes a
sandbox with the `studio` commands. On Linux it then runs the Playwright tests in
`web/e2e-live` against that Studio. On a microsandbox-capable host, the same by hand:

```sh
make build image
bin/studio &
bin/studio create smoke && bin/studio rm smoke --yes
```

The parsers of untrusted input have fuzz targets: `FuzzSniffTLS` and `FuzzSniffHTTP`
(`internal/gateway`), `FuzzSOCKSHandshake` (`internal/gateway`), `FuzzParseRepoURL`
(`internal/gitreview`) and `FuzzCompile` (`internal/caddyrule`). Their seeds run in every
`go test`. To fuzz one for a minute, run it alone:

```sh
go test -run='^$' -fuzz='^FuzzSniffTLS$' -fuzztime=60s ./internal/gateway/
```

A failing input is written to `testdata/fuzz/` and then runs as a regression test.

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
CI runs the suite on every push.

## Conventions

- Commit messages are a single line.
- User-facing behaviour is documented in `site/src/content/docs/`, implementation notes in
  `docs/`; update them with the change.
- No new dependencies without a discussion in the issue.
