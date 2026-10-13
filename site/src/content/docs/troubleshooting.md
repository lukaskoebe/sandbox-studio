---
title: Troubleshooting
description: Fix common setup, network, provider, Git, and checkpoint issues.
---

## Studio will not start a sandbox

Run `studio doctor` or open **Settings → Runtime**. Check that hardware virtualization is available and that the host has enough free disk space. On Linux, verify your user can read and write `/dev/kvm`; on Windows, enable Windows Hypervisor Platform and reboot. See [Getting started](../getting-started/).

On first start, Studio may need to download the runtime and base image. Check the host's network connection and available disk space. The app listens on loopback at `127.0.0.1:7878` by default.

## An agent cannot start

- Confirm the sandbox is running and has a teammate assigned.
- Confirm the teammate's harness supports the selected provider.
- Add an API key to the provider and verify it is still valid.
- Confirm an environment network rule does not deny the provider's API host.

Claude Pro/Max and ChatGPT subscriptions sign in from Settings → Providers. A Claude subscription works in terminal sessions, not in chats; use an Anthropic API key for chats.

## A command says it is waiting for network approval

Open the approval inbox and decide the request. The refused connection will not resume by itself: retry the command after allowing the host. Check the Network page if the request is denied by a rule.

## A Git push is still pending

Open the Git approval in the inbox and review the staged diff. Studio does not push it upstream before approval. If you intended to use a forge but see no Git approval, check that the forge is configured in this environment and that the sandbox remote routes through it.

## A checkpoint cannot be restored

Restore is disabled with the current runtime. Checkpoints can be saved and deleted, but not restored yet. For a portable project copy, export `/workspace`; this does not include Docker data or environment secrets.

## A preview does not open

Confirm the sandbox is running, the server is listening on the port you selected, and you opened the link on the machine running Studio. Preview links use `.localhost`; they are not intended for another device on your LAN.

## Move the data directory

Set `SANDBOX_STUDIO_HOME` before starting Studio. Moving the data directory also means moving the key material needed to decrypt the vault; keep the directory and its OS keychain entry together. Do not copy only the catalog database.
