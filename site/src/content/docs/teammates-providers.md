---
title: Teammates and providers
description: Configure an agent identity and connect it to an API-key provider.
---

A teammate is an agent identity in one environment. It has a name, role, optional Markdown instructions (“soul”), harness, provider, model, and Git author name and email. One teammate can own multiple sandboxes; a sandbox's owner is selected at creation and cannot be changed later.

![The Teammates page listing agent names, harnesses, providers, models, and Git identities](../../assets/screenshots/teammates.jpg)

Each teammate has a page of its own, reached from **Teammates** in the sidebar: its overview, tasks, chats, private memory, browser, and settings, where you edit it or delete it.

## Choose a provider

Studio supports API-key providers:

| Provider | Harnesses |
| --- | --- |
| Anthropic API key | Claude Code, OpenCode |
| OpenAI API key | Codex, OpenCode |
| OpenAI-compatible HTTPS endpoint | Codex, OpenCode |

An OpenAI-compatible endpoint needs a public HTTPS base URL and a model. If you choose Codex, the endpoint must support the OpenAI Responses API.

Claude Pro/Max and ChatGPT subscription sign-in are not available. Use an API key from a supported provider instead.

![The Providers settings page showing configured API-key providers](../../assets/screenshots/providers.jpg)

Add or rotate provider keys under **Settings → Providers**. The key remains in Studio's host-side vault; the guest receives a placeholder, which Studio substitutes only for the provider's bound API host. Provider keys are managed with the provider and cannot be edited from the general Secrets page.

## Default teammate

If an environment has no teammates when you add a provider, Studio adds a teammate named Iris on that provider. Iris is a generalist engineer that uses Claude Code with an Anthropic key, Codex with an OpenAI key, or OpenCode with a custom endpoint. The Git identity is `Iris (agent) <iris@agents.invalid>`. Iris is an ordinary teammate: you can edit or delete Iris. Studio adds Iris again only when the environment has no teammates and you add another provider.

## Start a teammate session

1. Add a provider and enter its API key.
2. Add a teammate and select a supported harness, provider, and model.
3. Create a sandbox and assign that teammate.
4. Open the sandbox and choose **Start session**. Studio opens the harness in a terminal tab.

To work with the teammate in a chat instead, see [Chat mode](../chat/).

The provider key must be bound to the correct API host, and the sandbox must be running. A sandbox without a teammate still has a terminal, but it cannot start an agent session or use teammate memory/browser tools.

Studio sets up the harness inside the sandbox. The teammate's name, role, and soul appear in its managed instruction file. The harness configuration uses the provider placeholder; it does not contain the real key. Changes to the teammate instructions take effect on the next session start.

## Tasks and check-ins

The **Tasks** tab lists the work a teammate should do. A task has a title, optional details in Markdown, and an optional due day. You add, edit, and mark tasks done; the teammate can add tasks to its own list while it works in a sandbox.

Check-ins are off until you turn them on under the teammate's **Settings**. With check-ins on, Studio gives the teammate a chat turn every few hours, during the hours you pick, whenever it has open tasks. The turn runs in the teammate's most recent sandbox, and the chat is marked as a heartbeat in the chat list. If that sandbox is stopped, Studio starts it for the check-in, and it keeps running afterwards.

A check-in that can't run is listed under **Activity** on the Tasks tab with the reason: the teammate has no sandbox, its provider has no key, approvals are waiting in its sandbox, or its sandbox isn't ready.

## Ownership and deletion

Teammate rules apply only to sandboxes owned by that teammate. A teammate with sandboxes cannot be deleted until those sandboxes are removed. Deleting a teammate also removes its teammate-scoped network rules.
