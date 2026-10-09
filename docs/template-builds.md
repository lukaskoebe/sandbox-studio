# Template builds

This guide describes the build API, worker, and Builds page. The page passed TypeScript,
lint, production-build, and desktop/mobile browser checks with simulated jobs. A completed
job publishes an environment-scoped, content-addressed template for later sandbox creation.

## Submit and follow a build

The source is a JSON string containing one YAML document. Submission validates and stores
the request, then returns HTTP `202 Accepted`; VM creation and build commands run in the
worker. The routes are scoped to an existing environment:

| Method | Route | Result |
|---|---|---|
| `GET` | `/api/environments/{env}/builds?limit=50` | Recent job summaries |
| `POST` | `/api/environments/{env}/builds` | Accept a build; HTTP 202 |
| `GET` | `/api/environments/{env}/builds/{id}` | Job and submitted YAML source |
| `POST` | `/api/environments/{env}/builds/{id}/cancel` | Request cancellation and return job state |
| `GET` | `/api/environments/{env}/builds/{id}/log` | Bounded log snapshot and truncation flag |

Example source:

```yaml
resources:
  cpus: 1
  memory: "512MiB"
  max_memory: "1024MiB"
  workspace: "1024MiB"
  docker: "1024MiB"
tools:
  node: "22"
apt:
  - postgresql-client
setup: |
  corepack enable
```

`resources` accepts `cpus`, `memory`, `max_memory`, `workspace`, and `docker`. CPU
counts are plain decimal integers. Memory and disk sizes are strings in `MiB`, `GiB`, or
`G`; Studio validates the resolved values against shared limits before persisting the
job. If omitted, resources use Studio defaults, and `max_memory` defaults to `memory`.
The example uses `MiB` strings to make resource units explicit.

The supported top-level keys are `resources`, `tools`, `apt`, and `setup`. Specs must be
one YAML mapping, at most 64 KiB, with valid UTF-8. Unknown or duplicate keys, aliases,
merge keys, custom tags, multiple documents, and invalid resource values are rejected.
Tool pins are currently limited to `node`, `python`, and `go`; versions must be quoted
numeric dotted strings, for example `node: "22"`. Apt entries are bounded package names.
The decoded setup string is preserved, up to 64 KiB, and cannot contain NUL.

An equivalent active request in the same environment reuses its existing job. The match
uses canonical spec, configured base reference, target platform, and exporter version, so
YAML comments, whitespace, and key order do not create a different request. Deduplication
applies only while the job is queued or running. A terminal request can be submitted again
as a new job; the worker may then reuse a ready template from cache.

Invalid specs return HTTP `422`; a missing environment or job returns `404`; a conflicting
state or non-equivalent active request returns `409`. The list limit defaults to 50 and is
capped at 100.

## Job lifecycle and cleanup

The public states are `queued`, `preparing`, `setting_up`, `exporting`, `ready`, `failed`,
and `cancelled`.

| State | Worker activity |
|---|---|
| `queued` | Waiting for the worker to claim the job |
| `preparing` | Preparing and inspecting the base, then resolving the cache key |
| `setting_up` | Booting a temporary builder and running apt, mise, and setup stages |
| `exporting` | Exporting the root layer, composing and publishing the template |
| `ready` | A ready template is linked to the job |
| `failed` / `cancelled` | Terminal result; temporary VM cleanup may still be pending |

Job status is separate from ownership of temporary prewarm and builder VMs. The API
exposes `cleanupPending` and `cleanupError` because a terminal or ready job can still own
a VM that needs removal. Studio records each owner's identity before creating its VM and
keeps that record until removal has been verified. Pending cleanup blocks another build
from allocating a VM. A ready job can therefore have `cleanupPending: true` while the
worker finishes removing its builder.

Cancellation records the cancelled state before signaling the active operation. The API
does not wait for an SDK call that has not returned. The worker then makes a bounded
cleanup attempt; if removal cannot be confirmed, the ownership record remains for retry.
Cancellation does not make an already-published template unusable: a complete cache entry
may be reused by a later request.

Logs are drained without blocking command output. Chunks can be dropped if the bounded
collector queue is full, and persisted output is limited to 1 MiB of valid UTF-8. The log
response's `truncated` field remains true if any output was dropped or truncated.

## Worker, base image, and cache

