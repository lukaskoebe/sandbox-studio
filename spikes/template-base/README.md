# Template base and guest configuration qualification

From the repository root on a Linux amd64 host with the Studio dev base already loaded:

```sh
probe_dir="$(mktemp -d)"
GOMAXPROCS=2 CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -p=1 -o "$probe_dir/studio-agent" ./cmd/studio-agent
GOMAXPROCS=2 go run -p=1 ./spikes/template-base -agent "$probe_dir/studio-agent"
rm -rf "$probe_dir"
```

This deliberately creates disposable VMs, one at a time. It first calls production
`PrepareTemplateBase`, which prewarms the exact local dev alias with `Never` pull policy
and deny-all networking. It then creates a separate, privately cataloged sandbox with
512 MiB initial memory, a 1 GiB ceiling, and unreachable proxy/DNS endpoints. The probe
uses a temporary CA and a dummy placeholder; it never opens the OS keychain.

The second VM runs the production guest boot and configuration ACK path. The probe checks
the SDK's persisted memory settings, the installed CA and environment files, and the
placeholder exported by a login shell. Cleanup verifies ownership before removing its VM.
If cleanup fails, the error identifies the VM and retains the private state directory.
Existing VMs are never stopped or deleted.

Passed on Linux amd64 with microsandbox 0.7.7 on 2026-10-09, using the five-layer Studio
dev base. No new prewarm VM remained, all configuration checks passed, and the final VM
and temporary state were removed. This qualifies the warm development-base path only;
cold-cache pulls, release digest wiring, other host platforms, and build-job recovery
remain separate work.
