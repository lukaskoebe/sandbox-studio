# Sandboxes and runtime

A sandbox is a microsandbox microVM that persists across stop and start. Code:
`internal/runtime` (the only package that imports the microsandbox Go SDK, which needs CGO),
`internal/sandboxes` (lifecycle, catalog, agent channel), `internal/resources` (limits).

## Identity and generations

- A Studio sandbox has a stable ID. Its VM is named `ss-<id>-g<generation>`
  (`sandboxes.VMName`); every VM Studio owns starts with `ss-`.
- Rebase (and restore, once enabled) creates generation `n+1` and retires `n`. The UI keeps
  one identity.
- Catalog rows also hold template build VMs, teammates' browser VMs and the system VMs of
  subscription logins (`store/system.go`). Only ordinary sandboxes appear in the sandbox API
  (`Manager.Public`).
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
- a vsock route from the host socket `<data>/run/<id>.sock` (a named pipe on Windows) to
  guest port 5000 for the [agent channel](guest-agent.md).

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

A create, a create from a template, a fork and a rebase return at once with the sandbox in
state `creating`. Its VM is pulled, started and booted in the background (`Manager.bootSandbox`,
`Manager.Fork`, `Manager.Rebase`). The sandbox view's `progress` names the operation
(`create`, `fork` or `rebase`) and the step it is on:

| Step | Meaning |
|---|---|
| `prepare` | the sandbox stops and starts again for the copy (rebase only) |
| `pull` | the image; the view carries layers and bytes while the pull reports them |
| `vm` | the VM starts |
| `copy` | the workspace copy (fork and rebase) |
| `boot` | `studio-agent boot` runs |
| `services` | the supervisor starts containerd, Docker and the agent |
| `agent` | the agent answers requests |
| `configure` | the guest acknowledges its CA and secret placeholders |

Every change publishes the `sandboxes` topic on `/api/events`, so open views refetch.

- A create or fork that fails keeps its record in state `failed`, with the step and the
  error. Its VM is removed. A failed sandbox can't be started; deleting it is the way out.
  The error of a create from a template never carries the private image or credential.
- A sandbox whose create, fork or rebase runs is busy: lifecycle actions, and forks and
  rebases of it, are refused until the operation ends.
- While a create or fork runs, Studio saves its progress under `sandbox.progress.<id>` in
  the settings. On start, `Reconcile` fails each create or fork that was still running, with
  the error "Studio stopped before this step finished", and removes its VM. So no sandbox
  stays `creating` after a restart.
