---
title: How Studio compares
description: Running a coding agent on your computer, in an OS sandbox, in a container, in Docker Sandboxes, in agents-sandbox, or in Sandbox Studio, side by side.
---

There are several ways to give a coding agent room to work. This page sets them side by side
on what decides how much an agent can reach: what separates it from your computer, which of
your files it sees, where its connections go, where your keys are, and what still asks you.

What we say about other tools comes from their own documentation, as of 22 October 2026, with
a link to the page each time. They change quickly, so check the source before you rely on a
cell. The comparison follows the one in
[agents-sandbox's README](https://github.com/inoio/agents-sandbox); the text here is our own.

The columns:

- **On your computer:** the agent runs as you, with the permission prompts it has itself.
- **OS sandbox:** Claude Code's sandbox for shell commands, built on bubblewrap on Linux and
  Seatbelt on macOS.
- **Docker container:** the agent in a container you start yourself.
- **Docker Sandboxes:** Docker's `sbx`.
- **agents-sandbox:** inoio's launcher for coding agents.
- **Sandbox Studio:** this project.

Sandbox Studio is young: it is at 0.x, runs on one computer for one person, and its interfaces
still change between releases.

## What separates the agent from your computer

| Approach | Separated by |
|---|---|
| On your computer | Nothing. The agent is a process of yours. |
| OS sandbox | Shell commands run under bubblewrap or Seatbelt on your kernel. Claude Code's own file tools, MCP servers and hooks run outside the sandbox.[^cc] |
| Docker container | Namespaces and control groups of your computer's own kernel.[^docker-security] |
| Docker Sandboxes | A microVM for each sandbox.[^ds-arch] |
| agents-sandbox | A [microsandbox](https://github.com/superradcompany/microsandbox) microVM for each project and agent.[^as] |
| Sandbox Studio | A microsandbox microVM for each sandbox, and one more for each teammate's browser. |

## Which of your files it sees

| Approach | Files |
|---|---|
| On your computer | All of them. |
| OS sandbox | It writes to the working directory and a temporary directory, and reads most of the computer, `~/.ssh` and `~/.aws/credentials` included, unless you deny them.[^cc] |
| Docker container | What you mount, usually the project, read-write. |
| Docker Sandboxes | The workspace, read-write at the same path as on your computer; or the repository read-only with a private clone to work in; or nothing.[^ds-security] |
| agents-sandbox | The project, read-write at `/workspace`. By default it also copies the agent's configuration in, which can hold credential files.[^as] |
| Sandbox Studio | None. Nothing on your computer is mounted: the workspace lives in the VM, and the agent clones repositories through Studio. |

## Where its connections go

| Approach | Connections |
|---|---|
| On your computer | Anywhere. |
| OS sandbox | Through a proxy on your computer, to the domains you allow. Your permission mode decides what happens to the others.[^cc] |
| Docker container | Anywhere: containers can connect out by default.[^docker-network] |
| Docker Sandboxes | Through a proxy on your computer, denied unless a policy allows it. The default policy allows broad wildcards such as `*.googleapis.com`. UDP and ICMP are blocked.[^ds-security] |
| agents-sandbox | Denied unless a profile or rule allows it.[^as] |
| Sandbox Studio | Every TCP connection, Docker's included, goes through Studio's gateway. A [security level](../network/) decides what connects without asking: nothing, a curated list of development hosts, an AI reviewer's choice, or every public host. A host on your local network or your computer itself is never allowed by a level. |

## Where your keys are

| Approach | Keys |
|---|---|
| On your computer | Wherever the agent can read them. |
| OS sandbox | Commands inherit Claude Code's environment, secrets included, unless you scrub them.[^cc] |
| Docker container | Inside, as environment variables or files you pass in. |
| Docker Sandboxes | Outside. The agent sees a stand-in, and the proxy puts the real key into requests that leave the VM.[^ds-arch] |
| agents-sandbox | Outside for secrets you declare: the guest sees a placeholder.[^as] |
| Sandbox Studio | Outside, encrypted on your computer. The VM sees a placeholder, which the gateway replaces only in requests to the hosts the [secret](../secrets/) is bound to. |

## Git pushes

| Approach | Pushes |
|---|---|
| On your computer | Sent with your credentials. |
| OS sandbox | Sent with whatever credentials the commands can read. |
| Docker container | Sent with the credentials you pass in. |
| Docker Sandboxes | Its documentation describes no review before a push.[^ds-faq] |
| agents-sandbox | Its README describes no review before a push.[^as] |
| Sandbox Studio | The sandbox holds no forge credentials. A push goes to Studio, you [read the diff](../git-review/), and Studio pushes upstream once you approve. |

## Browsing the web

| Approach | Browser |
|---|---|
| On your computer, OS sandbox, Docker container | Not part of them. |
| Docker Sandboxes | Its documentation describes no browser for the agent.[^ds-faq] |
| agents-sandbox | Its README describes no browser for the agent.[^as] |
| Sandbox Studio | Each teammate has a [Chromium of its own](../browser/) in a microVM. Clicks and typing on a new site ask you, uploads and downloads always do, and Studio fills in stored passwords without the agent seeing them. |

## What asks you

| Approach | Asks you |
|---|---|
| On your computer | The agent's own permission prompts. |
| OS sandbox | Claude Code's permission prompts, which sandboxed commands can skip.[^cc] |
| Docker container | Nothing, once it runs. |
| Docker Sandboxes | Nothing: the agent runs without prompts, and the sandbox is the boundary. Rules are set ahead, by you or by an organization's admins.[^ds-faq] |
| agents-sandbox | Rules are set ahead; its README describes no questions while the agent works.[^as] |
| Sandbox Studio | A connection to a host no rule covers waits for your answer, as do pushes, browser actions and shared memories. You answer on the web page, in the terminal UI or with the CLI, and an answer can become a rule. |

## Where it runs

| Approach | Platforms |
|---|---|
| On your computer | Anywhere the agent does. |
| OS sandbox | macOS, Linux and WSL2. On Windows itself, commands run without a sandbox.[^cc] |
| Docker container | Anywhere Docker does. |
| Docker Sandboxes | macOS 14 on Apple silicon, Windows 11 on x64 with the Windows Hypervisor Platform, Ubuntu 24.04 with KVM, and Docker's cloud.[^ds-install] |
| agents-sandbox | Linux with KVM, and macOS on Apple silicon.[^as] |
| Sandbox Studio | Linux on amd64 or arm64 with KVM, macOS on Apple silicon, and Windows 11 on amd64 as a preview. See [Getting started](../getting-started/). |

## Which to pick

- For a quick task in a project you trust, Claude Code's sandbox costs nothing to set up.
- Docker Sandboxes keeps the workspace on your disk, at the same path, and lets an
  organization manage policies centrally.
- agents-sandbox launches an agent in a microVM for the project you are in.
- Sandbox Studio suits agents that work for a while on their own: each gets a machine that
  stays, and what leaves it, connections, keys, pushes and clicks, passes you first.

[^cc]: Claude Code documentation, [Configure the sandboxed Bash tool](https://code.claude.com/docs/en/sandboxing), as of 22 October 2026.
[^docker-security]: Docker documentation, [Docker Engine security](https://docs.docker.com/engine/security/), as of 22 October 2026.
[^docker-network]: Docker documentation, [Networking overview](https://docs.docker.com/engine/network/): "Containers have networking enabled by default, and they can make outgoing connections." As of 22 October 2026.
[^ds-arch]: Docker documentation, [Docker Sandboxes architecture](https://docs.docker.com/ai/sandboxes/architecture/), as of 22 October 2026.
[^ds-security]: Docker documentation, [Docker Sandboxes security](https://docs.docker.com/ai/sandboxes/security/), as of 22 October 2026.
[^ds-faq]: Docker documentation, [Docker Sandboxes FAQ](https://docs.docker.com/ai/sandboxes/faq/), as of 22 October 2026.
[^ds-install]: Docker documentation, [Install Docker Sandboxes](https://docs.docker.com/ai/sandboxes/install/), as of 22 October 2026.
[^as]: [inoio/agents-sandbox README](https://github.com/inoio/agents-sandbox), as of 22 October 2026.
