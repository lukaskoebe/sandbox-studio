# Chat mode (design)

Status: design, not built. Researched 2026-10-11. Versions marked "verify" have not been run
in a VM.

Chat mode puts each agent session in a chat view for people who don't use terminals. The
same harnesses run in the same sandboxes. Studio drives them headless over the Agent Client
Protocol (ACP) instead of showing their TUI in tmux. There's one protocol for all three
harnesses, and the TUI is one click away for every chat.

Decisions taken before this doc:

- **One protocol.** ACP: JSON-RPC 2.0 over stdio.
- **Studio's UI.** It's built from shadcn's chat components (June 2026: MessageScroller,
  Message, Bubble, Attachment, Marker), not AI Elements.
- **Where you choose the view.** Onboarding asks for Chat or Terminal, and Settings can
  change it.
- **Permissions.** A permission request becomes a Studio approval.
- **No client capabilities.** The client advertises no `fs` or `terminal` capabilities. The
  agent works on the sandbox filesystem itself, and Studio never proxies its files or
  secrets.
- **Secrets.** They never enter sandboxes.

## 1. ACP status (verified 2026-10-11)

**The protocol.**

- Protocol version **1** is stable. On 2026-10-09 the schema reached `schema-v1.25.0` and
  the Rust library `v1.11.0`, and the TypeScript SDK `@agentclientprotocol/sdk` reached
  1.8.0.
- v2 is in alpha (`schema-v2.0.0-alpha.8`), so Studio speaks v1 only.
- Methods Studio uses:
  - Calls to the agent: `initialize`, `session/new`, `session/load`, `session/resume`,
    `session/prompt`, `session/close`.
  - Notifications to the agent: `session/cancel`.
  - Calls from the agent: `session/request_permission` and `elicitation/create`.
  - Updates from the agent: the `session/update` notification.
- Session update kinds:
  - Content: `user_message_chunk`, `agent_message_chunk`, `agent_thought_chunk`.
  - Tools and plans: `tool_call`, `tool_call_update`, `plan`.
  - Session state: `available_commands_update`, `current_mode_update`,
    `config_option_update`, `session_info_update`, `usage_update`.
  - Messages and compaction: `notice`, `compaction_update`, `compaction_summary_chunk`.
- Values Studio relies on:
  - Stop reasons: `end_turn`, `max_tokens`, `max_turn_requests`, `refusal`, `cancelled`.
  - Permission option kinds: `allow_once`, `allow_always`, `reject_once`, `reject_always`.
  - Permission outcomes: `selected` or `cancelled`.
  - Plan entries: `pending`, `in_progress` or `completed`, with a `high`, `medium` or `low`
    priority.

**What each harness supports.** All three advertise `loadSession` plus session `resume`,
`list`, `close` and `fork`, accept images and embedded context, and take MCP servers over
stdio and HTTP.

| Harness | ACP agent | Pin | Released |
|---|---|---|---|
| OpenCode | native `opencode acp` (since 0.15.9, 2025-10-20) | `opencode-ai@1.18.35` (already pinned) | 2026-10-06 |
| Claude Code | `@agentclientprotocol/claude-agent-acp` | **0.87.0** | 2026-10-07 |
| Codex | `@agentclientprotocol/codex-acp` | **2.1.1** | 2026-10-01 |

These pins all follow the "older than one day" rule. Newer versions exist but were too new
on 2026-10-11:

- claude-agent-acp: 0.88.0 (2026-10-08) and 0.89.x (2026-10-09 and 10).
- codex-acp: 2.2.x (2026-10-10).

**Claude Code:**

- **The package was renamed twice.**
  - `@zed-industries/claude-code-acp` ended at 0.16.2 (2026-02).
  - `@zed-industries/claude-agent-acp` ended at 0.23.1 (2026-03).
  - It's now `@agentclientprotocol/claude-agent-acp`.
