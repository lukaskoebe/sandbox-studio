---
title: Chat mode
description: Work with a teammate in a chat, approve its steps, undo its changes, and see its files and running app.
---

Chat mode lets you work with a teammate's agent like in a messenger instead of in its terminal app. The agent is the same one, with the same harness, model, and sandbox; Studio shows its work as messages, cards, and buttons. The terminal is always one click away.

## Choose Chat or Terminal

The first time you open Studio, it asks **How do you want to work with agents?**

- **Chat** (recommended): write to your teammates, see what they do, and approve risky steps with a click.
- **Terminal**: run each agent's own command-line app in the sandbox, as a developer would.

Closing the dialog without choosing picks Chat. You can change it any time under **Settings → Agents**. The choice only decides what a sandbox workspace shows first: with Chat, its latest chat; with Terminal, its terminals. Both stay one click away in the workspace strip either way.

## What chats need

A chat talks to the teammate that owns a sandbox, so you need:

1. A provider with an API key: Anthropic API key, OpenAI API key, or an OpenAI-compatible endpoint. See [Teammates and providers](../teammates-providers/).
2. A teammate that uses it.
3. A sandbox owned by that teammate, running.

**Claude subscriptions can't be used in chats.** Anthropic doesn't allow claude.ai logins in third-party agents, so Studio refuses to start a chat for a teammate on a Claude subscription. Give the teammate an Anthropic API key provider, or use a terminal session instead.

## Start a chat

Open **Chats** in the sidebar and choose **New chat**, or choose **New chat** on a sandbox's dashboard or in its workspace. Pick the sandbox at the top, write what the teammate should do, and send. The chat starts with that first message, so there are no empty chats.

- **Enter** sends; **Shift+Enter** starts a new line.
- Use the paperclip to attach files, up to 10 MiB per message in total.
- While the teammate works, **Stop** ends the current request. You can write your next message meanwhile and send it once the teammate is done.
- The agent may give the chat a title as it goes. **Chats** lists every chat of the environment; a sandbox lists its own on its dashboard and in its workspace.

The chat shows the agent's replies, what it is thinking, the tools it runs (expand a card to see details such as edits and output), and its plan as a checklist while it works. Under each finished request you see how many files it changed and, if the agent reports it, tokens, cost, and how full its memory is.

## Approve steps

When the agent wants to do something that needs your permission, a card appears in the chat with **Allow**, **Deny**, and **Ignore**. The same request also appears in Studio's approval inbox, so you can answer from any page.

- **Don't ask again for this** appears when the agent offers to remember the decision.
- **Ignore** says no without an answer; the agent stops waiting.

Network requests to hosts without a rule still go through [network approvals](../network/), as in a terminal session.

## Answer questions

Sometimes the agent asks you something, such as which option to pick. The question appears as a short form in the chat. Fill it in and choose **Send answer**, or choose **Skip this question**.

## Undo a change

Each finished request that changed files shows **Changed N files** and **Undo** below it. Undo puts the files in the sandbox's `/workspace` back to how they were before that message.

- **Undo also undoes every later change.** The confirmation says so, for example "Undo this and 2 later changes?".
- Undone messages stay in the chat, dimmed, with a "Changes undone" note.
- Your teammate is told about the undo with your next message, so it knows its earlier edits are gone.
- Undo is available when the teammate isn't working and the chat isn't open in the terminal.

What undo doesn't touch:

- Git history (`.git` folders) and files Git ignores, such as installed packages and build output.
- Files over 16 MiB.
- Anything outside `/workspace`, such as installed system packages or Docker data. For that, use [checkpoints](../checkpoints/).

Studio keeps undo for the newest 50 requests of a chat; older ones lose their Undo button. To keep the snapshots small, it also drops the oldest ones when a sandbox's snapshots reach 2 GiB.

## Changes, Files, and Preview

The panel next to the chat shows the sandbox at a glance. Use the panel button at the top right of a chat to show or hide it. Drag its left edge to resize it on a desktop; on small screens it fills the screen.

- **Changes:** the files one request created, changed, or deleted, with the lines added and removed. Pick the request at the top, or choose **Changed N files** under a message. Expand a file to see its changes. The changes compare the files before and after that request, so they stay accurate after later edits. Very long changes are cut, and non-text files are only listed. Once a request can no longer be undone, its list of files remains but the line-by-line details are gone.
- **Files:** a read-only view of `/workspace`. Open folders, view text files, and see PNG, JPEG, GIF, and WebP images. Files over 1 MiB, other file types, and files the agent can't read are listed but not shown. Files changed by the request selected in Changes are marked. Folders show at most 1,000 items. To change a file, ask your teammate, so undo stays accurate.
- **Preview:** the website or app running in the sandbox. When the teammate starts a server, its port appears here within a few seconds; pick the port if there are several. The preview reloads when a request finishes. Some apps refuse to be shown inside another page, and some browsers block the login it needs; if the preview stays blank or shows an error, choose **Open in new tab**. Previews only work on the computer running Studio. See also [Open a preview](../sandboxes/#open-a-preview).

## Open in the terminal and back

Choose **Open in terminal** at the top of a chat to continue the same conversation in the agent's own terminal app. Studio opens the sandbox workspace on that terminal, and the agent keeps the conversation so far.

While the chat is in the terminal, it is read-only and shows **Back to chat**. Choose it to end the terminal app and continue in the chat; what happened in the terminal shows up in the chat, marked "Continued in the terminal". Exiting the terminal app yourself doesn't bring the chat back. Open in terminal is available when the teammate isn't working.

Requests made in the terminal have no Undo or Changes of their own. Undoing an earlier chat message still puts back everything after it, including what happened in the terminal.

## When a chat ends

If the agent can't pick a conversation back up, for example because the sandbox was rebased and its home folder was replaced, the chat ends. An ended chat is read-only and offers **Start a new chat**.

Deleting a chat removes the conversation. What the agent changed in the sandbox stays.
