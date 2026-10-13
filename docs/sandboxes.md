# Sandboxes and runtime

A sandbox is a microsandbox microVM that persists across stop and start. Code:
`internal/runtime` (the only package that imports the microsandbox Go SDK, which needs CGO),
`internal/sandboxes` (lifecycle, catalog, agent channel), `internal/resources` (limits).

## Identity and generations

- A Studio sandbox has a stable ID. Its VM is named `ss-<id>-g<generation>`
  (`sandboxes.VMName`); every VM Studio owns starts with `ss-`.
- Rebase (and restore, once enabled) creates generation `n+1` and retires `n`. The UI keeps
  one identity.
- Catalog rows also hold template build VMs and teammates' browser VMs. Only ordinary
  sandboxes appear in the sandbox API (`Manager.Public`).
- On start, `Manager.Reconcile` re-attaches agent listeners and recovers interrupted imports,
  rebases and restores. Detached VMs kept running meanwhile.

## Storage and mounts

| Path | What |
|---|---|
| `/` | the template's layered root; persists across stop and start |
| `/workspace` | owned disk, included in checkpoints |
| `/home/agent` | owned disk, 4 GiB, included in checkpoints; the agent user's home |
| `/var/lib/docker` | owned disk, so Docker's overlay isn't nested on the root overlay |
| `/opt/studio` | read-only bind of `<data>/guest`; the agent is `/opt/studio/bin/studio-agent`, so updating Studio updates every guest |
| `/opt/mise` | template toolchains and mise config, on the root layer; they are not in the home |

Owned (not named) volumes, because disk snapshots capture owned volumes and skip named ones
(spike S5).

The home disk is seeded once, on the sandbox's first boot, from the skeleton the image keeps
at `/usr/share/sandbox-studio/home-skel` (the base image's home, or the template's home as
its last build step left it). Nothing seeds it again, so files you delete stay deleted. Its
size is fixed and not a setting. Browser VMs and template build VMs have no home disk.

A sandbox made from a template built before the home skeleton (exporter version 1) has no
home disk: its home stays on the root layer next to the toolchains that template keeps
there, and its forks stay that way. Rebuild the template to get a persistent home directory.
Rebasing such a sandbox onto a current template gives it a home disk and copies its home
over. Rebasing a sandbox that has a home disk onto a template without one is refused.

## Network

Fixed at creation (`runtime.Create`):

- egress: DNS to the sandbox's own resolver, TCP to public addresses only; everything else is
  denied (UDP and ICMP would bypass the gateway; QUIC falls back to TCP);
- TCP goes to the gateway as SOCKS5, user = sandbox ID, password from an env var;
- a vsock route from the host socket `<data>/run/<id>.sock` to guest port 5000 for the
  [agent channel](guest-agent.md).

See [network](network.md) for what happens at the gateway.

## Boot and shutdown

- No init system. microsandbox's agentd stays PID 1, which lets msb freeze the guest for live
  checkpoints. With an init handoff (systemd) msb 0.7.7 refuses that.
- After each boot Studio runs `studio-agent boot` as root. It prepares the disks, seeds the
  home disk if it is new, and starts a supervisor for containerd, dockerd and the agent,
  restarting them with backoff. It is a no-op if systemd is PID 1 (older sandboxes) or a
  supervisor already runs.
- Before a stop Studio runs `studio-agent shutdown`, because msb gives the guest two seconds
  before it kills every process. Stop resumes a suspended VM first.

Runtime statuses: `absent`, `created`, `starting`, `running`, `draining`, `suspended`,
`stopped`, `crashed`. The API adds `creating` and `failed` for a create or fork (below).

A sandbox view's `startedAt` is when its VM last started: a create, fork, template create,
import, transfer, rebase, start or resume that Studio completed. It is kept in the catalog,
so Studio restarts don't change it, and it stays after a stop. Show uptime only while the
sandbox runs. Sandboxes started before this field existed have none until their next start.

## Creation progress

A create, a create from a template and a fork return at once with the sandbox in state
`creating`. Its VM is pulled, started and booted in the background (`Manager.bootSandbox`,
`Manager.Fork`). The sandbox view's `progress` names the step it is on:

| Step | Meaning |
|---|---|
| `pull` | the image; the view carries layers and bytes while the pull reports them |
| `vm` | the VM starts |
| `copy` | the workspace copy (fork only) |
| `boot` | `studio-agent boot` runs |
| `services` | the supervisor starts containerd, Docker and the agent |
| `agent` | the agent answers requests |
| `configure` | the guest acknowledges its CA and secret placeholders |

Every change publishes the `sandboxes` topic on `/api/events`, so open views refetch.

- A create or fork that fails keeps its record in state `failed`, with the step and the
  error. Its VM is removed. A failed sandbox can't be started; deleting it is the way out.
  The error of a create from a template never carries the private image or credential.
- A sandbox whose create or fork runs is busy: lifecycle actions, and forks and rebases of it,
  are refused until the operation ends.
