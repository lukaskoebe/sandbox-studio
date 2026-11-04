# Transfer memory

Measures what a rebase's copy costs the host in memory: a VM's host process keeps each page of
guest memory resident once the guest has touched it, and a copy streams a tree through the
guest's page cache. One alpine VM (1 CPU, 2 GiB) in a scratch `MSB_HOME`, with an owned disk at
`/data` holding a tree of one 1200 MiB file and 20,000 files of 32 KiB. `guestcopy` runs the
guest's own export and import on it through a pipe within one process, so that one VM does what
two do in a rebase. The VM is started again before each copy, as a rebase starts its source, and
its host RSS is sampled twice a second.

The VM takes up to 2 GiB of host memory: check that much is free first.

```sh
J=$(mktemp -d -p "$HOME/git"); mkdir "$J/bin"
CGO_ENABLED=0 go build -o "$J/bin/guestcopy" ./spikes/transfer-memory/guestcopy
# guestcopy-old: the same program built from the checkout to compare with.
go build -o "$J/transfer-memory" ./spikes/transfer-memory
SPIKE_BIN="$J/bin" MSB_HOME="$J/msb" "$J/transfer-memory"
rm -rf "$J"
```

## 2026-11-04, Linux amd64, msb 0.7.8, kernel 6.12.111

| Copy of 1826 MiB | Took | Host RSS before | At the peak | Guest cache after |
|---|---:|---:|---:|---:|
| v0.15.0 | 48.1 s | 108 MiB | 2106 MiB | 1832 MiB |
| dropping the cache as it goes | 28.9 s | 108 MiB | 256 MiB | 8 MiB |

Slab grew from about 40 MiB to 74 MiB, against 114 MiB before: 20,000 files on each of two sides
of the copy cost about 1 KiB of directory entries and inodes per file for each side.

An earlier version of the spike measured the raw ways of doing I/O in the same VM, from 100 MiB
resident at boot, writing 1200 MiB:

- `O_DIRECT`: 101 MiB after both the write and the read, and the cache stays empty.
- Buffered: 1336 MiB after the write, 2085 MiB after a read on top.
- `drop_caches` afterwards: the guest's memory is free at once, and the host's follows slowly and
  partly. Left alone, RSS went from 2090 to 1447 MiB in about 40 s. With `Modify` of the live
  memory called meanwhile it went 1609, 841, 329, 165 MiB over 15 s, though each call answered
  `"actual": "2 GiB"` and `MemTotal` didn't move. Why the calls made the difference wasn't
  established, and the fix doesn't depend on it: the guest doesn't touch the pages to begin with.
