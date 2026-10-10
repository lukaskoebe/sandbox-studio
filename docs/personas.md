# Personas and providers

Both belong to one environment. Harness adapters, which turn a persona into config files
inside a sandbox, come later (PLAN.md §6.6).

## Providers

| Kind | Harnesses | Credential |
|---|---|---|
| `anthropic_api` | claude, opencode | key bound to `api.anthropic.com`, guests see `ANTHROPIC_API_KEY` |
| `openai_api` | codex, opencode | key bound to `api.openai.com`, guests see `OPENAI_API_KEY` |
| `openai_compatible` | opencode, codex | key bound to the base URL's host, `OPENAI_API_KEY` |
| `claude_subscription` | claude | none yet: state `login_required` |
| `chatgpt_subscription` | codex | none yet: state `login_required` |

- An API key is stored as an environment secret named `PROVIDER_<NAME>_KEY`. It is created,
  rotated and deleted only with its provider, in one transaction; the secrets API answers
  409 for it. Guests see only the placeholder, as with any secret.
- `openai_compatible` takes an `https://` base URL on a public host (no credentials, query
  or fragment, no private, loopback or link-local address) and the endpoint's single model.
- Name and kind are fixed. A provider used by a persona can't be deleted (409).
- The subscription login flow (`claude setup-token` / `codex login` in a system sandbox) is
  a TODO.

## Personas

- Name (unique in the environment), role, soul (markdown, at most 16 KiB), harness,
  provider, model, git name and email.
- The harness must be supported by the provider's kind. API-key providers need a model;
  `openai_compatible` defaults to the provider's model.
- Git name defaults to the name, git email to `<slug>@agents.invalid`. Both are one line
  without control characters, quotes or angle brackets, so they are safe in git config.
- A persona that owns sandboxes can't be deleted (409). Deleting a persona deletes the
  network rules scoped to it.

## Ownership and rules

- A sandbox can be created with a `personaId` (base or template). Ownership never changes;
  fork copies it. Existing sandboxes are unowned.
- A rule has one scope: environment, sandbox, or persona. A persona rule applies only to
  sandboxes that persona owns. Precedence: environment deny (a fence), then sandbox, then
  persona, then environment rules. The gateway looks the owner up in the catalog for each
  connection, so a sandbox can't claim another persona.
- An approval can be decided with scope `persona`; that fails for an unowned sandbox.
