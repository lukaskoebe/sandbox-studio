# Template build worker live qualification

This disposable Linux amd64 driver is intended to qualify the production
`templatebuild.Worker` against the already loaded `sandbox-studio-base:dev` image. Run it
only when you intend to create disposable test VMs. The base image and its immutable layers
must already be available in the local microsandbox cache. The worker uses the normal base
inspection path for cache identity. The SDK has no cached synthesized `repo@digest` alias
for the loaded dev base, so on a cache miss the worker asks the private loopback registry
for a short-lived, environment-scoped deterministic manifest/config by digest. The SDK
uses this authenticated image source for build-sandbox creation and reuses the cached
immutable layers; the registry does not copy or serve base-layer bytes. The worker releases
the metadata lease on every cache-miss return path, while cache hits skip this bridge
entirely. Registry credentials remain host-side for the SDK call and are not passed to the
guest.

From the repository root, compile the guest agent and pass its absolute path to the driver:

```sh
set -eu
agent_tmpdir="$(mktemp -d)"
trap 'rm -rf "$agent_tmpdir"' EXIT
GOMAXPROCS=2 CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -p=1 -trimpath -o "$agent_tmpdir/studio-agent" ./cmd/studio-agent
GOMAXPROCS=2 go run -p=1 ./spikes/template-jobs -agent "$agent_tmpdir/studio-agent"
```

`-scratch` optionally selects an existing parent directory for the private state root; it
defaults to `os.TempDir()`. The default driver creates a new private Store, environment,
registry storage tree, loopback registry listener, local fake sealer, and guest-agent mount
there. It uses only the dummy `TOKEN` placeholder. The default path does not open the OS
keychain, connect to production Studio or the gateway, create approvals, or autoapprove
network access. Each build sandbox receives unreachable DNS and proxy endpoints at
`127.0.0.1:9` and a random host-only password environment variable. Empty `apt` and `tools`
lists keep the default run independent of package and tool downloads.

The worker runs one job at a time with 1 CPU, 512 MiB initial memory, a 1024 MiB maximum,
and 1024 MiB workspace and Docker disks. The first job checks its login-shell environment,
CA files, and dummy placeholder, then writes a unique marker to
`/home/agent/template-build-proof`. After publication the driver inspects the OCI layer for
that proof and the managed mise login profile. It also verifies that the exporter omitted
`workspace`, `/tmp` mise cache state, runtime CA/environment files, and apt cache paths.

The next job submits canonically equivalent YAML and must get a new job ID, the same
`TemplateID`, and a cache-hit log without build-sandbox stages. A bounded log wait allows the
worker's collector to flush after the ready state is committed. The probe also checks that
the cache job did not increment the manager's egress attach counter; base prewarming does not
attach build-sandbox egress. After the cache hit, the manager is connected to the same
private registry and creates two named instances from the ready template, one at a time.
Each must retain the build's root proof and match the template's resources and ID. The probe
explicitly waits for readiness and pushes a distinct placeholder and the private environment
CA. A root command checks that placeholder, the installed trust, and the managed mise login
profile, then changes the root proof and writes markers to `/workspace` and
`/var/lib/docker`. The first instance is removed and its VM absence verified before the
second is created; the second must see the original root proof and neither data marker.
While the first instance pins the template, registry deletion must return a conflict and
the template artifacts must remain resolvable. Cleanup locates rows only by the unique
private environment, instance name, and template ID, verifies all normal Studio labels
plus `studio.template-id`, and then removes the VM and catalog row. A cancellation probe
waits for its setup marker before calling `Worker.Cancel`, then waits for cancellation and
owner cleanup. An `exit 7` failure probe runs only when fewer than five minutes have
elapsed; the whole driver has a six-minute deadline. The registry HTTP handler is started
on its private ephemeral listener and checked with credentials resolved only for this
temporary environment.

The driver snapshots `ss-` VM names before and after to detect new lingering names. It does
not pass unrelated names to any mutating API. The worker and its recorded owner methods
clean up build VMs. The instance probe uses the manager to create instances, runs assertions
only against the exact instance VM, and verifies its catalog identity and Studio labels
before owned removal. If worker shutdown, a native runtime command, or recorded cleanup is
still pending, the private root is retained with its Store and registry state for recovery.
Otherwise, the driver removes the temporary root after verifying that no new prefixed VM
name remains.