- **How it runs.**
  - It's built on the Claude Agent SDK and runs the Claude Code binary.
  - 0.87.0 depends on `@anthropic-ai/claude-agent-sdk@0.3.287`, which is built for Claude
    Code **2.1.287**, exactly our TUI pin. That's why we pin 0.87.0, not 0.88.0, whose SDK
    is built for 2.1.293.
  - Install it with `--omit=optional` and set `CLAUDE_CODE_EXECUTABLE=/usr/local/bin/claude`.
    That skips the SDK's 241 MiB per-architecture copy of the binary, and chat and TUI run
    the same binary.
- **Settings and permissions.**
  - It reads the user settings (`settingSources: user, project, local`), so Studio's
    `~/.claude/settings.json` applies. That covers the model, `bypassPermissions` and the
    memory hooks.
  - The Studio MCP server, which the TUI gets via `--mcp-config`, is passed in
    `session/new.mcpServers` instead.
  - Bypass mode is refused for root, which doesn't matter here: the agent user is UID 1000.
- **Login: API keys only.**
  - Auth is whatever the Claude Code binary has. `ANTHROPIC_API_KEY` set to the
    placeholder works exactly as in the TUI.
  - Anthropic's Agent SDK terms say: "Unless previously approved, Anthropic does not allow
    third party developers to offer claude.ai login or rate limits for their products,
    including agents built on the Claude Agent SDK."
  - So chat mode supports **API-key Claude providers only**. `claude_subscription` teammates
    stay terminal-only, which also keeps S8 out of chat's critical path.
  - The UI calls this agent "Claude" or "Claude Agent", never "Claude Code", per the same
    page's branding rules.

**Codex:**

- **The package moved.** `@zed-industries/codex-acp` was a Rust binary with
  per-architecture npm packages. It's deprecated, its repo is archived, and its last
  version was 0.16.0 (2026-06).
- **What it is now.**
  - `@agentclientprotocol/codex-acp` 2.x (since 2026-09-28) is a **Node package with no
    per-architecture binaries**. The release assets are empty, and the bun-compiled
    binaries are a local build target only.
  - It starts `codex app-server` and translates. ACP session IDs are Codex thread IDs.
- **Install.**
  - It depends on `@openai/codex@^0.159.1`.
  - Install it with `--omit=optional` and set `CODEX_PATH=/usr/local/bin/codex`. That uses
    the pinned 0.162.1 (released 2026-10-09) and skips a second 431 MiB Codex binary.
- **Environment.**
  - `NO_BROWSER=1` hides ChatGPT browser login.
  - `INITIAL_AGENT_MODE=agent-full-access` matches the TUI's `approval_policy = "never"`.
  - `MODEL_PROVIDER=studio` is set for OpenAI-compatible providers.
  - Auth uses `OPENAI_API_KEY`, set to the placeholder.

**No fallback is needed.** codex-acp 2.x already *is* the app-server bridge. If it lags
behind Codex, the fallback is to speak `codex app-server`'s JSON-RPC directly behind the
same Go interface. That would be a second protocol, so we don't build it unless forced.

**OpenCode:**

- **What works.** `opencode acp` works as in the TUI (docs updated 2026-10-10):
  - `session/load` replays saved messages; `resume` and `fork` don't.
  - The `cwd` must be the session's own directory.
  - Only one prompt runs per session at a time.
  - Questions are sent as `elicitation/create` forms when the client supports
    `elicitation.form`. Otherwise they're cancelled and the model continues.
- **What doesn't.** `/undo` and `/redo` are unsupported over ACP, which doesn't matter:
  Studio has its own undo (section 5).

**Installing the adapters.** One extra `RUN` in `images/base/Dockerfile` adds them as
exact versions:

```dockerfile
ARG CLAUDE_AGENT_ACP_VERSION=0.87.0
ARG CODEX_ACP_VERSION=2.1.1
RUN npm install -g --no-fund --no-audit --no-update-notifier --omit=optional \
        --before=2026-10-08T00:00:00Z \
        "@agentclientprotocol/claude-agent-acp@${CLAUDE_AGENT_ACP_VERSION}" \
        "@agentclientprotocol/codex-acp@${CODEX_ACP_VERSION}" \
    && npm cache clean --force && rm -rf /root/.npm
```

