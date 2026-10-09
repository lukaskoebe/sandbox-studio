# Vsock bulk transport spike

This Linux-only diagnostic sends the same 128 MiB deterministic byte pattern in two
sequential guest commands on one fresh, unfrozen VM: raw vsock, then one guest-opened yamux
stream using v0.1.2 defaults. For each mode, the host checks the announced mode and size,
exact payload count, and SHA-256, then sends a receipt ACK. The guest validates it before
half-closing its write side. The host rejects trailing bytes, requires genuine EOF, and only
then sends the final ACK the guest needs to exit successfully. It records byte progress,
duration, and up to 32 KiB of guest diagnostics per mode. It has no filesystem-freeze, OCI,
or gzip path; the guest probe makes no installs or filesystem writes beyond existing boot
logs.

After review, build from the repository root on Linux/amd64. Point `BUILD_DIR` at an
SSD-backed directory so the host binary does not land in quota-limited `/tmp`; `/tmp` is used
only for the small private guest copy and short Unix socket path.

```sh
set -eu
: "${BUILD_DIR:?set BUILD_DIR to an SSD-backed build directory}"
mkdir -p "$BUILD_DIR"
GOMAXPROCS=2 CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -p=1 -trimpath -o "$BUILD_DIR/vsock-bulk-guest" ./spikes/vsock-bulk/guest
GOMAXPROCS=2 go build -p=1 -trimpath -o "$BUILD_DIR/vsock-bulk-host" ./spikes/vsock-bulk
"$BUILD_DIR/vsock-bulk-host" -scratch /tmp -guest "$BUILD_DIR/vsock-bulk-guest"
```

To cap each underlying vsock `Write` at 16 KiB, add `-max-write 16384`:

```sh
"$BUILD_DIR/vsock-bulk-host" -scratch /tmp -max-write 16384 -guest "$BUILD_DIR/vsock-bulk-guest"
```

`-max-write` defaults to `0`, which leaves the connection unchanged. Values `1` through
`65536` cap each underlying `net.Conn.Write`; larger and negative values are rejected. The
cap wraps the connection before the raw/yamux choice, so a positive cap also applies to
yamux's underlying vsock writes. It does not change payload size, deadlines, or yamux windows.

The host requires the already cached `sandbox-studio-base:dev`; `PullNever` prevents image
pulls. It creates one uniquely named 1 CPU, 512 MiB, deny-all VM and mounts only its private
guest-binary directory read-only. Its private vsock route is port 5001, separate from the
Studio agent's port 5000. It never opens Studio's production catalog or keychain,
and refuses all three protected VM names. `-scratch` must name an existing short directory;
it defaults to `/tmp` because long `TMPDIR` paths can exceed the Unix socket route limit.

Each guest command and receiver has a 45 second deadline; the guest I/O deadline is 40
seconds. The full run has a 4 minute deadline and cleanup has a separate 15 second bound.
The second mode runs after a first-mode failure only when the native command exit is known
and runtime work has settled. Errors accumulate across modes, so partial transfer cannot
report a pass. Owned removal is guarded by exact name and labels and by `PendingRun`.

Before `CreateSandbox`, the host exclusively writes and syncs a mode-0600 `recovery.json`
containing the exact VM name and owner labels. It removes that file only after `RemoveOwned`
verifies VM absence. If creation returns an error or a native operation remains pending, the
VM state is unconfirmed and the private path stays in place. A `CreateSandbox` call that
returns after this process exits cannot have its late handle consumed by this invocation;
the retained record gives the exact identity for a later ownership-checked recovery.

## Earlier diagnostic evidence

The earlier uncapped full-template diagnostic had two separate stack captures. The guest
stack, captured at 8 seconds, showed a raw-vsock `Write` call length of 32,728 bytes; it does
not establish that the call blocked for 8 seconds. A separate host stack, captured at 15
seconds, showed 204 bytes remaining in a yamux DATA frame, not in that raw guest `Write`.
These captures predate the full-worker 16 KiB cap run, which still failed with EOF and a guest
write timeout (see the [template worker record](../template-jobs/README.md)).

The earlier uncapped bulk baseline failed too: raw delivered 0 bytes to the host while the
guest reported 262,112 sent, yamux delivered 12 payload bytes, and its runtime log had eight
`BufDescTooSmall` errors. That baseline used ACK-after-FIN ordering.

## Capped two-phase diagnostic — 2026-10-09

The new run used `-max-write 16384` and a two-phase receipt protocol: the guest receives the
receipt ACK before FIN, then receives the final ACK only after the host verifies EOF. Both
raw-write cap and ACK ordering changed from the old baseline, so this comparison does not
prove the cap alone fixes parser errors.

| Mode | Host payload received | Guest-reported completed writes | Duration | Result |
| --- | ---: | ---: | ---: | --- |
| Raw | 262,112 / 134,217,728 | 262,112 | 40.078s | FAIL |
| Yamux | 1,703,936 / 134,217,728 | 1,572,864 | 30.069s | FAIL |

The host receive count and guest completed-write count are separate observations; in
particular, the yamux values must not be treated as equal bytes delivered. The 4,042-byte
runtime capture contains no `BufDescTooSmall`, but it does contain shutdown and cleanup
`WARN` entries. The owned VM removal was verified; only the three protected VMs remained.
Logs: `/tmp/studio-m3-next/vsock-bulk-capped-live.log` and
`/tmp/studio-m3-next/vsock-bulk-capped-runtime.log`.

Focused and full Go race test suites plus `go vet` passed. The live
probe is diagnostic: its partial-transfer failures are observed evidence, not an acceptance
pass. Production transport is unchanged. These
results do not establish the root cause or resolve the separate template-export failure.

Creation uses the SDK directly because `internal/runtime.Create` adds mounts and egress
configuration. Guest commands and owned removal use `internal/runtime` identity checks and
pending-work guards.
