# Teammates and providers

Both belong to one environment. Harness adapters turn a teammate into config files inside
its sandboxes when an agent session starts (`docs/harnesses.md`).

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
- Name and kind are fixed. A provider used by a teammate can't be deleted (409).
- The subscription login flow (`claude setup-token` / `codex login` in a system sandbox) is
  a TODO.

## Teammates

- Name (unique in the environment), role, avatar, approval scope, soul (markdown, at most
  16 KiB), harness, provider, model, git name and email.
- The avatar is one emoji or one of the generated avatars, stored as `shape:<name>` (`circle`,
  `square`, `diamond`, `triangle`, `pentagon`, `hexagon`, `star`, `cross`), which the UI draws
  in the teammate's color. Empty shows the initials. Nothing is uploaded.
- The harness must be supported by the provider's kind. API-key providers need a model;
  `openai_compatible` defaults to the provider's model.
- Git name defaults to the name, git email to `<slug>@agents.invalid`. Both are one line
  without control characters, quotes or angle brackets, so they are safe in git config.
- A teammate that owns sandboxes can't be deleted (409). Deleting a teammate deletes the
  network rules scoped to it.
- The approval scope is `sandbox`, `teammate` or `environment` (the default, and what rules
  from approvals did before). It is the scope the approval cards for this teammate's
  sandboxes preselect for a new rule; the card still lets the user pick another. See
  [approvals](approvals.md#network-approval-cards).

## Default teammate

Adding a provider to an environment that has no teammates creates the default teammate on
that provider (`teammates.DefaultTeammate*`, created by the API's `createProvider`):

- name `Iris`, role `Generalist engineer`, and the soul "Curious, careful and direct. Says what
  it knows and what it doesn't, explains decisions briefly, prefers small verified steps, and
  asks before anything risky or hard to undo.";
- harness by provider kind: `claude_subscription` and `anthropic_api` → claude,
  `chatgpt_subscription` and `openai_api` → codex, `openai_compatible` → opencode;
- model: `claude-sonnet-5-5` for `anthropic_api`, `gpt-sol-6.1` for `openai_api`, the
  endpoint's model for `openai_compatible`, and none for subscriptions (the subscription's
  default);
- git identity `Iris (agent) <iris@agents.invalid>`.

Afterwards the default is an ordinary teammate that the user can edit or delete. A provider
added while the environment has a teammate creates nothing, and one added after the last
teammate was deleted creates Iris again. If the default can't be created, the provider is kept, the
failure is logged, and the teammate can be added by hand.

## Ownership and rules

- A sandbox can be created with a `teammateId` (base or template). Ownership never changes;
  fork copies it. Existing sandboxes are unowned.
- A rule has one scope: environment, sandbox, or teammate. A teammate rule applies only to
  sandboxes that teammate owns. Precedence: environment deny (a fence), then sandbox, then
  teammate, then environment rules. The gateway looks the owner up in the catalog for each
  connection, so a sandbox can't claim another teammate.
- An approval can be decided with scope `teammate`; that fails for an unowned sandbox.
