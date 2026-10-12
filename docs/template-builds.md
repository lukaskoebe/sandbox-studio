# Template builds

This guide describes the build API, worker, and the Templates page (Settings → Templates). The page passed TypeScript,
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
| `POST` | `/api/environments/{env}/builds/validate` | Check a source without creating a job; HTTP 200 |
| `GET` | `/api/environments/{env}/builds/{id}` | Job and submitted YAML source |
| `POST` | `/api/environments/{env}/builds/{id}/cancel` | Request cancellation and return job state |
| `GET` | `/api/environments/{env}/builds/{id}/log` | Bounded log snapshot and truncation flag |
| `GET` | `/api/template-spec/vocabulary` | Keys, tool names, and size units the parser accepts |

Validation runs the same strict parser and size limit as submission, but it creates no
job and needs no build backend. Unlike submission, it returns HTTP `200` for invalid specs
too, with `ok: false` and a `problems` list of at most 50 entries. Each problem has a
1-based `line` and `column`, where columns count characters. A `column` of `0` means only
the line is known; `line` and `column` of `0` mean the problem concerns the whole source.
The vocabulary route is static. The editor uses it for suggestions, and a Go test checks
that each suggested name parses.

Example source, the Web app template that new builds start from. The file
`web/src/lib/example-template.yaml` is the single source: the web client imports it, and a
Go test parses it.

```yaml
# Web app: Node.js and Python with the PostgreSQL client.
resources:
  cpus: 2
  memory: 4GiB
  workspace: 20GiB
  docker: 20GiB
tools:
  node: "22"
  python: "3.13"
apt:
  - postgresql-client
setup: |
  corepack enable
```

`resources` accepts `cpus`, `memory`, `max_memory`, `workspace`, and `docker`. CPU
counts are plain decimal integers. Memory and disk sizes are strings in `MiB`, `GiB`, or
`G`; Studio validates the resolved values against shared limits before persisting the
job. If omitted, resources use Studio defaults, and `max_memory` defaults to `memory`.

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

## Edit the spec in the editor

The Builds dialog edits the spec in a CodeMirror YAML editor that uses the Caddyfile
editor's theme. It highlights the YAML and suggests top-level and resource keys, tool
names, size units (`MiB` and `GiB`), and apt package names from a short built-in list.
About 400 ms after typing pauses, it sends the draft to the validate route and marks each
problem at its line and column. Checks create no job and run no commands.

**Insert example** replaces the draft with the Web app example; an empty draft offers the
same action. Undo brings back the text from before the insert.

## Import and export YAML source

In the Builds dialog, **Import YAML** reads one `.yaml` or `.yml` file into the draft; it
does not submit or run a build. Files must be at most 65,536 bytes, contain strict UTF-8,
and not be empty or contain NUL. A rejected or unreadable file leaves the existing draft
unchanged. Import does not submit a build: the editor checks the loaded draft, and
**Build template** submits it explicitly.

An unedited import keeps its UTF-8 BOM, comments, and line endings byte for byte. Editing
keeps CRLF line endings; a file with mixed line endings becomes all CRLF once edited. **Download
YAML** saves the current draft in the dialog, or the source submitted for that job in a job
sheet, as `spec.yaml`. It does not export a normalized spec, logs, or template artifacts;
the source can still contain literal secrets or other values entered by the user. To
rebuild in another environment, import the source there, review it, and explicitly build
it. The download does not include the environment's secret vault, built images, or
persistent disks.

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
prewarm VM, and inspects the actual resolved digest and platform through the SDK. Release
builds set the base to the digest-pinned image they publish (`version.BaseImage`, see
[release](release.md)); a template build on a release base has not been run live.

The cache key includes the canonical spec, inspected base digest, platform, and exporter
version. Exporter version 2 moved the toolchains to `/opt/mise` and added the home skeleton.
Templates built with version 1 still back sandboxes, but those sandboxes have no home disk.
Rebuild the template to get a persistent home directory: build the spec again, on a base
image that has `/opt/mise` and the skeleton. On a cache hit, the worker still prepares and inspects the base, then verifies
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
2. mise installs as `agent`, using the pinned tools in `/opt/mise`. The base image creates
   that directory. Toolchains stay out of `/home/agent`, which a sandbox's home disk replaces.
