# Transfer speed

Measures how fast bulk data leaves and enters a VM, to choose the transport of the copies
between VMs that fork, rebase and disk resize make: msb exec stdout and stdin, a shared host
directory, TCP through a SOCKS5 proxy (the way to Studio's gateway), and studio-agent's own
export and import of a real tree. One alpine VM in a scratch `MSB_HOME`, with the guest agent
bind mounted, a host tree mounted read-only at `/workspace`, an owned disk at `/home/agent`
and a shared directory at `/share`. The proxy is the spike's own; nothing leaves the host.

```sh
make agent
go build -o "$TMP/transfer-speed" ./spikes/transfer-speed
export MSB_HOME="$(mktemp -d -p "$HOME/git")"   # short: socket paths are limited to 108 bytes
"$TMP/transfer-speed" -agent internal/agentbin/dist/studio-agent-linux-amd64 -tree web/node_modules
rm -rf "$MSB_HOME"
```

`-archive-only` skips the raw streams, which take about four minutes, most of it exec stdout.

## 2026-10-31, Linux amd64, msb 0.7.8, 4 CPUs, 2 GiB

| Stream (1 GiB of zeros) | MB/s |
|---|---:|
| guest disk write, `dd` with fsync | 186 |
| msb exec stdout, guest to host | 4.9 |
| msb exec stdin, host to guest | 98 |
| shared directory, guest writes | 110 |
| shared directory, guest reads | 798 |
| TCP through SOCKS5, guest to host | 85 |
| TCP through SOCKS5, host to guest | 146 |

`web/node_modules`: 334 MB in 34,000 files, 68 MB as a zstd tar.

| Archive (MB of files per second) | MB/s |
|---|---:|
| `workspace-export --zstd` from the read-only host mount, into `/dev/null` | 17 |
| `home-import` over exec stdin | 71–78 |
| `home-export --zstd` from ext4, cold, into `/dev/null` | 48 |
| the same, warm | 108 |
| the same over exec stdout | 30 |
| the same into TCP, cold | 48 |
| `home-export --zstd` of 1 GB from `/dev/urandom`, into TCP | 121–128 |
| the same, uploaded by the agent over TLS (`STUDIO_TRANSFER_URL`) | 90 |

Exec stdout is the one slow way out of a guest, by 17 to 25 times. The host mount is slow
for many small files (FUSE lookups), so it says nothing about a real source, which is an
owned ext4 disk. A shared directory can't serve a copy anyway: the source VM's mounts were
fixed when it was created. So Studio's copies upload the export through the gateway
(`transfer.studio.internal`) and keep exec stdin for the import.
