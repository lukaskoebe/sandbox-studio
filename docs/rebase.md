# Rebase and fork

Both move `/workspace` from one VM to another. Neither uses msb restore or msb fork.
Restore drops Studio's DNS and proxy egress
([#1736](https://github.com/superradcompany/microsandbox/issues/1736)).

## Workspace copy

- `studio-agent workspace-export` writes `/workspace` as a gzip PAX tar to stdout.
  It keeps modes, owners, mtimes, symlinks and hardlinks. It skips `lost+found` and
  special files, with a warning on stderr.
- `studio-agent workspace-import` reads that tar from stdin. It refuses a non-empty
  `/workspace`. It rejects absolute paths, `..`, duplicates, entries whose parent is not a
  directory from the archive, and hardlinks to anything but an earlier regular file.
  Symlinks are created but never followed. Parents are opened with `openat2` and
  `RESOLVE_BENEATH`.
- Both run as root through msb exec. The host joins them with an `io.Pipe`. Nothing is
  staged on the host.
- The stream is limited to the target's workspace size. If either side fails, the copy
  fails.
- Transfer mode creates or starts a VM without `studio-agent boot`, so no guest services
  or containers run. `Boot` starts them afterwards.

## Rebase

`POST /api/environments/{env}/sandboxes/{id}/rebase` with `{"templateId": "..."}`.

1. Studio resolves the new template image. A failure here changes nothing.
2. It writes a durable rebase record with generations `n` and `n+1`.
3. It stops the source gracefully and starts it again in transfer mode.
4. It creates generation `n+1` from the new template in transfer mode and copies the
   workspace.
5. It stops the source. A sandbox that was running is booted; a stopped one is stopped.
6. One catalog update switches generation, template and resources.
7. Studio removes the old VM and ends the record.

If a step before the commit fails, Studio removes the target, stops the source and ends
the record. A sandbox that was running is then started again through the normal start
path. If that start fails, both errors are returned and the sandbox stays stopped. At
startup, a leftover record is resolved by generation: before the commit the target is
removed and the source stays stopped, after it the source is removed. Lifecycle actions on
that sandbox are refused until this succeeds.

Docker images, containers and volumes are lost. Tmux sessions end. Checkpoints stay with
their generation.

## Fork

`POST /api/environments/{env}/sandboxes/{id}/fork` with `{"name": "..."}`.

The fork gets a new ID and egress identity, with the same template and resources. Its
workspace is copied from the source. A running source keeps running, so the copy may be
inconsistent. A stopped source is started in transfer mode for the copy and then stopped
again. The fork is booted. A failed fork is removed.

## Limits

- The size limit counts compressed bytes. A full guest disk also fails the import.
- Recovery after a crash leaves the sandbox stopped, even if it was running.
- Restoring a checkpoint from before a rebase would bring back the old root while the
  sandbox names the new template. Restore is gated, so this cannot happen yet.
