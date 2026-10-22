# msb upgrade under a running sandbox

Studio replaces an msb in `~/.microsandbox` that is older than the release its SDK was built
against (`runtime.Ensure`). This spike checks that the replacement keeps sandboxes working: a
VM the old msb runs keeps running and answering, it stops and starts on the new msb, and
checkpoints taken on either side work.

It takes two builds. `before` runs on the old SDK, so build it from a checkout of the last
release on the old SDK (v0.5.2, SDK 0.7.7), with this directory copied in and `ensure.go`
replaced by `func ensure(ctx context.Context) error { return runtime.Ensure(ctx) }`. `after`
and `clean` run on this tree. Point `MSB_HOME` at a scratch directory, never at
`~/.microsandbox`:

```sh
export MSB_HOME="$(mktemp -d -p "$HOME/git")"   # short: socket paths are limited to 108 bytes
old/msb-upgrade before ghcr.io/lukaskoebe/sandbox-studio-base:v0.5.2 <guest dir>
new/msb-upgrade after ghcr.io/lukaskoebe/sandbox-studio-base:v0.5.2 <guest dir>
new/msb-upgrade clean
rm -rf "$MSB_HOME"
```

`<guest dir>` holds `bin/studio-agent`, as Studio's agent directory does.

Passed on Linux amd64 on 2026-10-22, upgrading msb 0.7.7 to 0.7.8 (SDK 0.7.7 to 0.7.8):

- `before`: SDK 0.7.7 installed msb 0.7.7, created and booted the VM (2m46s with a cold image
  cache), wrote a marker in `/workspace` and checkpointed the running VM.
- `after`: `runtime.Ensure` replaced msb 0.7.7 by 0.7.8 in 5.5 s. The VM stayed running, and
  SDK 0.7.8 ran a command in it that read the marker. A checkpoint of it, still running on the
  old msb, worked. It stopped, a checkpoint of the stopped VM worked, it started on msb 0.7.8,
  read the marker, wrote and read the workspace, and a checkpoint on the new msb worked. All
  four snapshots were listed, and it stopped cleanly.

Restore isn't covered: Studio disables it until upstream #1736 is fixed. macOS and Windows
are untested. On Windows the install renames files a running msb has open.
