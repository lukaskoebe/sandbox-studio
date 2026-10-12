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
| `/var/lib/docker` | owned disk, so Docker's overlay isn't nested on the root overlay |
| `/opt/studio` | read-only bind of `<data>/guest`; the agent is `/opt/studio/bin/studio-agent`, so updating Studio updates every guest |

Owned (not named) volumes, because disk snapshots capture owned volumes and skip named ones
(spike S5).

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
- After each boot Studio runs `studio-agent boot` as root. It prepares the disks and starts
  a supervisor for containerd, dockerd and the agent, restarting them with backoff. It is a
  no-op if systemd is PID 1 (older sandboxes) or a supervisor already runs.
- Before a stop Studio runs `studio-agent shutdown`, because msb gives the guest two seconds
  before it kills every process. Stop resumes a suspended VM first.

Runtime statuses: `absent`, `created`, `starting`, `running`, `draining`, `suspended`,
`stopped`, `crashed`. The API adds `creating` and `failed` for a create or fork (below).

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

Defaults: 2 CPUs, 4 GiB memory, 20 GiB workspace, 20 GiB Docker disk. `max_memory` defaults
to the initial memory and is passed as the VM's memory ceiling. A template's spec fixes the
resources of sandboxes created from it. There is no live resize.

## Checkpoints

- A checkpoint is a disk snapshot in the group `sbx-<id>`: root, workspace and Docker disks,
  of a running or a stopped sandbox.
- A running guest is frozen while msb flushes its filesystems, so the capture is
  crash-consistent at the filesystem level, not at the application level.
- Sandboxes created with systemd as init must be stopped first
  (`ErrLiveCheckpointUnsupported`).
- Deleting a checkpoint moves the group head to a surviving member first; Studio never
  forces removal. A checkpoint with dependent newer ones answers with a conflict.

**Restore is disabled.** With microsandbox 0.7.7 a restored VM gets msb's default network
(no resolver, no gateway proxy); restore options can't set either
([upstream #1736](https://github.com/superradcompany/microsandbox/issues/1736)).
`Runtime.CheckpointRestoreSupported` returns false, and the UI reads it from the sandbox
view. The restore path is implemented and tested with fakes: it needs a stopped sandbox,
restores a stopped replacement VM as a new generation, adopts it in the catalog, then
removes the old VM; a durable record lets startup finish or discard an interrupted restore.
As a guard, Studio refuses a candidate whose stored network config differs from Create's.

## Suspend and resume

An in-place pause (msb pause/resume): vCPUs stop, memory stays with the VM process, and
resume continues every process, TUIs and tmux included. It frees no memory and doesn't
survive a host reboot. The agent session is dropped on suspend and reconnects on resume.
Fork, rebase and checkpoints refuse a suspended sandbox; Stop and Delete resume it first.
A full (memory) snapshot plus stop would need restore, so it waits on #1736 too.

## Fork, rebase, export and import

- [Fork and rebase](rebase.md): the workspace is copied as a gzip tar over msb exec between
  two VMs. Docker data is not copied.
- [Export and import](export-import.md): a `.studio-sandbox` file (manifest plus workspace
  tar), recreated through a template build and the same workspace copy.

## Base image

`images/base`: Debian trixie with Docker CE (containerd, buildx, compose), git, tmux, mise,
Node, build tools and Python, plus pinned OpenCode, Claude Code, Codex and their ACP
adapters (versions in the Dockerfile and [harnesses](harnesses.md#versions)). User `agent`
(uid 1000, sudo, docker). No init system. Releases publish it to
`ghcr.io/lukaskoebe/sandbox-studio-base` and pin it by digest (`version.BaseImage`); dev
builds use `sandbox-studio-base:dev` from `make image`. Templates build on top of it:
[template builds](template-builds.md).
