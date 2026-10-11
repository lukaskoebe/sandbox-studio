# Contributing

Issues and pull requests are welcome. For anything larger than a fix, open an issue first:
Studio has one opinionated way to do each thing (see [PLAN.md](PLAN.md), §2), so a new
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
go test -tags nobadger,nomysql,nopgx ./...
make api                                      # commit web/openapi.json and schema.gen.ts if they change
sh scripts/test-install.sh                    # when install.sh changes

cd web
pnpm typecheck && pnpm lint && pnpm check     # pnpm format fixes formatting
```

The `nobadger,nomysql,nopgx` build tags leave out the storage backends of Caddy's PKI app,
which Studio doesn't use; release builds use them too. Tests use fakes and never start VMs.
The tests that boot the real base image need the `integration` tag, a microsandbox-capable
host, `make image` and `make agent`:

```sh
go test -tags integration -run TestSandboxLifecycle -v ./internal/sandboxes
```

## Conventions

- Commit messages are a single line.
- User-facing behaviour is documented in `docs/`; update it with the change.
- No new dependencies without a discussion in the issue.
