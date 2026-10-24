---
title: Teammates and providers
description: Configure an agent identity and connect it to an API key or a subscription.
---

A teammate is an agent identity in one environment. It has a name, role, avatar, optional Markdown instructions (“soul”), harness, provider, model, and Git author name and email. **New rules from approvals apply to** sets the scope its approval cards preselect: this sandbox, this teammate, or the environment. One teammate can own multiple sandboxes; a sandbox's owner is selected at creation and cannot be changed later.

![The Teammates page listing agent names, harnesses, providers, models, and Git identities](../../assets/screenshots/teammates.jpg)

Each teammate has a page of its own, reached from its name under **Teammates** in the sidebar, where a word says what it is doing or a number how much waits for you. The page is your conversation with the teammate beside the screen of its computer: the sandbox it works in, with its terminal, its browser, a preview of what it serves and its changes (see [the computer's screen](../sandboxes/#the-computers-screen)).

The conversation opens on the teammate's home: its threads as cards by day, marked **New** while one has something you haven't seen, with what it learned, its tasks and what waits for you between them. Below, one box sends a message that starts a thread on the computer shown, hands over a task that it starts on right away, or teaches it something to remember. Open a thread to go on with it; its changes and terminal show on the screen, and **Back** or Escape returns to the home.

Choose the teammate's name at the top for its profile: its open tasks, what it remembers, its computers and its main settings. Each heading opens all of it in a sheet. In the settings, each part (identity, agent, git, check-ins, network level, secrets and more) is a card that says what it is set to and opens to edit it; one **Save** keeps the changes of every card, and the last card deletes the teammate. To use the same teammate in another environment, choose **Copy to environment…** at the bottom of its profile; its sandboxes, chats, memory, secrets and keys stay behind.

## Choose a provider

Studio supports these providers:

| Provider | Harnesses |
| --- | --- |
| Anthropic API key | Claude Code, OpenCode |
| OpenAI API key | Codex, OpenCode |
| OpenAI-compatible HTTPS endpoint | Codex, OpenCode |
| Claude Pro/Max subscription | Claude Code |
| ChatGPT Plus/Pro subscription | Codex |

An OpenAI-compatible endpoint needs a public HTTPS base URL and a model. If you choose Codex, the endpoint must support the OpenAI Responses API.

Tick **The model can see images** if the endpoint's model takes images. It starts off, because many local models read text only. While it is off, the teammate is sent no images: notes you pin on its browser go as text alone, and a chat refuses an attached image with a message that says why.

Claude Pro/Max and ChatGPT subscriptions sign in from the provider dialog: Studio runs the vendor's own login in a short-lived VM and keeps the token on the host; sandboxes get placeholders only. A Claude subscription works in terminal sessions and in chats.

A subscription is not an API key. With an API key you paste the key and are done; with a subscription you sign in with your account in the browser and there is no key to paste. Add the provider, or choose **Sign in** next to it under **Settings → AI providers**, then follow the steps in the dialog:

- **Claude Pro/Max:** open the sign-in page, sign in and choose Authorize, copy the code the page shows, and paste it into the dialog's code field (or press **Paste**). Studio hands the code to Claude Code and saves the sign-in by itself; you don't type into the terminal. **Show the terminal** displays what Claude Code says, and opens by itself if it rejects the code. If you'd rather, run `claude setup-token` on your own computer and paste the `sk-ant-oat…` token it prints under **Use a token from claude setup-token instead**.
- **ChatGPT Plus/Pro:** open the sign-in page, enter the code the dialog shows, and sign in. Studio notices when you're done; there is nothing to paste back.

### Logins on this computer

If Claude Code, Codex or opencode is signed in on the computer Studio runs on, the setup and **Settings → AI providers** list it under **On this computer**:

- **Use Claude subscription** and **Use ChatGPT subscription** open the provider dialog with the kind chosen. Studio still signs in on its own and never copies the computer's login, because a copied login that refreshes first signs the computer out. For ChatGPT, the dialog names the account Codex is signed in with, so you can sign in with the same one.
- **Import keys…** lists the credentials opencode stores. Anthropic and OpenAI API keys can be imported one by one; they're sealed in Studio's vault like a typed key, and opencode keeps its own. opencode's subscription sign-ins can't be imported; **Sign in** adds the matching subscription instead.

Until you choose one of these, Studio only checks that the CLIs' files exist and reads nothing in them.

![Settings → AI providers showing configured API-key providers](../../assets/screenshots/providers.jpg)

Add or rotate provider keys under **Settings → AI providers**. The key remains in Studio's host-side vault; the guest receives a placeholder, which Studio substitutes only for the provider's bound API host. Provider keys are managed with the provider and cannot be edited from **Settings → Secrets**.

## Default teammate

If an environment has no teammates when you add a provider, Studio adds a teammate named Iris on that provider. Iris is a generalist engineer that uses Claude Code with an Anthropic key or a Claude subscription, Codex with an OpenAI key or a ChatGPT subscription, or OpenCode with a custom endpoint. The Git identity is `Iris (agent) <iris@agents.invalid>`. Iris is an ordinary teammate: you can edit or delete Iris. Studio adds Iris again only when the environment has no teammates and you add another provider.

## Start a teammate session

1. Add a provider and enter its API key, or sign in with a subscription.
2. Add a teammate and select a supported harness, provider, and model. A teammate needs a provider for its model: in an environment without one, **New teammate** says so and leads to **Settings → AI providers**.
3. Give the teammate a computer from its page, or create a sandbox and assign that teammate.
4. Write to the teammate on its page: that starts a thread, a chat. See [Chat mode](../chat/).
5. To use the harness's own interface instead, choose **Continue in the terminal** in the thread's **⋯**. Studio opens the harness in a new terminal session on the computer's **Terminal** tab.

The provider key must be bound to the correct API host, and the sandbox must be running. A sandbox without a teammate still has a terminal, but it cannot start an agent session or use teammate memory/browser tools.

Studio sets up the harness inside the sandbox. The teammate's name, role, and soul appear in its managed instruction file. The harness configuration uses the provider placeholder; it does not contain the real key. Changes to the teammate instructions take effect on the next session start.

## Tasks and check-ins

The teammate's tasks, from **Tasks** on its profile, list the work it should do. A task has a title, optional details in Markdown, and an optional due day. You add, edit, and mark tasks done; the teammate can add tasks to its own list while it works in a sandbox.

Check-ins are off until you turn them on in the teammate's settings. With check-ins on, Studio gives the teammate a chat turn every few hours, during the hours you pick, whenever it has open tasks. The turn runs in the teammate's most recent sandbox, and the chat is marked as a heartbeat in the chat list. If that sandbox is stopped, Studio starts it for the check-in, and it keeps running afterwards.

A check-in that can't run is listed under **Activity** in the teammate's tasks with the reason: the teammate has no sandbox, its provider has no key, approvals are waiting in its sandbox, or its sandbox isn't ready.

A task can also get a schedule of its own in its dialog: once at a day and time, on some weekdays at a time, or every few hours inside a window. At each time Studio starts a new chat for that task in the teammate's latest sandbox, or the one you pick, and the chat list marks it as scheduled. If the teammate is busy then, the run waits up to 30 minutes. Each run starts with a clean chat, so the teammate keeps what the next run should know in the task's notes. Check-ins leave scheduled tasks to their own runs. The task list shows each schedule and its next run, and **Run now** starts a run at once. Runs, including the skipped and missed ones, are listed under **Activity**. Only you set schedules; the teammate can't.

## Ownership and deletion

Teammate rules apply only to sandboxes owned by that teammate. A teammate with sandboxes cannot be deleted until those sandboxes are removed. Deleting a teammate also removes its teammate-scoped network rules.
