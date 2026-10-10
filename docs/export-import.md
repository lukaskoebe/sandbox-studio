# Export and import

A sandbox can be saved to a `.studio-sandbox` file and recreated from it, in the same
or another environment or Studio install.

## Format

```
sandbox-studio-export v1\n          magic line
uint32 big-endian                   manifest length, at most 1 MiB
manifest JSON
workspace archive                   gzip PAX tar of /workspace, to end of file
```

The manifest:

```json
{
  "format": 1,
  "exportedAt": "2026-10-10T12:00:00Z",
  "name": "my-sandbox",
  "templateSpecYAML": "base: ...\n",
  "resources": { "cpus": 2, "memoryMiB": 4096, "maxMemoryMiB": 8192,
                 "workspaceMiB": 20480, "dockerMiB": 20480 },
  "workspaceBytes": 0
}
```

- `templateSpecYAML` is the canonical template spec, or `null` for a base-image sandbox.
- `workspaceBytes` is `0` because the archive is streamed. Import reads to end of file. A
  non-zero value is enforced as the exact archive length.

## Included and not included

Included: name, template spec, resources and the contents of `/workspace`.

Not included: secrets, egress identity, approvals, checkpoints and Docker data. The
imported sandbox gets a fresh identity and runs with the target environment's secrets
and policy.

## Export

`GET /api/environments/{env}/sandboxes/{id}/export` streams the file. The archive comes from
the same `workspace-export` used by rebase and fork ([rebase.md](rebase.md)).

- A stopped sandbox is started in transfer mode, without guest services, and stopped again
  afterwards.
- A running sandbox is exported live. There is no freeze. Files written during the export
  may be captured half-written or not at all. Stop the sandbox first for a consistent copy.
- If the export fails after the response started, the connection is aborted, so a
  truncated download never looks complete.

## Import

`POST /api/environments/{env}/sandbox-imports` with the file as an
`application/octet-stream` body. The reply is `202` with an import status resource at
`/api/environments/{env}/sandbox-imports/{id}`. `POST .../{id}/cancel` cancels it.

The states are `uploading`, `building`, `creating`, `importing`, `ready` (with
`sandboxId`) and `failed` (with `error`).

1. Studio checks the header and manifest before anything is stored. The file is
   untrusted: reads are bounded, unknown fields are rejected, names must be valid and the
   template spec must parse and match the manifest resources.
2. Free disk must cover the workspace size plus 1 GiB. The upload is capped at the
   workspace size and staged under `<data>/imports`. Nothing is extracted on the host.
3. The template spec goes through the normal template build, so a cached template is reused.
4. Studio creates the sandbox in transfer mode, streams the archive into
   `workspace-import` in the guest, and boots it.
5. A name already in use gets a suffix: `name-2`, `name-3`, and so on.

The staged file is deleted on success and failure. A failure removes the half-made
sandbox. Error messages never contain host paths. At startup, imports still in progress
are marked failed, their sandboxes are removed and the staging directory is emptied.
Statuses older than 7 days are pruned.
