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
defaults to `os.TempDir()`. The driver creates a new private Store, environment, registry
storage tree, loopback registry listener, local fake sealer, and guest-agent mount there. It
uses only the dummy `TOKEN` placeholder. It does not open the OS keychain, connect to
production Studio or the gateway, create approvals, or autoapprove network access. Each
build sandbox receives DNS and proxy endpoints at `127.0.0.1:9` and a random host-only
password environment variable. Empty `apt` and `tools` lists keep this run independent of
package and tool downloads.

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

Rebase, YAML file import/export, apt/mise network installs, cold-cache pulls, macOS and
Windows host VM runs, arm64, and the release template base digest pin remain open, so M3
is incomplete. UI validation used mocked desktop/mobile workflows and web checks;
no live-worker UI build or workflow was run. Restore remains blocked by microsandbox
[#1736](https://github.com/superradcompany/microsandbox/issues/1736).