`--before` makes their caret-range dependencies resolve the same way on every build. The
first-party binaries come from the existing pins.

**Studio's client.**

- **Its own package.** The Go client is our own package `internal/acp`, like the
  hand-rolled MCP server in `internal/agentcall/mcp.go`.
- **Why not `coder/acp-go-sdk`.** It's the only Go SDK, and it was last released at v0.13.5
  on 2026-06-02. Schema v1 has moved on since then.
- **Unknown fields.** The client ignores update kinds and `_meta` it doesn't know.

## 2. Mapping ACP to Studio and the UI

**One agent turn.** A chat is one ACP session. A turn is one `session/prompt`, from the
request to its `stopReason`. Updates are folded into **items**. An item is one row per
visible thing, updated in place, not one row per chunk.

| ACP | Studio item (`kind`) | UI |
|---|---|---|
| prompt we sent (plus attachments) | `user` | `Message` with `Bubble` (right), `Attachment` |
| `agent_message_chunk` | `agent`: text appended until another item starts | `Message` with `Bubble` (left), markdown |
| `agent_thought_chunk` | `thought`: appended | `Collapsible` "Thinking…" with the `shimmer` utility while live |
| `tool_call` / `tool_call_update` | `tool`, keyed by `toolCallId`; status `pending`, `in_progress`, `completed` or `failed`; content `content`, `diff` or `terminal` merged | `Card` with a kind icon (read, edit, execute, …), title and status `Badge`, collapsible body: diff, output, locations |
| `plan` | `plan`: one per turn, replaced whole | `Card` checklist (`Checkbox`, read-only), pinned above the composer while the turn runs |
| `session/request_permission` | `permission` item and a `chat.permission` approval | inline `Card` with the approval buttons; also in the approval stack and notifications |
| `elicitation/create` (form) | `question` | inline questionnaire `Card` (shadcn Questionnaire) |
| `usage_update` | on the turn: tokens and cost | turn footer, muted |
| `session_info_update` (title) | `chats.title` | chat list and header |
| `notice`, `compaction_*`, stop reasons other than `end_turn` | `marker` | `Marker` ("Context compacted", "Stopped", "Hit the token limit") |
| `available_commands_update`, `config_option_update`, `current_mode_update` | not stored | not shown (no model or mode picker; the teammate decides) |

**Sending prompts.**

- Content is text, plus `image` blocks for pasted images.
- Other attachments are written to `/workspace/attachments/<name>` through the guest and
  sent as `resource_link` with a `file://` URI. The agent reads them from its own
  filesystem.
- Only one prompt runs per chat. Studio has no prompt queue.

**Cancelling.** Stop sends `session/cancel`. Studio settles every open `chat.permission`
approval of the chat as dismissed and answers its requests with `outcome: cancelled`. The
turn ends with `stopReason: cancelled`, and a "Stopped" `Marker` closes it. If the agent
hasn't answered within 10 s, Studio closes the ACP stream (killing the process) and marks
the turn `interrupted`.

**Resuming and reconnecting.**

- **When the ACP process starts.** It starts on demand: when the chat page opens or a
  prompt is sent.
- **Within a process:** `session/resume` (no replay) when the agent advertises it, which all
  three do.
- **After Studio restarts or the process dies:** the next prompt starts a new process and
  calls `session/resume`. A turn that was running is marked `interrupted`, with a marker.
- **`session/load` only for reconciling**, after the session was used in the terminal
  (section 4). The replay is authoritative for message content. Studio rebuilds the chat's
  items from it and keeps its own rows (turn checkpoints, permissions, markers) attached
  by turn order.

**Permission requests become approvals.**

- **Approval kind `chat.permission`.**
  - Its payload: chat ID and title, sandbox and teammate name, the tool call's title, kind
    and locations, a diff summary (paths and line counts, never file bodies), and the
    offered options (`optionId`, `name`, `kind`).
  - `approvalView` decodes it into `ApprovalView.ChatPermission`, and `approval-kinds.ts`
    gets `"chat.permission"`:
    - Title: "Agent permission".
    - Summary: the tool call's title.
    - Icon: `ChatCircleDotsIcon`.
    - Page: `/e/$env/chats`.