For a file-backed catalog, only one worker may operate on it at a time. At the start of
`Worker.Run`, before startup recovery, Studio takes a nonblocking OS lock associated with
the resolved database file. A conflicting process gets `ErrConflict` and must not recover
or claim jobs. The worker keeps this lock while `runtime.Run` has native SDK activity
pending. In-memory catalogs use a per-Store mutex instead of a cross-process lock.

On startup, queued jobs remain eligible for normal processing. An interrupted active job
is failed with a restart message; apt, mise, or user setup commands are never replayed.
If a job reached `exporting` and its ready cache entry and registry artifacts are present,
recovery verifies them and repairs the job to `ready`. Temporary VM ownership remains
separate and is cleaned up afterward.

The worker takes its base reference only from Studio runtime configuration. The exact
`sandbox-studio-base:dev` alias is accepted for local development. Other references must
be canonical OCI repository references pinned by lowercase `sha256` digest. Base
preparation records its temporary VM owner before SDK creation, uses a deny-all 512 MiB
prewarm VM, and inspects the actual resolved digest and platform through the SDK. A
release default digest pin is not configured yet; release wiring must provide a controlled
digest-pinned base before release template builds are enabled. This applies to template
builds; ordinary Studio application releases are unaffected.

The cache key includes the canonical spec, inspected base digest, platform, and exporter
version. On a cache hit, the worker still prepares and inspects the base, then verifies
the environment-scoped ready template and registry artifacts. It skips builder creation
and does not run apt, mise, or setup again. For a miss, Studio composes deterministic base-only metadata from that inspection and
serves it through an authenticated, environment-scoped temporary registry lease. The builder
pulls that metadata by digest, reusing the immutable layers already in microsandbox's cache.
This avoids relying on a mutable development alias during builder creation. The temporary
lease contains only manifest and config files; it creates no template catalog record and
is released after the build. This uses the same controlled-base configuration subset as
derived templates, not a lossless arbitrary OCI mirror.

## Commands and native runtime behavior

After boot, the worker waits for the guest agent, then calls `ConfigureGuest`. It does not
run any build command until the guest acknowledges CA and placeholder installation. The
commands run in order:

1. apt update/install as `root` (apt cache is cleaned afterward).
2. mise installs as `agent`, using the pinned tools in `/home/agent`.
3. The user `setup` command as `agent`, with `/home/agent` as its working directory.

After the commands succeed, the worker exports and validates the root layer, composes and
publishes the derived image through the persistent authenticated registry, and marks the
job ready. Cleanup can finish after that status transition.

`runtime.Run` requests bounded startup, cancellation, output, and cleanup handling, but
the microsandbox FFI may continue a native call after its Go context is cancelled. The
runtime retains its slot while known native calls or cleanup remain active. If canceled
`ExecStream` may have registered a native handle but returned no handle to Go, Studio
cannot prove that it stopped: the runtime quarantines the slot and the worker retains the
OS lock until SDK completion can be confirmed or the process restarts. There is no
age-based lock expiry or automatic takeover of a possibly live worker.

## Current qualification boundary

On 2026-10-09, the production worker passed a live Linux amd64 qualification with a warm
cache, private isolated Store and registry, and a 512 MiB builder; no package or tool
network access was used. The fresh build verified its setup environment, dummy
placeholder, persistent proof, login profile, and excluded runtime state. The private
loopback registry HTTP handler authenticated the private environment. A
canonical-equivalent request created a new job, reused the same `TemplateID`, and made
no build-sandbox attach.
Cancellation after observing the setup marker and an `exit 7` build that reached failed
status both completed owned sandbox cleanup. The worker then stopped with no runtime
command or owner cleanup pending; no new prefixed VM names remained, and private
qualification state was removed after cleanup. See the
[live worker record](../spikes/template-jobs/README.md).

UI validation used mocked desktop/mobile workflows and web checks; no live-worker UI
build or workflow was run. Earlier Linux amd64 registry and exporter/source-import
qualifications remain in the [M3 spike report](spikes.md#persistent-registry-qualification-2026-10-09)
and its [layer-transfer results](spikes.md#layer-transfer-and-capture-foundations).
M3 remains incomplete: the fresh-template instance API and rebase, YAML file
import/export, apt/mise network installs, cold-cache base pulls, macOS and Windows host VM
runs, arm64, and the release template base digest pin remain open. Future template
instances will use fresh creation through the normal runtime path. Restore remains blocked
by microsandbox [#1736](https://github.com/superradcompany/microsandbox/issues/1736).