- While a create or fork runs, Studio saves its progress under `sandbox.progress.<id>` in
  the settings. On start, `Reconcile` fails each create or fork that was still running, with
  the error "Studio stopped before this step finished", and removes its VM. So no sandbox
  stays `creating` after a restart.
- Rebase is not part of this: it still runs in the request.

## Resources

Defaults: 2 CPUs, 4 GiB memory, 20 GiB workspace, 20 GiB Docker disk. The 4 GiB home disk is
fixed. `max_memory` defaults to the initial memory and is passed as the VM's memory ceiling.
A template's spec fixes the resources of sandboxes created from it, and a rebase builds the
new generation from the template, so it resets the CPUs and memory as well.

`PUT /api/environments/{env}/sandboxes/{id}/resources` with `{"cpus", "memoryMiB"}` sets the
CPUs (1–64) and the memory size (at least 512 MiB). The sandbox dashboard's Resources dialog
calls it. Studio asks msb's `modify` for a dry run and applies each changed resource as the
plan says:

- **live**: a running sandbox's VM takes the change now. msb allows this up to the VM's
  boot-time capacity: its memory ceiling, and its CPU maximum, which Studio sets to the CPU
  count the VM boots with.
- **nextStart**: the change is saved in the sandbox's config and used at its next start, and
  the running VM is left alone. Stopped and suspended sandboxes always get this, and so does a
  running sandbox's change above its boot-time capacity.

The response's `applied` says how each resource changed: `live`, `nextStart` or `unchanged`.
Live changes are made one resource per call, and the rest in one call with msb's no-restart
policy, so a resize never restarts a running VM. A memory size above the ceiling raises the
ceiling to match, as msb requires. If a step fails, the steps before it stay in effect, and the
catalog records what took effect.

Verified vs. pending: the Studio side is covered by fake-runtime and API tests. Nothing has run
against a live VM yet. A live run should check that a CPU decrease and a memory decrease apply
to the running VM without a restart, and that a growth above the boot-time capacity reports
`nextStart` and takes effect at the next start.

## Checkpoints

- A checkpoint is a disk snapshot in the group `sbx-<id>`: root, workspace, Docker and home
  disks, of a running or a stopped sandbox.
- A running guest is frozen while msb flushes its filesystems, so the capture is
  crash-consistent at the filesystem level, not at the application level.
- Sandboxes created with systemd as init must be stopped first
  (`ErrLiveCheckpointUnsupported`).
- Deleting a checkpoint moves the group head to a surviving member first; Studio never
  forces removal. A checkpoint with dependent newer ones answers with a conflict.
- A checkpoint can be renamed (`PUT .../checkpoints/{checkpoint}` with `{"name"}`). Its
  name is a catalog label; the disks are not touched.

