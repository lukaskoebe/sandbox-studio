# Harnesses and agent sessions

An agent session is a teammate's harness TUI in a named tmux session in one of its sandboxes
(teammates and providers: [teammates](teammates.md)). Code: `internal/harness` (adapters), `internal/api/sessions.go` (API),
`internal/guest/homefiles.go` and `sessions.go` (guest side).

## Starting a session

`POST /api/environments/{env}/sandboxes/{id}/sessions` refuses with 409 when:

- the sandbox has no owner teammate,
- the teammate's provider needs a sign-in: `login_required`, or `expired` for a ChatGPT login
  that lapsed ([subscription logins](teammates.md#subscription-logins)),
- the provider's key isn't bound to its host (the vendor host, or the base URL's host for
  `openai_compatible`): the gateway swaps the placeholder only on bound hosts,
- the sandbox isn't running.

Then Studio:

1. Renders the harness's files and the git identity.
2. Writes them through the guest agent (`home-files` stream).
   - Owner: the agent user. Paths are relative to `/home/agent`.
   - Resolution: openat2 with `RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS`.
   - Atomic: each file goes to a temporary file that is renamed over the target.
   - Limits: at most 16 files of 256 KiB each.
3. Starts the TUI (`start-session` stream).
   - Runs as `tmux new-session -d` in `/workspace`, through a login shell.
   - The provider's variable is set to the placeholder after the profile has run.
   - The session is named after the harness (`claude`, `claude-2`, …) and tagged with the
     tmux option `@studio_harness`.
   - The harness must be on the login shell's `PATH`. If it isn't (an image built before
     the harnesses were added), the start fails with 409 and says to rebuild the base image
     and create a new sandbox.
   - The pane is kept with `remain-on-exit` for the first 1.5 s. A harness that exits in
     that time fails the start with 409, its exit status and its last output lines (at most
     600 characters, without the command line); its session is removed. After that the
     option goes back to the default, so a harness that exits later ends its session.

`GET .../sessions` lists the tagged tmux sessions. `DELETE .../sessions/{name}` ends one.
Sessions are terminals too, so the sandbox workspace opens them as terminal tabs.

**No file and no environment variable contains a real key.** The adapters get the
placeholder and never see the secret. Config files name only the variable (`{env:VAR}`,
`env_key`); the session gets `VAR=<placeholder>`. The golden tests in
`internal/harness/testdata` and `TestSessionStart` check this.

## Files

Files marked "managed" are rewritten on every session start, and whenever the sandbox's guest
connects or its environment's secrets or teammates change, so edits made in the sandbox are
lost. Writing them on connect sets the harness up in a plain terminal too: `opencode` or
`claude` started by hand finds the owner's config, and new shells get the provider's
placeholder variable (`sandboxes.Manager.SetHarnessConfig`). A teammate that can't run yet,
without a provider or a sign-in, leaves the home as it is. The instructions files hold a managed block between
`<!-- sandbox-studio:begin -->` and `<!-- sandbox-studio:end -->`: Studio replaces only the
block, or puts it first, and keeps text outside it. The block has the teammate's name,
role and soul, says where it works (this sandbox, the names of its other sandboxes, and that
files, unpushed commits, tools, images and servers stay in the sandbox that made them; a
session can't reach or switch to another, so missing work goes through git or the user),
describes the machine (sudo and Docker; tools through `mise use`; a held connection waits up
to a minute for the user, and a 403 with `X-Sandbox-Studio: refused` is the policy; servers
are reached through Studio's Previews, not localhost; `git push` goes to [git
review](git-review.md)) and points to the memory and task tools. `TestInstructionsStayShort`
caps the block at 5000 bytes without a soul: what changes per sandbox belongs behind a tool. Files the harness owns, like Claude Code's `~/.claude.json`, get
only a few keys merged in; the rest of the file is kept. A guest agent older than Studio
gets no such file, since it would replace the whole file.

The soul reaches the agent only through this block, never through the session-start
hook: the harness reads its instructions file in every session, including resumed ones and
those whose hook didn't fire, so the soul is there exactly once. A soul edited in Studio
takes effect with the next session start, which rewrites the block.

| | OpenCode | Claude Code | Codex |
|---|---|---|---|
| Config (managed) | `~/.config/opencode/opencode.json` | `~/.claude/settings.json` | `~/.codex/config.toml` |
| Instructions (block) | `~/.config/opencode/AGENTS.md` | `~/.claude/CLAUDE.md` | `~/.codex/AGENTS.md` |
| Provider | `provider.anthropic`/`openai` with `apiKey: "{env:VAR}"`; compatible: provider `studio` via `@ai-sdk/openai-compatible` with `baseURL` | key from `ANTHROPIC_API_KEY` | `[model_providers.studio]` with `base_url`, `env_key`, `wire_api = "responses"` |
| Model | `model = "<provider>/<model>"` | `model` | `model` |
| Auto-approve | `"permission": "allow"` | `permissions.defaultMode = "bypassPermissions"`, `skipDangerousModePermissionPrompt`, `/workspace` trusted in `~/.claude.json` | `approval_policy = "never"`, `sandbox_mode = "danger-full-access"`, `/workspace` trusted |
| Updates off | `"autoupdate": false` | `env.DISABLE_AUTOUPDATER = "1"` | `check_for_update_on_startup = false` |
| Env | `ANTHROPIC_API_KEY` or `OPENAI_API_KEY` | `ANTHROPIC_API_KEY` | `OPENAI_API_KEY` |
| Hooks | plugin `~/.config/opencode/plugins/studio-memory.js` | `hooks` in settings.json | `~/.codex/hooks.json` |
| MCP | `"mcp": {"studio": {"type": "local", …}}` | `~/.claude/studio-mcp.json` | `[mcp_servers.studio]` |
| TUI | `opencode` | `claude --mcp-config ~/.claude/studio-mcp.json` | `codex --dangerously-bypass-hook-trust` |
| TUI for a chat | `opencode --session <id>` | `claude --resume <id> --mcp-config …` | `codex resume <id> --dangerously-bypass-hook-trust` |
| State dirs | `~/.local/share/opencode`, `~/.local/state/opencode` | `~/.claude` (plus `~/.claude.json`) | `~/.codex` |

Auto-approve is safe here because the VM is the sandbox and the gateway's policy governs
the network. Every harness also gets the git identity in `~/.config/git/config` (managed),
with no credentials: the teammate's, or the host user's when the teammate uses it
([teammates](teammates.md#teammates)). Git reads `~/.gitconfig` after that file, so a user's `~/.gitconfig`
still wins.

Codex needs an OpenAI-compatible endpoint that speaks the Responses API, its only
`wire_api`. OpenCode works with Chat Completions endpoints.

## Memory hooks

Every hook runs `/opt/studio/bin/studio-agent hook <event> --harness <h>` (docs/memory.md).

- Claude Code and Codex: one matcher-less group each for `SessionStart`,
  `UserPromptSubmit`, `PreCompact`, `Stop` and `SessionEnd`, with timeouts of 15, 10, 15, 15
  and 3 seconds. They read `session_id`, `transcript_path`, `cwd`, `source` and `prompt`
  and answer `hookSpecificOutput.additionalContext` for session start and prompts.
- Codex's hooks are in the home, not the system layer. Codex runs such hooks only once they
  are trusted, also in `codex exec` and the app-server behind codex-acp, where untrusted hooks
  are skipped silently. So `config.toml` gets a `[hooks.state]` entry per hook with the
  `trusted_hash` Codex computes for it. The TUI also starts with
  `--dangerously-bypass-hook-trust`, as a fallback if the hash ever changes.
- Codex's app-server also runs a short titling thread per chat. Its hooks come with
  `transcript_path` null and Codex's own title prompt, so studio-agent answers them neutrally.
- OpenCode's plugin default-exports `sandbox-studio.memory` with both V2 `setup(ctx)` and
  V1 `server()` entrypoints (V1 object plugins require 1.18.29+). V2 uses the `prompt`,
  `context` and `compaction` hooks plus an abortable event subscription; both APIs inject
  session and prompt context. V2 idle extraction reads [Session.Message.Info](https://github.com/anomalyco/opencode/blob/v2.0.26/packages/schema/src/session-message.ts)
  user text and assistant text content; V2 compaction reads model messages. Both send bounded transcript
  text. Context also resumes a session after plugin reload, including tool-driven
  continuations. Hook registration failures fail plugin setup; runtime memory-hook failures
  stay fail-open. The plugin resolves the session's directory and reads no OpenCode files.
  V2 event handling follows the [v2.0.26 event envelope](https://github.com/anomalyco/opencode/blob/v2.0.26/packages/schema/src/event.ts),
  [session lifecycle](https://github.com/anomalyco/opencode/blob/v2.0.26/packages/schema/src/session-event.ts),
  [status events](https://github.com/anomalyco/opencode/blob/v2.0.26/packages/schema/src/session-status-event.ts)
  and [compaction events](https://github.com/anomalyco/opencode/blob/v2.0.26/packages/schema/src/session-compaction-event.ts).
  [Session.Info](https://github.com/anomalyco/opencode/blob/v2.0.26/packages/schema/src/session.ts)
  and the [move payload](https://github.com/anomalyco/opencode/blob/v2.0.26/packages/schema/src/session-inbox.ts)
  define `location.directory`. Event payloads use `data`; a move refreshes the cached
  directory and session context.
- Hook fixtures: `internal/harness/testdata/hooks/<harness>/<event>.json` hold one payload
  per harness and hook event, recorded from the pinned versions in spike S7 (2026-10-17).
  `session-start-compact.json` is the session start after a compaction, and Codex's
  `user-prompt-title.json` is its titling thread. `TestHookFixtures` and
  `TestHookFixtureVariants` check the neutral event each one parses to. OpenCode's plugin
  sends no `session-end` (its `session.deleted` handler only clears caches), so that fixture
  is the shape the parser accepts, with the recorded session ID.
  `internal/agentcall/testdata` holds a trimmed recorded Claude transcript and Codex rollout.
- Golden files: `internal/harness/testdata/hooks/<harness>.golden`, the rendered answers
  for the fixtures (`go test ./internal/harness -run TestHookGolden -args -update` after a
  deliberate change).

## Persistence

`/home/agent` is the sandbox's home disk ([sandboxes](sandboxes.md#storage-and-mounts)).
Harness history and sessions:

- survive stop/start and suspend,
- are captured by checkpoints,
- survive rebase, fork and export, which copy the home with the workspace. A chat resumes
  its session after a rebase. A fork keeps the copy, but its chats stay with the source.

A sandbox made from a template built before the home skeleton (exporter version 1) keeps
`/home/agent` on the root disk, with that template's toolchains. Rebuild the template to get
a home disk.

tmux sessions end when the sandbox stops.

## Versions

The base image (`images/base/Dockerfile`) installs these versions.

| Component | Version | Source |
|---|---|---|
| Node.js | 24.21.0 LTS | official tarball, checked against its sha256 |
| `opencode-ai` | 1.18.35 | npm, exact version |
| `@anthropic-ai/claude-code` | 2.1.287 (stable tag) | npm, exact version |
| `@openai/codex` | 0.162.1 | npm, exact version |
| `@agentclientprotocol/claude-agent-acp` | 0.87.0 | npm, exact version, `--omit=optional --before=2026-10-08` |
| `@agentclientprotocol/codex-acp` | 2.1.1 | npm, exact version, `--omit=optional --before=2026-10-08` |

## Chats (ACP)

Chats run each harness's Agent Client Protocol agent instead of its TUI
(`docs/design/chat-mode.md`). The guest starts it on a `KindACP` stream, in `/workspace`
through a login shell, with the session's environment plus the harness's ACP variables.
Like a terminal session's, that environment holds only placeholders and the fixed values
below; a golden test enforces it.

| Harness | Command | Extra environment | MCP servers in `session/new` |
|---|---|---|---|
| OpenCode | `opencode acp` | none | none (read from `opencode.json`) |
| Claude | `claude-agent-acp`; a Claude subscription: `studio-agent claude-acp` | `CLAUDE_CODE_EXECUTABLE=/usr/local/bin/claude` | `studio`: `studio-agent mcp` |
| Codex | `codex-acp` | `CODEX_PATH=/usr/local/bin/codex`, `NO_BROWSER=1`, `INITIAL_AGENT_MODE=agent-full-access`, `MODEL_PROVIDER=studio` (API-key providers) | none (read from `config.toml`) |

The adapters are installed without their own copies of the Claude Code and Codex binaries,
so chats and terminals run the same pinned versions. Studio advertises no file system or
terminal capability. A Claude subscription's chats run `studio-agent claude-acp` instead
([below](#claude-subscription-chats)). The guest kills the agent's process group when the stream closes (SIGTERM,
SIGKILL after 5 s) and runs at most 4 per sandbox, one per chat; its stderr goes to
`/run/studio-agent/acp-<chat>.log`, capped at 256 KiB.

Studio stops a chat's agent after 30 minutes without a turn, and when a fifth chat of the
sandbox needs one it stops the agent that sat idle longest. An agent that works or waits on
the user is never stopped; with four of those, the fifth chat is told to wait. The next
message to a stopped chat starts its agent again and resumes the session. A session the
agent no longer finds ends the chat (orphaned), unless the agent never answered in it, as
when its first message was stopped: then the chat starts a new one.

A message sent while a turn runs waits in a queue in memory and goes once the chat is idle,
one turn each. One that can't go then, because the agent can't start, stays first with the
reason; the rest wait behind it until the user's next message, which tries again. Stop
while the agent still starts stops the message before it goes, and the composer gets it
back.

A chat opened in the terminal runs its harness's TUI with the resume command above, in the
tmux session `chat-<chat>` tagged `@studio_chat`. The `<id>` is the ACP session ID, which
is the harness's own session ID. Studio only passes IDs matching
`[A-Za-z0-9][A-Za-z0-9._:-]{0,127}`. The ACP agent is stopped while the TUI runs, since only
one process may write a session.

### Claude subscription chats

A chat for a Claude subscription runs `studio-agent claude-acp`, the guest's copy of Studio's
agent binary, as its ACP agent. The shim (`internal/claudeacp`) serves one session on stdio
and runs Claude Code itself for its turns: `claude -p --input-format stream-json
--output-format stream-json --verbose --include-partial-messages`, one process per session,
which stays up between prompts. API-key chats keep `claude-agent-acp` for now; switching
every Claude chat to the shim is a later step, after a live check.

**Whether this use of a Claude subscription fits Anthropic's terms is the owner's decision.**
The design notes in [chat mode](design/chat-mode.md) quote the terms on claude.ai logins in
third-party agents. The shim runs the Claude Code binary, not the Agent SDK, but the question
is the same.

Claude Code's output becomes ACP updates:

| Claude Code | ACP |
|---|---|
| `stream_event` text and thinking deltas | `agent_message_chunk`, `agent_thought_chunk` |
| `assistant` message with a `tool_use` block (complete input) | `tool_call`, pending; the title and kind come from the tool, with its input and the files it touches |
| `user` message with a `tool_result` | `tool_call_update`, `completed` or `failed` (`is_error`), with the result's text |
| `assistant` `TodoWrite` | `plan` |
| `system` `compact_boundary` | `compaction_update` |
| `result` | the prompt's response: `end_turn`, `max_tokens`, `refusal` or `max_turn_requests`; an error result fails the prompt; `total_cost_usd` is a `usage_update` cost |

Text that streamed as deltas isn't shown again from the complete message. Messages of
subagents (`parent_tool_use_id` set) aren't shown.

**Permissions.** The shim passes `--permission-mode bypassPermissions`, the mode the
teammate's `settings.json` sets for the TUI: the VM is the sandbox, and network access goes
through the gateway's policy, so tools run without asking, as they do for Codex
(`approval_policy = "never"`) and OpenCode (`"permission": "allow"`). It also passes
`--permission-prompt-tool mcp__plugin_studio_permissions__prompt`, which Claude Code asks
before an action it doesn't approve itself; in this mode that leaves the tools that need the
user in any mode. The tool is the permissions server, `studio-agent
claude-permission`, which sends the question over the session's socket to the shim. The socket
is `0600` in the shim's private run directory. The shim asks Studio with
`session/request_permission`, offering **Allow** (`allow_once`) and **Deny** (`reject_once`);
Studio's Ignore is a denial. Studio's own MCP tools run without asking, through
`--allowedTools`, since the `studio` server decides what the teammate may read and write.
`AskUserQuestion` is denied with a message, because chats don't take answers yet. The options
don't include "don't ask again": the shim doesn't write Claude Code rules, and the prompt tool's
rule-writing result isn't verified.

**Plugin.** The shim writes a plugin and loads it with `--plugin-dir`: a manifest named
`studio`, and a `.mcp.json` with the servers `session/new` sends (`studio`, which is
`studio-agent mcp`) and the permissions server. Plugin servers start with the session and need
no approval, unlike a project's `.mcp.json`. Their tools are named
`mcp__plugin_studio_<server>__<tool>`. The Claude Code docs support `--plugin-dir` and plugin
`.mcp.json`, so there is no `--mcp-config` fallback.

**Sessions and cancel.** `session/new` picks a UUID and starts Claude Code with `--session-id`.
Once a transcript exists (`<config>/projects/<project>/<id>.jsonl`), a new process uses
`--resume`. `session/resume` answers "Resource not found" when there is no transcript, and
Studio ends the chat. The session ID is Claude Code's, so the terminal's `claude --resume <id>`
continues the same conversation. `session/cancel` sends SIGTERM to Claude Code (SIGKILL after 5
s), and the turn ends `cancelled`. The next prompt starts Claude Code with `--resume`, which
the docs say leaves an interrupted turn as it is.

Not done yet:

- Back to chat after a terminal session shows a marker, "Continued in the terminal. This agent
  can't show those messages here.", not the terminal's turns. That needs `session/load` with a
  transcript replay.
- Attachments: text files as text, PDFs as documents, PNG, JPEG, GIF and WebP as images, other
  files named in a text block.

The docs were checked, not a binary: the flags, the plugin layout and tool names, the
stream-json fields and the transcript location come from the Claude Code docs at
code.claude.com. `internal/claudeacp/testdata` holds fixtures written from those docs, not
recordings. The tests run a fake `claude` (the test binary) that prints them and asks the real
permissions server over MCP. Spike S7 then ran the real Claude Code 2.1.287 through
`studio-agent claude-acp`, with an API key and a fake model (below).

## Verified vs pending

Spike S7 ran the pinned versions on 2026-10-17: OpenCode 1.18.35, Claude Code 2.1.287 and
Codex 0.162.1, in the v0.3.2 base image, against a fake model endpoint (Anthropic Messages
and OpenAI Responses) with placeholder keys. Verified:

- Hook payloads. The fixtures are now those recordings. Claude Code and Codex send the field
  names Studio reads, plus more it ignores (`turn_id`, `model`, `permission_mode`,
  `last_assistant_message`).
- Context injection in all three: Claude Code shows `additionalContext` to the model in a
  system reminder, Codex as a developer message, OpenCode in the system prompt.
- Hooks fire in the TUI, in `claude -p` and `codex exec`, and through each ACP adapter.
- After a compaction: Claude Code and OpenCode fire SessionStart with source `compact` right
  away, Codex at the next turn. Codex and OpenCode fire PreCompact. `--resume` gives source
  `resume`.
- Claude Code: no onboarding, trust or bypass prompt; the API key approved by the last 20
  characters of its placeholder (without them it asks); `--mcp-config` servers connect without
  an approval prompt; `~/.claude/CLAUDE.md` is loaded.
- Codex: `env_key` needs no `codex login`; Responses API with tool calls; which rollout lines
  are noise. Its compaction summary is an ordinary assistant message, so the condensed
  transcript keeps it.
- OpenCode: the V1 plugin path, `client.session.messages`, and the global rules path
  `~/.config/opencode/AGENTS.md`.
- Every auto-approve setting: a shell command ran without a prompt in each harness.
- Resume: each ACP session ID is the ID the TUI resumes (`opencode --session`,
  `claude --resume`, `codex resume`).
- Claude chats through `studio-agent claude-acp`: the stream-json input, `--session-id` and
  `--resume`, the permission prompt tool's request and answer, and "Resource not found" for an
  unknown session.

Fixed by S7:

- Codex ran no Studio hooks without `--dangerously-bypass-hook-trust`, so chats through
  codex-acp had no memory. Studio now writes the trust state into `config.toml`.
- Codex's titling thread sent its own prompt as a user prompt. studio-agent skips it.
- tmux 3.5a rejects `set-option -t "=name"`, which failed every agent session start. The
  session options now target `=name:`.
- Claude transcripts: `/compact` summaries and local command lines are no longer condensed as
  conversation.

Still pending:

- OpenCode V2: payload shapes are source-checked against the v2.0.26 schemas above, and the
  plugin loaded in a 2.0.26 VM, but model-driven hook delivery is unrun.
- The model ID `gpt-6-luna` and its price: S7 had no OpenAI credentials. Codex itself titles
  chats with `gpt-5.6-luna`.
- Claude subscription chats: whether the subscription's token is accepted by `claude -p` in
  the sandbox (S8), `--allowedTools` on plugin tools, and cancelling a turn by SIGTERM.

## Subscription logins: built, pending live verification

The sign-in flows, the placeholders, the refresh and the utility calls of
[subscription logins](teammates.md#subscription-logins) are built and covered by fakes in tests.
No test ran a vendor CLI or reached a vendor's host. The owner checks them with their own
accounts. Each assumption is marked `verify (S8)` in the code.

Verified: the Claude sign-in (step 1) worked on the owner's machine on 2026-10-17. Steps 2 to 7
are still to be run. The steps:

1. Claude Pro/Max. In Settings → AI providers, Add provider → Claude subscription → Add. Choose
   Sign in with Claude. Expect the dialog's steps: open the sign-in page, sign in and authorize,
   copy the code the page shows, and paste it into the dialog's code field (or choose Paste).
   The terminal running `claude setup-token` is hidden behind "Show the terminal Claude Code
   runs in". Expect "The sign-in is saved." and the provider `ready`. If the token isn't read,
   "Use a token from claude setup-token instead" opens: paste the token that
   `claude setup-token` prints on your own machine.
2. Claude teammate. Create one on that provider with the Claude Code harness and start a session.
   In its terminal, `echo $CLAUDE_CODE_OAUTH_TOKEN` shows a `studio-` placeholder, and a prompt
   gets an answer.
3. A chat on the subscription. It streams an answer, and a tool call that needs approval shows a
   permission card with Allow and Deny. Studio's own tools (memory, tasks) don't ask. Then a
   memory extraction for the same environment, with the subscription as its utility model (a
   utility call through `claude -p`): check the activity for extraction results or a reported
   failure.
4. ChatGPT. Add provider → ChatGPT subscription → Add, then Sign in with ChatGPT. Expect the page
   and the code. Enter the code on the page. Expect "The sign-in is saved." and `ready`.
5. ChatGPT teammate. Start a session. In its terminal, `~/.codex/auth.json` holds `studio-`
   placeholders and no refresh token, and a prompt gets an answer.
6. Refresh. Nothing to do, unless an access token is near its end: then the log shows the refresh,
   and a running session keeps working.
7. Failures. Cancel a sign-in, Sign in again, and check that an expired ChatGPT login shows
   "Sign-in expired" and refuses sessions.

The assumptions to confirm against the pinned CLIs: the hosts in `internal/policy/system.go`; the
`sk-ant-oat` token format and how `claude setup-token` prints it; tmux `remain-on-exit` and
`pane_dead`; the device login's page and code text, and `~/.codex/auth.json`'s shape; the
ChatGPT token endpoint and client ID; the JSON that `claude -p --output-format json` and
`codex exec --json` print; and that Codex reads only the claims of its JWTs in the guest's copy.