3. The user `setup` command as `agent`, with `/home/agent` as its working directory.
4. As `root`, the home is copied to `/usr/share/sandbox-studio/home-skel`, owned by `agent`.
   A new sandbox's home disk is seeded from that copy on its first boot. Setup's files in
   the home therefore reach new sandboxes, and nothing else from the build does.

After the commands succeed, the worker runs `studio-agent export-layer` as root over msb
exec and streams its stdout into the untrusted-input validator, which normalizes and
compresses the root layer. Guest→host bulk data over a vsock route stalls on msb 0.7.7 and
0.7.8, so exports never use the agent channel. The export fails if no bytes arrive for
60 s or if it runs longer than the export byte limit allows at 2 MB/s (at most 2 h); its
stderr goes to the build log. The guest keeps its root filesystem frozen for the export and
receives the same time limit as `--max-freeze` (60 s by default). Its watchdog thaws at that
limit, or earlier if `export-layer` exits or dies, including after its stdout breaks. The worker then composes and
publishes the derived image through the persistent authenticated registry, and marks the
job ready. Cleanup can finish after that status transition.

`runtime.Run` requests bounded startup, cancellation, output, and cleanup handling, but
the microsandbox FFI may continue a native call after its Go context is cancelled. The
runtime retains its slot while known native calls or cleanup remain active. If canceled
`ExecStream` may have registered a native handle but returned no handle to Go, Studio
cannot prove that it stopped: the runtime quarantines the slot and the worker retains the
OS lock until SDK completion can be confirmed or the process restarts. There is no
age-based lock expiry or automatic takeover of a possibly live worker.

## Create a sandbox from a ready template

The Templates page offers **Create sandbox** for a ready build that has a template. The
dialog loads that same-environment ready template, displays its resource sizes, and sends
only a sandbox name. Resource limits are fixed by the template's canonical spec; the
instance API does not accept resource overrides.

Creation pins the ready template to the new sandbox in the catalog. The template supplies
the root image for a fresh sandbox instance, while the workspace, Docker and home disks are
new and empty, except that the home is seeded from the template's skeleton. A version 1
template has no skeleton and gives no home disk. Creation assigns the normal new sandbox and proxy identity, creates its agent
socket, and follows the ordinary guest boot/configuration path, including the environment
CA and placeholder push. It does not restore another sandbox's root, workspace, or Docker
data.

| Method | Route | Result |
|---|---|---|
| `GET` | `/api/environments/{env}/templates` | Ready templates in the environment |
| `GET` | `/api/environments/{env}/templates/{id}` | Ready template details and resources |
| `POST` | `/api/environments/{env}/templates/{id}/sandboxes` | Create from a ready same-environment template; body: `{ "name": "my-project" }`; HTTP 201 |

A template has no name in its spec or build, so Studio derives a display name, `name` on
template views and `templateName` on sandboxes. It is the spec's tool pins, such as
`node-22+python-3.13` (`base` when the spec pins no tools), then `@` and the template's
1-based position among the environment's templates with that stack, in creation order:
`node-22+python-3.13@2` is the second such template. Deleting an older template renumbers
the ones after it. A template whose spec doesn't parse has no name.

On 2026-10-09, two sequential live Linux amd64 instances preserved the built root files,
received current CA and placeholder configuration, and had independent fresh workspace
and Docker data. The second instance did not inherit root or disk changes from the first.
A live instance blocked template artifact deletion. Both instances and their catalog
references were removed through verified owned cleanup. Desktop/mobile dialog checks
used mocked creation responses; they did not launch VMs through the browser.

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
Still open: live qualification of the exec export path including apt/mise network
installs, cold-cache base pulls, a template build on a release base, macOS and Windows host
VM runs, arm64, and oversized or backpressured captures. Restore remains blocked by
microsandbox [#1736](https://github.com/superradcompany/microsandbox/issues/1736).