**Restore is disabled.** With microsandbox 0.7.7 a restored VM gets msb's default network
(no resolver, no gateway proxy); restore options can't set either
([upstream #1736](https://github.com/superradcompany/microsandbox/issues/1736)).
`Runtime.CheckpointRestoreSupported` returns false, and the UI reads it from the sandbox
view. The restore path is implemented and tested with fakes: it needs a stopped sandbox,
restores a stopped replacement VM as a new generation, adopts it in the catalog, then
removes the old VM; a durable record lets startup finish or discard an interrupted restore.
As a guard, Studio refuses a candidate whose stored network config differs from Create's.

On 2026-10-13 the gate is still closed, and no release of the pinned microsandbox can open it.
[Upstream #1736](https://github.com/superradcompany/microsandbox/issues/1736) was filed on
2026-10-02 and is still open, with no pull request linked to it. On 2026-10-02 a maintainer
said they were leaning towards exposing the flags on `msb restore` rather than on
`msb run --from-sandbox`, and that a fix was in progress. Neither released version carries it:
msb 0.7.7 (pinned) and 0.7.8 (2026-10-09) have the same restore surface. The SDK's restore takes
a network policy with rules and default egress and ingress only, and the Rust `RestoreBuilder`
has no setter for a proxy or a DNS resolver. The 0.7.9 release bump (#1829) is still open. So
there is nothing to bump yet. When a release adds restore options for the proxy and resolver,
the bump updates the `sdk/go` module in `go.mod` and `go.sum` and the msb runtime that
`runtime.Ensure` installs. Then `CheckpointRestoreSupported` can return true and
`RestoreCheckpoint` must pass Studio's egress to the restore. `checkEgress` and its tests stay
as the guard.

## Lineage

Each sandbox keeps its history in `sandbox_events` (`store.SandboxEvent`): one row per
successful step, in order. The kinds are `create` (also an import), `fork`, `rebase`,
`restore`, `checkpoint` and `delete`. A failed operation writes nothing. Code:
`internal/sandboxes/lineage.go` and `internal/store/lineage.go`.

- A fork refers to its source by ID, generation and time (`forked_from_*` on the sandbox,
  with no foreign key). The fork records a `fork` event on each side: the child's history
  starts with it, and the source's shows the child it branched off to. Both are recorded once
  the copy has booted.
- A rebase or restore records its event in the same transaction as the generation switch
  (`CommitRebase`, `CommitRestore`), so it is recorded once, even when cleanup or recovery
  runs later.
- A delete records a tombstone: the event keeps the sandbox's name, template and fork
  source. A deleted sandbox's history stays, and its forks stay under it in the family tree.
  A sandbox whose create failed leaves no tombstone.
- Deleting a checkpoint records nothing. A checkpoint's history is its `checkpoint` event.
- Migration 023 backfills a `create` event for each existing sandbox (its template is known
  only at generation 1) and a `checkpoint` event for each ready checkpoint. Forks made
  before it have no source reference.

The API: `GET /api/environments/{env}/sandboxes/{id}/lineage` gives the events, the parent,
the children (live or tombstoned), the checkpoints with `restoreAllowed` (the runtime's
restore gate, see above, and the checkpoint being ready), and `checkpointRestoreSupported`.
`GET /api/environments/{env}/lineage` gives the family tree: sandboxes grouped by template,
each fork under its source. A node's `status` is `running`, `waiting` (still starting, or a
create or fork that runs), `failed`, `deleted`, or another runtime status.

The web sandbox page draws the history as a timeline, and Home shows the family tree.

## Suspend and resume

An in-place pause (msb pause/resume): vCPUs stop, memory stays with the VM process, and
resume continues every process, TUIs and tmux included. It frees no memory and doesn't
survive a host reboot. The agent session is dropped on suspend and reconnects on resume.
Fork, rebase and checkpoints refuse a suspended sandbox; Stop and Delete resume it first.
A resize of a suspended sandbox is saved for its next start (see [resources](#resources)).

Suspend to disk (a full memory snapshot, then the VM's process freed) is not built. A memory
snapshot resumes only through restore, and restore can't set the sandbox's gateway proxy or DNS
resolver (see [checkpoints](#checkpoints)). Studio never restores a VM without them, so the
feature waits for #1736. The restore would also need the source's vsock route, which
[spikes](spikes.md#s5--volumes-snapshots-and-forks) records as a device-layout requirement.
Studio therefore keeps suspend in place.

## Fork, rebase, export and import

- [Fork and rebase](rebase.md): the workspace is copied as a gzip tar over msb exec between
  two VMs, and the home too where there is a home disk. Docker data is not copied.
- [Export and import](export-import.md): a `.studio-sandbox` file (manifest, home tar and
  workspace tar), recreated through a template build and the same copies.

## SSH access

microsandbox serves a running VM over SSH with its own `msb ssh` commands (msb 0.7.7).
Studio neither starts nor stops them: you run them yourself, for example for an editor that
connects over SSH, such as VS Code Remote-SSH.

1. **Find the VM.** Studio names a sandbox's VM `ss-<id>-g<generation>`, where `<id>` is the
   sandbox ID shown in its workspace status line and the generation is 1 until the sandbox is
   rebased. `msb list` shows the names (every VM Studio owns starts with `ss-`). Studio shows
   the name nowhere yet, so it is not in the API or the UI.
2. **Authorize your key.** `msb ssh authorize --file ~/.ssh/id_ed25519.pub` adds your own
   public key to microsandbox's SSH authorization. Use your own key only. Studio's keys and
   secrets never go into a sandbox; an SSH login shell gets the same placeholders as a terminal
   (see [guest agent](guest-agent.md#ca-trust-and-placeholders-in-the-guest)).
3. **Connect.** `msb ssh connect ss-<id>-g1` opens a shell. For Remote-SSH, add a host to
   `~/.ssh/config` that serves the connection over stdio:

   ```
   Host studio-<id>
       HostName ss-<id>-g1
       ProxyCommand msb ssh serve --stdio ss-<id>-g1
       IdentityFile ~/.ssh/id_ed25519
   ```

   `msb ssh serve --port <port> <name>` listens on a host port instead, for clients that can't
   run a ProxyCommand.

SSH sessions run inside the sandbox's VM, so the network policy applies to them as it does to
terminals: egress is fixed when the VM is created, and only what it allows gets out (see
[network](network.md)).

Verified vs. pending: the commands and flags come from `msb 0.7.7 --help`. Nothing here has
been run against a live VM. The login user, the default port and the `ProxyCommand` form are
unverified until someone connects once.

## Base image

`images/base`: Debian trixie with Docker CE (containerd, buildx, compose), git, tmux, Node,
build tools and Python, plus pinned mise, OpenCode, Claude Code, Codex and their ACP
adapters (versions in the Dockerfile and [harnesses](harnesses.md#versions)). User `agent`
(uid 1000, sudo, docker). It creates `/opt/mise` for template toolchains and keeps the
agent's home as the skeleton that home disks are seeded from. No init system. Releases
publish it to `ghcr.io/lukaskoebe/sandbox-studio-base` and pin it by digest
(`version.BaseImage`); dev builds use `sandbox-studio-base:dev` from `make image`. Templates
build on top of it: [template builds](template-builds.md).
