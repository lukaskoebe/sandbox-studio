# M0 spike results

Host for all Linux results: Ryzen 5 3450U laptop (4 cores/8 threads, 14 GiB RAM), Linux 7.0,
microsandbox 0.7.7, Go 1.26. Spike code lives in `spikes/` and is not production code.

| # | Topic | Linux | macOS | Windows |
|---|---|---|---|---|
| S1 | Gateway + Docker-in-sandbox | ✅ | ⏳ needs the Mac | ⏳ no machine |
| S2 | Studio DNS resolver | ✅ (with caveats) | ⏳ | ⏳ |
| S3 | vsock agent channel | ✅ | ⏳ | ⏳ (named pipes) |
| S4 | CA injection / MITM | ✅ (Docker containers open) | n/a (guest-side) | n/a |
| S5 | Volumes, snapshots, forks | ✅ (decision made, one upstream gap) | ⏳ | ⏳ |
| S6 | Hold-and-ask client timeouts | ✅ | n/a (guest-side) | n/a |
| S7 | Harness hooks | ⏳ | | |
| S8 | Subscription auth | ⏳ needs your logins | | |
| S9 | Local embeddings | ✅ | ⏳ | ⏳ |

## S1 — Gateway and Docker in a sandbox

`go run ./spikes/s1-gateway`

- Sandbox boot about 1 s; `dockerd` ready about 1 s later (`docker:dind`, Docker data on an
  owned 8 GiB disk).
- Every TCP connection from the sandbox **and from containers inside its Docker** arrived at
  the SOCKS5 gateway tagged with the sandbox's SOCKS username.
- Hostnames recovered from TLS SNI and the HTTP Host header; denying `www.wikipedia.org`
  worked in the sandbox and in a nested container. Docker Hub pulls worked through the gateway.
- Gotcha: `MSB_HOME` must be short (Unix socket paths are limited to 108 bytes); the default
  `~/.microsandbox` is fine.
- Gotcha: microsandbox uses the host's resolver list by default, and the first resolver on this
  host (`127.0.0.1`) refuses connections, so DNS failed until resolvers were pinned. Studio
  always pins its own resolver (S2).

## S2 — Studio DNS resolver

`spikes/gw` + `msb run --dns-nameserver 127.0.0.1:15353 …`

- A loopback nameserver is accepted; every guest lookup reached the Studio resolver.
- NXDOMAIN for denied names works.
- IP→name mapping from DNS answers identified a non-TLS connection (`github.com:22`).
- microsandbox's rebind protection drops private answers (`10.1.2.3` never reached the guest),
  but `198.18.0.1` passes and the connection reaches the gateway with the right Host header.
  **Decision:** host services are virtual hosts under `*.studio.internal` resolving to
  `198.18.0.1` and served by the gateway itself.
- **Bug to avoid:** the spike waited 5 s for the client to speak before deciding; SSH servers
  speak first. The gateway must decide immediately on non-HTTP ports when the DNS map already
  knows the name (and use a short sniff timeout otherwise).
- Open: a single resolver port can't tell sandboxes apart. Use one resolver port per sandbox
  (cheap) so the name map and DNS-level decisions are per sandbox.

## S3 — vsock agent channel

`spikes/guest` (Linux, CGO off) mounted with `--mount-file`, `--vsock /path.sock:5000`

- The guest dials CID 2 port 5000; the host accepts on a Unix socket; yamux runs on top.
- Streams in both directions work: guest→host round trip about 5 ms, host→guest about 1.4 ms.
- Mounting the agent binary as a single read-only file works, so updating Studio updates the
  guest agent with no image rebuild.

## S4 — CA injection and TLS interception

`spikes/gw -mitm example.org,example.net -ca …`; the CA is copied into
`/usr/local/share/ca-certificates/` and `update-ca-certificates` runs at boot.