## Opt-in network-install qualification

The opt-in path preserves the default no-network path and requires explicit
`-network-installs`. A full Linux amd64 warm-cache retry passed; its apt/mise approvals,
published layer, cache reuse, and fresh-instance checks are recorded below. Subsequent live
runs installed tree and Node but failed during export: the host received
`read export frame: unexpected EOF` after 38.959, 38.287, and 35.802 seconds. Captured guest
diagnostics identify `write guest export: write export data frame: connection write timeout
(watchdog cleanup: exit status 1)`. An experimental normalized-tar staging change reproduced
the failure and did not establish a fix. The captured kernel tail contains no OOM-killer
entry, but does not prove the absence of OOM. The earlier pass remains valid, but does not
establish reliable export.

## Follow-up transport probes — 2026-10-09

An unfrozen 128 MiB raw-vsock/yamux probe failed in both modes: raw delivered 0 bytes to the
host after the guest sent 262,112 bytes, yamux delivered 12 payload bytes, and runtime logs
reported `BufDescTooSmall`. This demonstrates a lower-transport failure in that large-call
probe, not the full template-export cause. Evidence: `/tmp/studio-m3-next/vsock-bulk-live.log`
and `/tmp/studio-m3-next/vsock-bulk-runtime.log`.

The earlier uncapped diagnostic had separate stack captures. At the 8-second guest capture, a
raw-vsock `Write` call length was 32,728 bytes; this does not show that the call blocked for
8 seconds. At the 15-second host capture, a yamux DATA frame had 204 bytes remaining; those
bytes were not remaining in the raw guest `Write`. These captures are from the uncapped
diagnostic, not the subsequent 16 KiB cap run.

The separate full-worker export with raw writes capped at 16 KiB still ended with host EOF at
37.788s and a guest write timeout. Its 5,364-byte runtime log capture had no `WARN`,
`ERROR`, or `BufDescTooSmall` entries. Evidence:
`/tmp/studio-m3-next/network-chunks-runtime-live.log` and
`/tmp/studio-m3-next/network-chunks-runtime.log`.

The later capped two-phase bulk diagnostic failed in both raw and yamux modes. It changes both
the write cap and ACK order from the earlier ACK-after-FIN baseline, so it does not isolate a
cap effect; detailed counts and logs are in the [vsock bulk probe record](../vsock-bulk/README.md).
This is separate from the full template-worker 16 KiB cap failure above, which remains
unresolved. Production transport is unchanged. The private probe VMs were removed, and no
existing user VM was targeted.

```sh
set -eu
agent_tmpdir="$(mktemp -d)"
trap 'rm -rf "$agent_tmpdir"' EXIT
GOMAXPROCS=2 CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -p=1 -trimpath -o "$agent_tmpdir/studio-agent" ./cmd/studio-agent
GOMAXPROCS=2 go run -p=1 ./spikes/template-jobs -agent "$agent_tmpdir/studio-agent" -network-installs
```

It submits one 1-CPU build with 512 MiB initial memory, 1024 MiB maximum memory, and 1 GiB
workspace and Docker disks. The build installs `apt: [tree]` and the exact mise tool pin
`node: "22.14.0"`; the setup log records the observed `tree` package and command versions,
installed `mise` version, Node version, and inspected base digest. The OCI capture is capped
at 512 MiB input and output. The whole qualification has a 20-minute context deadline and
skips the already-qualified cancellation and exit-7 probes. The same canonical request is
repeated once to check the warm template cache without rerunning apt or mise, then one fresh
instance verifies its inherited proof, current private CA and placeholder, and executes
`tree` plus Node through the agent login profile. Fresh-instance verification does not run
the build approval controller and does not autoapprove gateway TCP connections.

This mode creates its own private Store, environment, gateway key, policy engine, gateway,
resolvers, CA, registry, and approval records under the temporary root. The gateway SOCKS
listener and each resolver use dynamically selected loopback ports. Resolver startup binds
both UDP and TCP; collisions are rolled back and retried a bounded number of times. Approval
polling reads only the private environment and validates that each approval belongs to the
active durable build job and its same sandbox. After observing a held connection for at least
500 ms, it can grant only these exact host/port pairs, scoped to that builder sandbox:

| Host | Ports |
| --- | --- |
| `deb.debian.org` | 80, 443 |
| `security.debian.org` | 80, 443 |
| `download.docker.com` | 443 |
| `nodejs.org` | 443 |
| `mise-versions.jdx.dev` | 443 |
| `mise.jdx.dev` | 443 |

The first live probe refused `mise.jdx.dev`. The TLS observation identified the host, not
the exact URL; this host was added on port 443 after review of the [official mise security
documentation](https://github.com/jdx/mise/security), which identifies it as hosting project
assets and the `VERSION` used for occasional update checks.

The fixture uses the dummy `TOKEN` placeholder and private generated gateway/registry
credentials; it does not access real user credentials or a host keychain. Guest resource,
capture-size, and total runtime-duration bounds constrain this probe, but it does not qualify
host-gateway connection or traffic resource limits under adversarial load.

The successful retry inspected base `sha256:99c7dca226e66d0cf26b8b75469dcb59b05b9d306b9858b8120873f6f241c29b`
and observed `tree` package `2.2.1-1`, mise `2026.10.4 linux-x64 (2026-10-07)`, and Node
`v22.14.0`. The same connection ID for each observed connection was seen pending and then
allowed: `deb.debian.org:80`, `download.docker.com:443`, `mise-versions.jdx.dev:443`,
`nodejs.org:443`, and `mise.jdx.dev:443`. The published layer retained tree and Node; the
canonical warm-cache repeat made no build-sandbox attach. A fresh instance ran both tools with
the current private CA and placeholder, and no gateway TCP connection was observed from it.
This assertion does not claim that the instance produced no network traffic; DNS forwarding
via `dnsproxy.SystemUpstreams()` is outside the approval flow and was not qualified. Cleanup
completed, no new VM names remained, and private state was removed. Later failures are
described above; the successful run does not establish reliable export.

There are no wildcard or all-host grants. In this probe, an unlisted outbound TCP destination
through the gateway fails qualification: its pending approval is not granted, the private
test build is canceled, and the host is reported for review. This does not apply to DNS
forwarding, which is not approval-gated or qualified here. The qualification requires at
least one connection with the same ID observed pending and then open or allowed under the
exact sandbox rule, and no pending private approvals at successful finish. Gateway connection
evidence is copied before per-sandbox detach forgets it. The gateway handlers are drained
with a deadline before the private Store closes; unresolved ownership retains the private root.

The base image source in `images/base/Dockerfile` installs mise from `https://mise.run`
without a version pin, and its apt package sources are refreshed during builds. The Node tool
pin is exact; the apt `tree` package and base-image mise release are not reproducible pins, so
their observed versions and the inspected base digest must accompany any qualification result.
The base Dockerfile cleans apt metadata, and the exporter excludes `/tmp`, apt lists, and apt
archives. It does not exclude `/usr/bin/tree` or
`/home/agent/.local/share/mise/installs`; the opt-in layer inspection and fresh-instance
commands verify those installed files survived. Any unlisted redirect host needs source and
destination review before the exact allowlist is changed.

## Observed qualification — 2026-10-09

The production worker passed on Linux amd64 with a warm cache, private isolated Store and
registry, and a 512 MiB builder, with no package or tool network access. The fresh template
build verified setup environment, dummy placeholder, persistent proof, login profile, and
excluded runtime state. The private loopback registry HTTP handler authenticated the
private environment. A canonical-equivalent request created a new job, reused the same
`TemplateID`, and made no build-sandbox attach. Cancellation after observing the setup
marker and an `exit 7` build both reached their expected statuses (`cancelled` and
`failed`) and completed owned sandbox cleanup. The worker stopped with no runtime command
or owner cleanup pending; no new prefixed VM names remained after cleanup, and private
qualification state was removed.

The extended probe also passed on 2026-10-09: two sequential template instances retained
the built root proof and resource sizes, received distinct identities and credentials, and
accepted current CA and placeholder configuration. The second instance inherited neither
the first instance's root changes nor its workspace/Docker markers. A live instance pin
blocked registry deletion, and both instances completed owned cleanup.

Rebase, apt/mise network-install export-transport qualification, cold-cache pulls,
macOS and Windows host VM runs, arm64, and the release template base digest pin remain
open, so M3 is incomplete. UI validation used mocked desktop/mobile workflows and web checks;
no live-worker UI build or workflow was run. Restore remains blocked by microsandbox
[#1736](https://github.com/superradcompany/microsandbox/issues/1736).
