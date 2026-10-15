---
title: Checkpoints
description: Save sandbox disk snapshots by hand or on a schedule, manage them, and understand the current restore limit.
---

A checkpoint is a snapshot of a sandbox's disks: its root filesystem, `/workspace`, its home directory, and Docker data. It does not save running processes or terminal sessions.

You can save a checkpoint while a sandbox is running or stopped. If it is running, the snapshot is crash-consistent: files being written may be incomplete. For application-level consistency, stop the application and sandbox before saving.

![Checkpoints, with save and delete controls](../../assets/screenshots/checkpoints.jpg)

## Save and delete

The **Checkpoints** section of the sandbox's dashboard lists them. Select **Save checkpoint**, enter a name and select **Save**.

Deleting a checkpoint removes only that snapshot, in any order. It does not change the sandbox.

Each checkpoint holds a full copy of the sandbox's disks, so it takes about as much disk space as the sandbox uses.

## Take checkpoints on a schedule

Under **Take checkpoints**, choose **Every hour**, **Every 4 hours**, **Every 12 hours** or **Daily**. It is **Off** by default. Studio takes the first checkpoint right away, then one each interval while the sandbox runs. A stopped sandbox gets one only if it ran since the last, and a suspended one waits until it resumes.

Scheduled checkpoints are named after the time they were taken and marked **auto**. Studio keeps the newest three and the last of each day for a week, and deletes older ones it took, so a sandbox has at most ten. It never deletes the checkpoints you saved. To keep a scheduled checkpoint, rename it on the **History** line; it is then yours.

If a scheduled checkpoint can't be taken, the section says why, and Studio tries again 15 minutes later. Turning the schedule **Off** keeps the checkpoints it took.

## Rename

Select a checkpoint on the sandbox's **History** line and choose **Rename**. Renaming changes only the label, and makes a scheduled checkpoint yours. The saved disks stay as they are.

## Restore is currently unavailable

Checkpoint restore is gated in the current runtime, and Studio disables the Restore control. You can save and delete checkpoints, but you cannot use them to roll a sandbox back yet. **Fork from here** is in the same menu, disabled for the same reason.

For a portable copy of project files, use [Export](../sandboxes/#export-a-workspace). Export includes `/workspace` and the home directory, not the root filesystem or Docker data.
