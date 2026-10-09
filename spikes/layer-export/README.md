# In-guest layer freeze probe

This disposable-guest spike qualifies only the pinned-upper freeze/read window and watchdog lifecycle. It does not stream yamux data, create a tar archive, or establish exporter correctness.

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

## Qualification limits

All four modes passed on 2026-10-09 in a Linux amd64 guest on microsandbox 0.7.7. Each
reported writer resumption and successful cleanup; only `normal` reported a capture
candidate. Docker remained responsive afterward.

The spike exercises a small concurrent writer and a 0.6-second frozen read window. It
does not qualify full-tree tar consistency, xattrs/whiteouts/metacopy, symlink and hardlink
handling, yamux backpressure, or recovery from a stuck freeze ioctl, watchdog failure,
or complete guest-agent failure. Those are still production-export qualification work.