- **How the user's decision maps to an option.**

  | Decision | Remember off | Remember on |
  |---|---|---|
  | Allow | `allow_once` | `allow_always` |
  | Deny | `reject_once` | `reject_always` |
  | Dismiss | `reject_once` | `reject_once` |

  If the agent didn't offer that exact kind, Studio uses the nearest one it did offer.
- **No timeout.** The request waits until it's decided or the turn is cancelled. There's no
  30 s hold as in the browser broker, because the agent is blocked, not a network client.
- **Rarely seen.** The harness configs still auto-approve (docs/harnesses.md). The VM and
  the gateway are the boundary, and egress keeps its own network approvals. So
  `chat.permission` covers only what the agents still ask: Claude's plan exit, mode switches
  and anything a project's settings require.

## 3. Architecture

```text
browser ──SSE/HTTP──▶ internal/api (chats.go) ──▶ internal/chat (Service)
                                                    │  internal/acp (JSON-RPC client)
                                                    ▼
                         agentchan.Hub.OpenACP ──yamux stream "acp"──▶ studio-agent
                                                    │  stdin/stdout of the adapter
                                                    ▼
                              opencode acp | claude-agent-acp | codex-acp  (user agent, /workspace)
```

**Guest (`internal/guest/acp.go`, `internal/agentproto/acp.go`).**

- **A new stream kind, `KindACP`** (host → guest).
  - The header carries `Session` set to the chat ID.
  - The host writes one `StartACP` line: `{chat, harness, command, env}`, checked like
    `StartSession`.
  - After that, the stream is the adapter's raw stdin and stdout, newline-delimited JSON
    with no framing, as with `KindTCP`.
- **How the guest runs the adapter.** It runs the command as the agent user in `/workspace`,
  through `bash -lc 'exec env "$@"'` exactly like `startCommand`. Env holds the provider
  variable set to its placeholder, plus the harness's ACP variables (section 1).
- **Stderr** goes to a capped log, `/run/studio-agent/acp-<chat>.log`, 256 KiB.
- **Lifetime: the process lives exactly as long as the stream.**
  - When the stream closes, the guest closes stdin and sends SIGTERM, then SIGKILL after
    5 s.
  - When the process exits, the guest closes the stream.
  - There are no orphans to supervise, because Studio reattaches with `session/resume`. A
    Studio restart therefore ends running turns, which is acceptable for a local app.
- **Limits:** at most 4 ACP processes per sandbox, and one per chat. The guest refuses a
  duplicate chat ID.
- **The harness interface.** `harness.Harness.ACPCommand()` returns these, with `Env`
  gaining the per-harness variables:
  - `["opencode", "acp"]`
  - `["claude-agent-acp"]`
  - `["codex-acp"]`
- **Turn snapshots** and **files** use their own new stream kinds, `KindSnapshot` and
  `KindFiles` (section 5).

**Host.**

- **`internal/acp`.**
  - A connection over any `io.ReadWriteCloser`: request IDs, a pending map, and the
    handlers for agent → client requests.
  - Typed v1 structs for the subset in section 2. Unknown variants are kept as raw JSON
    and dropped.
  - `initialize` sends `protocolVersion: 1` and
    `clientCapabilities: {elicitation: {form: {}}}`. It sends no `fs`, no `terminal` and no
    `auth.terminal`. A reply with `auth_required` fails the chat with "provider needs a
    login", and Studio never runs ACP auth.
- **`internal/chat`.** It holds one `Service` per Studio, with a live session map keyed by
  chat ID.
  - Starting a chat reuses `planSession`: same checks, same rendered files, written with
    `WriteHomeFiles`. Then come `OpenACP`, `initialize`, and `session/new` with
    `cwd: /workspace`. For Claude, `mcpServers` also gets
    `studio: /opt/studio/bin/studio-agent mcp`.
  - Updates are folded into items in memory and persisted. Item writes are coalesced
    (at most every 250 ms per item while streaming, always at item end and turn end) and
    published to the chat's SSE subscribers.
  - It implements `integrations.Integration`, with no routes and the `chat.permission`
    kind, so `decideApproval` settles permissions through the existing seam.
