---
title: Git review
description: Keep forge credentials on the host and approve pushes before Studio sends them upstream.
---

For a forge added in **Settings → Git forges**, Studio can act as the virtual Git remote. When a sandbox pushes through Studio, the change is staged in a private per-sandbox repository and appears in the approval inbox as a diff. The upstream branch does not change until you approve the push.

Pushes and pull requests waiting for you are on **Home** and in the approval inbox. **Settings → Activity** lists the decided ones; its **Git** filter shows only them, and each push opens its review.

![Settings → Git forges showing configured forges](../../assets/screenshots/git.jpg)

## Add a forge

Studio supports GitHub, GitLab, Forgejo/Gitea/Codeberg, other HTTPS Git hosts, SSH Git hosts, and folders of repositories on this computer. Add each forge to the environment where its sandboxes will use it. Forge hosts must be public; private and LAN hosts are refused. Paste a repository URL, such as `git@github.com:owner/repo.git`, and Studio fills in the rest of the form: an SSH URL keeps its user, here `git`. The SSH address of an **Other SSH** forge takes a clone URL too.

- For GitHub, GitLab or Forgejo/Gitea/Codeberg, add a token with access limited to the repositories the agent needs. A GitLab token needs the **api** and **write_repository** scopes.
- For another HTTPS host, provide its username and token.
- For a folder on this computer, paste a repository's path, such as `~/git/app`: Studio makes a **Folder on this computer** forge for the folder it is in, with no token. Sandboxes can fetch every repository in the folder; an approved push goes in with your git, as `git push` to the folder would, so the repository's hooks run and a push to the branch a working copy has checked out is refused unless the repository sets `receive.denyCurrentBranch` to `updateInstead`.
- For SSH, Studio signs with the keys in your SSH agent, the same ones `ssh` uses. Compare the scanned host-key fingerprint with the fingerprint published by the host, then confirm it. For GitHub, GitLab and Bitbucket, Studio already knows the keys they publish: their scan shows "The key GitHub publishes" (or GitLab, or Bitbucket) with nothing to compare, and any other key is refused. To keep a key for Studio alone, choose a Studio key instead: Studio creates it and shows its public key for you to add to the host.

Forge tokens and Studio's SSH keys stay in Studio's host-side vault. Your SSH agent is only used on the host. Neither reaches a sandbox. If your agent asks you to confirm each signature, as hardware keys and password managers do, Studio's own fetch before a push runs at most once every 30 seconds per sandbox and repository, so a burst of pushes asks once. **Settings → Git forges** can test a forge connection and replace an expiring token.

The forge row shows its Studio remote URL, in the form `https://git.studio.internal/<forge>/<path>.git`. Copy that URL when cloning from a plain terminal. When an agent session starts, Studio also writes managed Git URL rules so common URLs for configured forges can route through Studio.

## Push and review

When a sandbox pushes through a configured forge, Studio stages the push and asks for review. The approval shows the sandbox, branch, commits, and a bounded diff. You can approve, reject, or ignore it. Studio checks that the upstream branch has not changed unexpectedly before it pushes the approved commit.

To tell the agent what to change, comment on lines of the diff with the comment button that appears when you point at a line, and add a note below. A rejection and its note reach the agent the next time it fetches or gets a prompt. To tell it right away, choose one of the sandbox's chats or agent terminals under **Send to** before you decide: Studio sends the outcome, your note and your line comments there. A chat that is working gets them when its current turn ends. **Send without deciding** sends them and keeps the push waiting, so the agent can push a fix that replaces it. Plain shell terminals are not offered, since text typed into them would run as commands.

GitHub, GitLab and Forgejo/Gitea forges can also request approval to open a pull request (a merge request on GitLab) after a reviewed push to a non-default branch. Other HTTPS, SSH and local forges do not provide pull-request proposals.

If a host is not configured as a forge, access follows normal network rules and is not staged for Git review. Do not assume every arbitrary Git remote receives review protection.

See the [Git review guide](https://github.com/lukaskoebe/sandbox-studio/blob/main/docs/git-review.md) for supported forge details and the SSH host-key flow.