- A rebase runs through the steps `prepare`, `pull`, `vm`, `copy` and, for a sandbox that
  was running, `boot`. The agent then connects and is configured as after any start. A
  failed rebase rolls back ([rebase](rebase.md#rebase)), so the sandbox keeps its status;
  only its `progress` stays, in state `failed` with the step and the error, until the next
  rebase, a delete, a restart of Studio, or `DELETE …/sandboxes/{id}/progress` (the
  Dismiss button). A rebase's progress isn't saved: its own rebase record recovers it.

## Resources

Defaults: 2 CPUs, 4 GiB memory, 20 GiB workspace, 20 GiB Docker disk. The 4 GiB home disk is
fixed. `max_memory` defaults to the initial memory and is passed as the VM's memory ceiling.
A template's spec fixes the resources of sandboxes created from it, and a rebase builds the
new generation from the template, so it resets the CPUs and memory as well.

`PUT /api/environments/{env}/sandboxes/{id}/resources` with `{"cpus", "memoryMiB"}` sets the
CPUs (1–64) and the memory size (at least 512 MiB). The Resources dialog in the
sandbox's details calls it. Studio asks msb's `modify` for a dry run and applies each
changed resource as the plan says:

- **live**: a running sandbox's VM takes the change now, where msb offers it (msb 0.7.7
  doesn't, see below), up to the VM's boot-time capacity: its memory ceiling, and its CPU
  maximum, which Studio sets to the CPU count the VM boots with.
- **nextStart**: the change is saved in the sandbox's config and used at its next start, and
  the running VM is left alone. Stopped and suspended sandboxes always get this, and so does a
  running sandbox's change above its boot-time capacity.

The response's `applied` says how each resource changed: `live`, `nextStart` or `unchanged`.
Live changes are made one resource per call, and the rest in one call with msb's next-start
policy, so a resize never restarts a running VM. A memory size above the ceiling raises the
ceiling to match, as msb requires, and a CPU count above the maximum raises the maximum. If a
step fails, the steps before it stay in effect, and the catalog records what took effect.

**msb 0.7.7 has no live resize.** Its dry run marks every CPU or memory change to a running
VM "requires restart", with the reason "live CPU and memory resize are not available in this
runtime yet". So with the pinned msb every change to a running sandbox is `nextStart`,
decreases included. `live` shows only when the running VM already has the new value (for
example after a change saved for the next start is set back). The live path is kept for an msb
that offers it.

Verified on 2026-10-17 with msb 0.7.7 and base images v0.3.2 and v0.3.4, on a running sandbox
created with 2 CPUs and 2 GiB: a decrease to 1 CPU and 1 GiB answered `nextStart` for both;
the guest kept 2 CPUs, 2 GiB and its boot ID, and after a stop and start it had 1 CPU and
1 GiB. A growth to 3 CPUs and 2.5 GiB, above the boot-time maximum of 2 CPUs and 2 GiB, also
answered `nextStart`, raised both maximums, and took effect at the next start. A resize of a
stopped and of a suspended sandbox answered `nextStart`, and the same request again
`unchanged`. The Studio side is also covered by fake-runtime and API tests.

### Live use

`GET /api/environments/{env}/sandboxes/{id}/usage` reports a running sandbox's CPU and memory
use from msb's metrics (`SandboxHandle.Metrics`, which msb samples every few seconds):

- `cpuPercent`: the share of all the sandbox's CPUs in use over the last sample window, 0 to
  100. msb reports vCPU time where 100 is one vCPU busy; Studio divides by the catalogued CPU
  count and caps at 100, since a live CPU decrease can leave the boot-time vCPUs higher.
- `memoryBytes`: the memory the guest uses, against `memoryTotalBytes`, the catalogued memory
  size (msb's limit when the catalog has none).

A sandbox that doesn't run answers 409, and a runtime without metrics 501. The workspace's
status line polls it every 5 seconds while the sandbox runs. The e2e runtime reports half a
vCPU and 768 MiB for every running VM.

Verified vs. pending: the scaling and the API are covered by fake-runtime tests. On 2026-10-17
an idle sandbox with 3 CPUs and 2.5 GiB answered `cpuPercent` 0.1–0.4 and `memoryBytes` 173 MiB,
while the guest's `MemTotal − MemAvailable` was 137 MiB and the VM process's host RSS 314 MiB.
So `memoryBytes` follows the guest's own use, not the host's, but it is not exactly a
`/proc/meminfo` figure; what msb counts in it is still taken from its metrics crate.

## Checkpoints

- A checkpoint is a disk snapshot in the group `sbx-<id>`: root, workspace, Docker and home
  disks, of a running or a stopped sandbox.
- A running guest is frozen while msb flushes its filesystems, so the capture is
  crash-consistent at the filesystem level, not at the application level.
- Sandboxes created with systemd as init must be stopped first
  (`ErrLiveCheckpointUnsupported`).
- Deleting a checkpoint moves the group head to a surviving member first, then removes it
  with force, so an older checkpoint can go before newer ones. That is safe: a snapshot's
  parent is lineage only, and each snapshot directory holds full copies of the disk layers.
  Checked on 2026-10-15 with msb 0.7.7: after force-removing the first two of three
  snapshots, the third restored with every file intact. The cost is disk space: each
  checkpoint takes about as much as the sandbox's used disks.
- A checkpoint can be renamed (`PUT .../checkpoints/{checkpoint}` with `{"name"}`). Its
  name is a catalog label; the disks are not touched.

### Scheduled checkpoints

A sandbox can take checkpoints on a schedule, off by default
(`GET`/`PUT .../checkpoint-schedule` with `{"everyHours"}`: 0, 1, 4, 12 or 24). The table
`checkpoint_schedules` holds one row per sandbox with a schedule;
`Manager.RunCheckpointSchedules` checks them every minute, and setting a schedule wakes it
at once, so the first checkpoint is taken right away.

- A checkpoint is due when the schedule's newest one (`checkpoints.auto`) is `everyHours`
  old. A running sandbox gets one then. A stopped one gets one only if it started since the
  last, as its disks can't have changed otherwise; a suspended one waits.
- It is named `Auto YYYY-MM-DD HH:MM` in local time and records no history event, since it
  is deleted again.
- After each new one the schedule prunes its own: it keeps the newest 3 and the newest of each
  of the 7 days before today (local time), so at most 10. Checkpoints taken by hand are never
  pruned, and renaming a scheduled one makes it the user's (`auto` is cleared).
- A failed attempt is logged and saved as the schedule's `note`, shown in the UI; the
  schedule tries again after 15 minutes. `ErrBusy` (another operation on the sandbox) just
  waits for the next minute. A systemd sandbox, which can't be checkpointed while it runs,
  gets its note while running and its checkpoint once it stopped.
- Turning the schedule off keeps the checkpoints it took.

Verified on 2026-10-17 with msb 0.7.7, in a short run of a few minutes rather than a day: an
hourly schedule on a sandbox with 1 CPU and 1 GiB, with the scheduled checkpoints' times (and
once the sandbox's start time) moved back in the scratch catalog between ticks, so that each
minute tick, or a kick from setting the schedule again, found one due. Over 12 ticks the
schedule took 10 checkpoints of the running, then stopped, VM, each in under a second, and
pruned 5, none of them the group's head, so each went with force. It took the first at once
when turned on; took one on a plain minute tick; named a second one in the same minute
`Auto … (2)`; pruned the oldest of today beyond the newest 3, a 9-day-old one, and the older
of two from yesterday while keeping the newer; kept one from 2 days ago; took none while the
sandbox was suspended and one right after it resumed; took none of a stopped sandbox that had
not started since, and one after a start and stop. A checkpoint taken by hand and one renamed
during the run stayed. `studio checkpoints` and `GET .../checkpoints` listed the same 6
checkpoints as `msb snapshot ls` and the group's directory, and turning the schedule off kept
them. A day-long run on real time is still to be done.

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

The web sandbox page draws the history as a timeline, and Home shows the family tree once a sandbox was forked.

## Suspend and resume

An in-place pause (msb pause/resume): vCPUs stop, memory stays with the VM process, and
resume continues every process, TUIs and tmux included. It frees no memory and doesn't
survive a host reboot. The agent session is dropped on suspend and reconnects on resume.
Fork, rebase and checkpoints refuse a suspended sandbox; Stop and Delete resume it first.
A resize of a suspended sandbox is saved for its next start (see [resources](#resources)).

Verified on 2026-10-17 with msb 0.7.7 and base images v0.3.2 and v0.3.4: `studio suspend`
took 0.2 s and left the VM `paused` in `msb list`, with its process still resident. A tmux
session writing a counter and the time every second stopped while suspended and went on after
`studio resume` (0.4 s) with the same process and no restart: the log showed a gap the length
of the suspension and then one line a second again. The guest's clock matched the host's
right after resume, and the agent had reconnected by the time resume answered (the files API
worked at once). While suspended, a checkpoint and a fork answered 409, and a resize
`nextStart`. `studio stop` of a suspended sandbox stopped it in 2.6 s, and `studio rm` of one
deleted it with its VM and checkpoints. Resume resets the uptime shown, as a start does.
Rebase of a suspended sandbox was not tried.

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
2. **Quick shell.** `msb ssh connect ss-<id>-g1` opens a root shell in the VM. It needs no
   key: it goes through msb's own local connection, as `msb exec` does.
3. **Authorize your key** for an SSH client. `msb ssh authorize --file ~/.ssh/id_ed25519.pub`
   adds your own public key to `~/.microsandbox/ssh/authorized_keys`. That file is msb's, not
   Studio's, and covers every msb VM on this machine; remove the line to take the key back. Use
   your own key only. Studio's keys and secrets never go into a sandbox.
4. **Connect with OpenSSH or Remote-SSH.** Add a host to `~/.ssh/config` that serves the
   connection over stdio. Set `User agent`: the login user is the SSH user name, and without it
   your local user name is used and the session fails with "guest user not found".

   ```
   Host studio-<id>
       HostName ss-<id>-g1
       User agent
       ProxyCommand msb ssh serve --stdio ss-<id>-g1
       IdentityFile ~/.ssh/id_ed25519
   ```

   `msb ssh serve --port <port> <name>` listens on a host port instead, for clients that can't
   run a ProxyCommand. Without `--port` it listens on `127.0.0.1:2222`.

An SSH session's shell is `/bin/sh`, not a login shell, and starts in `/`, whatever the
user's login shell is. It reads no profile, so it has neither the CA variables
(`SSL_CERT_FILE`, `NODE_EXTRA_CA_CERTS` and the others) nor the secret placeholders a terminal
has (see [guest agent](guest-agent.md#ca-trust-and-placeholders-in-the-guest)). The system CA
bundle still includes Studio's CA. For the terminal's environment, run `bash -l`, for example
`ssh -t studio-<id> bash -l`.

SSH sessions run inside the sandbox's VM, so the network policy applies to them as it does to
terminals: egress is fixed when the VM is created, and only what it allows gets out (see
[network](network.md)).

Verified on 2026-10-17 with msb 0.7.7 and base images v0.3.2 and v0.3.4: `msb ssh connect`
without any authorized key (root, in `/`); `ssh` through the `ProxyCommand` host above as
`agent` and as `root`, with a key added by `msb ssh authorize`; a key that was not authorized
refused with "Permission denied (publickey…)"; an `ssh -p` login through
`msb ssh serve --port`, which listens on `127.0.0.1:2222` without `--port`; and `ssh -t … bash -l` giving a login shell with the CA variables. VS Code
Remote-SSH itself was not tried.

## Base image

`images/base`: Debian trixie with Docker CE (containerd, buildx, compose), git, tmux, bash
completion (git's too, in the login shells tmux starts), Node, build tools and Python, plus pinned mise, OpenCode, Claude Code, Codex and their ACP
adapters (versions in the Dockerfile and [harnesses](harnesses.md#versions)). User `agent`
(uid 1000, sudo, docker). It creates `/opt/mise` for template toolchains and keeps the
agent's home as the skeleton that home disks are seeded from. No init system. Releases
publish it to `ghcr.io/lukaskoebe/sandbox-studio-base` and pin it by digest
(`version.BaseImage`); dev builds use `sandbox-studio-base:dev` from `make image`. Templates
build on top of it: [template builds](template-builds.md).
