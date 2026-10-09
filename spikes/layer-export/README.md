# Layer-export guest qualification

This directory contains two separate qualifications: the earlier Python freeze-lifecycle
probe below, and the later full Go guest-export/import roundtrip. Results from the Python
probe alone do not establish exporter correctness.

## Earlier Python freeze-lifecycle probe

This disposable-guest probe qualifies only the pinned-upper freeze/read window and watchdog
lifecycle. It does not stream yamux data, create a tar archive, or establish exporter
correctness.

From the repository root, run one mode at a time against a disposable sandbox. This sends
the script as a process argument; it does not need to install anything in the guest:

```sh
python3 - <disposable-sandbox-name> normal <<'PY'
import pathlib, subprocess, sys
script = pathlib.Path("spikes/layer-export/freeze_probe.py").read_text()
msb = pathlib.Path.home() / ".microsandbox/bin/msb"
subprocess.run([str(msb), "exec", sys.argv[1], "--", "python3", "-c", script, sys.argv[2]],
               check=True, timeout=45)
PY
```

Repeat with `disconnect`, `death`, and `timeout`. Each mode must run only on a disposable test guest. Do not run against a user VM. The script uses guest metadata only to identify `/dev/vdb`; it never opens metadata paths as host paths. It discovers the pinned `upperfs` dynamically under guest `/proc/1/fd`, confirms `st_dev == /dev/vdb.st_rdev`, reads through a read-only pinned upper fd with `O_NOATIME`, and keeps control/status files in the verified `/dev/shm` tmpfs. A direct-child `setsid` watchdog owns FIFREEZE, sends ready only after the ioctl succeeds, and thaws on release, owner EOF, or timeout.

Exit status zero means the selected lifecycle scenario and cleanup assertions passed. The JSON field `captureCandidate` is true only for `normal`, and only after the watchdog reports normal release and the frozen read stayed stable. Disconnect, SIGKILL, and timeout scenarios must report `captureCandidate: false`; their success means cleanup behavior was exercised, not that capture is valid.

## Python probe results and limits

All four modes passed on 2026-10-09 in a Linux amd64 guest on microsandbox 0.7.7. Each
reported writer resumption and successful cleanup; only `normal` reported a capture
candidate. Docker remained responsive afterward.

The Python probe exercises a small concurrent writer and a 0.6-second frozen read window.
It does not qualify full-tree tar consistency, xattrs/whiteouts/metacopy, symlink and
hardlink handling, yamux backpressure, or recovery from a stuck freeze ioctl, watchdog
failure, or complete guest-agent failure. The full Go roundtrip below separately qualifies
several of these exporter behaviors.

## Full Go exporter source/import roundtrip

Build a static Linux amd64 guest agent into `/tmp`, then run the roundtrip harness from the
repository root:

```sh
set -eu
agent_tmpdir="$(mktemp -d)"
trap 'rm -rf "$agent_tmpdir"' EXIT
agent_bin="$agent_tmpdir/studio-agent-linux-amd64"
GOMAXPROCS=2 CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -p=1 -trimpath -o "$agent_bin" ./cmd/studio-agent
GOMAXPROCS=2 go run -p=1 ./spikes/layer-export/roundtrip -agent "$agent_bin"
```

**Run this only when you intend to create disposable test VMs.** The harness creates and
removes its own two deny-all VMs; it does not attach to a user VM. It uses the local
`sandbox-studio-base:dev` image with `PullPolicyNever` and the production registry against
a private temporary catalog. It reopens that catalog and registry on the same allocated
port before the destination pulls. The dev-base cache must already be warm. The source
VM is removed before the destination starts to fit the memory budget.

On 2026-10-09, this full Go guest-export source/import roundtrip (then over the vsock
`Hub.Export`, now over msb exec) passed on Linux amd64, microsandbox 0.7.7 and kernel 6.12.111. Owner SIGKILL after the first data
frame and SIGKILL of the exact freeze watchdog both rejected the transfer without an
artifact, and source writes resumed after owner death and emergency thaw. The normal
source/import validation covered regular files, modes, symlinks, hardlinks, whiteouts,
opaque directories, untouched base state and exclusions. See the [full evidence and
remaining qualifications](../../docs/spikes.md#layer-transfer-and-capture-foundations).

The persistent authenticated registry is implemented and qualified by this harness.
Template builder/cache invocation, recovery jobs, API and UI are not wired. Remaining live
qualifications are macOS and Windows hosts, an arm64 guest, and
oversized or backpressured full captures. Userspace cannot guarantee automatic recovery if
a kernel freeze/thaw call never returns; stopping or rebooting the VM remains possible.
This roundtrip does not complete all M3 work.