- **`agentchan.Hub.OpenACP(ctx, id, chat, StartACP) (net.Conn, error)`.**

**Persistence: migration "next", currently 018, as `NNN_chats.sql`.**

```sql
CREATE TABLE chats (
    id             TEXT PRIMARY KEY,
    environment_id TEXT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    sandbox_id     TEXT NOT NULL REFERENCES sandboxes(id) ON DELETE CASCADE,
    teammate_id     TEXT NOT NULL,
    harness        TEXT NOT NULL,
    acp_session_id TEXT NOT NULL DEFAULT '',
    title          TEXT NOT NULL DEFAULT '',
    state          TEXT NOT NULL DEFAULT 'idle', -- idle | running | terminal | orphaned
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
);
CREATE TABLE chat_turns (
    id          TEXT PRIMARY KEY,
    chat_id     TEXT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    seq         INTEGER NOT NULL,
    snapshot    TEXT NOT NULL DEFAULT '',  -- turn snapshot taken before the prompt
    stop_reason TEXT NOT NULL DEFAULT '',  -- ACP stop reason, or interrupted
    usage       BLOB,                      -- JSON: tokens, cost
    changes     BLOB,                      -- JSON: files changed by the turn
    origin      TEXT NOT NULL DEFAULT 'chat', -- chat | terminal
    undone_at   INTEGER,
    started_at  INTEGER NOT NULL,
    ended_at    INTEGER,
    UNIQUE (chat_id, seq)
);
CREATE TABLE chat_items (
    id           TEXT PRIMARY KEY,
    chat_id      TEXT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    turn_id      TEXT NOT NULL REFERENCES chat_turns(id) ON DELETE CASCADE,
    seq          INTEGER NOT NULL,          -- order within the chat
    kind         TEXT NOT NULL,             -- user | agent | thought | tool | plan | permission | question | marker
    tool_call_id TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL DEFAULT '',
    approval_id  TEXT NOT NULL DEFAULT '',
    body         BLOB NOT NULL,             -- JSON, kind-specific
    updated_at   INTEGER NOT NULL,
    UNIQUE (chat_id, seq)
);
```

Item bodies are capped at 256 KiB. Tool output beyond that is truncated, with a
"truncated" flag. The agent's own transcript stays in the guest, and Studio stores only
what it shows.

The view preference is not a table. It's the settings key `ui.agent_view`, valued `chat` or
`terminal`.

**API: `internal/api/chats.go`, huma style, tag `chats`.**

Paths under `/api/environments/{env}`:

| Method and path | Op | Notes |
|---|---|---|
| `GET /chats` | `listChats` | All chats of the environment, newest first |
| `GET /sandboxes/{id}/chats` | `listSandboxChats` | |
| `POST /sandboxes/{id}/chats` | `createChat` | Body `{prompt?}`. 409 for the same reasons as `startSession`, plus subscription providers |
| `GET /chats/{chat}` | `getChat` | The chat, its turns and items |
| `DELETE /chats/{chat}` | `deleteChat` | Also `session/delete` when live |
| `POST /chats/{chat}/prompt` | `sendPrompt` | Body `{text, attachments[]}` (base64, at most 10 MiB in total). 202 with the turn. 409 while running |
| `POST /chats/{chat}/cancel` | `cancelChat` | |
| `POST /chats/{chat}/turns/{turn}/undo` | `undoTurn` | 409 while running |
| `POST /chats/{chat}/terminal` | `openChatInTerminal` | Returns the tmux session name |
| `POST /chats/{chat}/return` | `returnToChat` | Ends the TUI and reconciles |
| `GET /sandboxes/{id}/files?path=` | `listFiles` | |
| `GET /sandboxes/{id}/files/content?path=` | `readFile` | |
| `GET /api/settings/ui` / `PUT /api/settings/ui` | `getUISettings` / `setUISettings` | `{agentView: chat\|terminal\|""}` |

