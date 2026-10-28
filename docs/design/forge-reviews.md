# Pull request reviews by a teammate

A teammate reads a pull request on a forge and drafts a review: a verdict, a summary and
comments on lines. Studio shows the draft to the user, who can edit it, and posts it only once
they approve. The forge's token stays on the host, as it does for pushes and pull requests
([git remotes](git-remotes.md)).

Forgejo (and Gitea, which shares its API) comes first. GitHub follows with the same tools.
GitLab's merge requests work differently and come last.
The card and the review page are mockup E in the "Studio feedback mockups" artifact.

## Decisions

- **Every review asks, comment-only ones too.** A review posts under the user's name or a
  teammate's account, and other people read it. There is no per-repository switch to skip the
  approval: nobody has asked for one yet, and it would be one more setting.
- **The attribution line is optional and off by default.** A teammate's account already says
  who wrote the review. With the user's own token, the user approved the draft, so the review
  is theirs. The teammate's settings can turn on a closing line, "Review by Ada (agent)", for
  forges whose people want to know.
- **A teammate can have its own forge account.** It is optional and falls back to the user's
  token. With one, Ada's reviews come from Ada's account, and a formal verdict on a pull
  request the user opened is possible (see [Own pull requests](#own-pull-requests)).

## Flow

1. The user asks in a chat ("review acme/web#42"), or a task names the pull request.
2. The teammate calls `git_pull_request_read` for the title, description, files, existing
   reviews and comments. It reads the code through the git remote: Studio's mirror fetches
   `refs/pull/<n>/head` on request, so `git fetch origin pull/42/head` works in the sandbox.
3. The teammate calls `git_pull_request_review` with its draft. Nothing is posted yet. The call
   answers at once with "waiting for approval", like a push.
4. A `git.review` approval card shows in the conversation and the stack: "Ada wants to review
   acme/web#42: request changes, 3 comments". **Review** opens the pull request's diff in the
   git review viewer, with the draft's comments on their lines and the summary on top.
5. The user edits the summary, a comment or the verdict, drops comments, and then clicks
   **Post review** or **Discard**. A note on Discard goes back to the teammate, as a rejected
   push's note does.
6. Studio posts the review with the teammate's account if it has one, else the user's token.
   `git_pull_request_review_status` (or the existing status tool) reports posted, discarded or
   failed, with the review's URL.

## Tools

`git_pull_request_read(repository, number)`, with no approval. It answers with the title,
description, author, base and head branches with their commits, the changed files with their
status and line counts, and the reviews and comments so far. Diffs are left out, because the
code is in git. A pull request on a repository the sandbox has no remote for is refused, the
same rule as for pushes.

`git_pull_request_review(repository, number, verdict, summary, comments[])`:

- `verdict` is `comment`, `approve` or `request_changes`.
- `comments` are `{path, line, side: "new" | "old", body}`. A line outside the diff is refused
  before the card exists, so the user never sees a draft the forge would refuse.
- A second draft for the same pull request replaces one still waiting, as a new push of a
  pending branch does.

## Records

The approval kind is `git.review`, beside `git.push` and `git.pr`. The draft is stored as the
approval's payload: repository, number, the head commit it was written against, verdict,
summary and comments. The user's edits go into the same record before posting. Activity lists
reviews with the git events. If the head commit moved since the draft, the card says so
("2 new commits since Ada read it"), and posting still works because the forge anchors
comments to the commit sent.

## Forge APIs

- **Forgejo and Gitea:**
  - `GET /repos/{owner}/{repo}/pulls/{index}`, `…/pulls/{index}/files`,
    `…/pulls/{index}/reviews` and `/issues/{index}/comments` for the read;
  - `POST /repos/{owner}/{repo}/pulls/{index}/reviews` to post, with `event` `COMMENT`,
    `APPROVED` or `REQUEST_CHANGES`, `body`, `commit_id` and `comments` of
    `{path, body, new_position | old_position}`. The positions are line numbers in the new
    or the old file.
- **GitHub:** the same shape, `POST /repos/{owner}/{repo}/pulls/{number}/reviews` with `event`
  `COMMENT`, `APPROVE` or `REQUEST_CHANGES` and comments of `{path, line, side, body}`.
- **GitLab:** no review object. Comments are discussions with a position, and approval is
  `POST …/merge_requests/:iid/approve`. GitLab gets its own adapter method and is left out of
  the first phases.

The adapter gains `ReadPullRequest` and `PostReview`. Kinds without an API (`https`, `ssh`,
`local`) offer neither tool.

## Own pull requests

Forgejo, Gitea and GitHub refuse an approve or a request-changes verdict from the author of
the pull request. Studio opens a teammate's pull requests with the user's token, so the user's
account is their author. A verdict on those needs the teammate's own account. Without one:

- the tool still accepts the draft;
- the card says "posted as a comment: you opened this pull request";
- Studio posts it with `event: COMMENT`, and the summary starts with the verdict in words.

This is to verify on each forge before phase 1 ships: the exact error, and whether an
organization's settings change it.

## Teammate accounts

Settings → Git forges → a forge → **Accounts** lists the user's token and the teammates' ones.
A teammate's token is checked like the user's (`Check` returns the account) and is used only
for that teammate's reviews. Pushes and pull requests keep using the user's token: those
already go through the user's approval, and the pull request's author should stay the user,
who will merge it. Changing that is a separate decision.

## Phases

1. Forgejo: the read tool, the mirror fetching `refs/pull/<n>/head`, the review draft, the card
   and the review page in the git review viewer, and posting with the user's token. M.
2. Teammate accounts and the optional attribution line. S.
3. GitHub, on the same tools. S.
4. Review requests as tasks: every 10 minutes Studio polls the forges for pull requests that
   request a review from a teammate's account and adds a task. There are no webhooks, because
   a local Studio can't be reached from the forge. M.
5. GitLab. M.

**Built** on `feedback-v0.8` ([pull request reviews](../git-review.md#pull-request-reviews)):
phases 1 and 3, and the attribution line of phase 2, off by default and remembered per
teammate in the browser. The mirror needs nothing: `git fetch origin pull/<n>/head` already
goes to the forge. The review also asks the forge whether the pull request moved since the
draft ("1 new commit since Ada read it"). Later (2026-10-28): `git_repositories` and
`git_pull_request_list`, so a teammate finds the pull requests to review itself instead of
being handed one, and the read shows the reviews' line comments. Not yet: teammate accounts
(so until then a verdict on the user's own pull request posts as a comment that starts with
it), review requests as tasks, and GitLab.

## Open questions

- Whether a review should also run the tests: the teammate can, in its sandbox, and say so in
  the summary. Studio doesn't need to know.
- Comments on a range of lines (GitHub's `start_line`, which Forgejo lacks). Phase 1 takes one
  line per comment.
