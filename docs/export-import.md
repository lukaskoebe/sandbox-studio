# Export and import

A sandbox can be saved to a `.studio-sandbox` file and recreated from it, in the same
or another environment or Studio install.

## Format

```
sandbox-studio-export v1\n          magic line
uint32 big-endian                   manifest length, at most 1 MiB
manifest JSON
home chunks                         format 2 only: each a uint32 big-endian length and that
                                    many bytes, ended by a zero length; the chunks hold a
                                    gzip PAX tar of /home/agent
workspace archive                   gzip PAX tar of /workspace, to end of file
```

The manifest:

```json
{
  "format": 2,
  "exportedAt": "2026-10-10T12:00:00Z",
  "name": "my-sandbox",
  "templateSpecYAML": "base: ...\n",
  "resources": { "cpus": 2, "memoryMiB": 4096, "maxMemoryMiB": 8192,
                 "workspaceMiB": 20480, "dockerMiB": 20480 },
  "workspaceBytes": 0
}
```

- `format` is `2`. Format 1 files, from before the home was exported, have no home chunks and
  import with a home seeded from the image skeleton. A format 2 file of a sandbox without a
  home disk (one made from a legacy template) has no home chunks either, only the zero
  length, and imports the same way.
- `templateSpecYAML` is the canonical template spec, or `null` for a base-image sandbox.
- `workspaceBytes` is `0` because the archive is streamed. Import reads to end of file. A
  non-zero value is enforced as the exact archive length.

## Included and not included

Included: name, template spec, resources, the contents of `/workspace` and the contents of
`/home/agent`. The home holds what the agent keeps there: harness sessions and history,
logins, caches, and any credential you put in it yourself. Read it before you share a file.

Not included: secrets, egress identity, approvals, checkpoints, Docker data and the root
filesystem outside the home. The imported sandbox gets a fresh identity and runs with the
target environment's secrets and policy. Secrets scoped to the original sandbox or its
teammate stay behind.

## Export

`GET /api/environments/{env}/sandboxes/{id}/export` streams the file. The archives come from
the same `home-export` and `workspace-export` used by rebase and fork ([rebase.md](rebase.md)).

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
   template spec must parse. The manifest's resources are the sandbox's, which a resize may
   have moved away from its template's; the import keeps them, and `homeMiB` (absent: 4 GiB)
   sizes the home disk.
2. Free disk must cover the archives plus 1 GiB. The upload is capped at the workspace size
   plus the home disk's size, and staged under `<data>/imports`. Nothing is extracted on the host.
3. The template spec goes through the normal template build, so a cached template is reused.
4. Studio creates the sandbox in transfer mode, streams the home archive into `home-import`
   (when the file has one) and the workspace archive into `workspace-import` in the guest,
   and boots it. The new sandbox gets a home disk only if its template has one; the import's
   template is always built from the current exporter, so it does.
5. A name already in use gets a suffix: `name-2`, `name-3`, and so on.

The staged file is deleted on success and failure. A failure removes the half-made
sandbox. Error messages never contain host paths. At startup, imports still in progress
are marked failed, their sandboxes are removed and the staging directory is emptied.
Statuses older than 7 days are pruned.

## Environments

An environment's configuration is exported as a `.studio-environment` file, a JSON document,
and imported as a new environment. Settings → General has both buttons.

- `GET /api/environments/{env}/export` downloads the file.
- `POST /api/environment-imports` with the file as an `application/json` body creates the
  environment and replies `201` with the environment and what is still to add.

Included: the name, the providers (name, kind, base URL and model for an OpenAI-compatible
endpoint), the teammates (role, avatar, approval scope, soul, harness, provider by name,
model, git identity and whether it uses the host's), the network rules of the environment and of its teammates, the secret
names with the hosts they bind to and the teammate they are scoped to, and the template
specs in YAML.

Not included: sandboxes, sandbox-scoped rules and secrets, provider keys, secret values,
Studio's own secrets (forge tokens), memory, approvals, chats, the approval pattern levels
teammates remember, and built templates (only their specs). The template specs and the
teammates' soul are free text: they can contain whatever the user wrote, so read the file
before sharing it.

On import, the file is untrusted. It is read with a 2 MiB limit, unknown fields and trailing
data are refused, and every value goes through the same checks as the API: names, hosts,
models, base URLs, avatars, harness and provider pairing, rule configuration (a Caddyfile is
compiled, and a header may name only a secret the file lists), template specs. Teammates,
providers, secrets and rules refer to each other by name within the file. An invalid file
creates nothing; the errors name the offending value, never a host path. Validation runs
against the file's own secrets, which are not created yet, so a rule may name them.

The import creates everything in one transaction. Providers come without keys, which makes
them `key_required` until a key is added on Settings → AI providers (`PUT /api/environments/{env}/
providers/{id}` with `apiKey`). Secrets are not created, because a secret needs a value;
`toAdd` lists each one with its hosts and teammate. `templates` holds the template specs to
build from Settings → Templates. A name already in use gets a number: `work 2`, `work 3`.