**Streaming** is a plain handler like `streamEvents`:
`GET /api/environments/{env}/chats/{chat}/stream?after=<seq>`.

- SSE carries `item` events (a whole item's JSON), `turn` events and `chat` events.
- It first sends everything after `after`, then live changes. A reconnect resumes from the
  last seen sequence number.
- Chat list changes go to the existing bus as topic `chats`, so other tabs refetch.

**UI** (`web/src/`):

- **Routes.**
  - `routes/e.$env.chats.tsx`: the list, across sandboxes, with "New chat", which picks a
    sandbox that has an owner teammate.
  - `routes/e.$env.chats.$chat.tsx`: the chat itself.
  - The sidebar gets "Chats" above "Sandboxes".
- **Chat page.**
  - **Left:** `MessageScroller` (from `@shadcn/react` 0.3.1, released 2026-08-31, React ≥ 19,
    no other dependencies) with `Message`, `Bubble`, `Attachment` and `Marker` from
    `pnpm dlx shadcn@latest add message-scroller message bubble attachment marker
    questionnaire`.
  - **The composer** is our own: `Textarea`, attach and send/stop buttons. shadcn ships
    none.
  - **Right:** a `Tabs` side panel with Preview, Files and Changes.
  - **Header:** teammate avatar, sandbox, state badge, and "Open in terminal".
- **Components** in `components/chat/`:
  - `chat-items.tsx`: one renderer per item kind.
  - `tool-call-card.tsx`, `plan-card.tsx`, `permission-card.tsx` (reuses
    `approval-decision.tsx`), `question-card.tsx`.
  - `composer.tsx`, `preview-panel.tsx`, `files-panel.tsx`, `changes-panel.tsx`.
- **Sandbox page:** "New session" follows the preference. A chat-backed tmux session shows
  "Back to chat", and a chat shows "Open in terminal".
- **Onboarding and the preference.**
  - On first load, while `agentView` is empty, `__root.tsx` shows a dialog: "How do you
    want to work with agents? Chat (recommended) / Terminal". The answer is saved with
    `setUISettings`.
  - Settings shows the same choice as a `ToggleGroup` under "Agents".
  - The preference sets the default only. Both views exist for every session.

## 4. Open this chat in the terminal

Every harness keeps the ACP session in the same store as its TUI. Only one process may write
a session at a time, so Studio hands it over instead of sharing it.

1. **Chat to terminal** (chat must be idle).
   - Studio sends `session/close` and ends the ACP stream.
   - It starts a tmux session through `StartSession`, with the resume command below. The
     session is tagged `@studio_harness` plus a new option `@studio_chat=<chat>`.
   - The chat goes to state `terminal`, the composer is replaced by "Continue in chat", and
     the UI opens the terminal tab.
2. **Terminal to chat.**
   - "Continue in chat", or the TUI exiting, ends the tmux session. Each harness saves after
     every message.
   - Studio starts the ACP process and calls `session/load`, then reconciles (section 2).
     Turns that exist only in the replay get `origin = terminal`, no snapshot, and a
     "Continued in the terminal" `Marker`.

| Harness | Resume in the TUI |
|---|---|
| OpenCode | `opencode --session <id>` |
| Claude Code | `claude --resume <id> --mcp-config ~/.claude/studio-mcp.json` |
| Codex | `codex resume <id> --dangerously-bypass-hook-trust` |

The ACP session ID is the harness's own ID. The Claude adapter passes it as the SDK's
`resume`, and codex-acp maps it to Codex thread IDs.

**Where it doesn't work:**

- **Both at once.** The chat is read-only while its TUI runs.
- **Subscription Claude teammates.** They have no chat at all (section 1).
- **After a rebase or fork.** The home directory isn't copied, so the agent's session is
  gone. The chat becomes `orphaned`: read-only, with "Start a new chat".
- **Subagent child sessions.** They show inside their parent's tool card only.
- **TUI-only history edits.** `/clear`, `/compact` or a rewind in the TUI change what
  `session/load` replays. The replay wins, and undo is unavailable for turns before the
  edit.
- **Terminal sessions started outside a chat.** They aren't imported. `session/list` would
  make that possible later, but it's not in scope.

## 5. Undo, preview and files

**Undo this change.**

- **The ask** was to build it on disk checkpoints. As of microsandbox 0.7.7 and 0.7.8, they
  can't do per-request undo:
  1. **Restore is gated off.** Upstream
     [#1736](https://github.com/superradcompany/microsandbox/issues/1736) is still open, and
     the restored VM would lose Studio's egress (PLAN.md 6.1).
  2. **A restore is too much.** It needs a stopped sandbox and a new generation, which
     kills the ACP process and dev servers. It would also roll back the root and Docker
     disks, not just the request's file changes.
  3. **Checkpoints can't be pruned from the oldest end.** They form a dependency chain:
     `ErrCheckpointInUse` means newer checkpoints depend on older ones. One per prompt
     would grow without bound.
- **What we build instead: turn snapshots** in the guest. They follow the same model as the
  workspace export (`internal/guest/workspace.go`): a content-addressed copy of
  `/workspace`.
  - **Taking one** (`KindSnapshot`, op `take`) happens before each prompt.
    - The guest walks `/workspace` and skips every `.git` directory.
    - Inside a git work tree it keeps exactly what `git ls-files -co --exclude-standard`
      lists, so ignored files like `node_modules` and build output are not snapshotted.
      Outside git work trees it keeps everything.
    - It skips files over 16 MiB.
    - It stores blobs by SHA-256 in `/home/agent/.local/state/studio/snapshots/` (root disk,
      so disk checkpoints capture it), plus one manifest per turn. The manifest records
      path, mode, size, mtime and hash.
    - Unchanged files are found by size and mtime and not reread. Typically this takes well
      under a second.
  - **After the turn**, op `changes` diffs the manifest against the live tree into
    `chat_turns.changes`: added, modified and deleted paths with line counts. The Changes
    tab and the turn footer ("Changed 3 files") show it.
  - **Undo turn N** (op `restore`) puts the workspace back to the snapshot taken before
    turn N. That reverts turn N and every later turn, and the button says so: "Undo this and
    2 later changes".
    - It rewrites changed and deleted files and removes files created since, but never
      touches `.git` or ignored paths.
    - The turns get `undone_at` and render dimmed.
    - The next prompt starts with a text block: "[Studio] The user undid your changes from
      the last N requests; the files are back as they were before '<first prompt>'." The
      agent knows its earlier edits are gone.
  - **Limits.** The store is capped at 2 GiB per sandbox. Studio keeps snapshots for the
    newest 50 turns per chat and garbage-collects unreferenced blobs. Older turns lose
    their Undo button.
- **Disk checkpoints stay** the manual, whole-sandbox tool in the Checkpoints sheet.

**Preview panel.**

- It polls the existing `Ports` API every 5 s while the chat page is open. When a new port
  appears during a turn, it opens it and adds a `Marker`: "Preview ready on port 3000".
- It embeds `openPreview`'s URL in an `iframe`, and reloads it when a turn ends.
- **A cookie blocker.** The preview cookie is `SameSite=Lax`. The previews are cross-site
  to Studio (`<port>-<id>.localhost` vs `127.0.0.1`), so the cookie isn't sent inside an
  iframe.
  - The preview login gets an `embed=1` mode. It sets the preview cookie as
    `SameSite=None; Secure; Partitioned`, which browsers allow on `*.localhost` because
    it's a secure context.
  - If the frame still doesn't load (`X-Frame-Options` from the app, or a browser that
    blocks partitioned cookies), "Open in a new window" is always next to it.

**Files panel.**

- It's a read-only tree and viewer of `/workspace`.
- `KindFiles` ops:
  - `list`: one directory, at most 1,000 entries.
  - `read`: at most 1 MiB, text or image.
- Both use `openat2` with `RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS`, as `writeHomeFiles`
  does.
- Files changed by the selected turn are highlighted.
- Actions: Download, and "Attach to message", which inserts a `resource_link`.
- There's no editing. Changes go through the agent, so undo stays truthful.

## 6. Stages

Each stage ends green on the rules' checks (`go vet`, `go test`, `make api`, `tsc`, lint,
prettier) using fakes. **VM** marks the checks that need a live VM, and **LLM** those that
need a real provider.

