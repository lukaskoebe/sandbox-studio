---
title: Templates and builds
description: Build a reusable sandbox root filesystem from a small YAML specification.
---

A template describes the software in a sandbox's root filesystem. Its YAML can set CPU, memory, workspace, and Docker sizes; install Node.js, Python, or Go; install apt packages; and run a setup script.

![The Builds page with the template build dialog](../../assets/screenshots/build-dialog.jpg)

## Example

```yaml
resources:
  cpus: 1
  memory: "512MiB"
  max_memory: "1024MiB"
  workspace: "1024MiB"
  docker: "1024MiB"
tools:
  node: "22"
apt:
  - postgresql-client
setup: |
  node --version
```

Quote tool versions. The currently supported tools are `node`, `python`, and `go`. Resource sizes use `MiB`, `GiB`, or `G`; CPU counts are whole numbers.

## Importing YAML does not run it

**Import YAML** only loads the file into the editor. It does not validate or submit the spec, create a VM, or execute commands. Review the text, then select **Build template** when you want Studio to validate and run the build.

After you submit a build, Studio runs the apt installs, language tool installs, and `setup` script in a temporary builder VM. Apt runs as root; the setup script runs as the `agent` user. Treat `setup` as executable shell code. You can follow the job status and read its bounded log on the Builds page. A successful template is cached for later builds. **A ready build does not start a sandbox**; choose **Create sandbox** separately when you want to use it.

**Download YAML** saves the draft or the source submitted for a build. It does not export a built image, secrets, or sandbox disks.

## Create a sandbox from a template

Choose **Create sandbox** on a ready template. Studio boots a new sandbox using the template's root filesystem. The new sandbox has a fresh, empty `/workspace` and Docker disk; it does not inherit a previous sandbox's files or containers.

Template builds and ready templates belong to one environment. A ready template can also be used when rebasing a sandbox. Rebase carries over `/workspace`, but drops Docker data and terminal sessions. See [Sandboxes](../sandboxes/).
