# Git review

Sandboxes never hold forge credentials. They clone, fetch and push through a virtual remote
that Studio serves in the gateway:

```
https://git.studio.internal/<forge>/<path>.git
```

`<forge>` is the name of a forge added under **Settings → Git**. `<path>` has 1 to 8
segments (each `^[A-Za-z0-9_][A-Za-z0-9._-]{0,99}$`, no `..`), so gitolite's `repo`,
GitHub's `owner/repo` and GitLab's `group/sub/repo` all work; on forges with an API
(Forgejo, GitHub) it must be exactly `owner/repo`. The store keeps everything before the
last segment in `owner`. The host resolves only
inside sandboxes (dnsproxy) and is served with the environment CA, like any intercepted
host. Code: `internal/gitreview`, the API in `internal/api/git.go`, the UI in
`web/src/routes/e.$env.git.tsx` and `web/src/components/git-review.tsx`.

## Forges

A forge is a kind, a base URL, a username, one studio-only credential and, for SSH, a pinned
host key. An SSH forge can sign with your SSH agent instead of a credential. The kind chooses
the transport (HTTPS or SSH) and whether there is an API for pull requests.

- Per environment: short name (`^[a-z0-9][a-z0-9-]{0,31}$`, used in the remote URL), kind and
  base URL. Pasting a repository URL such as `git@github.com:owner/repo.git` into the Add
  forge dialog fills these in. An HTTPS base URL follows the same rules as a provider's: `https://`, a public
  host, no credentials, query or fragment.