1. **ACP client and the guest bridge.**
   - Build:
     - `internal/acp` with a scripted fake agent.
     - `KindACP` in agentproto, the guest and the hub.
     - `ACPCommand` and `Env` for all three harnesses.
     - The Dockerfile pins.
   - Checks:
     - A fake-agent round trip covering initialize, new, prompt, updates, cancel and a
       permission request.
     - The guest kills the process when the stream closes, and refuses a fifth process or a
       duplicate chat.
     - Golden tests: the ACP env holds placeholders only.
   - VM: `initialize` against each pinned adapter in the built base image. Record the
     capabilities into `internal/harness/testdata/acp/`.
2. **Chats and persistence.**
   - Build: the migration, `internal/chat` (fold updates into items, coalesce, SSE), the
     chats API, and the `chats` topic.
   - Checks:
     - Recorded update streams per harness fold into the expected items (golden files).
     - A restart marks running turns `interrupted`.
     - Reconnecting to the SSE stream with `after` resumes without gaps.
   - VM and LLM: one prompt per harness ending in `end_turn`. Claude's memory hooks fire.
     Codex runs without `codex login` and with `agent-full-access` taking effect.
3. **Chat UI and the preference.**
   - Build: the shadcn chat components, `@shadcn/react@0.3.1`, the chat routes and item
     renderers, the composer with attachments, the onboarding dialog, and the Settings
     toggle.
   - Checks: tsc, lint and prettier. Component tests with recorded items cover every item
     kind, and stop.
   - Manual: a non-developer walkthrough with the fake agent.
