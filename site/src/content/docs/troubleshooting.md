---
title: Troubleshooting
description: Fix common setup, network, provider, Git, and checkpoint issues.
---

## Studio will not start a computer

Run `studio doctor` or open **Settings → Runtime**. Check that hardware virtualization is available and that the host has enough free disk space. On Linux, verify your user can read and write `/dev/kvm`; on Windows, enable Windows Hypervisor Platform and reboot. See [Getting started](../getting-started/).

On Windows, if creating a computer fails with "Access is denied", Studio was started inside a job object that keeps it from starting VMs detached, as CI runners and some service managers do. Start Studio from a normal terminal or the Start menu instead.

On first start, Studio may need to download the runtime and base image. Check the host's network connection and available disk space. The app listens on loopback at `127.0.0.1:7878` by default.

If creating a computer or signing in says that `ghcr.io` refused the login saved on this computer, a `docker login ghcr.io` from earlier has probably expired. microsandbox pulls with a saved login when there is one and doesn't fall back to pulling without it, although Studio's images are public. Run `docker logout ghcr.io` (or `msb registry logout ghcr.io` if you logged in through msb), then try again. `studio doctor` mentions a saved login for the base image's registry while the image isn't downloaded yet.

## Studio stops answering on Windows

Studio's console window must stay open: closing it stops Studio. Studio turns off the console's selection mode (QuickEdit), which used to freeze it after a click in the window. A page that lost Studio connects again by itself once Studio runs. The log is `%LOCALAPPDATA%\Sandbox Studio\logs\studio.log`.

## A sandbox says microsandbox lost its VM

microsandbox keeps its list of sandboxes in its own database, `~/.microsandbox/db/msb.db`. When Studio starts and finds that database damaged, it moves it aside as `msb.db.broken-<time>`, keeping it, and rebuilds it from the rows it can still read, even when the file's first page, which SQLite needs to read it at all, is lost ("file is not a database"). If it could read none, microsandbox starts with an empty list, and the computers it lost can't start, although their disks are kept. Each one's screen says so and offers **Repair**, which gives the VM a new record around the disks it kept, and `studio doctor`'s **Sandbox VMs** check names them. Studio also keeps backups of the database, as `msb.db.backup-<time>`.

Studio doesn't repair the database while another process has it open, such as a computer's VM that kept running after Studio quit, or a second Studio: moving it aside under them damages the new one. Settings and `studio doctor` then name those processes; stop them, for example with `kill <pid>`, and start Studio again. Only one Studio runs on a microsandbox home: a second one, even with its own data directory, stops with an error. Give it a home of its own with `MSB_HOME`. Keep `~/.microsandbox/db` out of sync and backup tools that replace files while they are open.

To get the lost records back instead, quit Studio and run:

```sh
studio restore-msb-db
```

It looks at every copy kept next to the database: the broken ones, including one an earlier Studio couldn't read, the ones it replaced before, and Studio's backups. It counts how many of the computers Studio expects each one knows, and restores the one that knows the most, the newer on a tie. If none knows more than the database in place, it keeps that one and says so. The one in place is moved aside as `msb.db.replaced-<time>`. If it knows sandboxes, the command names them and stops; run it with `-replace` to restore anyway. Afterwards it names the computers still missing: start Studio and choose **Repair** on each. To restore from an older file, name it: `studio restore-msb-db ~/.microsandbox/db/msb.db.broken-20261022T072947Z`.

## An agent cannot start

- Confirm the computer is running and has a teammate assigned.
- Confirm the teammate's harness supports the selected provider.
- Add an API key to the provider and verify it is still valid. For a subscription, choose **Sign in** next to the provider under **Settings → AI providers**; a ChatGPT sign-in can expire.
- Confirm an environment network rule does not deny the provider's API host.

Claude Pro/Max and ChatGPT subscriptions sign in from Settings → AI providers. A Claude subscription works in terminal sessions and in chats.

## A command says it is waiting for network approval

