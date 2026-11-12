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
host key. An SSH forge can sign with your SSH agent instead of a credential, and can have an
API with a token of its own. The kind chooses the transport (HTTPS or SSH) and whether there is
an API for pull requests.

- Per environment: name (`^[a-z0-9][a-z0-9-]{0,31}$`, used in the remote URL), kind and
  base URL. An HTTPS base URL follows the same rules as a provider's: `https://`, a public
  host, no credentials, query or fragment.
- The **Add forge** dialog has two steps. The first takes a repository: its clone URL, HTTPS
  or SSH, its page in the browser, a forge's own address such as `https://codeberg.org`, or a
  folder here, typed or picked with **Pick a folder on this computer…**. **Detect**
  (`POST …/forges/parse-url` with `detect`) reads it and asks a host Studio doesn't know
  which forge it runs. The second step says what Studio found (`git.example.org runs
  Forgejo`, the repository, whether pull requests work) and asks only for what is left: the
  token, or for SSH the host key, the authentication and an optional API token. A host that
  answers as no known forge, **Not right? Pick the kind**, and **Set it up by hand instead**
  show the kinds to pick from; an HTTPS address stays when another HTTPS kind is picked.
  **More** holds the name ("Name in Studio") and the addresses. The name comes from the host
  in kebab case, without its domain ending or a leading `www`, `git`, `ssh` or `altssh`, or a
  `git-` prefix: `forgejo.mycompany.com`, `git.example.org` and `ssh.github.com` become
  `forgejo-mycompany`, `example` and `github`; a folder is `local`; `-2` and so on if taken.
  It stays the host's until the user types their own, and the dialog shows the remote URL
  sandboxes will use.
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
  as `FORGE_<NAME>_SSH_KEY`, and its API's token, if any, as `FORGE_<NAME>_TOKEN`; an SSH
  forge that signs with the agent and has no API has no secret) marked
  studio-only: it has no host binding, so the gateway never
  substitutes it into sandbox traffic, network rules can't name it, and the secrets API
  refuses to edit or delete it (409; the secrets page shows it as "Forge token" or "Forge
  SSH key"). It is created and deleted with its forge.
- "Replace token" (`PUT …/forges/{id}/token`) swaps the token in place and keeps the staged
  pushes, since tokens expire.
- "Test" asks the API whose token it is (`GET /api/v1/user`, GitHub `GET /user` with the
  token's expiry, amber under 14 days) and, given a repository, lists its branches over git.
  Kinds without an API need the repository, except `local`. With a repository, a token forge also asks
  for the refs as a push would (the `git-receive-pack` advertisement), without pushing: a
  forge refuses that to a token that can only read, and the test then says so in amber
  (`pushRefused`).
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
  rest of the form; the second step scans the host key straight away.
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
  SHA256:…"). Pull requests only with an API.
- **API:** an SSH forge on a Forgejo, Gitea, GitHub or GitLab host can have that host's API
  too, with an access token: "Pull requests" in the Add forge dialog, or "Add an API token"
  in its row (`PUT …/forges/{id}/api` with `{kind, baseUrl, token}`; `DELETE …/forges/{id}/api`
  takes it away and deletes the token). git keeps going over SSH; the API, at `baseUrl` (the
  forge's web address, such as `https://codeberg.org`), serves what it does for a forge of
  its kind: the repository list, the default branch and pull request of a push, and the pull
  request tools. A pasted SSH URL of github.com, gitlab.com or codeberg.org suggests their
  API. The token is replaced in place like any forge's (`PUT …/forges/{id}/token`). One forge
  for both keeps a single name for the agent, where an SSH forge beside an API forge for the
  same host would be two.
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
  a forge's folder does. Like every `/api` route it needs the owner's session. The dialog's
  first step offers the same picker as **Pick a folder on this computer…**. A repository's
  path, picked or pasted (`~/git/app`), makes the folder it is in the forge, named `local`,
  path `app`; a folder with repositories in it (`~/git`) is the forge itself.
- `https://git.studio.internal/<forge>/<path>.git` is `<folder>/<path>.git` (bare) or
  `<folder>/<path>` (a working copy, or bare). `splitPath` already refuses `..` and segments
  starting with a dot, so the path stays in the folder. Every repository in the folder can
  be fetched by the environment's sandboxes.
- No credential: `auth` is `none` and there is no secret (migration 038 widens the CHECKs).
- Studio runs the host's git, without git's own environment variables: `git upload-pack
  --stateless-rpc` for the sandboxes' fetches, as `git http-backend` does (protocol v2 when
  the sandbox asks), and go-git's file transport (`git-upload-pack`, `git-receive-pack`) for
  the staging mirror and the approved push. The push is thus what `git push` to the folder
  would do: the repository's config applies and its hooks run.
- A push to the branch a working copy has checked out: staging marks the review `checkedOut`
  (`checkedOut` in `local.go`: the repository has a `.git` and `git symbolic-ref HEAD` names
  the branch), and the card offers **Update the checked-out files too**, ticked. Approved so
  (`updateCheckedOut`, omitted means yes), the host's git pushes from the staging repository
  with `--receive-pack="git -c receive.denyCurrentBranch=updateInstead receive-pack"` and
  `--force-with-lease` on the old tip (go-git's file transport takes no config), so the
  working tree follows the branch, and git refuses it when the tree has changes of its own
  or the branch moved since the sandbox pushed. Unticked, the push is go-git's, which git
  refuses with "refusing to update checked out branch" unless the repository sets
  `receive.denyCurrentBranch` itself. **Push again** updates the files.
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
  push state becomes `pushed` or `failed` with the forge's message. The push runs inside the
  decision, so the web tells how it went as the approval closes: "Pushed main to owner/repo",
  or an error that stays until it's closed, with the reason and a link to the review. A 403
  from the forge, which go-git words only as "authorization failed", is explained as a
  credential that may only read; a 401 as one that expired or was revoked.
- **Push again:** `POST /api/environments/{env}/approvals/{id}/push-again` retries a `failed`
  push once its cause is fixed (a token given write access, a forge that was down); the
  commit stays staged. Its state and result say how it went, and a new failure replaces the
  reason. Pushed, its pull request waits for a `git.pr` approval. 409 for a push that didn't
  fail. The review of a failed push shows the reason and a "Push again" button.
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
  - `https` forges, and `ssh` forges without an API, get no pull request.

Approvals are decided through the usual `POST /api/environments/{env}/approvals/{id}/decide`
endpoint (`note` is optional); the integration owning the approval kind handles it.

## Review feedback

The delivery above waits for the agent: its next fetch or prompt. The review can also tell
an agent at once, in the sandbox's chat or terminal it works in.

- **The review** (`GitReview` in `web/src/components/git-review.tsx`, the same in the sheet,
  the review page and the sandbox's Git tab) shows the branch and its change, then a push in
  tabs: **Changes**, the diff at full length, with its files listed beside it (above it when
  narrow) to jump to; **Commits**, with the repository and sandbox, each commit opening to its own
  diff from its first parent (`GET …/approvals/{id}/commits/{sha}`, read from the staging
  repository, for the commits the review lists; 404 once Studio no longer keeps it);
  and while it waits **Comments**, the note and the line comments, each of which jumps to its
  line. A footer that stays in view holds Send to, the pull request choice, Reject and
  Approve, with Send without deciding and Ignore in its More menu. A `git.pr` approval has
  no diff and no tabs.
- **Stacked or split.** A full-length diff (the review's Changes tab, a commit's diff, the
  Changes tab of the screen and the thread's changes) shows its lines stacked, old above new,
  or split, old beside new. The switch above the diff is remembered per browser
  (`studio.diff.style` in `localStorage`, `web/src/lib/diff-style.ts`); short diffs, like a
  chat's tool calls, stay stacked.
- **Line comments.** A line of the review's diff takes a comment from the button on its number
  while the pointer is over it, or from a click or tap on the number. A comment covers several
  lines when you drag over their numbers (or press the button and drag), or click one and
  shift-click another; it shows under the last. Neither has a keyboard route yet (`PLAN.md`).
  A pull request review's draft keeps comments on one line, as its fields have one.
  `web/src/components/review-feedback.tsx` numbers the lines from the hunk headers and folds
  the comments into the note, after the general note: one item per comment with the file, the
  line number and the line's code, then the comment. A range names its first and last line and
  quotes up to 20 of its lines. So they reach the agent by every route,
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

## Pull request reviews

An agent can review any pull request on a forge with an API (Forgejo, Gitea, Codeberg, GitHub
or GitLab, whose merge requests the tools take as pull requests numbered by their `!iid`; also
an SSH forge with that API), whoever opened it, in any repository the forge's token reaches. It finds them with `git_repositories` and `git_pull_request_list`, reads with
`git_pull_request_read` and git, and drafts the review with `git_pull_request_review`.
Nothing reaches the forge until the user posts the draft, as the forge's token's account.
A comment on someone else's pull request is a review with the verdict `comment`.

- **Finding repositories.** `git_repositories(forge?, query?)` answers each forge of the
  environment (or the one named) with its kind, its virtual remote's URL prefix
  (`https://git.studio.internal/<forge>/`, to clone `<prefix><path>.git`), whether the pull
  request tools work on it, and the repositories Studio's token reaches, as Settings lists
  them (`Repositories`, at most `MaxRepositories`). `query` keeps those whose path or
  description contains it. An SSH forge with an API says which (`api`). A forge without an
  API (kind https, or ssh without one) has no list and says why; a forge whose API fails says
  why, and the others are still listed. When no forge has an API, the pull request tools
  answer with each forge and why it has none, such as "work is an SSH forge without an API
  token; the user adds one to it in Settings → Git forges".
- **Listing pull requests.** `git_pull_request_list(repository, forge?, state?, limit?)`
  asks the forge for the repository's pull requests (`GET /repos/{o}/{r}/pulls`, sorted by
  the latest update; GitLab's `GET /projects/:id/merge_requests?order_by=updated_at`, where
  `closed` takes in the merged ones as on the other forges), `open` by default or `closed` or `all`, 30 by default and at most
  100: number, title, author, state, draft, merged, base and head branches, requested
  reviewers, labels, last update and URL.

- **Reading.** `git_pull_request_read(repository, number, forge?)` answers the pull
  request's title, description, author, branches, commits (up to 50), changed files with line
  counts (up to 50), the reviews so far, their comments on lines (up to 50, `{author, path,
  line, side}`: GitHub's `…/pulls/{n}/comments`, Forgejo's `…/reviews/{id}/comments` of each
  review that has some) and the conversation's comments, from the forge with the forge's
  token. On GitLab the commits are the newest 50 (`…/merge_requests/:iid/commits`, listed
  oldest first), the files come from `…/diffs` (`…/changes` before GitLab 15.7), the reviews
  are the approvals and requested changes its system notes record plus `…/approvals`, and
  the line comments and conversation are the diff notes and the other notes of `…/notes`.
  The diff is left out: `git fetch origin pull/<n>/head` (on GitLab
  `merge-requests/<n>/head`) works through the virtual remote, since fetches go to the forge
  as they are. `forge` is needed only when the
  environment has more than one forge with an API.
- **As files.** `~/forge` in the sandbox holds the listing and, for the sandbox's own branches
  and what it read, these same answers as Markdown, kept fresh while a session runs
  ([agent-cli.md](agent-cli.md#forge)).
- **Drafting.** `git_pull_request_review(repository, number, verdict, summary, comments,
  forge?)` takes a verdict (`comment`, `approve`, `request_changes`), a summary (needed
  unless approving) and up to 50 line comments `{path, line, side, body}`, where side `new`
  (the default) is an added or kept line numbered in the new file and `old` a removed line.
  Studio reads the pull request and its diff (up to `MaxDiff`), refuses a comment on a line
  the diff doesn't show, asks the forge for the token's account, and creates a `git.review`
  approval with the subject `<forge>/<owner>/<repo>#<n>` and a `git_reviews` row
  (migration 051) holding the head commit the draft was written against. The agent gets
  "awaiting approval" at once, with the approval's ID for `approval_status` and `studio wait`
  (docs/agent-cli.md#waiting-for-the-user). A newer draft for the same pull request from the
  same sandbox supersedes one still waiting, except a call with an `idempotency_key` the
  sandbox already drafted with, which answers with that draft.
- **The review** (`ForgeReview` in `web/src/components/forge-review.tsx`, in the same sheet,
  review page and Git tab as a push) shows the pull request, a link to it on the forge, the
  verdict as a toggle, the summary, the diff with the draft's comments on their lines, and a
  footer: "Posts as <account> on <host> · N comments", **Discard…** and **Post review**.
  Everything is editable while it waits; comments are edited or dropped in place and new ones
  added with a line's comment button. `GET …/approvals/{id}/review-head` asks the forge
  whether the pull request moved since the draft: "2 new commits since Ada read it", "Its
  branch was rewritten", merged or closed.
- **Posting.** Allow with `review: {verdict, summary, comments, attribution}` in the decide
  body posts the edited review (without it, the draft as it is): Forgejo's
  `POST /repos/{o}/{r}/pulls/{n}/reviews` with `APPROVED`, `REQUEST_CHANGES` or `COMMENT`
  and `new_position`/`old_position`, GitHub's with `APPROVE`, `REQUEST_CHANGES` or `COMMENT`
  and `line` with side `RIGHT`/`LEFT`, both pinned to the reviewed head with `commit_id`.
  GitLab has no review object, so Studio approves first (`POST …/approve` with the reviewed
  head as `sha`; GitLab refuses it once newer commits arrived, and then nothing is posted; one the
  token's user already gave stands),
  then opens a discussion per line comment (`POST …/discussions` with a text position on the
  diff version of the reviewed head: `new_line` for an added line, `old_line` for a removed
  one, both for a kept one), then posts the summary as a note, whose link the review keeps.
  A request for changes starts that note with **Changes requested.** and first sets the
  reviewer state (`POST …/draft_notes/bulk_publish` with `reviewer_state: requested_changes`,
  the only REST call that sets it). GitLab publishes the user's pending draft comments with
  it, so Studio refuses while there are any. A GitLab that answers 404 has the note only, and one
  that ignores the field (older than the state) too. GitLab blocks the merge on the state only on
  Premium and Ultimate. This was tested against a fake of the API, not a real GitLab.
  The approval's payload is updated to what was posted. A forge refuses a verdict from the pull
  request's own author, so when the token's account opened it the review posts as a comment
  that starts with **Approved.** or **Changes requested.**, and the review says so.
- **Attribution.** "End with “Review by Ada (agent)”" appends that line (or "Review by an
  agent in <sandbox>" for a sandbox without a teammate). It is off by default and the web
  remembers the choice per teammate in the browser.
- **Discarding.** Deny with a note discards the draft and keeps the note; dismiss closes it
  without an answer.
- **The agent learns** the outcome from `git_pull_request_review_status(repository, number,
  forge?)` (awaiting approval, posted with its URL, discarded with the note, superseded, or
  failed with the forge's answer), from the next prompt's note ("What became of your pull
  request reviews"), or right away when the user picks a chat or terminal under **Send to**
  in the Discard popover: `ReviewFeedback` writes "I posted it; it is at <url>." or "I
  discarded it; nothing was posted." and the note.
- **Not yet:** accounts of their own for teammates (so a review posts as the teammate, and
  a verdict on the user's own pull request isn't turned into a comment), Studio noticing
  review requests for a teammate, and GitLab's batched draft notes (one notification for a
  whole review) ([forge-reviews.md](design/forge-reviews.md)).

## CI checks

`git_pull_request_checks(repository, number?, branch?, forge?)` (`studio pr checks`) reads
the CI checks of a pull request's head commit (`internal/gitreview/checks.go`). Without a
number it takes the open pull request whose head is `branch`. In a checkout, the command fills in
the branch from `HEAD`.

- **GitHub:** the check runs (`GET …/commits/{sha}/check-runs?filter=latest`) and the combined
  commit status (`GET …/commits/{sha}/status`).
- **GitLab:** the jobs of the merge request's newest pipeline on its head
  (`…/merge_requests/:iid/pipelines`, then `…/pipelines/:id/jobs`). A job allowed to fail, or
  one that waits to be started by hand, is `neutral`.
- **Forgejo and Gitea:** the combined commit status, which their Actions report as well.

**States.** Each check is `pending`, `success`, `failure`, `cancelled`, `skipped` or
`neutral`. The answer's `state` sums them up:

- `pending` while one runs;
- then `failure` if one failed or was cancelled;
- otherwise `success`;
- `none` when there are no checks.

**Logs.** The first three failed checks with a log carry its end: the last 40 lines and at
most 6000 bytes, without terminal colours, GitLab's section markers, GitHub's timestamps or
the token.

- GitHub serves an Actions job's log through a redirect to its storage, which Studio follows
  once, without the token.
- GitLab serves a job's trace.
- A commit status only links to its run.

`~/forge` gives each open pull request in full a `checks.md`
([agent-cli.md](agent-cli.md#forge)).

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

Git review serves eight tools: the three below, which work from Studio's records of the calling
sandbox's pushes, and the five of [pull request reviews](#pull-request-reviews).


- `git_push_status(limit?)`: the sandbox's recent pushes, newest first, with their state
  (`pending`, `pushed`, `rejected`, `failed`, `superseded`), the forge's result and the
  reviewer's note.
- `git_pull_request_status(branch, forge?, repository?)`: the branch's latest push and its
  pull request: `none`, `awaiting approval`, `opened` (with its URL, also when the push
  updated an open one), `not opened` or
  `failed` (with the reason). `forge` and `repository` pick one when the branch went to
  more than one repository.
- `git_pull_request_create(branch, title, body?, base?, forge?, repository?)`: proposes the
  pull request of the branch's newest push, once Studio pushed it to the forge, on the same
  `git.pr` card a push's own proposal raises, and answers `awaiting approval`. `base` defaults
  to the repository's default branch, and the body ends with who pushed it, as a proposal's
  does. One open from the branch, which it asks the forge for, or one awaiting approval is
  answered instead; a push still waiting for review is refused, since its reviewer can open
  the pull request while approving it.

## Limits and open points

- Only branches; tags and deletes are refused.
- The approve push runs inside the decide request (up to five minutes).
- Computing a review walks the new commits (`revlist`); very large first pushes are slow.
- No GitHub App sign-in.
