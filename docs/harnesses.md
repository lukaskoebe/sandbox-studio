# Harnesses and agent sessions

An agent session is a persona's harness TUI in a named tmux session in one of its sandboxes
(PLAN.md §6.6). Code: `internal/harness` (adapters), `internal/api/sessions.go` (API),
`internal/guest/homefiles.go` and `sessions.go` (guest side).

## Starting a session

`POST /api/environments/{env}/sandboxes/{id}/sessions` refuses with 409 when:

- the sandbox has no owner persona,
- the persona's provider is `login_required` (subscriptions, until S8),
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

`GET .../sessions` lists the tagged tmux sessions. `DELETE .../sessions/{name}` ends one.
Sessions are terminals too, so the sandbox page opens them as terminal tabs.

**No file and no environment variable contains a real key.** The adapters get the
placeholder and never see the secret. Config files name only the variable (`{env:VAR}`,
`env_key`); the session gets `VAR=<placeholder>`. The golden tests in
`internal/harness/testdata` and `TestSessionStart` check this.

## Files

Files marked "managed" are rewritten on every session start, so edits made in the sandbox
are lost. The instructions files hold a managed block between
`<!-- sandbox-studio:begin -->` and `<!-- sandbox-studio:end -->`: Studio replaces only the
block, or puts it first, and keeps text outside it. The block has the persona's name and
role and points to the memory tools. The soul comes with memory's session-start context.

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
- OpenCode's plugin maps `session.created` and `session.compacted` to session start,
  `chat.message` to prompts, `session.idle` to stop and `experimental.session.compacting`
  to pre-compact. It adds the context through `experimental.chat.system.transform` and
  sends the transcript inline (text parts from the session's messages) with an offset.
  The guest reads no OpenCode files.
- Golden files: `internal/harness/testdata/hooks/`.

## Persistence

`/home/agent` is on the sandbox's root disk. Harness history and sessions:

- survive stop/start and suspend,
- are captured by checkpoints,
- are lost on rebase and fork, which copy only `/workspace` onto a fresh root disk.

tmux sessions end when the sandbox stops.

## Versions

The base image (`images/base/Dockerfile`) installs these versions.

| Component | Version | Source |
|---|---|---|
| Node.js | 24.21.0 LTS | official tarball, checked against its sha256 |
| `opencode-ai` | 1.18.35 | npm, exact version |
| `@anthropic-ai/claude-code` | 2.1.287 (stable tag) | npm, exact version |
| `@openai/codex` | 0.162.1 | npm, exact version |

## Verified vs pending

Every format above comes from the vendor docs cited in each adapter. None of it has been
run against the real harnesses yet: the image wasn't built and no VM could start here.

Pending S7 (`// verify (S7)` in the code):

- Claude Code may ask once whether to use `ANTHROPIC_API_KEY`, and a fresh home shows its
  onboarding. Both are answered in the TUI and remembered in `~/.claude.json`, which Studio
  doesn't write.
- Whether Codex's custom provider with `env_key` needs no `codex login`.
- The OpenCode global rules path.
- Whether each auto-approve setting takes effect without a prompt.
- Hook payload fields and answers for the pinned versions; Claude Code's
  `--mcp-config` servers starting without an approval prompt; Codex's
  `--dangerously-bypass-hook-trust` flag; the OpenCode plugin's event and hook names and
  `client.session.messages`; which lines of Codex rollouts are noise.
- The model ID `gpt-6-luna` and its price (third-party listings only).

Pending S8: subscription logins (`claude_subscription`, `chatgpt_subscription`). Sessions
refuse these providers until the login flow exists.
