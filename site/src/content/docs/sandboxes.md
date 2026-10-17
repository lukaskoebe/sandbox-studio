---
title: Sandboxes, terminals, and previews
description: Create a Linux sandbox, keep work across sessions, and view a service running inside it.
---

A sandbox is a Linux microVM with Docker, a persistent `/workspace`, and its own terminal. Creating a sandbox boots it. Its name is also its hostname in Studio.

## Create one

1. Choose **New sandbox** on Home, or **+** next to **Sandboxes** in the sidebar.
2. Enter a name using lowercase letters, digits, and dashes.
3. Optionally choose a teammate. This choice cannot be changed later.
4. Select **Create**. Studio opens the sandbox while it boots. The page lists the creation steps and shows the progress of the image download. If a step fails, the page shows the error and a **Delete** button.

![The New sandbox dialog, with a name and optional teammate](../../assets/screenshots/new-sandbox.jpg)

You can create a blank sandbox or choose a ready template from **Settings → Templates**. A template sets up the root filesystem; a newly created sandbox still gets its own empty workspace and Docker disk. Its home directory (`/home/agent`, 4 GiB) starts from the template's home, so your agent's sessions and logins are kept on that disk.

## Work in a terminal

Open a sandbox from the sidebar to see its dashboard: a band with its status, owner, template, resources and age, then its chats, terminals, history and checkpoints, with ports and recent connections at the side. Choose **Workspace** in its header to work in it. Terminals run inside the sandbox at `/workspace`. Use **+** in the tab strip to open an additional terminal, run a shell yourself, or start the selected teammate's agent session with **Start session**. The sandbox's chats open in the same strip. The sidebar lists a running sandbox's terminals under its name, agent sessions included; choose one to open it in the workspace from any page. Docker runs inside the sandbox and uses its persistent Docker disk. The status line along the bottom of the workspace shows the sandbox's state, ID, template, ports and waiting approvals, and while it runs its CPU and memory use, such as `cpu 12% · mem 1.2/4 GiB` (CPU as a share of all its CPUs).

![A terminal open at /workspace in a running sandbox](../../assets/screenshots/sandbox-terminal.jpg)

To copy from a terminal, drag across the text: it is copied when you let go. Right-click opens **Copy** and **Paste**. With the keyboard, Ctrl+Shift+C copies a selection made with Shift-drag (⌘C on a Mac, with Option-drag), and Ctrl+V or ⌘V pastes. Programs in the sandbox can put text on your clipboard but can't read it.

## Stop, suspend, and resume

Start and stop are on the dashboard. The options menu (**⋯**, on both pages) has these actions:

| Action | What it does |
| --- | --- |
| **Stop** | Powers off the VM. Files on its disks stay, but running processes and terminal sessions end. Use **Start** to boot it again. |
| **Suspend** | Pauses a running VM with its memory and processes. **Resume** continues it, including terminal sessions. Suspended memory does not survive a host reboot. |
| **Fork** | Copies the sandbox into a new one with the same template and resources, then starts it. `/workspace` and the home directory are copied; Docker images, containers and volumes are not. The fork gets its own network approvals. |
| **Rebase** | Moves the sandbox onto another template (see below). |
| **Delete** | Permanently removes the sandbox and its disks. Export the sandbox first if you need a portable backup. |

To change a sandbox's CPUs or memory, choose **Resources** on its dashboard. The change applies the next time the sandbox starts: the microsandbox version Studio uses can't resize a running VM, so stop and start a running sandbox to get it.

## Side panels

In the workspace, **Browser**, **Connections**, and **Files** open a panel beside the terminal or chat. **Browser** shows only when the sandbox has a teammate. **Connections** lists where the sandbox connects. **Files** shows the sandbox's files, the changes of its chats, and its running apps. To copy files in, drop them onto the Files panel or choose **Upload…** from its folder menu. They go into the folder shown, up to 100 MiB each. If a name is taken, you choose whether to replace the file or keep both. Drag the panel's left edge to resize it on a desktop; on a phone it fills the screen. Close it with its close button or Escape.

Rebasing changes the template and carries over `/workspace` and the home directory; Docker data and terminal sessions are not carried over. The rebase runs in the background: the sandbox page shows its steps as for a new sandbox. If it fails, the sandbox stays on its old template and the page names the step that failed until you dismiss it. See [Templates](../templates/) and the [rebase guide](https://github.com/lukaskoebe/sandbox-studio/blob/main/docs/rebase.md).

## See a sandbox's history

The sandbox page has a **History** section. It draws the sandbox's history as a line from left to right, in the order things happened.

- Each generation of the sandbox is labelled with its template. A rebase shows as a change of template, and a restore shows as a jump back to a checkpoint.
- Checkpoints are the points on the line. Select one for its menu: **Restore** when the runtime allows it, **Fork from here**, **Rename** and **Delete**.
- Forks branch off to their child sandboxes, and a forked sandbox shows the one it came from. A deleted sandbox stays in the history, marked as deleted.
- The end of the line is the sandbox as it is now. **Save checkpoint** in the **Checkpoints** section below saves its current state.

Once a sandbox was forked, the Home page has a **Family** card. It lists each template with its sandboxes, and shows each fork under the sandbox it was made from. Green marks a running sandbox, amber one that is still starting or being created, and red one that failed.

## Open a preview

If a web server is listening on a sandbox port, open **Previews** in the workspace toolbar and choose the port. The dashboard lists the ports too, each with **Open**. Studio opens a host-scoped `.localhost` link through the agent channel. The link is meant for the browser on the machine running Studio.

Studio removes its own authentication cookies before forwarding preview requests and filters Studio cookies from preview responses. The preview app's own cookies are retained. Preview links do not expose the sandbox to your LAN.

Each port keeps its own browser storage, so a service worker an app registered stays after the app stops, and may keep serving it to whatever runs on the port next. In **Previews**, choose **Open fresh** and then the port (or the broom next to **Open** on the dashboard, or **Reload fresh** in a chat's preview) to clear the preview's caches, storage and service workers before it opens. Its cookies stay.

## Export a workspace

Use **Export** in the options menu to save a `.studio-sandbox` file that can be imported in another environment or Studio install, with **Import sandbox** on Home. It contains the template specification, resource settings, `/workspace`, and the home directory (`/home/agent`), which holds your agent's sessions and anything you saved there. Read the home directory before you share the file. It does not include secrets, approvals, Git credentials, checkpoints, or Docker data. A live export is not frozen; stop the sandbox first for a consistent copy. See [Export and import](https://github.com/lukaskoebe/sandbox-studio/blob/main/docs/export-import.md).