- Kinds:
  - `forgejo` (Forgejo, Gitea, Codeberg): git over HTTPS as `sandbox-studio:<token>`, API
    `<base>/api/v1`.
  - `github` (github.com, GitHub Enterprise Server): git over HTTPS as
    `x-access-token:<token>`, API `https://api.github.com` or `<base>/api/v3`, with
    `Authorization: Bearer`, `X-GitHub-Api-Version: 2022-11-28`. Use a fine-grained personal
    access token for only the repositories sandboxes may use, with **Contents: read and
    write**, **Pull requests: read and write** and **Metadata: read**. A fine-grained token
    can't push where you are only an outside collaborator; a classic token with `repo`
    works there too.
  - `https` (GitLab, Bitbucket, any smart-HTTP host): git over HTTPS as
    `<username>:<token>`; the username defaults to `git` (Bitbucket wants `x-token-auth`,
    GitLab takes any). No API: no pull requests, and the default branch isn't needed.
  - `ssh` (gitolite, a VPS, any host serving git over SSH): see [SSH forges](#ssh-forges).
- Private, loopback, link-local and CGNAT hosts are refused, for HTTPS and SSH alike, and
  every upstream connection goes through `gateway.DialPublic`, so DNS can't rebind past the
  check. A LAN forge would let a sandbox drive requests into your network through Studio.
- The token is stored as a secret `FORGE_<NAME>_TOKEN` (for `ssh` with a Studio key, the key
  as `FORGE_<NAME>_SSH_KEY`; an SSH forge that signs with the agent has no secret) marked
  studio-only: it has no host binding, so the gateway never
  substitutes it into sandbox traffic, network rules can't name it, and the secrets API
  refuses to edit or delete it (409; the secrets page shows it as "Forge token" or "Forge
  SSH key"). It is created and deleted with its forge.
- "Replace token" (`PUT …/forges/{id}/token`) swaps the token in place and keeps the staged
  pushes, since tokens expire.
- "Test" asks the API whose token it is (`GET /api/v1/user`, GitHub `GET /user` with the
  token's expiry, amber under 14 days) and, given a repository, lists its branches over git.
  Kinds without an API need the repository.
- Deleting a forge also deletes its staging repos.

## Remote mapping

The managed git config Studio writes into each sandbox at session start
(`~/.config/git/config`, `harness.GitConfig`) sends the forges' real URLs to the virtual
remote, so `git clone https://github.com/o/r` and existing remotes just work:

```
[url "https://git.studio.internal/gh/"]
	insteadOf = "https://github.com/"
	insteadOf = "git@github.com:"
	insteadOf = "ssh://git@github.com/"
[url "https://git.studio.internal/box/"]
	insteadOf = "ssh://gitolite@git.example.org/"
	insteadOf = "gitolite@git.example.org:"
```

HTTPS kinds get the base URL plus the scp and `ssh://` forms with user `git`; an `ssh`
forge gets its own user and port (the scp form only on port 22). When two forges share a
host, the oldest one gets the rules; the other is reached with its explicit virtual URL.
The config holds only these internal URLs, never a token or key.

## SSH forges

- The base URL is `ssh://<user>@<host>[:<port>]`; the dialog also takes `git@host` and
  `git@host:` and normalizes them (user `git` if left out, port 22 omitted).
- A pasted `git@host:owner/repo.git` or `ssh://` URL in the Add forge dialog fills in the
  rest of the form.
- **Agent (the default):** Studio signs with the keys in your SSH agent, the one `ssh` uses:
  `SSH_AUTH_SOCK` on Linux and macOS, the OpenSSH agent's pipe `\\.\pipe\openssh-ssh-agent`
  on Windows. Studio reads the environment it was started with, so start it from a shell where
  `ssh-add -l` lists your key, or set `SSH_AUTH_SOCK` for autostart. Studio offers the first
  six keys. Only Studio's own fetches and approved pushes use the agent; the socket is never
  forwarded into a sandbox, and sandboxes still go through review. A hardware key or a
  password manager may ask you to confirm each signature. When a signature takes more than two
  seconds, the forge row suggests a dedicated Studio key, which you can dismiss. If the agent
  can't be reached, the forge says "SSH agent not available", and `studio doctor` warns.
- **Studio key:** choose "A Studio key" in the dialog, or switch the forge to it. Studio
  generates an ed25519 key and seals it in the vault as `FORGE_<NAME>_SSH_KEY`; it is never
  written to a file and never reaches a sandbox. The public key (`ssh-ed25519 …
  sandbox-studio/<env>/<forge>`) is shown under **Settings → Git** with a copy button: add it to the
  host with write access (a deploy key for one repository is enough). Switching back to the
  agent deletes the key. Studio never reads your `~/.ssh` keys.
- **Host key:** the Add forge dialog scans the host (`POST …/forges/scan`: handshake only,
  no authentication) and shows the `SHA256:` fingerprint; you confirm it is the one the
  host publishes. Every connection pins exactly that key, with the host-key algorithms
  restricted to its type. If the host offers another key, fetches, pushes and Test fail
  with "the host key of <host> changed (pinned …, offered …)", the forge row shows the
  offered fingerprint, and "Re-pin" scans again and pins the key you confirm
  (`PUT …/forges/{id}/host-key`).
- **Client:** Studio's own client on `golang.org/x/crypto/ssh` over a `DialPublic`
  connection (go-git's SSH transport reads `~/.ssh/config` and proxies from the
  environment, so it isn't used). It runs `git-upload-pack '<path>'` and
  `git-receive-pack '<path>'`, sets no environment variable but `GIT_PROTOCOL`, and
  requests no agent forwarding, pty or port forwarding. One connection per request.
- **Bridging fetches:** each HTTP request from the sandbox opens one SSH session. When the
  sandbox sends `Git-Protocol: version=2`, Studio sets `GIT_PROTOCOL=version=2`; it reads
  the advertisement up to the flush packet, then for `info/refs` returns it (behind the
  `# service=` header for v0), and for a `POST` discards it, writes the request body,
  closes stdin and streams the response. Protocol v2 is stateless, so this is exact.
  Servers that ignore `GIT_PROTOCOL` (OpenSSH without `AcceptEnv GIT_PROTOCOL`) answer v0;
  git then speaks v0 stateless RPC, and each negotiation round runs as a fresh
  upload-pack session that answers it and stops at end of input. Both are tested against
  real git with multi-round negotiation.
- **Paths:** the repository path is sent as written, relative to the user's home like the
  scp form `user@host:path` (no `.git` is added). Hosts that need an absolute path can't be
  reached through the virtual remote.
- Pushes are approved like any other: `git-receive-pack` over SSH with the staged pack.
  The approval says how Studio will push ("over SSH to ssh://git@host/path, host key
  SHA256:…"). No pull requests.
- A sandbox reaching an SSH host without a forge makes a plain TCP connection through the
  gateway, decided by the network policy like any host and port; it has no key unless the
  agent made one and someone registered it, which bypasses review.

## Fetch

`GET info/refs?service=git-upload-pack` and `POST git-upload-pack` are proxied to
`<baseUrl>/<path>.git` (bridged to SSH for `ssh` forges). Studio adds basic auth with the
token on the host; the sandbox's own `Authorization` header is dropped, and error texts from the
forge are masked. Dumb HTTP, other paths and `..`-style names are refused. A fetch request
body is limited to 16 MiB.

## Push

1. `GET info/refs?service=git-receive-pack`: Studio mirrors the forge's branches and tags
   into a host-side bare repo
   (`<data>/git-staging/<env-id>/<sandbox-id>/<forge-id>/<path>.git`) and advertises them.
   Each sandbox has its own staging repo, so sandboxes and environments never see each
   other's pushes.
   Deleting a sandbox deletes its staging repos (`<data>/git-staging/<env-id>/<sandbox-id>`);
   a failure there is logged and doesn't fail the delete. At startup Studio sweeps the
   staging root and removes, with a log line each, the directories of sandboxes no longer in
   the catalog; stopped or crashed sandboxes keep theirs. Removal only accepts store IDs
   and plain directories below the staging root: symlinks and other entries are never
   followed or removed.
2. `POST git-receive-pack`: the pack (at most 100 MiB, else 413) is stored. Each command
   is checked:
   - only `refs/heads/*`, no deletes, at most 32 updates;
   - the old value must match the mirrored ref ("stale info" otherwise);
   - the new commit and everything it reaches must be present;
   - the new commit must descend from the old one: non-fast-forward pushes are refused.
3. An accepted update is kept under `refs/studio/push/<id>`, a `git.push` approval is
   raised (subject `<forge>/<path>:<branch>`, deduplicated; a newer push to the
   same branch supersedes a pending one), and git prints
   `remote: Sandbox Studio: <branch> queued for review (approval <id>)`. The push
   "succeeds" from git's point of view; the upstream branch is unchanged.

The approval shows teammate, sandbox, repository, branch, old and new commit, up to 50
commits, and a unified diff bounded at 512 KiB. Binary files, submodules and files over
1 MiB are summarized instead of diffed.

## Decisions

- **Approve:** Studio pushes the staged commit upstream with the token or key, provided the
  upstream branch is still at the reviewed old commit (or already at the new one). The
  push state becomes `pushed` or `failed` with the forge's message.
- **Reject** (optionally with a note) or **ignore:** the push becomes `rejected`.
- A rejected or failed push is delivered to the sandbox on its next fetch of that
  repository: that fetch fails once with the reason and note, and the next one works.
  Not built: also notifying the agent over MCP.
- **Pull request** (Forgejo, GitHub): after an approved push to a branch other than the
  repository's default branch, Studio raises a `git.pr` approval proposing
  `<branch> → <default>` with a title and body from the commits. Approving it calls
  `POST /api/v1/repos/<owner>/<repo>/pulls` (Forgejo, `Authorization: token <token>`) or
  `POST /repos/<owner>/<repo>/pulls` (GitHub) and records the PR URL, or
  "failed: <reason>" (for example when a PR for the branch already exists). Rejecting it
  records "not opened". `https` and `ssh` forges have no API and get no PR proposal.

Approvals are decided through the usual `POST /api/environments/{env}/approvals/{id}/decide`
endpoint (`note` is optional); the integration owning the approval kind handles it.

## Integration interface

`internal/integrations` holds a minimal compiled-in interface:

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
- Computing a review walks the new commits (`revlist`); very large first pushes are slow.
- No GitLab merge requests yet (GitLab works as kind `https`), no GitHub App sign-in.
