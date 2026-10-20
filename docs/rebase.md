# Rebase and fork

Both move `/workspace` from one VM to another, and `/home/agent` too when the sandbox has a
home disk ([sandboxes](sandboxes.md#storage-and-mounts)). Neither uses msb restore or msb fork.
Restore drops Studio's DNS and proxy egress
([#1736](https://github.com/superradcompany/microsandbox/issues/1736)).
Both need a running or stopped sandbox; a suspended one is refused until it is resumed.

## Workspace and home copy

- `studio-agent workspace-export` writes `/workspace` as a gzip PAX tar to stdout, and
  `home-export` does the same for `/home/agent`. They keep modes, owners, mtimes, symlinks
  and hardlinks. They skip `lost+found` and special files, with a warning on stderr.
- `studio-agent workspace-import` and `home-import` read that tar from stdin. Each refuses a
  non-empty target. They reject absolute paths, `..`, duplicates, entries whose parent is
  not a directory from the archive, and hardlinks to anything but an earlier regular file.
  Symlinks are created but never followed. Parents are opened with `openat2` and
  `RESOLVE_BENEATH`.
- `home-import` also marks the home, so the home disk is never seeded from the skeleton
  over the copy. Before that it removes the mise login block that templates built before
  `/opt/mise` appended to `~/.profile`, since it would point the home back at toolchains
  that are gone.
- A source without a home disk (a sandbox of a legacy template, exporter version 1) gives
  its root `/home/agent` to a target that has a home disk. The toolchains that those
  templates kept there are left out: the target template has them in `/opt/mise`.
- Which copies happen follows the target template. A target with a home disk gets the home.
  A legacy target has none, so only the workspace is copied, as before. A sandbox with a
  home disk can't move to a legacy template: rebase is refused with a conflict, before
  anything changes, since its home would have nowhere to go.
- All run as root through msb exec. The host joins them with an `io.Pipe`. Nothing is
  staged on the host.
- The workspace stream is limited to the target's workspace size, and the home stream to
  the 4 GiB home disk. A home that doesn't fit fails with a message that says to delete
  caches and try again. If either side fails, the copy fails.
- Transfer mode creates or starts a VM without `studio-agent boot`, so no guest services
  or containers run. `Boot` starts them afterwards.

## Rebase

`POST /api/environments/{env}/sandboxes/{id}/rebase` with `{"templateId": "..."}`.

The request checks the template, the home disk and the sandbox's state, and answers at once
with the sandbox in state `creating`. The rebase then runs in the background, holding the
sandbox, and publishes its steps on the `sandboxes` topic like a fork
([creation progress](sandboxes.md#creation-progress)): `prepare` (steps 2–3 below), `pull`
and `vm` (step 4's VM), `copy`, and `boot` for a sandbox that was running. The rebase dialog
closes once the request is accepted, and the sandbox page shows the steps. `?wait=true`
answers only once the rebase has ended, with the rebased sandbox or the error, as the
request did before it ran in the background.

1. Studio resolves the new template image. A failure here changes nothing and fails the
   request.
2. It writes a durable rebase record with generations `n` and `n+1`.
3. It stops the source gracefully and starts it again in transfer mode.
4. It creates generation `n+1` from the new template in transfer mode and copies the
   workspace and the home.
5. It stops the source. A sandbox that was running is booted; a stopped one is stopped.
6. One catalog update switches generation, template and resources.
7. Studio removes the old VM and ends the record.

A failed rebase leaves the sandbox on its old template, in its old status. Its view keeps
`progress` with `operation: rebase`, `state: failed`, the step and the error: the workspace
shows a note naming the failed step, with a Dismiss button
(`DELETE /api/environments/{env}/sandboxes/{id}/progress`). The next rebase replaces
the report.

If a step before the commit fails, Studio removes the target, stops the source and ends
the record. A sandbox that was running is then started again through the normal start
path. If that start fails, both errors are returned and the sandbox stays stopped. At
startup, a leftover record is resolved by generation: before the commit the target is
removed and the source stays stopped, after it the source is removed. Lifecycle actions on
that sandbox are refused until this succeeds.

Docker images, containers and volumes are lost. Tmux sessions end. Checkpoints stay with
their generation. The catalog's switch to the new generation records a `rebase` event in the
sandbox's history, see [lineage](sandboxes.md#lineage). With a home disk on the new generation, harness sessions and logins
survive, and a chat resumes its agent session in the new generation
([chat mode](design/chat-mode.md)).

## Fork

`POST /api/environments/{env}/sandboxes/{id}/fork` with `{"name": "..."}`.

The fork gets a new ID and egress identity, with the same template and resources. Its
workspace is copied from the source, and its home too if the source has a home disk. A fork
of a sandbox without one has none, as before. A running source keeps running, so the copy
may be inconsistent. A stopped source is started in transfer mode for the copy and then
stopped again. The fork is booted. The request returns before the copy and boot finish:
the fork is in state `creating` until it runs, and `failed` if a step fails. A failed fork
keeps its record until it is deleted; see [creation progress](sandboxes.md#creation-progress).
The source keeps its chats; the fork starts without any.
On success the fork and its source each record a `fork` event in their history, see [lineage](sandboxes.md#lineage).

## Limits

- The size limit counts compressed bytes. A full guest disk also fails the import.
- The home disk is 4 GiB. A home that holds more fails the copy; delete caches in it and
  try again.
- Recovery after a crash leaves the sandbox stopped, even if it was running.
- Restoring a checkpoint from before a rebase would bring back the old root while the
  sandbox names the new template. Restore is gated, so this cannot happen yet.
