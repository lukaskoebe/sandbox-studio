---
title: Chat mode
description: Work with a teammate in a chat, approve its steps, undo its changes, and see its files and running app.
---

Chat mode lets you work with a teammate's agent like in a messenger instead of in its terminal app. The agent is the same one, with the same harness, model, and sandbox; Studio shows its work as messages, cards, and buttons. The terminal is always one click away.

## Chat or Terminal

You can work with an agent in two ways:

- **Chat** (recommended): write to your teammates like in a messenger, see what they do, and approve risky steps with a click.
- **Terminal**: run each agent's own command-line app in the sandbox, as a developer would.

A teammate's page has both side by side: the conversation with its threads on the left, and its computer's screen, with the terminal, on the right. A thread's **⋯** continues it in the agent's own interface on the terminal, so both stay one click away.

## What chats need

A chat talks to the teammate that owns a sandbox, so you need:

1. A provider: an Anthropic API key, an OpenAI API key, an OpenAI-compatible endpoint, or a signed-in Claude or ChatGPT subscription. See [Teammates and providers](../teammates-providers/).
2. A teammate that uses it.
3. A sandbox owned by that teammate, running.

A Claude subscription runs chats too: Studio runs Claude Code itself for the teammate, and its permission requests appear as cards like any other agent's. Studio's own tools (memory and tasks) don't ask.

## Start a chat

Open the teammate's page and write in the box at the bottom of its home: what you send starts a thread on the computer its screen shows, and the thread opens. A chat is a thread on its teammate's page. You can also open **All chats** from Home and choose **New chat**, then pick the sandbox at the top. The chat starts with that first message, so there are no empty chats.

- **Enter** sends; **Shift+Enter** starts a new line.
- Use the paperclip to attach files, up to 10 MiB per message in total.
- Type **@** to mention a file of the sandbox's `/workspace`. The list ranks names that match first and marks the files Git reports changed. **↑** and **↓** choose, **Enter** adds the file, **Tab** goes into a folder, and `:20-28` after the name mentions those lines. In a Git repository the list has the files Git knows, without the ones it ignores; elsewhere it leaves out folders such as `node_modules`. The agent gets the message as you wrote it, with `@web/src/checkout/coupon.ts:20-28`, and a link to each file, and reads the files itself, so it sees them as they are. In the sent message each file is a link that opens it in **Files**.
- While the teammate works, **Stop** ends the current request. A message you send meanwhile waits above the box and goes as soon as the teammate finishes, so you can steer it without stopping it. Messages wait in the order you sent them; **Edit** takes one back into the box, the **×** removes it. Waiting messages don't survive a restart of Studio.
- The agent may give the chat a title as it goes. **All chats** lists every chat of the environment; the teammate's home lists its own as cards by day.
- A card says whether its agent is working or waits for you, and is marked **New** while it has something you haven't seen yet, such as a finished reply or a chat that started while you were away, until you open it. What you have seen is remembered in this browser.

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

The screen beside a thread shows the sandbox at a glance: its **Changes** tab has the thread's changes and the sandbox's files, its **Preview** tab what it serves. Enlarge the screen with the arrows at its top right to give it the page, with the conversation as a strip beside it, or fold it with the arrow next to them; on small screens it is a strip of tab icons, and a tab opens over the conversation.

- **Changes:** the files one request created, changed, or deleted, with the lines added and removed. Pick the request at the top, or choose **Changed N files** under a message. Expand a file to see its changes. The changes compare the files before and after that request, so they stay accurate after later edits. Lines are highlighted by the file's language. Very long changes are cut, and non-text files are only listed. Once a request can no longer be undone, its list of files remains but the line-by-line details are gone.
- **Files:** a read-only view of `/workspace`. Open folders, view text files with line numbers and highlighted by their language, and see PNG, JPEG, GIF, and WebP images. Each folder and file you open is a step in the browser's history, so Back (Alt+← or the mouse's back button) returns to the one before, and the address links to the file in view. Files over 1 MiB, other file types, and files the agent can't read are listed but not shown. Files changed by the request selected in Changes are marked. Folders show at most 1,000 items. **@** at the top of a file mentions it in the chat beside it, or the lines selected in it. To change a file, ask your teammate, so undo stays accurate.
- **Preview:** the website or app running in the sandbox, while **Previews** is on under **Settings → General**. When the teammate starts a server, its port appears here within a few seconds; pick the port if there are several. The preview reloads when a request finishes. Some apps refuse to be shown inside another page, and some browsers block the login it needs; if the preview stays blank or shows an error, choose **Open in new tab**. Previews only work on the computer running Studio. See also [Open a preview](../sandboxes/#open-a-preview).

## Open in the terminal and back

Choose **Continue in the terminal** in a thread's **⋯** menu to continue the same conversation in the agent's own terminal app. Studio opens that terminal on the computer's screen, and the agent keeps the conversation so far.

While the chat is in the terminal, it is read-only and shows **Back to chat**. Choose it to end the terminal app and continue in the chat; what happened in the terminal shows up in the chat, marked "Continued in the terminal". Exiting the terminal app yourself doesn't bring the chat back. Continue in the terminal is available when the teammate isn't working.

Requests made in the terminal have no Undo or Changes of their own. Undoing an earlier chat message still puts back everything after it, including what happened in the terminal.

## When a chat ends

If the agent can't pick a conversation back up, for example because the sandbox was rebased and its home folder was replaced, the chat ends. An ended chat is read-only and offers **Start a new chat**.

Deleting a chat removes the conversation. What the agent changed in the sandbox stays.
