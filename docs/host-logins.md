# Logins on this computer

Studio runs on the user's own computer, where Claude Code, Codex or opencode are often signed in
already. The setup's provider step and **Settings → AI providers** offer the providers that
match those logins under **On this computer** (`internal/hostlogins`, `web/src/components/host-logins.tsx`).

## Never a copied sign-in

Studio never copies a subscription's login from the computer. Claude's and Codex's refresh
tokens rotate: when two clients refresh the same token, the first refresh invalidates the
other's, and the computer is signed out (Codex calls it `refresh_token_reused`). Anthropic's
terms also forbid collecting Claude.ai tokens. Studio signs in on its own instead, through its
login VM ([subscription logins](teammates.md#subscription-logins)), which gives it a login of
its own and leaves the computer's as it is.

## What Studio reads, and when

The files are read only by the user's choice, one provider at a time:

| Login | Where | Without asking | By the user's choice |
|---|---|---|---|
| Claude Code | `.credentials.json` in `$CLAUDE_CONFIG_DIR` or `~/.claude`, or `~/.claude.json` (macOS keeps the login in the keychain) | that a file exists | nothing: **Use Claude subscription** opens the provider dialog, and Studio's own `claude setup-token` makes a separate token |
| Codex | `auth.json` in `$CODEX_HOME` or `~/.codex` | that the file exists | picking **ChatGPT subscription** in the provider dialog reads the `email` and `chatgpt_plan_type` claims of the ID token, to say which account to sign in with. The tokens themselves are not kept |
| opencode | `opencode/auth.json` in `$XDG_DATA_HOME` or `~/.local/share`, on every OS | that the file exists | **Import keys…** lists the stored credentials by provider and type, without their content. **Import** on one of them reads that key and stores it like a typed one |

- Only plain API keys (`"type": "api"`) of `anthropic` and `openai` can be imported; they
  become `anthropic_api` and `openai_api` providers, sealed in the vault and bound to the
  vendor's host. opencode keeps its own copy.
- An OAuth entry is a subscription sign-in: the import dialog offers **Sign in** instead, which
  adds the matching subscription provider.
- A key of another provider is listed, and can be added by hand as an OpenAI-compatible
  endpoint.
- The offer of a subscription disappears once the environment has a provider of that kind.

## The API

| Endpoint | What |
|---|---|
| `GET /api/system/host-logins` | `{claude, codex, opencode}`: which files exist |
| `GET /api/system/host-logins/codex` | `{email, plan}` of the Codex login; 404 without one |
| `GET /api/system/host-logins/opencode` | opencode's credentials as `{provider, type, kind}`; `kind` is set on those Studio can import |
| `POST /api/environments/{env}/providers/import-opencode` | `{provider, name}`: creates a provider from that key |

Studio looks in the home directory of the user it runs as. The e2e build looks in
`e2e-host-home` in its data directory instead, and ignores the CLIs' variables, so tests never
read the home of whoever runs them.
