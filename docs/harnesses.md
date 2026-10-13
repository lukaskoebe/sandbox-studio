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

Files marked "managed" are rewritten on every session start, so edits made in the sandbox
are lost. The instructions files hold a managed block between
`<!-- sandbox-studio:begin -->` and `<!-- sandbox-studio:end -->`: Studio replaces only the
block, or puts it first, and keeps text outside it. The block has the teammate's name,
role and soul and points to the memory tools.

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
| Auto-approve | `"permission": "allow"` | `permissions.defaultMode = "bypassPermissions"`, `skipDangerousModePermissionPrompt` | `approval_policy = "never"`, `sandbox_mode = "danger-full-access"`, `/workspace` trusted |
| Updates off | `"autoupdate": false` | `env.DISABLE_AUTOUPDATER = "1"` | `check_for_update_on_startup = false` |
| Env | `ANTHROPIC_API_KEY` or `OPENAI_API_KEY` | `ANTHROPIC_API_KEY` | `OPENAI_API_KEY` |
| Hooks | plugin `~/.config/opencode/plugins/studio-memory.js` | `hooks` in settings.json | `~/.codex/hooks.json` |
| MCP | `"mcp": {"studio": {"type": "local", …}}` | `~/.claude/studio-mcp.json` | `[mcp_servers.studio]` |
| TUI | `opencode` | `claude --mcp-config ~/.claude/studio-mcp.json` | `codex --dangerously-bypass-hook-trust` |
| TUI for a chat | `opencode --session <id>` | `claude --resume <id> --mcp-config …` | `codex resume <id> --dangerously-bypass-hook-trust` |
| State dirs | `~/.local/share/opencode`, `~/.local/state/opencode` | `~/.claude` (plus `~/.claude.json`) | `~/.codex` |

Auto-approve is safe here because the VM is the sandbox and the gateway's policy governs
the network. Every harness also gets the git identity in `~/.config/git/config` (managed),
with no credentials. Git reads `~/.gitconfig` after that file, so a user's `~/.gitconfig`
still wins.

Codex needs an OpenAI-compatible endpoint that speaks the Responses API, its only
`wire_api`. OpenCode works with Chat Completions endpoints.

## Memory hooks

Every hook runs `/opt/studio/bin/studio-agent hook <event> --harness <h>` (docs/memory.md).

- Claude Code and Codex: one matcher-less group each for `SessionStart`,
  `UserPromptSubmit`, `PreCompact`, `Stop` and `SessionEnd`, with timeouts of 15, 10, 15, 15
  and 3 seconds. They read `session_id`, `transcript_path`, `cwd`, `source` and `prompt`
  and answer `hookSpecificOutput.additionalContext` for session start and prompts.
- Codex's hooks are in the home, not the system layer, so the TUI starts with
  `--dangerously-bypass-hook-trust` instead of a trust prompt.
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
  per harness and hook event (15 in all). They are documentation-derived, not live
  recordings: Claude Code's hooks reference and Codex's hooks page give the stdin fields,
  and the OpenCode fixtures follow what the plugin writes. No harness was run to record
  them; live recording is spike S7. `TestHookFixtures` checks the neutral event each one
  parses to. OpenCode's plugin sends no `session-end` today (its `session.deleted` handler
  only clears caches), so that fixture is the shape the parser accepts.
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
| Claude | `claude-agent-acp` | `CLAUDE_CODE_EXECUTABLE=/usr/local/bin/claude` | `studio`: `studio-agent mcp` |
| Codex | `codex-acp` | `CODEX_PATH=/usr/local/bin/codex`, `NO_BROWSER=1`, `INITIAL_AGENT_MODE=agent-full-access`, `MODEL_PROVIDER=studio` (API-key providers) | none (read from `config.toml`) |

The adapters are installed without their own copies of the Claude Code and Codex binaries,
so chats and terminals run the same pinned versions. Studio advertises no file system or
terminal capability. A Claude subscription runs chats too, with the same token placeholder as
sessions. The guest kills the agent's process group when the stream closes (SIGTERM,
SIGKILL after 5 s) and runs at most 4 per sandbox, one per chat; its stderr goes to
`/run/studio-agent/acp-<chat>.log`, capped at 256 KiB.

A chat opened in the terminal runs its harness's TUI with the resume command above, in the
tmux session `chat-<chat>` tagged `@studio_chat`. The `<id>` is the ACP session ID, which
is the harness's own session ID. Studio only passes IDs matching
`[A-Za-z0-9][A-Za-z0-9._:-]{0,127}`. The ACP agent is stopped while the TUI runs, since only
one process may write a session.

## Verified vs pending

The updated OpenCode plugin was loaded as active in an existing OpenCode 2.0.26 VM.
Compatibility tests cover the V1 hooks and V2 lifecycle, context, compaction and subprocess
failure handling. End-to-end model-driven hook delivery still needs qualification.

Pending S7 (`// verify (S7)` in the code):

- Hook payloads as the pinned versions send them: the fixtures under
  `internal/harness/testdata/hooks/` are documentation-derived, not recordings.

- Claude Code may ask once whether to use `ANTHROPIC_API_KEY`, and a fresh home shows its
  onboarding. Both are answered in the TUI and remembered in `~/.claude.json`, which Studio
  doesn't write.
- Whether Codex's custom provider with `env_key` needs no `codex login`.
- The OpenCode global rules path.
- Whether each auto-approve setting takes effect without a prompt.
- Live hook and event delivery for the pinned versions; OpenCode V2 event and session
  payload shapes are source-checked against the v2.0.26 schemas above. Claude Code's
  `--mcp-config` servers starting without an approval prompt; Codex's
  `--dangerously-bypass-hook-trust` flag; V1 `client.session.messages`; which lines of
  Codex rollouts are noise.
- The resume commands for chats opened in the terminal, and that each harness's ACP session
  ID is the ID its TUI resumes.
- The model ID `gpt-6-luna` and its price (third-party listings only).

## Subscription logins: built, pending live verification

The sign-in flows, the placeholders, the refresh and the utility calls of
[subscription logins](teammates.md#subscription-logins) are built and covered by fakes in tests.
No test ran a vendor CLI or reached a vendor's host. The owner checks them with their own
accounts. Each assumption is marked `verify (S8)` in the code. The steps:

1. Claude Pro/Max. In Settings → Providers, Add provider → Claude subscription → Add. Choose
   Sign in with Claude. Expect a terminal showing `claude setup-token` and a page link. Open the
   link, sign in, and paste the code into the terminal. Expect "The sign-in is saved." and the
   provider `ready`. If the token isn't read, paste the printed token in the dialog instead.
2. Claude teammate. Create one on that provider with the Claude Code harness and start a session.
   In its terminal, `echo $CLAUDE_CODE_OAUTH_TOKEN` shows a `studio-` placeholder, and a prompt
   gets an answer.
3. Claude chat for the same teammate, and a memory extraction (a utility call through
   `claude -p`). Check the activity for extraction results or a reported failure.
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
