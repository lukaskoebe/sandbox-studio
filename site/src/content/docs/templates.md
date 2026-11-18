---
title: Templates and builds
description: Build a reusable root filesystem for computers from a small YAML specification.
---

A template describes the software in a computer's root filesystem. Its YAML can set CPUs, memory, and the size of the workspace, Docker and home disks; install Node.js, Python, Go, or Nix; install apt packages; and run a setup script. Templates are under **Settings → Templates**, and belong to the environment you are in.

A lighter way to start every new computer from the same setup is a [base](../sandboxes/#a-fresh-computer-per-task): a computer you prepared by hand, which new computers copy.

![The Templates settings page with the template build dialog](../../assets/screenshots/build-dialog.jpg)

## Example

New builds start from this **Web app** template. **Insert example** in the editor puts it back, or inserts **Studio dev**, the template Studio's own development uses.

```yaml
resources:
  cpus: 2
  memory: 4GiB
  workspace: 20GiB
  docker: 20GiB
tools:
  node: "22"
  python: "3.13"
apt:
  - postgresql-client
setup: |
  corepack enable
```

Quote tool versions. The currently supported tools are `go`, `node`, `nix`, and `python`. `nix` takes only the Nix releases Studio pins, currently 2.35.1. Resource sizes use `MiB`, `GiB`, or `G`; CPU counts are whole numbers. `resources` also takes `home`, the size of the disk that holds `/home/agent` with the agents' sessions and caches; it is 4 GiB unless you name another.

## Importing YAML does not run it

**Import YAML** only loads the file into the editor. It does not submit the spec, create a VM, or execute commands. Review the text, then select **Build template** when you want Studio to run the build.

As you type, the editor highlights the YAML, suggests keys, tool names, and size units, and marks each problem at its line and column. These checks use the same parser as the build and create nothing.

After you submit a build, Studio runs the apt installs, language tool installs, and `setup` script in a temporary builder VM. Apt runs as root; the setup script runs as the `agent` user. Treat `setup` as executable shell code. You can follow the job status and read its bounded log on **Settings → Templates**. A successful template is cached for later builds. **A ready build does not start a computer**; choose **Create sandbox** separately when you want to use it.

**Download YAML** saves the draft or the source submitted for a build. It does not export a built image, secrets, or a computer's disks.

## Create a computer from a template

On **Settings → Templates**, open a ready build and choose **Create sandbox** on its sheet or its page. Studio boots a new computer using the template's root filesystem. The new computer has a fresh, empty `/workspace` and Docker disk, and a home directory seeded from the template; it does not inherit another computer's files or containers.

Template builds and ready templates belong to one environment. A ready template can also be used when rebasing a computer: choose **Rebase…** under **Maintenance** on its **Details** tab. Rebase carries over `/workspace`, the home folder and the tools from `mise use`, but drops terminal sessions, and Docker's data unless you ask to keep it. A template built on an older base image than this Studio's has **Rebuild on the current base**, which builds the same spec again as the template's next version. See [Computers](../sandboxes/#rebase-onto-a-newer-base).
