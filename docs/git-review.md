# Git review

Sandboxes never hold forge credentials. They clone, fetch and push through a virtual remote
that Studio serves in the gateway:

```
https://git.studio.internal/<forge>/<owner>/<repo>.git
```

`<forge>` is the name of a forge added on an environment's Git page. The host resolves only
inside sandboxes (dnsproxy) and is served with the environment CA, like any intercepted
host. Code: `internal/gitreview`, the API in `internal/api/git.go`, the UI in
`web/src/routes/e.$env.git.tsx` and `web/src/components/git-review.tsx`.

## Forges

- Per environment: name (`^[a-z0-9][a-z0-9-]{0,31}$`, part of the remote URL), kind and
  base URL. The base URL follows the same rules as a provider's: `https://`, a public host,
  no credentials, query or fragment, no private, loopback or link-local address.
- Kind `forgejo` (Forgejo, Gitea, Codeberg). `github` is accepted by the interface and
  answers "not implemented yet".
- The token is stored as a secret `FORGE_<NAME>_TOKEN` marked studio-only: it has no
  host binding, so the gateway never substitutes it into sandbox traffic, network rules
  can't name it, and the secrets API refuses to edit or delete it (409; the secrets page
  shows it as "Forge token"). It is created and deleted with its forge.
- "Test" calls the forge API (`GET /api/v1/user`) and shows whose token it is.
- Deleting a forge also deletes its staging repos.

## Fetch

`GET info/refs?service=git-upload-pack` and `POST git-upload-pack` are proxied to
`<baseUrl>/<owner>/<repo>.git`. Studio adds `Authorization: Basic sandbox-studio:<token>` on
the host; the sandbox's own `Authorization` header is dropped, and error texts from the
forge are masked. Dumb HTTP, other paths and `..`-style names are refused. A fetch request
body is limited to 16 MiB.

## Push

1. `GET info/refs?service=git-receive-pack`: Studio mirrors the forge's branches and tags
   into a host-side bare repo
   (`<data>/git-staging/<env-id>/<sandbox-id>/<forge-id>/<owner>/<repo>.git`) and advertises them.
   Each sandbox has its own staging repo, so sandboxes and environments never see each
   other's pushes.
2. `POST git-receive-pack`: the pack (at most 100 MiB, else 413) is stored. Each command
   is checked:
   - only `refs/heads/*`, no deletes, at most 32 updates;
   - the old value must match the mirrored ref ("stale info" otherwise);
   - the new commit and everything it reaches must be present;
   - the new commit must descend from the old one: non-fast-forward pushes are refused.
3. An accepted update is kept under `refs/studio/push/<id>`, a `git.push` approval is
   raised (subject `<forge>/<owner>/<repo>:<branch>`, deduplicated; a newer push to the
   same branch supersedes a pending one), and git prints
   `remote: Sandbox Studio: <branch> queued for review (approval <id>)`. The push
   "succeeds" from git's point of view; the upstream branch is unchanged.

The approval shows persona, sandbox, repository, branch, old and new commit, up to 50
commits, and a unified diff bounded at 512 KiB. Binary files, submodules and files over
1 MiB are summarized instead of diffed.

## Decisions

- **Approve:** Studio pushes the staged commit upstream with the token, provided the
  upstream branch is still at the reviewed old commit (or already at the new one). The
  push state becomes `pushed` or `failed` with the forge's message.
- **Reject** (optionally with a note) or **ignore:** the push becomes `rejected`.
- A rejected or failed push is delivered to the sandbox on its next fetch of that
  repository: that fetch fails once with the reason and note, and the next one works.
  `TODO(M5)`: also notify the agent over MCP.
- **Forgejo pull request:** after an approved push to a branch other than the repository's
  default branch, Studio raises a `git.pr` approval proposing `<branch> → <default>` with a
  title and body from the commits. Approving it calls
  `POST /api/v1/repos/<owner>/<repo>/pulls` (`Authorization: token <token>`) and records the
  PR URL, or "failed: <reason>" (for example when a PR for the branch already exists).
  Rejecting it records "not opened".

Approvals are decided through the usual `POST /api/environments/{env}/approvals/{id}/decide`
endpoint (`note` is optional); the integration owning the approval kind handles it.

## Integration interface

`internal/integrations` holds the minimal compiled-in interface from PLAN.md §6.9:

```go
type Integration interface {
    ID() string
    Routes() []gateway.VirtualHost
    ApprovalKinds() []ApprovalKind // kind + Decide func
}
```

Git review is the only integration. MCP tools and jobs are added to the interface when an
integration needs them.

## Limits and open points

- Only branches; tags and deletes are refused.
- The approve push runs inside the decide request (up to five minutes).
- Staging repos are removed with their forge, not yet with their sandbox.
- Computing a review walks the new commits (`revlist`); very large first pushes are slow.