4. **Permissions and questions.**
   - Build: `chat.permission` through the integrations seam, the `approval-kinds.ts` entry,
     the permission card, and elicitation forms in the Questionnaire card.
   - Checks:
     - Every decision maps to the right option kind.
     - Cancel dismisses open approvals.
     - Unknown option kinds fall back to the nearest one.
   - VM and LLM: Claude's plan exit asks and is answered from the approval stack. An
     OpenCode question renders as a form.
5. **The terminal bridge.**
   - Build: `openChatInTerminal`, `returnToChat`, the `@studio_chat` tag, and replay
     reconciliation.
   - Checks: the fake agent's replay is reconciled. Terminal-only turns get
     `origin = terminal`.
   - VM and LLM: for each harness, chat, then terminal, then back, and the TUI's turn
     appears in the chat. Verify the three resume flags at the pinned versions.
6. **Undo, Changes and Files.**
   - Build: `KindSnapshot` (take, changes, restore, gc), `KindFiles`, the undo endpoint,
     and the panels.
   - Checks: guest tests on a temp tree:
     - Restoring brings back modified, deleted and created files.
     - `.git` and ignored files are untouched.
     - The caps and GC work.
     - Symlinks never escape.
   - VM: snapshot time on a real repo with `node_modules`.
7. **The preview panel.**
   - Build: the embedded preview login and the panel.
   - Checks: `webauth` tests for the embed cookie attributes.
   - Manual: the iframe loads in Chrome, Firefox and Safari, and the fallback button works.

**Unverified:**

- **Claude adapter.**
  - That it runs with `CLAUDE_CODE_EXECUTABLE` set to the npm-installed 2.1.287 binary, and
    honours `bypassPermissions` from settings.
- **Codex adapter.**
  - That codex-acp 2.1.1 works against Codex 0.162.1's app-server. It was built for
    ^0.159.1.
  - That it needs no `authenticate` call with an API-key placeholder or a custom provider.
- **OpenCode.**
  - Its ACP behaviour at 1.18.35. Our source is the docs page dated 2026-10-10, which
    already advertises "OpenCode v2".
- **Previews.** That partitioned cookies work on `*.localhost` in every browser.
