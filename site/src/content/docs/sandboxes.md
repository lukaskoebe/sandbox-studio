---
title: Sandboxes, terminals, and previews
description: Create a Linux sandbox, keep work across sessions, and view a service running inside it.
---

A sandbox is a Linux microVM with Docker, a persistent `/workspace`, and its own terminal. Creating a sandbox boots it. Its name is also its hostname in Studio.

## Create one

1. Choose **New sandbox** from the overview.
2. Enter a name using lowercase letters, digits, and dashes.
3. Optionally choose a teammate. This choice cannot be changed later.
4. Select **Create**. Studio opens the sandbox while it boots. The page lists the creation steps and shows the progress of the image download. If a step fails, the page shows the error and a **Delete** button.

![The New sandbox dialog, with a name and optional teammate](../../assets/screenshots/new-sandbox.jpg)

You can create a blank sandbox or choose a ready template from the Builds page. A template sets up the root filesystem; a newly created sandbox still gets its own empty workspace and Docker disk. Its home directory (`/home/agent`, 4 GiB) starts from the template's home, so your agent's sessions and logins are kept on that disk.

## Work in a terminal

Open a sandbox from the sidebar to see its terminal. Terminals run inside the sandbox at `/workspace`. You can open additional terminal tabs, run a shell yourself, or start the selected teammate's agent session. Docker runs inside the sandbox and uses its persistent Docker disk.

![A terminal open at /workspace in a running sandbox](../../assets/screenshots/sandbox-terminal.jpg)

## Stop, suspend, and resume

| Action | What it does |
| --- | --- |
| **Stop** | Powers off the VM. Files on its disks stay, but running processes and terminal sessions end. Use **Start** to boot it again. |
| **Suspend** | Pauses a running VM with its memory and processes. **Resume** continues it, including terminal sessions. Suspended memory does not survive a host reboot. |
| **Delete** | Permanently removes the sandbox and its disks. Export the sandbox first if you need a portable backup. |

Rebasing changes the template and carries over `/workspace` and the home directory; Docker data and terminal sessions are not carried over. See [Templates](../templates/) and the [rebase guide](https://github.com/lukaskoebe/sandbox-studio/blob/main/docs/rebase.md).

## See a sandbox's history

The sandbox page has a **History** section. It draws the sandbox's history as a line from left to right, in the order things happened.

- Each generation of the sandbox is labelled with its template. A rebase shows as a change of template, and a restore shows as a jump back to a checkpoint.
- Checkpoints are the points on the line. Select one for its menu: **Restore** when the runtime allows it, **Fork from here**, **Rename** and **Delete**.
- Forks branch off to their child sandboxes, and a forked sandbox shows the one it came from. A deleted sandbox stays in the history, marked as deleted.
- The end of the line is the sandbox as it is now. **Save checkpoint** there saves its current state.

The Home page has a **Family** card. It lists each template with its sandboxes, and shows each fork under the sandbox it was made from. Green marks a running sandbox, amber one that is still starting or being created, and red one that failed.

## Open a preview

If a web server is listening on a sandbox port, open **Previews** in the sandbox toolbar and choose the port. Studio opens a host-scoped `.localhost` link through the agent channel. The link is meant for the browser on the machine running Studio.

Studio removes its own authentication cookies before forwarding preview requests and filters Studio cookies from preview responses. The preview app's own cookies are retained. Preview links do not expose the sandbox to your LAN.

## Export a workspace

Use **Export** to save a `.studio-sandbox` file that can be imported in another environment or Studio install. It contains the template specification, resource settings, `/workspace`, and the home directory (`/home/agent`), which holds your agent's sessions and anything you saved there. Read the home directory before you share the file. It does not include secrets, approvals, Git credentials, checkpoints, or Docker data. A live export is not frozen; stop the sandbox first for a consistent copy. See [Export and import](https://github.com/lukaskoebe/sandbox-studio/blob/main/docs/export-import.md).
