# Git review

Sandboxes never hold forge credentials. They clone, fetch and push through a virtual remote
that Studio serves in the gateway:

```
https://git.studio.internal/<forge>/<path>.git
```

`<forge>` is the name of a forge added under **Settings → Git forges**. `<path>` has 1 to 8
segments (each `^[A-Za-z0-9_][A-Za-z0-9._-]{0,99}$`, no `..`), so gitolite's `repo`,
GitHub's `owner/repo` and GitLab's `group/sub/repo` all work; on Forgejo and GitHub it must
be exactly `owner/repo`. The store keeps everything before the last segment in `owner`. The host resolves only
inside sandboxes (dnsproxy) and is served with the environment CA, like any intercepted
host. Code: `internal/gitreview`, the API in `internal/api/git.go`, the UI in
`web/src/routes/e.$env.settings.forges.tsx`, `web/src/routes/e.$env.git.reviews.$id.tsx` and
`web/src/components/git-review.tsx`.

## Forges

A forge is a kind, a base URL, a username, one studio-only credential and, for SSH, a pinned
host key. An SSH forge can sign with your SSH agent instead of a credential. The kind chooses
the transport (HTTPS or SSH) and whether there is an API for pull requests.

- Per environment: name (`^[a-z0-9][a-z0-9-]{0,31}$`, used in the remote URL), kind and
  base URL. Pasting a repository URL such as `git@github.com:owner/repo.git` into the Add
  forge dialog fills these in. The dialog calls the name "Name in Studio" and fills it in
  from the host as the URL is typed (`github.com` and `git.example.org` become `github` and
  `example`, a folder `local`, with `-2` and so on if taken), until the user types their
  own; it shows the remote URL sandboxes will use. An HTTPS base URL follows the same rules as a provider's: `https://`, a public
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
  - `gitlab` (gitlab.com, self-hosted GitLab): git over HTTPS as `oauth2:<token>`, API
    `<base>/api/v4` with `Authorization: Bearer`. Use a personal or project access token with
    the **api** and **write_repository** scopes. Repositories may be nested (`group/sub/repo`).
    Pasting `https://gitlab.com/…` fills in this kind.
  - `https` (Bitbucket, any smart-HTTP host): git over HTTPS as `<username>:<token>`; the
    username defaults to `git` (Bitbucket wants `x-token-auth`). No API: no pull requests,
    and the default branch isn't needed.
  - `ssh` (gitolite, a VPS, any host serving git over SSH): see [SSH forges](#ssh-forges).
  - `local` (a folder of repositories on this computer, such as `~/git`): see
    [Local forges](#local-forges).
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
  Kinds without an API need the repository, except `local`.
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
  password manager may ask you to confirm each signature; Studio fetches each staging mirror
  at most once every 30 seconds ([Push](#push)). When a signature takes more than two
  seconds, the forge row suggests a dedicated Studio key, which you can dismiss. If the agent
  can't be reached, the forge says "SSH agent not available", and `studio doctor` warns.
- **Studio key:** choose "A Studio key" in the dialog, or switch the forge to it. Studio
  generates an ed25519 key and seals it in the vault as `FORGE_<NAME>_SSH_KEY`; it is never
  written to a file and never reaches a sandbox. The public key (`ssh-ed25519 …
  sandbox-studio/<env>/<forge>`) is shown under **Settings → Git forges** with a copy button: add it to the
  host with write access (a deploy key for one repository is enough). Switching back to the
  agent deletes the key. Studio never reads your `~/.ssh` keys.
- **Host key:** the Add forge dialog scans the host (`POST …/forges/scan`: handshake only,
  no authentication) and shows the `SHA256:` fingerprint; you confirm it is the one the
  host publishes. Every connection pins exactly that key, with the host-key algorithms
  restricted to its type. If the host offers another key, fetches, pushes and Test fail
  with "the host key of <host> changed (pinned …, offered …)", the forge row shows the
  offered fingerprint, and "Re-pin" scans again and pins the key you confirm
  (`PUT …/forges/{id}/host-key`).
- **Published keys:** Studio has the host keys GitHub (`github.com:22`, `ssh.github.com:443`),
  GitLab (`gitlab.com:22`, `altssh.gitlab.com:443`) and Bitbucket (`bitbucket.org:22`,
  `altssh.bitbucket.org:443`) publish built in (`internal/gitreview/knownhosts.go`). A scan of
  one of them that sees a published key returns `publishedBy`, and the dialog shows "The key
  GitHub publishes" with nothing to confirm. A scan that sees any other key fails, and creating
  or re-pinning such a forge with a key it doesn't publish is refused (422), as that means
  someone may be intercepting the connection. When one of them rotates its keys, the list
  needs updating; until then, scanning that host fails.
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

## Local forges

A `local` forge is a folder on the host, so a sandbox can push to a repository you work on
here, reviewed like any other push.

- The base URL is the folder's absolute path; the dialog also takes `~/git` and
  `file:///…`. It must exist, and can't be the file system's root. "Browse…" picks it from
  the host's folders: the browser can't name them, so `GET /api/system/folders?path=` lists
  the subfolders of a folder (only folders, dot folders with `hidden=true`, at most 1000),
  marks the git repositories (a `.git`, or `HEAD` and `objects`), and gives its parent. It
  starts at the home directory, or at the folder the field names, and refuses the root, as
  a forge's folder does. Like every `/api` route it needs the owner's session. Pasting a repository's
  path (`~/git/app`) fills in the form: the folder it is in, named `local`, path `app`.
- `https://git.studio.internal/<forge>/<path>.git` is `<folder>/<path>.git` (bare) or
  `<folder>/<path>` (a working copy, or bare). `splitPath` already refuses `..` and segments
  starting with a dot, so the path stays in the folder. Every repository in the folder can
  be fetched by the environment's sandboxes.
- No credential: `auth` is `none` and there is no secret (migration 038 widens the CHECKs).
- Studio runs the host's git, without git's own environment variables: `git upload-pack
  --stateless-rpc` for the sandboxes' fetches, as `git http-backend` does (protocol v2 when
  the sandbox asks), and go-git's file transport (`git-upload-pack`, `git-receive-pack`) for
  the staging mirror and the approved push. The push is thus what `git push` to the folder
  would do: the repository's config applies and its hooks run. A push to the branch a
  working copy has checked out fails with git's "refusing to update checked out branch"
  unless the repository sets `receive.denyCurrentBranch=updateInstead`.
- The approval says "Approving pushes it to <folder>/<path> on this computer." No pull
  requests. "Test" without a repository checks the folder.

## Cloning into a sandbox

The setup's sandbox step can clone repositories of a forge into the new sandbox, so it
starts with the code.

- `GET …/forges/{id}/repositories` lists what Studio's credential reaches, through the API,
  at most 300 (`gitreview.MaxRepositories`): Forgejo `GET /api/v1/user/repos` (pages of 50),
  GitHub `GET /user/repos?sort=pushed` (pages of 100), GitLab `GET /api/v4/projects` with
  `membership=true&simple=true&order_by=last_activity_at` (pages of 100). A path the virtual
  remote can't serve is left out. A `local` forge lists the repositories in its folder and
  one level down (`owner/repo`), by name, without hidden folders; a bare `app.git` is listed
  as `app`. Kinds `https` and `ssh` have no list (422).
- `POST …/sandboxes/{id}/clone` with `{forgeId, repo}` runs `git clone --
  https://git.studio.internal/<forge>/<path>.git /workspace/<name>` in the running sandbox,
  as the agent user, through the guest's `run` stream (`git` is in
  `agentproto.RunCommands`), with `GIT_TERMINAL_PROMPT=0` and a 10-minute limit. The clone
  is an ordinary one of the virtual remote: its fetches carry no token, and its pushes come
  for review. The path is checked as the remote checks it, so `<name>`, the last segment,
  stays in `/workspace` and can't start with a dash. git refuses a folder that exists and
  isn't empty; the answer is 409 with the end of git's output, as for any failed clone.

## Credential hints

The managed git config also names Studio's credential helper and sends it the path:

```
[credential]
	helper = /opt/studio/bin/studio-agent git-credential
	useHttpPath = true
```

git calls it when an HTTPS host answers 401, which a forge's host never does, since its URLs go
to the virtual remote. `studio-agent git-credential get` sends `{protocol, host, path}` to
Studio (`git_credential`, a call that isn't among the agent's MCP tools) and prints Studio's
answer to stderr, where git shows it to the agent. It never prints a credential, so git fails
as it would have. `store` and `erase` and other protocols are ignored.

Studio (`gitreview/credential.go`) posts a `git.credential` approval for the host, one at a
time per subject (`<teammate or sandbox> needs access to <host>`), with the repository's URL
and the kind `ParseRepoURL` reads from it. **Add forge** allows it and opens Settings → Git
forges with `?add=<url>`, which opens the Add forge dialog as if the URL had been pasted.
**Not now** dismisses it and, until Studio restarts or for 24 hours, answers the host with
"the user chose not to" and no card. Hosts under `studio.internal` and the hosts of existing
forges get no card. Sandboxes pick up the helper when their next session starts, like the URL
rules.

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
   The mirror fetch is rate-limited per staging repo, since a fetch signed by the SSH agent
   may ask you to confirm it: a mirror that fetched within the last 30 seconds
   (`MirrorFetchInterval`) is advertised as it is, and pushes that arrive while a fetch of
   the same mirror runs wait for it and share its result. A failed fetch isn't remembered,
   so the next push tries again. An approved push, whether it went through or the forge
   refused it, makes every sandbox's mirror of that repository fetch again on its next push.
   A branch moved on the forge by something other than Studio within the window shows up
   at approval as "moved on the forge"; the push after that fetches again.
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
   - the new commit descends from the old one (a fast-forward), or the push is *forced*: it
     rewrites the branch, such as `git push --force-with-lease` after a rebase. A forced push
     of the repository's default branch is refused ("non-fast-forward"); while the forge
     doesn't say which that is (no API, or the API failed), `main` and `master` are.
3. An accepted update is kept under `refs/studio/push/<id>`, a `git.push` approval is
   raised (subject `<forge>/<path>:<branch>`, deduplicated; a newer push to the
   same branch supersedes a pending one), and git prints
   `remote: Sandbox Studio: <branch> queued for review (approval <id>)`. The push
   "succeeds" from git's point of view; the upstream branch is unchanged.

The approval shows teammate, sandbox, repository, branch, old and new commit, up to 50
commits, and a unified diff bounded at 512 KiB. Binary files, submodules and files over
1 MiB are summarized instead of diffed.

A forced push's review (`forced: true`) also lists the commits it drops (`dropped`: on the
forge's branch, not in the push; up to 50), each with `replacedBy`, the pushed commit with
the same subject and author where a rebase kept one, and `tipFiles`, the files whose content
differs between the old and the new tip (up to 20): none for a rebase that only moved the
commits, what changed for one that also edited them. Its diff starts where the new history
forks from the forge's, as a new branch's does (`base`, and `baseBranch` when that is a
branch's tip), so it shows the rewritten commits rather than whatever the rebase took in.
The review page shows it under **Rewrites history**, and approving it takes **Force-push**:
`decide` with `allow` needs `"force": true` for a forced push and is refused (400) without
it, so a client that doesn't know about rewrites can't approve one by accident.

## Decisions

- **Approve:** Studio pushes the staged commit upstream with the token or key, provided the
  upstream branch is still at the reviewed old commit (or already at the new one). The push
  names that old commit, so the forge updates the branch only from it: a lease, which keeps
  a forced push from dropping commits that reached the forge after the review. The
  push state becomes `pushed` or `failed` with the forge's message.
- **Reject** (optionally with a note) or **ignore:** the push becomes `rejected`.
- A rejected or failed push is delivered to the sandbox on its next fetch of that
  repository: that fetch fails once with the reason and note, and the next one works.
- The agent hears about it in its chat too. The next prompt of any chat in that sandbox
  starts with a `[Studio] Some of your pushes did not go upstream` note that lists its
  rejected and failed pushes no prompt has carried yet, and each is listed once. The note
  rides on the prompt (the same way as the note after an undo). A chat takes a new prompt
  only when it is idle, and an MCP notification never reaches the model, so a running turn
  hears about it at its next prompt. The chat sends the note only once the prompt went out, so a
  failed prompt leaves it for the next one.
- **Pull request** (Forgejo, GitHub, GitLab): when a push arrives for a branch other than the
  repository's default, Studio asks the forge for an open pull request from that branch
  (`GET …/pulls?state=open` on Forgejo, filtered by head; `?head=<owner>:<branch>` on GitHub;
  `merge_requests?state=opened&source_branch=<branch>` on GitLab), with a 20-second limit.
  The review then carries either `openPullRequest`, the URL of the one the push updates, or
  `pullRequest`, a proposal `<branch> → <default>` with a title and body from the commits.
  The push records the default branch it saw.
  - The decision says what to do with the proposal: `pullRequest: {open, title?}` on an
    `allow`. `open: true` opens it right after the push, under `title` if given, by
    `POST /api/v1/repos/<owner>/<repo>/pulls` (Forgejo, `Authorization: token <token>`),
    `POST /repos/<owner>/<repo>/pulls` (GitHub) or
    `POST /api/v4/projects/<owner>%2F<repo>/merge_requests` (GitLab, with the project path
    URL-encoded), and records the PR URL, or "failed: <reason>". `open: false` only pushes.
    The web and the TUI tick it by default for a new branch.
  - A decision without `pullRequest`, from an older client, raises a `git.pr` approval for
    the proposal as before; approving it opens the pull request, rejecting it records
    "not opened".
  - A push that updates an open pull request records its URL with the result `updated`.
  - `POST /api/environments/{env}/approvals/{id}/pull-request` (`{title?}`) opens the pull
    request of a push approved without one, or whose pull request failed or was not
    approved; 409 if there is none to open, 502 if the forge refuses. If one was opened on
    the forge meanwhile, the push records that it updates it.
  - `https` and `ssh` forges have no API and get no pull request.

Approvals are decided through the usual `POST /api/environments/{env}/approvals/{id}/decide`
endpoint (`note` is optional); the integration owning the approval kind handles it.

## Review feedback

The delivery above waits for the agent: its next fetch or prompt. The review can also tell
an agent at once, in the sandbox's chat or terminal it works in.

- **The review** (`GitReview` in `web/src/components/git-review.tsx`, the same in the sheet,
  the review page and the sandbox's Git tab) shows the branch and its change, then a push in
  tabs: **Changes**, the diff at full length; **Commits**, with the repository and sandbox;
  and while it waits **Comments**, the note and the line comments, each of which jumps to its
  line. A footer that stays in view holds Send to, the pull request choice, Reject and
  Approve, with Send without deciding and Ignore in its More menu. A `git.pr` approval has
  no diff and no tabs.
- **Line comments.** Each line of the review's diff has a comment button (on hover or focus,
  and always on touch screens).
  `web/src/components/review-feedback.tsx` numbers the lines from the hunk headers and folds
  the comments into the note, after the general note: one item per comment with the file, the
  line number and the line's code, then the comment. So they reach the agent by every route,
  fetch and prompt included. The prompt note keeps the note's lines (up to 8000 bytes); the
  fetch message keeps one line of 500.
- **Send to.** The review lists the sandbox's idle and running chats and its agent
  terminals: tmux sessions Studio started for a harness (`@studio_harness`, see
  [harnesses](harnesses.md)). A chat continued in the terminal is reached through that
  terminal. Plain shells are not offered, since text typed into one runs as commands.
- **A new chat** is offered for a sandbox with a teammate, and is the default there: the
  feedback is the first prompt of a chat started in the push's sandbox. The web sends it only
  with a note or a line comment, since the outcome alone reaches the agent anyway. Telling the
  chat that pushed instead would need Studio to know which chat a push came from, and git's
  remote doesn't say.
- `POST /api/environments/{env}/approvals/{id}/feedback` with `{note, chat}`,
  `{note, terminal}` or `{note, newChat: true}` sends `gitreview.Service.Feedback`'s message: "Review of your push of
  <sha> to <branch> in <repo> on <forge>:" with the outcome (approved and pushed, with the
  pull request it opened or updates, the push's failure, rejected, still waiting for review), then "My comments:" and the note. It is the
  user's own message to the agent, not quoted data like the prompt note above. The web sends
  it right after the decision, or alone with **Send without deciding**, which keeps the push
  waiting; a later push to the branch supersedes it.
- **To a chat** it is a prompt (`chat.Service.Queue`): sent now if the chat is idle, or kept
  in memory and sent when the running turn ends. A prompt still waiting when Studio stops is
  lost; the push's prompt note then still delivers a rejection.
- **To a terminal** `sandboxes.Manager.SendToAgent` runs tmux in the guest over the run
  stream (`KindRun`, as the agent user): `load-buffer` from stdin, `paste-buffer -p` (a
  bracketed paste, so the TUI takes the lines as one input) and `send-keys Enter`, one call
  each so the paste is read before the Enter. Claude Code, Codex and OpenCode all accept
  bracketed paste; a live check against the pinned CLIs is pending.
- Once sent, the push is marked delivered and notified, so its next fetch and prompt don't
  tell the agent again.

## Integration interface

`internal/integrations` holds a minimal compiled-in interface:

```go
type Integration interface {
    ID() string
    Routes() []gateway.VirtualHost
    ApprovalKinds() []ApprovalKind // kind + Decide func
    Tools() []Tool                 // MCP tools: definition + Handle on the host
    Jobs() []Job                   // periodic background work: Every + Run
}
```

- **MCP tools.** A tool's definition (name, description, JSON schema) lives in
  `agentcall.Tools`, the list `studio-agent mcp` serves in every sandbox. A call from the
  agent comes back over the sandbox's channel, the same way memory calls do, and the hub
  routes it by method name to the integration whose `Tools` names it. The handler gets the
  calling sandbox, from the channel, and never a sandbox or teammate from the arguments.
  Handlers decode their arguments strictly, so an unknown one is refused. An error the
  handler wraps with `ToolError` is shown to the agent; any other error is logged and the
  agent gets "<tool> failed".
- **Jobs.** `Set.RunJobs` runs each job every `Every` until Studio stops; a failed run is
  logged. No integration has a job yet.

Git review serves two tools, both answered from Studio's records of the calling sandbox's
pushes and never from a forge:

- `git_push_status(limit?)`: the sandbox's recent pushes, newest first, with their state
  (`pending`, `pushed`, `rejected`, `failed`, `superseded`), the forge's result and the
  reviewer's note.
- `git_pull_request_status(branch, forge?, repository?)`: the branch's latest push and its
  pull request: `none`, `awaiting approval`, `opened` (with its URL, also when the push
  updated an open one), `not opened` or
  `failed` (with the reason). `forge` and `repository` pick one when the branch went to
  more than one repository.

## Limits and open points

- Only branches; tags and deletes are refused.
- The approve push runs inside the decide request (up to five minutes).
- Computing a review walks the new commits (`revlist`); very large first pushes are slow.
- No GitHub App sign-in.
