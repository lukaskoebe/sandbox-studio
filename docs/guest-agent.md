# Guest agent and channel

`studio-agent` is a static Linux Go binary (`cmd/studio-agent`, `internal/guest`, CGO off,
amd64 and arm64). Studio embeds it (`internal/agentbin`), installs it into
`<data>/guest/bin/` and mounts that directory read-only at `/opt/studio` in every sandbox,
so updating Studio updates every guest.

## Channel

- The agent dials the host over vsock port 5000. microsandbox routes that to a host socket
  per sandbox (`<data>/run/<id>.sock`). On Windows, where msb routes vsock only to named
  pipes, it is the pipe `\\.\pipe\sandbox-studio-<hash of the data directory>-<id>`, whose
  DACL admits only the user Studio runs as.
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
| `snapshot`, `files` | host → guest | chat turn snapshots and the file browser's listings and reads |
| `upload` | host → guest | a file streamed into a folder of `/workspace` (see [uploads](#uploads)) |
| `browser` | host → guest | agent-browser commands in a browser VM |
| `forwards` | host → guest | the loopback ports a browser VM forwards to its driving sandbox ([browser](browser.md#localhost)) |
| `forward` | guest → host | one connection to a forwarded port; after the host's `{}`, raw bytes |
| `call` | guest → host | hooks, memory tools and browser tools |

Bulk transfers (template layer export, workspace copies) don't use the channel: guest→host
bulk data over vsock stalls on msb 0.7.7/0.7.8, so they run over msb exec instead (see
[spikes](spikes.md#layer-transfer-and-capture-foundations)).

### Limits of the channel on msb's vsock

The guest's writes to the host are the weak direction. Two limits were measured on
2026-10-13 with a scratch Studio (msb 0.7.7, amd64), using file reads of the same reply
shape as a browser command's:

- **A single guest write over 32 KiB breaks the channel.** Reading a 24,500-byte file (a
  reply of about 32.7 KB) took 3 ms; a 24,540-byte file (about 32.8 KB) stalled until the
  host gave up after 24 to 40 s, and the host saw a yamux protocol error, a `\x00` in a JSON
  reply, or EOF. yamux writes a frame's body in one call, so the guest's `channelConn`
  (`internal/guest/agent.go`) splits every write into pieces of at most 16 KiB. With that,
  the same reads finish in milliseconds.
- **A reply longer than one stream window (256 KiB) waits for the host's window update,
  which takes seconds.** A 196,500-byte file (a reply of about 262 KB) took 9 ms; a
  196,700-byte file (about 262.3 KB) took 8 s; a 300 KB file (about 410 KB) ended with EOF
  after 15 to 21 s. yamux also gives each frame 10 s to be written, so a reply that waits
  this long can fail. Keep replies within one window. Browser screenshots are capped at
  180 KiB of JPEG for this (`agentproto.MaxBrowserScreenshot`), which keeps their reply near
  246 KB.

Other replies can exceed these limits too (file reads go up to 1 MiB), so keep new replies
small or move their bytes to msb exec.

## Subcommands

| Command | Run by | What |
|---|---|---|
| `connect` (default) | the supervisor | the channel loop |
| `boot`, `shutdown`, `supervise` | Studio via msb exec | see [sandboxes](sandboxes.md#boot-and-shutdown) |
| `hook <event> --harness <name>` | the harness | reads the hook JSON on stdin, calls Studio, prints the harness's response |
| `mcp` | the harness | stdio MCP server with the memory and browser tools |
| `claude-acp` | Studio's chat, for a Claude subscription | the chat's ACP agent: runs `claude -p` for its turns ([harnesses](harnesses.md#claude-subscription-chats)) |
| `claude-permission` | Claude Code, from the chat's plugin | the permission prompt tool of that agent, which asks the shim |
| `oci-runtime` | Docker, as `runc` | CA trust for containers (below) |
| `export-layer`, `workspace-export`, `workspace-import`, `home-export`, `home-import` | Studio via msb exec | template builds, fork, rebase, export, import |
| `version` | | |

`hook` and `mcp` reach the agent through `/run/studio-agent/call.sock`, which becomes a
`call` stream. See [harnesses](harnesses.md) and [memory](memory.md#in-agent-sessions).

## Terminals and previews

- Terminals: xterm.js ↔ WebSocket (`internal/api/terminal.go`) ↔ Studio ↔ `pty` stream ↔
  tmux. Sessions are named and survive UI reloads and Studio restarts. tmux runs with
  `internal/guest/tmux.conf`: the mouse wheel scrolls its history in copy mode, where Ctrl-L
  leaves copy mode and reaches the shell. A mouse selection stays in copy mode so that it
  stays in view, and the agent sends a key tmux binds to leaving it ahead of the keys typed
  after a click (`internal/guest/clicks.go`), so typing reaches the shell. A tmux server
  reads the config when it starts, so a running sandbox gets a change at its next start.
- Previews: `http://<port>-<sandbox-id>.localhost:<studio-port>/` is proxied to that guest
  loopback port over a `tcp` stream (`internal/preview`). The guest dials `127.0.0.1`,
  then `::1` (Vite and others bind only `::1` for "localhost"), and closes its side when
  the server does, so the host never reuses a dropped keep-alive connection. Each preview
  is its own origin.
  Opening one goes through a short-lived ticket (see [API](api.md#auth)). The UI lists the
  listening ports from the `ports` stream.
- Service workers: when the browser fetches a preview's service worker script (header
  `Service-Worker: script`), Studio puts a small fetch handler on its first line that sends
  Studio's own paths (`/__studio/`) to the network before the app's handlers run. A worker
  that answers navigations from its cache can then still not keep the preview login from
  reaching Studio. Scripts over 16 MiB pass unchanged. "Open fresh" (`fresh=1` on the login
  link) answers the login with `Clear-Site-Data: "cache", "storage"`, which removes the
  preview's caches, storage and service workers but keeps its cookies, for an app that left a
  worker behind from whatever ran on that port before.
- Files: the Files panel browses `/workspace` read-only. Listings and reads never follow
  symlinks, and only files the `agent` user may read are shown.

## Uploads

`POST /api/environments/{env}/sandboxes/{id}/files?path=&name=&conflict=` streams the body
(`application/octet-stream`, with a Content-Length) to the guest as a `upload` stream: a
request line `UploadRequest` with the folder, the name and the size, then exactly that many
bytes, then one reply line. `agentproto.MaxUploadBytes` (100 MiB) bounds a file.

- The folder is resolved beneath `/workspace` with `openat2` (`RESOLVE_BENEATH` and
  `RESOLVE_NO_SYMLINKS`), so no symlink on the way leads out of the workspace, and the folder
  must exist. The name is one file name: no separator, NUL, `.` or `..`, at most 255 bytes.
- The file is written under a hidden `.studio-upload-*` name in its folder, owned by the
  `agent` user with mode 0644, and synced. Only then is it linked or renamed to its name, so
  readers never see half a file and a failed upload leaves nothing behind.
- `conflict` decides a name already taken: `fail` (the default) answers 409 and writes
  nothing; `replace` renames over a regular file but never over a folder or a link; `keep`
  takes the next free `name (2).ext`. Links take a name atomically, so concurrent uploads
  can't both take one.
- The guest reads the whole body even when it refuses the upload, so the host's writes
  complete and the reply arrives on the same stream.
- Uploads never extract anything: a `.tar` is just a file.

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
