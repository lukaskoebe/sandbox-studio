# M3 rebase: workspace transfer plan

**Status: planned and unqualified. Product rebase remains disabled.** This plan records the
next bounded work and its gates; it does not enable rebase or claim workspace consistency.

## Established constraints

- The M3 model assigns each sandbox a stable Studio ID and versioned VM generations. The
  workspace and Docker storage are separate owned disks; checkpoints include both
  ([PLAN.md](../PLAN.md#L228-L267), [S5](spikes.md#L85-L115)). The installed microsandbox
  Go SDK v0.7.7 source reviewed for this work exposes no supported copy/rebind operation for
  a sandbox's owned disk. `Runtime.Create` allocates fresh owned disks
  ([runtime.go](../internal/runtime/runtime.go#L118-L163)).
- The existing `KindExport` / `Hub.Export` path exports a writable OCI root layer into a
  normalized layer artifact ([agentproto](../internal/agentproto/proto.go#L28-L38),
  [agentchan](../internal/agentchan/export.go#L31-L80)). It is not a workspace archive
  receiver or an extractor and must not be reused as one. `agentproto.WriteExport` and
  `ReadExport` provide bounded framing, byte counts, SHA-256 completion, and truncation
  rejection ([export.go](../internal/agentproto/export.go#L17-L55),
  [export.go](../internal/agentproto/export.go#L151-L180)). Existing root-layer roundtrip
  evidence explicitly excludes workspace and Docker data ([spikes.md](spikes.md#L299-L318)).
- A normal `Runtime.Create`/`Start` runs `studio-agent boot`, which starts guest services
  ([runtime.go](../internal/runtime/runtime.go#L234-L255)). A new transfer-only guest mode
  and a separate workspace quiescence contract are required before product rebase. Raw tar
  transfer success alone does not establish source consistency.
- Restore remains gated: SDK v0.7.7 restore cannot restore Studio DNS and proxy routing;
  do not use checkpoint restore or fork as the rebase path
  ([checkpoints.go](../internal/runtime/checkpoints.go#L15-L30),
  [PLAN.md](../PLAN.md#L257-L267)). Do not share proxy or vsock identity between distinct
  Studio sandboxes. Disposable transfer VMs use distinct sandbox IDs and their own isolated
  network identities.

## Next slice: bounded workspace archive and receiver

Implement a workspace-specific agent protocol and guest exporter/receiver. Reuse the existing
bounded frame/checksum primitives only where their contract fits; keep workspace archive
validation separate from OCI layer normalization. The host may stage a size-bounded,
checksummed tar as an opaque file in a private operation directory. It must never extract the
archive into host paths. The guest receiver accepts input only when `/workspace` is verified
fresh and empty; it must not overwrite an existing tree. A failed or partial import rejects
the disposable destination rather than retrying into a possibly modified workspace.

Define explicit rejection behavior for absolute or escaping paths, duplicate entries,
path/type changes, hardlinks, symlink traversal, special files, entry-count and expanded-size
limits, archive-size limits, truncation, checksum mismatch, and disconnect. Preserve regular
file modes and UID/GID, symlinks, and hardlinks only where safely supported; otherwise fail
explicitly. Do not silently drop or alter metadata. Bound archive bytes, entry count, and total
expanded bytes. The exact numeric limits are a qualification choice and must be recorded with
the protocol version. Any invalid entry, limit failure, truncation, checksum error, or
disconnect fails the transfer without success completion; the destination stays unadopted and
is discarded, while the source remains intact.

Qualify transfer using private disposable VMs with **distinct sandbox IDs**, deny-all egress,
and **at most one VM running at a time**. Export from the source, stop it and verify stopped,
then start the destination; the source remains stopped and retained until destination contents
and metadata verify. Use a static fixture to qualify transport and receiver semantics. This
pass does not qualify concurrent-write consistency or permit product rebase.

## Product-rebase gates, after transfer qualification

1. Add a transfer-only guest mode and a defined quiescence gate. No user containers, shells,
   or other writers may modify `/workspace` during capture. The gate must fail closed if
   quiescence cannot be established. Tmux/PTY processes and other in-memory process state
   cannot move to a fresh generation; require no active sessions or report clearly that they
   end. Checkpoints capture disks, not application consistency or process
   migration ([PLAN.md](../PLAN.md#L244-L255)).
2. Keep the source generation `n` authoritative and stopped before starting target `n+1`.
   Retain the source VM and its disks until the candidate is stopped, its workspace is
   verified, and `ConfigureGuest` acknowledges the current environment CA and placeholders
   ([config.go](../internal/sandboxes/config.go#L25-L35)). Do not rely on the asynchronous
   `OnConnect` callback for that acknowledgement ([hub.go](../internal/agentchan/hub.go#L129-L145)).
3. Add a durable rebase journal that pins both source and target templates and records the
   source/target generations, VM names, archive identity/digest, and phase. Before recording
   an archive as staged, fsync the opaque file, atomically rename it into the private operation
   directory, and fsync the directory. The current
   sandbox template pin is immutable and template deletion checks sandbox references
   ([009_template_instances.sql](../internal/store/migrations/009_template_instances.sql#L34-L40),
   [templates.go](../internal/store/templates.go#L348-L368)); rebase needs explicit journal
   references and a migration. Atomically switch generation and template pin only after
   candidate verification, stopped-state confirmation, and CA/config acknowledgement. Then
   remove the source VM; release its pin only after exact source cleanup succeeds. A
   pre-commit recovery removes the candidate before releasing the target pin and keeps the
   source/catalog pin; a post-commit recovery keeps the candidate and removes the source
   before releasing its journal pin. Unknown state or failed cleanup blocks
   further lifecycle changes. Reconcile this journal before listeners, following the existing
   restore recovery shape ([manager.go](../internal/sandboxes/manager.go#L122-L165),
   [checkpoints.go](../internal/sandboxes/checkpoints.go#L318-L344)).
4. Serialize rebase with start, stop, delete, checkpoint, and restore operations. Keep existing
   checkpoints attached to their captured generations; do not reinterpret or delete them as
   part of rebase. Restore remains unavailable under the current upstream restore gate.

## Open contract and qualification limits

- Docker persistence across rebase is unresolved. The plan gives `/var/lib/docker` its own
  owned disk and M3 acceptance promises workspace retention
  ([PLAN.md](../PLAN.md#L943-L948)), but does not explicitly say
  whether images, containers, and Docker volumes survive a template change. Prefer preserving
  Docker state if it can be qualified safely; production rebase must not silently discard it.
  Docker transfer/preservation is a separate qualification and is not part of the workspace
  slice.
- The first lifecycle qualification assumes a compatible guest platform and workspace
  capacity sufficient for the archive. CPU, memory, disk-size changes, and Docker capacity
  changes remain outside that slice until separately qualified. Workspace transport does not
  imply those geometry changes are safe.