| Client | Result |
|---|---|
| curl | ✅ trusts the Studio CA |
| git (https) | ✅ |
| Python `urllib` | ✅ (upstream returned 403 to Python's user agent; TLS verified) |
| pip | ✅ |
| Node `fetch` with `NODE_EXTRA_CA_CERTS` | ✅ |
| Node `fetch` without it | ❌ `SELF_SIGNED_CERT_IN_CHAIN` (expected; the env var is required) |
| Containers inside the guest's Docker | ⏳ not covered: they have their own trust stores |

Decisions: the base image sets `NODE_EXTRA_CA_CERTS`, `SSL_CERT_FILE`, `REQUESTS_CA_BUNDLE`
and `GIT_SSL_CAINFO` system-wide. Container registries default to passthrough (`allow`)
rules so `docker pull` never needs the CA. For containers that call intercepted hosts, ship a
documented helper (`studio-ca mount`) that adds `-v /etc/ssl/certs:/etc/ssl/certs:ro` and the
env vars.

Update (M2): no helper after all. A `runc` shim in the guest adds the CA to every container
and build step; see PLAN.md, CA trust. Verified live with curl, wget, Python, git and Node in
`python`, `node` and `alpine/git` containers and in a `docker build` step.

## S5 — Volumes, snapshots and forks

`msb volume`, `msb snap`, `msb fork` with an owned disk at `/owned` and a named disk at `/ws`.

- Disk snapshots capture the root and **owned** volumes as a point-in-time copy; named
  volumes are excluded entirely. A restore without an explicit binding doesn't mount them,
  and a full restore refuses to start until they're bound or waived.
- A full snapshot of a running sandbox took about 1 s. The restore resumed a running
  background process from its captured state (counter continued 6 → 8).
- Forking a running sandbox requires that its resources are inherited when it uses an
  outbound proxy, TLS interception or secrets (`dangerously_inherit_resources`). The child
  then shares the parent's SOCKS identity and vsock route.

Decisions:
- `/workspace` and `/var/lib/docker` are **owned disks**. Checkpoints, suspend/resume and
  restores then capture everything consistently.
- Restore-as-new-generation inherits resources; that is exactly right because it is the same
  Studio sandbox. (Wrong for the network: see the M3 update below.)
- Fork: until microsandbox can re-key the proxy and vsock route on fork, a forked child shares
  its parent's network identity. Studio labels it as such in the UI, and the guest agent
  re-registers with a new token written via `msb exec` (the exec channel is per sandbox and
  trustworthy). **Upstream feature request:** allow overriding `outbound_proxy` credentials
  and vsock routes when forking or restoring.
- Rebase (new template) copies the workspace through the agent channel (tar stream);
  measure in M3.

M3 integration finding: disk-only capture with microsandbox 0.7.7's default guest flush
rejects the systemd base image: `workload freezer is unavailable: PID 1 handoff workloads
are not wholly owned by agentd's cgroup`. Studio therefore requires a stopped sandbox
for disk checkpoints, and does not fall back to `GuestFlushSkip`. Full execution snapshots
need separate validation with this base image before exposing suspend/resume.

Disk restore also loses the `WithInit(Init.Auto())` configuration: the replacement boots
with `init.krun` as PID 1, `systemctl` fails and the Studio agent does not reconnect. This
matches open upstream [#1676](https://github.com/superradcompany/microsandbox/issues/1676).
The v0.7.7 SDK exposes neither a restore init option nor an init modification API. Studio
therefore gates restore off before modifying either the VM or catalog. The candidate
adoption path additionally checks persisted init configuration as a regression guard.
Catalog/recovery tests use a supported fake runtime; they do not qualify real restore.

M3 update, guests without an init: with agentd as PID 1, microsandbox 0.7.7 freezes the guest
for every capture, so Studio dropped systemd (see PLAN.md 6.1, Init). On the base image, a
live disk checkpoint through Studio took 0.65 s (msb marks it crash-consistent), a full
snapshot 3 to 6 s while the source kept running, and a fork of a running sandbox 2.8 s.
#1676 no longer matters: Start runs `studio-agent boot` whether or not the VM has an init.

Restore stays gated, for another reason: a restored VM doesn't keep the source's network.
With `dangerously_inherit_resources`, a disk restore's stored config has no policy (msb's
default), no resolver, no outbound proxy and no vsock route; inheriting covers host mounts
only. The restore options take policy rules and vsock routes, but neither DNS nameservers nor
a proxy ([upstream #1736](https://github.com/superradcompany/microsandbox/issues/1736), open;
a maintainer says a fix is in progress). Started, such a VM would reach the internet without
the gateway. Studio keeps restore disabled and, as a regression guard, refuses a candidate
whose stored network config doesn't match Create's.

A full restore has the same gap, so suspend/resume waits for #1736 as well. It also needs the
source's vsock route: without one the device layout changes and the restore fails with
`incompatible virtio state: saved IRQ Some(18) does not match destination IRQ Some(17)`.

Checkpoint deletion must move a group head to a surviving member before removing it.
Studio prefers the parent, keeps head changes within the sandbox's group, and always
removes without force. Indexed child dependencies return a conflict; sandbox deletion
removes children before retrying their parents. A singleton head can be removed directly.

## S6 — Hold-and-ask client timeouts

The gateway held TLS connections without answering, and measured when each client gave up:

| Client | Gives up after |
|---|---|
| Node `fetch` (undici) | 10 s (connect timeout includes TLS) |
| pip | 15 s per attempt; about 110 s in total with its 5 retries |
| apt | 30 s per connection, and `apt-get update` still exits 0 |
| curl | 300 s (connect timeout) |
| git over https | 300 s |
| npm | about 5.5 min (`fetch-timeout` 5 min) |

Decisions: hold for up to 60 s. That covers curl, git, npm and apt, and pip succeeds on a
retry after approval. The base image sets `PIP_DEFAULT_TIMEOUT=60` and
`Acquire::http::Timeout "60"`. Node `fetch` fails fast, so the UI popup must appear within a
second or two, and the agent sees a clear 403 message on retry if it's still pending.

## S9 — Local embeddings (yzma + llama.cpp + embeddinggemma-300m Q8_0)

`go run ./spikes/s9-embed -dir <cache>`

- Assets: llama.cpp CPU libraries (about 16 MiB) and the GGUF (334 MB, ungated on Hugging
  Face) downloaded on first run.
- Model load 0.8 s; 768 dimensions; process RSS about 450 MB.
- About 128 ms per document embedding and about 70 ms per short query (CPU, no tuning).
- Retrieval sanity check: 5/5 queries ranked the right note first, using embeddinggemma's
  `task: search result | query:` / `title: none | text:` prompt formats.
- yzma requires Go 1.26, so the module now targets Go 1.26.

Follow-ups: tune threads and context size for query latency; measure on the Mac (Metal) and
Windows.

## M3 — OCI template image spike

On microsandbox 0.7.7, a loopback registry served a derived image made from the installed
Studio base plus one small gzip layer. Creating a fresh deny-all sandbox took about
2.5 seconds. The registry received manifest, config and new-layer requests, with no base
layer blob requests. Assertions in the guest confirmed an added file, a whiteout hiding
an existing base file, an opaque directory hiding base entries, and an untouched base
file. The spike VM and image reference were removed afterward.

This qualifies image composition with a warm cache, not the template builder. Cached
EROFS layers are keyed by diff ID; if a base layer is missing, the template registry must
serve it or the base must be recovered first. A partial registry is not a portable OCI
image distribution service. The SDK also exposes only a subset of the base config, so
the initial implementation is restricted to Studio's controlled base image. Use a stable
authenticated endpoint and immutable image references in production, not the spike's
ephemeral port.

Source review confirms that `IfMissing` requires a complete valid EROFS/fsmeta/VMDK cache,
not just cached metadata. The v0.7.7 Go SDK has no standalone image-pull method; creating a
temporary deny-all base sandbox can validate/prewarm it. Use the digest-pinned upstream
reference with `IfMissing` for releases, or `Never` for the local-only dev base. This
recovery sequence is source-reviewed; cold-cache recovery was not exercised by the spike.

Guest layer export remains unqualified. A dedicated yamux stream avoids relying on
undocumented FIFO behavior in the filesystem-read API, but needs explicit completion,
bounded framing, cancellation and capture-consistency tests. Privately mounting the
upper filesystem does not itself stop concurrent writes. Host-side tar validation must
never extract guest-controlled entries onto the host.

## Remaining M0 work

- S1–S3 and S9 on the Mac; Windows needs a cloud VM with nested virtualization.
- S7: verify hook payloads and context injection for OpenCode, Claude Code and Codex against
  a fake model endpoint (no credentials needed).
- S8: subscription logins — needs your Claude and ChatGPT accounts in an interactive session.