Answer the request: on the computer's screen, **1 request waiting** opens it; elsewhere it shows as a card at the bottom right, and on Home and **Network**. Studio holds the connection for a minute, and an answer in that time lets it through. After that the command sees a failure: choose **Allow next time** and run it again. If a rule denies the host, change it on **Network**.

## A computer cannot reach this machine or a host on the local network

Computers reach public addresses only, so `localhost`, the Studio machine's own addresses, a NAS or a host behind a VPN fail by default, and a rule alone does not open them: **Network** marks such a rule with the reason. Open the port under **This computer** on **Settings → Ports and LAN** and connect to `host.studio.internal`, or add the host under **Local network**. See [Network rules](../network/#this-computer).

## A page says "Is Studio older than this page?"

The page sent something the running Studio doesn't know, which means the page is newer than that Studio. Run `studio restart`, so the version you installed last runs, then reload the page.

## A page says Studio was updated since this window loaded

Studio was updated while the page was open, so a part of the old version is gone. Choose **Reload** to get the new one.

## A Git push is still pending

A push waits on Home and in the computer's **Changes** tab under **Waiting for you**, with a dot on the tab; **Review** opens its diff. Studio does not push it upstream before approval. If you intended to use a forge but see no push to review, check that the forge is configured in this environment and that the computer's remote routes through it.

## A checkpoint cannot be restored

Restore waits on a fix in microsandbox ([#1736](https://github.com/superradcompany/microsandbox/issues/1736)). Checkpoints can be saved, renamed and deleted, but not restored yet. For a portable copy, [export](../sandboxes/#export-a-workspace) the computer: the file holds `/workspace` and the home, not Docker data or the environment's secrets.

## A preview does not open

Confirm the computer is running, the server is listening on the port you selected, and you opened the link on the machine running Studio. Preview links use `.localhost`; they are not intended for another device on your LAN. If the preview shows an old version of the app, or an app that no longer runs on that port, choose **Reload fresh** in the preview's bar: a service worker may be answering from its cache.

## Checkpoints, forks or disk resizes fail after an upgrade

A computer that was running while Studio upgraded microsandbox still runs as the version before, so checkpoints, forks and disk resizes fail until it restarts. Stop it and start it again; `studio doctor`'s **Sandbox storage** check names such computers.

## Studio says a computer's disks must move

microsandbox keeps computers' disks in a newer layout, and moves a computer's disks there as it starts. Where it can't do that by itself, mostly on macOS, `studio doctor`'s **Sandbox storage** check and a notice on Home and on **Computers** say so. **Move disks** in the notice stops those computers, moves their disks, keeping the originals, and starts again what ran. Remove any VMs that `studio doctor` lists under **Leftover VMs** first: microsandbox won't move disks while one seems to run. If it says microsandbox's folders can be written by other accounts, run the `chmod` command it prints, or restart Studio where microsandbox's home is a link.

## A disk is full

When a computer's disk is nearly full, the strip above its screen or chat says so and has **Resize disks…**. The same is **Resize…** under **Disks** on its **Details** tab. See [Computers](../sandboxes/#stop-suspend-and-resume).

## Studio asks you to unlock your keychain

Studio keeps the key to your secrets in the OS keychain. On Linux the keychain usually unlocks when you log in on the desktop, so after a restart of the computer with only SSH logins it stays locked. Studio then waits instead of starting, and its page says **Unlock your keychain**. Log in on the computer's desktop, or unlock the keychain another way, and Studio starts by itself; the page goes on with your session. **Retry** asks the keychain again at most every 15 seconds, which may show its password prompt on the desktop, and five times in all while it stays locked; after that, unlock it on the desktop. A Studio that waits like this right after an update isn't undone for waiting: the 15 minutes an update has to start count from when the keychain unlocks.

## Move the data directory

Set `SANDBOX_STUDIO_HOME` before starting Studio. Moving the data directory also means moving the key material needed to decrypt the vault; keep the directory and its OS keychain entry together. Do not copy only the catalog database.
