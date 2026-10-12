# Guest agent and channel

`studio-agent` is a static Linux Go binary (`cmd/studio-agent`, `internal/guest`, CGO off,
amd64 and arm64). Studio embeds it (`internal/agentbin`), installs it into
`<data>/guest/bin/` and mounts that directory read-only at `/opt/studio` in every sandbox,
so updating Studio updates every guest.

## Channel

- The agent dials the host over vsock port 5000. microsandbox routes that to a host socket
  per sandbox (`<data>/run/<id>.sock`).
- One yamux session per guest; either side opens streams. Every stream starts with a JSON
  header line naming its kind (`internal/agentproto`).
- The host side is `internal/agentchan`. The identity of a guest is the socket its
  connection came in on, so requests never name a sandbox, teammate or environment.
- On connect the host sends the sandbox's config (CA and the placeholders of the secrets it
  sees), and sends it again when one of them changes.
- Concurrent requests per guest are bounded; lines and frames are capped at 1 MiB.

Stream kinds:

| Kind | Direction | Use |
|---|---|---|
| `hello` | guest → host | version, arch, hostname |
| `pty` | host → guest | a terminal attached to a tmux session |
| `tcp` | host → guest | raw bytes to a guest loopback port (previews) |
| `sessions`, `kill` | host → guest | list or end tmux sessions |
| `ports` | host → guest | listening TCP ports |
| `config` | host → guest | CA and placeholders; acknowledged when applied |
| `home-files` | host → guest | harness config files into `/home/agent` (at most 16 files, 256 KiB each, no symlinks) |
| `start-session` | host → guest | a harness TUI in a detached tmux session |
| `acp` | host → guest | stdin/stdout of an ACP agent process (chats) |
| `snapshot`, `files` | host → guest | chat turn snapshots and the read-only file browser |
| `browser` | host → guest | agent-browser commands in a browser VM |
| `call` | guest → host | hooks, memory tools and browser tools |

Bulk transfers (template layer export, workspace copies) don't use the channel: guest→host
bulk data over vsock stalls after roughly 128–256 KiB on msb 0.7.7/0.7.8, so they run over
msb exec instead (see [spikes](spikes.md#layer-transfer-and-capture-foundations)).

## Subcommands

| Command | Run by | What |
|---|---|---|
| `connect` (default) | the supervisor | the channel loop |
| `boot`, `shutdown`, `supervise` | Studio via msb exec | see [sandboxes](sandboxes.md#boot-and-shutdown) |
| `hook <event> --harness <name>` | the harness | reads the hook JSON on stdin, calls Studio, prints the harness's response |
| `mcp` | the harness | stdio MCP server with the memory and browser tools |
| `oci-runtime` | Docker, as `runc` | CA trust for containers (below) |
| `export-layer`, `workspace-export`, `workspace-import` | Studio via msb exec | template builds, fork, rebase, import |
| `version` | | |

`hook` and `mcp` reach the agent through `/run/studio-agent/call.sock`, which becomes a
`call` stream. See [harnesses](harnesses.md) and [memory](memory.md#in-agent-sessions).

## Terminals and previews

- Terminals: xterm.js ↔ WebSocket (`internal/api/terminal.go`) ↔ Studio ↔ `pty` stream ↔
  tmux. Sessions are named and survive UI reloads and Studio restarts.
- Previews: `http://<port>-<sandbox-id>.localhost:<studio-port>/` is proxied to that guest
  loopback port over a `tcp` stream (`internal/preview`). The guest dials `127.0.0.1`,
  then `::1` (Vite and others bind only `::1` for "localhost"), and closes its side when
  the server does, so the host never reuses a dropped keep-alive connection. Each preview
  is its own origin.
  Opening one goes through a short-lived ticket (see [API](api.md#auth)). The UI lists the
  listening ports from the `ports` stream.
- Files: chats have a read-only browser of `/workspace` that never follows symlinks and
  only shows files the `agent` user may read. There is no upload.

## CA trust and placeholders in the guest

When the agent applies a config (`internal/guest/config.go`):

- it installs the CA with `update-ca-certificates`, which rebuilds the system bundle
  `/etc/ssl/certs/ca-certificates.crt`, and writes `ca.crt` and `ca-bundle.crt` (the system
  roots including the CA) to `/etc/sandbox-studio`;
- it writes the placeholders to a profile hook, `/etc/profile.d/sandbox-studio-env.sh`, so
  new login shells export `NAME=studio-…` for each secret.

The base image points `SSL_CERT_FILE`, `NODE_EXTRA_CA_CERTS`, `REQUESTS_CA_BUNDLE` and
`GIT_SSL_CAINFO` at the system bundle, and sets `PIP_DEFAULT_TIMEOUT=60` and an apt
timeout of 60 s to match the gateway's hold time.

Containers: the image shadows `runc` with `/usr/local/bin/runc`, which calls
`studio-agent oci-runtime`. It bind-mounts `/etc/sandbox-studio` read-only at
`/dev/sandbox-studio` (containers have a tmpfs at `/dev`, so it never lands in an image)
and sets the CA variables plus `CURL_CA_BUNDLE`, unless the container already sets them. This covers `docker run`
and `docker build` steps without flags. Images that set their own (e.g. `curlimages/curl`)
need `-e CURL_CA_BUNDLE=/dev/sandbox-studio/ca-bundle.crt`.
