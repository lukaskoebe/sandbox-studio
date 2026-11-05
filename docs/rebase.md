# Rebase, disk resize and fork

All three move `/workspace` from one VM to another, and `/home/agent` too when the sandbox has
a home disk ([sandboxes](sandboxes.md#storage-and-mounts)); a disk resize moves Docker's data
as well. None uses msb restore or msb fork.
Restore drops Studio's DNS and proxy egress
([#1736](https://github.com/superradcompany/microsandbox/issues/1736)).
A rebase and a disk resize take a sandbox in any state with a VM: the copy stops the VM and
starts it for itself, as both trees must be read from a running guest. One that was running
restarts on the new generation, and any other stays stopped, a suspended one losing what it had
in memory. A fork needs a running or stopped sandbox; a suspended one is refused until it is
resumed.

## Workspace and home copy

- `studio-agent workspace-export` writes `/workspace` as a PAX tar to stdout, and
  `home-export` does the same for `/home/agent`. They keep modes, owners, mtimes, symlinks
  and hardlinks. They skip `lost+found` and special files, with a warning on stderr. Fork
  and rebase pass `--zstd`: the guest agent comes from the host, so both VMs read it, and it
  compresses on every CPU. On a 24 MB `node_modules` package it exported in 0.18 s instead
  of gzip's 0.31 s, unpacked in 0.06 s instead of 0.15 s, and came out smaller. An export file
  stays gzip, which older Studios import.
- `studio-agent workspace-import` and `home-import` read either from stdin, as the first
  bytes say. Each refuses a
  non-empty target. They reject absolute paths, `..`, duplicates, entries whose parent is
  not a directory from the archive, and hardlinks to anything but an earlier regular file.
  Symlinks are created but never followed. Parents are opened with `openat2` and
  `RESOLVE_BENEATH`.
- `studio-agent docker-export` and `docker-import` do the same for `/var/lib/docker`, which
  only a disk resize copies. Docker's data is made of what a workspace archive leaves out:
  overlay whiteouts are character devices 0/0, an opaque directory is the
  `trusted.overlay.opaque` xattr, and image files keep `security.capability`. So these keep
  character and block devices, FIFOs and every extended attribute, as `SCHILY.xattr.*` PAX
  records, the way GNU tar does. The import sets a file's xattrs after its owner, since a
  chown drops `security.capability`, and a symlink's or a device's through
  `/proc/self/fd/<parent>/<name>`, so nothing is followed. Sockets are still skipped.
  Docker and containerd don't run in transfer mode, so the data holds still.
- `home-import` also marks the home, so the home disk is never seeded from the skeleton
  over the copy. Before that it removes the mise login block that templates built before
  `/opt/mise` appended to `~/.profile`, since it would point the home back at toolchains
  that are gone.
- A source without a home disk (a sandbox of a legacy template, exporter version 1) gives
  its root `/home/agent` to a target that has a home disk. The toolchains that those
  templates kept there are left out: the target template has them in `/opt/mise`.
- Which copies happen follows the target template. A target with a home disk gets the home.
  A legacy target has none, so no sandbox can move to one: rebase is refused with a conflict,
  before anything changes, since the home would have nowhere to go. That goes for a sandbox of
  a legacy template too. It stays as it is, and the base image or a rebuilt template takes it,
  home included.
- All run as root through msb exec, one at a time per VM. Nothing is staged on the host.
  The export doesn't write its archive to msb exec's stdout, which is slow from the guest,
  but POSTs it to `https://transfer.studio.internal/<token>` through the gateway
  (`sandboxes.Transfers`, `agentproto.TransferURLEnv`), trusting the environment's CA, which
  Studio passes it. The token is random, used once, and only the source sandbox may send
  it. The gateway hands the request body on to the import's stdin, which is fast, and
  answers once the import has read it all. A guest that can't upload, failing before it sent
  anything, exports over stdout as before. Measured on 2026-10-31 (msb 0.7.8, amd64, four
  CPUs, [`spikes/transfer-speed`](../spikes/transfer-speed/README.md)):

  | From the guest | MB/s |
  |---|---:|
  | msb exec stdout | 4.9 |
  | TCP through a SOCKS5 proxy | 85–128 |
  | `home-export --zstd` of 1 GB that doesn't compress, uploaded over TLS | 90 |
  | the same over msb exec stdout | ≈ 5 |

  Into the guest, msb exec stdin takes 98 MB/s and `home-import` 70–80 MB/s of files, so the
  import keeps it. A tree of small files is limited by reading them cold instead:
  `node_modules` (334 MB, 34,000 files, 68 MB as zstd tar) goes at 48 MB/s uploaded and 30
  MB/s over stdout, where its compression hides most of the difference.
- The workspace stream is limited to the target's workspace size, the home stream to the
  target's home disk (`homeMiB`, 4 GiB unless set), and Docker's to its Docker disk. That counts
  compressed bytes, so a tree can also fill the target's disk during the import, and either way
  it fails with the same message: a home's says to delete caches, or give the sandbox a bigger
  home disk, and try again; Docker's says to prune. If either side fails, the copy fails.
- A sparse file stays sparse: the archive holds its holes as zeros, and the import moves past a
  run of zeros instead of writing it. So a file larger than its disk still copies, though the
  export takes the time to read the holes.
- A copy tells how far it got (`Progress.Copy`). A zstd export first adds up the sizes of the
  tree's files, reading only their status, and writes the sum in the PAX record `STUDIO.size`
  of a global header at the start of the archive; an export file stays gzip, without it. The
  import writes `copied <bytes> <total>` to its stdout about once a second, and
  `Runtime.ImportTree` always reads that stdout, even with nobody to tell: lines left for
  `runAgent` would crowd out a failure's message in its bounded channel.
- A copy doesn't fill the VMs' memory. Everything read or written passes through the guest's
  page cache, and the VM's host process keeps each guest page resident once touched, so an
  uncached copy grows both VMs to their memory size on the host. Giving pages back afterwards
  is slow and partial (the balloon's free page reporting), and page cache isn't freed at all
  until the guest runs short. So the guest doesn't touch them in the first place, reusing the
  same few pages: the export drops each file it
  read from the cache, every 32 MiB and at its end, unless the file had pages cached before,
  as a running sandbox's working files may. The import has only new files: it starts their
  writeback as each is written, and once 32 MiB or 256 files are waiting, waits for it and
  drops them (`internal/guest/pagecache.go`). Measured on 2026-11-04
  ([`spikes/transfer-memory`](../spikes/transfer-memory/README.md)), copying 1.8 GiB in a 2
  GiB VM: 2106 MiB resident at the peak and 48 s before, 256 MiB and 29 s after. Directory
  entries and inodes stay cached, about 1 KiB per file, until the guest needs the memory.
- Transfer mode creates or starts a VM without `studio-agent boot`, so no guest services
  or containers run. `Boot` starts them afterwards.

## Rebase

`POST /api/environments/{env}/sandboxes/{id}/rebase` with `{"templateId": "..."}` (and
`"keepTools": true` to [keep the mise tools](#keeping-tools)), or
without a template ID onto the current base image, which is what new sandboxes get. A rebase
onto the base keeps the sandbox's resources; one from a template sandbox drops the template's
tools. A rebase onto a template takes its CPUs and memory and doesn't shrink a disk: a disk the
template makes smaller than the sandbox's, as one resized for its data is, keeps the sandbox's
size, and one it makes larger grows. The rebase dialog offers the base first, then the ready templates, and picks the
sandbox's template rebuilt on the current base when there is one
([base images](sandboxes.md#which-base-a-sandbox-runs)).

The request checks the template, the home disk and the sandbox's state, and answers at once
with the sandbox in state `creating`. The rebase then runs in the background, holding the
sandbox, and publishes its steps on the `sandboxes` topic like a fork
([creation progress](sandboxes.md#creation-progress)): `prepare` (steps 2–3 below), `pull`
and `vm` (step 4's VM), `copy`, and `boot` for a sandbox that was running. The rebase dialog
closes once the request is accepted, and the sandbox page shows the steps. `?wait=true`
answers only once the rebase has ended, with the rebased sandbox or the error, as the
request did before it ran in the background.

1. Studio resolves the new template image, or takes the base image. A failure here changes nothing and fails the
   request.
2. It writes a durable rebase record with generations `n` and `n+1`.
3. It stops the source gracefully and starts it again in transfer mode.
4. It creates generation `n+1` from the new template in transfer mode and copies the
   workspace and the home, and with `keepTools` the tools (below).
5. It stops the source. A sandbox that was running is booted; a stopped one is stopped.
6. One catalog update switches generation, template, resources and the base it records.
7. Studio removes the old VM and ends the record.

A failed rebase leaves the sandbox on its old template and base, in its old status. Its view keeps
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

Docker images, containers and volumes are lost, and so is what was installed outside
`/workspace` and `/home/agent`: apt packages, `/etc` changes and, unless the rebase keeps
them, the tools `mise use` installed (in `/opt/mise`). Tmux sessions end. Checkpoints stay with
their generation. The catalog's switch to the new generation records a `rebase` event in the
sandbox's history, see [lineage](sandboxes.md#lineage). With a home disk on the new generation, harness sessions and logins
survive, and a chat resumes its agent session in the new generation
([chat mode](design/chat-mode.md)).

### Keeping tools

`mise use -g` pins a tool in `/opt/mise/config/config.toml` and installs it in
`/opt/mise/installs`, on the root disk, which the new template replaces; a project's
`mise use` pins it in the workspace, but its install is there too. With `{"keepTools": true}`
(the rebase dialog's **Keep the tools installed with mise use**, ticked by default) the copy
also runs `studio-agent tools-export` in the source and `tools-import` in the target, both as
root (`internal/guest/tools.go`):

- The export holds `installs`, `plugins` and `config` of `/opt/mise`; shims and caches stay
  behind. It is capped at 32 GiB (`ErrToolsTooLarge`).
- The import extracts it beside the target's `/opt/mise` and moves in each toolchain version,
  version alias and plugin the target lacks. One the template has stays the template's.
- The source's global pins (`config.toml` over `conf.d/*.toml`) that the target's global
  config doesn't pin go to `/opt/mise/config/conf.d/studio-kept-tools.toml`, so they stay on
  the PATH while the new template's pins win; the next rebase carries that file along as
  part of the global config. Other settings of the old config are not kept. A config that
  doesn't parse keeps the toolchains without their pins, with a warning.
- `mise reshim` runs as the agent user last.

Toolchains are copied as they were built, for the old template's system libraries; one that
links against a library the new base lacks needs `mise install` again. A disk resize, which
keeps the template, doesn't copy tools.

## Disk resize

`PUT /api/environments/{env}/sandboxes/{id}/disks` with any of `workspaceMiB`, `dockerMiB` and
`homeMiB`; a size left out stays (`Manager.ResizeDisks`). msb 0.7.8 can't change the size of
a VM's disk volumes, only grow its root disk, so a resize is a rebase onto the sandbox's own
template, or onto the default image for a sandbox without one, with other disk sizes:

- It keeps the sandbox's CPUs and memory, not the template's, so a sandbox resized after it
  was made keeps that.
- After the workspace and the home, it copies Docker's images, containers and volumes, so
  they survive. Containers with a restart policy start again when Docker does.
- A disk may shrink as long as what it holds fits; a copy that doesn't fit fails and rolls
  back. A request that changes nothing or a size under 1024 MiB is refused with 422 before
  anything changes.
- A sandbox without a home disk (a legacy template's) is refused with 422 too, whichever disk
  was asked for. Its home is on the root disk, which a resize replaces, so the home would be
  lost. A rebase onto a template with a home disk moves it there, and after that it can be
  resized. The dialog says this and offers no resize.
- Its record is a rebase record of kind `disks` (migration 055), so failure, rollback and
  recovery are the rebase's. The progress operation is `disks`, with the rebase's steps, and
  its background run may take 2 hours instead of 30 minutes, for a big Docker disk.
- The switch records a `disks` event in the sandbox's history, which the history timeline
  shows as "disks resized".

As with a rebase, what was installed outside the three disks is lost, the root disk is new,
and tmux sessions end. A sandbox without a template moves to the current base image, as a
fork does. While the copy runs, the copied data takes its space twice on the host. The dialog
shows each disk's size, and its free space where the guest reported the disk nearly full.

It has not been run against a real VM yet. The Docker archive's whiteouts, FIFOs and xattrs
are covered by a unit test that runs as a normal user, so with `user.*` xattrs; the
`trusted.*` ones and block devices need the guest's root, and Docker starting on the copied
data needs a live run.

## Fork

`POST /api/environments/{env}/sandboxes/{id}/fork` with `{"name": "..."}`, and optionally
`docker`, `tools` and `script`.

The fork gets a new ID and egress identity, with the same template and resources. Its
workspace is copied from the source, and its home too if the source has a home disk. A fork
of a sandbox without one has none, as before. A running source keeps running, so the copy
may be inconsistent. A stopped source is started in transfer mode for the copy and then
stopped again. The fork is booted. The request returns before the copy and boot finish:
the fork is in state `creating` until it runs, and `failed` if a step fails. A failed fork
keeps its record until it is deleted; see [creation progress](sandboxes.md#creation-progress).
The source keeps its chats; the fork starts without any.

`tools` also merges the toolchains and global config in `/opt/mise`, as a rebase that keeps
tools does. `docker` also copies Docker's disk, as a disk resize does, with its longer time
limit. It needs a stopped source, because Docker has to be down for the copy, and a running one
gets a 409 rather than being stopped: a fork has no recovery record to start the source again
after a crash. `script` runs `~/.config/studio/on-fork.sh` in a plain terminal session named
`on-fork` once the guest is configured, through a `KindStartSession` without a harness. It ends
in the terminal's normal shell, so its output and status stay readable. The script only runs when
asked for, never because the file exists, since an agent in the source could have written it. A
failure to start it is logged and doesn't fail the fork.

An option left out takes the source's `forkDefaults`, off for an ordinary sandbox. A merge's
scratch copy ignores them. `PUT .../base` sets those defaults, which is what makes a sandbox a
base, and `DELETE .../base` clears them. A base is otherwise an ordinary sandbox: the column
is all it adds, the fork doesn't inherit it, and New computer's "Start from" lists the bases and
posts a fork without options. The TUI and CLI forks send only a name, so they follow the
defaults too. Rules aren't copied: a teammate-scoped rule or secret reaches every fork, as
a fork keeps its source's teammate.

On success the fork and its source each record a `fork` event in their history, see [lineage](sandboxes.md#lineage).

## Limits

- The size limit counts compressed bytes. A full guest disk also fails the import.
- The home disk is 4 GiB unless the sandbox has another size. A home that holds more fails
  the copy; delete caches in it, or resize the home disk, and try again.
- Recovery after a crash leaves the sandbox stopped, even if it was running.
- Restoring a checkpoint from before a rebase would bring back the old root while the
  sandbox names the new template. Restore is gated, so this cannot happen yet.
