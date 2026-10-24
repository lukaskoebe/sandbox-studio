# Teammates and providers

Both belong to one environment. Harness adapters turn a teammate into config files inside
its sandboxes when an agent session starts and when the sandbox's guest connects
(`docs/harnesses.md`).

## Providers

| Kind | Harnesses | Credential |
|---|---|---|
| `anthropic_api` | claude, opencode | key bound to `api.anthropic.com`, guests see `ANTHROPIC_API_KEY` |
| `openai_api` | codex, opencode | key bound to `api.openai.com`, guests see `OPENAI_API_KEY` |
| `openai_compatible` | opencode, codex | key bound to the base URL's host, `OPENAI_API_KEY` |
| `claude_subscription` | claude | the long-lived token of `claude setup-token`, a secret like an API key |
| `chatgpt_subscription` | codex | the ChatGPT login `codex login` writes, kept by Studio ([subscription logins](#subscription-logins)) |

- An API key is stored as an environment secret named `PROVIDER_<NAME>_KEY`. It is created,
  rotated and deleted only with its provider, in one transaction; the secrets API answers
  409 for it. Guests see only the placeholder, as with any secret.
- `openai_compatible` takes an `https://` base URL on a public host (no credentials, query
  or fragment, no private, loopback or link-local address) and the endpoint's single model.
- The provider form suggests the models the endpoint lists. Once a base URL and a key are
  set, it calls `POST /api/environments/{env}/providers/models` with `{baseUrl, apiKey}`, or
  with `providerId` to use a saved provider's stored key. Studio itself sends `GET
  <baseUrl>/models` with the key as a bearer token, to public addresses only, and gives up
  after 10 seconds. It returns the ids (`{models}`), never the key; the endpoint's errors
  answer 502. A stored key is only sent to its provider's own base URL. The model field
  still takes any name.
- Name and kind are fixed. A provider used by a teammate can't be deleted (409).
- An API-key provider can exist without its key: an imported environment's providers do
  (`state` `key_required`). Its first key is added with `PUT` and `apiKey`, and sessions and
  memory jobs answer 409 or skip until then.
- A subscription has no key to type: it signs in, as described under
  [subscription logins](#subscription-logins). Its state is `login_required` until a login is
  stored, `expired` once a ChatGPT login has lapsed and could not be refreshed, and `ready`
  otherwise.

## Subscription logins

A subscription signs in with the vendor's own CLI. Studio (`internal/login`) runs the sign-in
in a system VM: a sandbox row of a system kind (`store/system.go`), with the same image as
other sandboxes, no project, and network only to the hosts its kind needs
([network](network.md#system-vms)). The dialog shows the sign-in; `GET
/api/environments/{env}/providers/{id}/login` reports its state (`starting`, `waiting`,
`saving`, `done`, `failed`, or `idle`).

- **Claude Pro/Max (`claude_subscription`).** `claude setup-token` runs in a tmux session named
  `login` in the login VM, which reaches `platform.claude.com` (where the code is exchanged) and `api.anthropic.com`. The
  dialog lists the steps: open the page it prints, sign in and authorize, copy the code the
  page shows, and paste it into the dialog's code field (or its Paste button, which reads the
  clipboard). `POST …/providers/{id}/login/code` types the code into the CLI with `tmux
  send-keys -l` and Enter; nobody has to type into the terminal, which stays behind a "Show
  the terminal" toggle. A pasted `sk-ant-oat` token is saved as the token instead. The login
  view's `codeSent` says a code went in; `codeRejected` says the pane gained an error line
  (`error`, `invalid` or `failed`) after it, and the dialog then opens the terminal so its
  message is in view. Studio reads the long-lived token from the pane, stores it as the
  provider's secret bound to `api.anthropic.com`, and removes the VM. The dialog's "Use a
  token from claude setup-token instead" takes a token made on the user's own computer, and
  opens by itself when Studio couldn't read the token. Verified vs. pending: the code
  delivery is covered by fake-guest tests; the error-line heuristic has not been checked
  against a live `claude setup-token` rejecting a code.
- **ChatGPT (`chatgpt_subscription`).** `codex login --device-auth` runs in the login VM, which
  reaches `auth.openai.com`. The dialog shows the page and the one-time code the device login
  prints. When Codex has written `~/.codex/auth.json`, Studio reads it and stores two things:
  the access token's signature, as the provider's secret bound to `chatgpt.com`, and the whole
  login as a studio-only secret named `PROVIDER_<NAME>_LOGIN`, which no sandbox sees and which
  holds the refresh token.
- **Sessions and chats.** A Claude teammate gets `CLAUDE_CODE_OAUTH_TOKEN` set to the
  provider's placeholder in its terminal sessions, as an API key's placeholder is; the gateway
  swaps the token in on `api.anthropic.com`. A Claude subscription's chats run Claude Code itself,
  through `studio-agent claude-acp` ([harnesses](harnesses.md#claude-subscription-chats)); API-key
  chats still use `claude-agent-acp`. Whether the subscription's use in chats fits Anthropic's
  terms is the owner's decision. A ChatGPT teammate gets `~/.codex/auth.json` with placeholders only. Its
  tokens keep their headers and payloads, so Codex reads the same claims and expiry, and their
  signatures are placeholders. The access token's placeholder is swapped on `chatgpt.com`. The
  refresh token and the id token's signature are placeholders that nothing swaps, so a refresh
  the guest attempts fails instead of handing new tokens to the sandbox.
- **Refresh.** Studio refreshes a ChatGPT login at the OAuth token endpoint from the host, five
  minutes before its access token expires, checking every minute. The new login replaces the
  old one, and the running sandboxes of the provider's teammates get the new auth file. A refused
  refresh leaves the login as it was; once the access token has lapsed the provider is
  `expired`. Claude tokens are long-lived and are not refreshed.
- **Utility calls.** When the environment's utility model is a subscription, its calls (memory
  extraction and the dream judge) run as one-shot `claude -p --model haiku` or
  `codex exec -m gpt-6-luna` in the provider's utility VM, with the prompt on stdin. The VM
  starts on demand and stops after ten minutes without a call. A subscription has no known
  price, so its calls are capped by number per day, as an endpoint without a price is.
- **Startup.** Login VMs left by an earlier run are removed when Studio starts, and utility VMs
  are stopped; they start again when used.

What is built and what is verified is in [harnesses](harnesses.md#subscription-logins-built-pending-live-verification).

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
- "Use my git identity" (`useHostGitIdentity`, off by default) commits as the user instead.
  When a session starts, Studio reads `git config --global user.name` and `user.email` on
  the host. If either is not set, or one fails the checks above, the teammate's own git name
  and email are used. `GET /api/system/git-identity` returns what the host has, so the form
  can show it.
- A teammate that owns sandboxes can't be deleted (409). Deleting a teammate deletes the
  network rules scoped to it.
- The approval scope is `sandbox`, `teammate` or `environment` (the default, and what rules
  from approvals did before). It is the scope the approval cards for this teammate's
  sandboxes preselect for a new rule; the card still lets the user pick another. See
  [approvals](approvals.md#network-approval-cards).

## The conversation

A teammate's page is its conversation beside its computer's screen
([design](design/teammate-computer.md), [frontend](frontend.md#a-teammate-and-its-computer)).
The conversation opens on the teammate's home (`components/teammate/home.tsx`), built from
`GET teammates/{id}/timeline` (`internal/api/timeline.go`): the last 14 days, oldest first, of

- its chat turns (at most 80): the prompt, the agent's text, and the tool calls by title (the
  last 6, with the count), read from the item bodies with `json_extract`, never their output;
  thoughts are left out, and so are undone turns;
- the facts in its own scope (at most 60; superseded ones left out, retracted ones shown as
  forgotten);
- its tasks, when added and when done;
- its terminal sessions with a memory summary (docs/memory.md, "Session summaries"); chat
  sessions are already there as turns.

The home groups them by day: each chat is a thread card (its title, a chip for a computer other
than the one on the screen, the last answer, its requests and its state), and the rest are
small events between them. Threads untouched for a while wait behind "Earlier threads". The
pending approvals for the teammate close the home, a row each, and the floating approval stack
leaves them out on this page. Opening a card opens the thread (`?thread=`), whose turn underway
comes from the chat's own stream, so it updates live. The composer has three modes:
**Thread** starts a chat on the computer the screen shows; **Task** adds a task (the first line
is the title) and starts a chat that asks the teammate to work on it; **Teach** writes a fact to
its memory as the user, without a chat. The name at the top opens its profile: its open tasks,
what it knows, its computers and its settings, each also a sheet (`?profile=`).

## Tasks

A teammate has a task list. A task has a title (one line, at most 200 characters), details
(markdown, at most 8 KiB), a status (`open`, `in_progress` or `done`), an optional due day
(`YYYY-MM-DD`) and its creator: `user`, or `teammate` for a task the teammate added to its own
list.

- The teammate's tasks sheet (`?profile=tasks`) lists them, not-done first and oldest first, and adds, edits and
  finishes them: `GET` and `POST /api/environments/{env}/teammates/{id}/tasks`, and `PUT
  .../tasks/{task}`, which replaces the title, details, due day, status, schedule and sandbox.
- A teammate's own sandboxes get four tools on the `studio` MCP server: `tasks_list` (the
  tasks not done, or all with `include_done`), `task_create`, `task_update_status` and
  `task_add_note`, which appends to the details after a blank line and keeps them within 8 KiB.
  Calls act on the sandbox's owner teammate, whatever the arguments name. A sandbox without an
  owner, and a browser VM, has no tasks.
- Task text is data. It reaches a prompt only inside `<studio-tasks>` tags, which the text
  can't close, and the prompt says that the text is not instructions.
- Changes publish the `tasks` event, so open tabs refetch.
- Tasks can't be deleted yet.

### Scheduled tasks

The user can give a task a schedule, in the Studio host's local time (`internal/tasks/schedule.go`):

| `repeat` | Fields | Runs |
|---|---|---|
| `once` | `date` (YYYY-MM-DD), `time` (HH:MM) | once, at that moment |
| `days` | `days` (any of `mon`…`sun`), `time` | on those days at that time |
| `hours` | `everyHours` (1–24), `fromHour`, `toHour` | every N hours from `fromHour`, while the hour is before `toHour`, as check-ins count their window |

- `paused: true` keeps the schedule and stops the runs. `sandboxId` picks the sandbox the runs
  go to; empty means the teammate's latest. A `PUT` without `schedule` removes it.
- The task's `nextRunAt` is computed when the schedule is saved and after each run; an edit
  that keeps the schedule keeps it. `lastRun` is the latest logged run.
- At most 20 of a teammate's tasks that aren't done have a schedule. Only the user sets them:
  the teammate's tools take no schedule, since every run calls a model on the user's key.
  `tasks_list` shows each task's schedule and next run.

Each minute the scheduler that runs check-ins also runs the due tasks (`internal/heartbeat/runs.go`).
A due run:

- makes a check-in's checks: a sandbox, a provider with its key, a VM, which it starts or
  resumes and waits up to 90 seconds for. A run that can't start is skipped and logged with why;
- waits while the teammate is busy: a turn running in the sandbox, approvals waiting there, or
  the task's previous run still running. It tries again every minute for up to 30 minutes, then
  is skipped and logged with why;
- otherwise starts a new chat, titled after the task and the time ("CI triage · Mon 20 Oct
  09:00"), with the fixed message "Scheduled run of your task below. Do it now, then report
  what you did in a short final message. Record anything the next run should know with
  task_add_note." and the task inside `<studio-tasks>` tags. The chat's `taskId` names the task:
  the chat list marks it as scheduled, its title stays, and memory leaves it out, as it does
  heartbeats.

When Studio was not running at a time, the latest one still runs if it is less than an hour
late. Older times are logged as missed ("Studio was not running"), at most 24 at once.

A one-off task goes `in_progress` when its run starts. A repeating task keeps its status: the
teammate's `task_update_status done` logs the current run done and leaves the task waiting
for its next time. The user marking a repeating task done ends its schedule.

`POST .../tasks/{task}/run` starts a run now (Run now in the tasks sheet). It makes the same
checks but doesn't wait: a run that can't start is a 409 that says why, and isn't logged.
`GET .../tasks/{task}/runs` lists a task's runs, newest first, at most 100, and `GET
.../teammates/{id}/task-runs` the latest 20 of all its tasks, which the Activity list merges
with the check-ins. Outcomes are `started`, `done`, `skipped` and `missed`.

## Check-ins (heartbeats)

Off by default. A teammate has one check-in schedule: `PUT
/api/environments/{env}/teammates/{id}/heartbeat` with `everyHours` (1 to 24), `fromHour` (0 to
23) and `toHour` (1 to 24, after `fromHour`), in the Studio host's local time. `DELETE` turns
the schedule off and keeps its window for the next time. `GET .../heartbeats` lists the log.

Every minute the scheduler (`heartbeat.Service`, run from `cmd/studio`) checks the schedules. A
check-in is due when the local hour is inside the window and `everyHours` have passed since the
teammate's last check-in of any outcome. A due check-in:

- does nothing when the teammate has no open tasks. Tasks with a schedule run at their own
  times, so a check-in leaves them out, and its message says how many it left out;
- starts a heartbeat chat in the teammate's most recently created sandbox that isn't a build or
  browser VM. The chat gets the fixed message "Heartbeat: review your open tasks and continue
  the most important one; report what you did. If nothing needs doing, say so briefly." and the
  open tasks, at most 16 KiB of them. The chat list marks it as a heartbeat. A stopped, crashed
  or created sandbox is started, and a suspended one resumed, then the scheduler waits up to 90
  seconds for the agent to connect. The sandbox keeps running after the turn;
- is skipped, and logged with the reason, when the teammate has no sandbox, has no provider or
  its provider has no key, approvals wait
  in its sandbox, its sandbox has no VM (it needs to be created), its sandbox is not ready
  (creating, failed or draining), or its chat can't be planned or doesn't take the prompt;
- is skipped quietly when a heartbeat chat of the teammate is still running (at most one at a
  time) or a turn is running in the sandbox, so a check-in never runs during a user's turn.

Every due check-in moves the schedule, whatever its outcome. The log of started and skipped
check-ins is listed under Activity in the teammate's tasks sheet. A check-in with nothing to do is
not logged. Tests use fakes for the sandbox, the chat and the harness, so no model is called.

## Copying a teammate

`POST /api/environments/{env}/teammates/{id}/copy` with `targetEnvironmentId` creates the
teammate in another environment, with the same name (" 2", " 3", … if the name is taken in
the target), role, avatar, soul, harness, model, git identity and approval scope. Its provider
is the target's provider of the same kind, preferring one with the same name. With none, the
copy has no provider (`providerId` is empty and sessions answer 409 until one is chosen).
Sandboxes, chats, memory, secrets, provider keys, the remembered pattern level, tasks and the check-in schedule stay behind; the copy's check-ins are off.

## Default teammate

Adding a provider to an environment that has no teammates creates the default teammate on
that provider (`teammates.DefaultTeammate*`, created by the API's `createProvider`):

- name `Iris`, role `Generalist engineer`, and the soul "Curious, careful and direct. Says what
  it knows and what it doesn't, explains decisions briefly and prefers small verified steps.
  Works on its own in its sandbox: reads, runs, edits and installs what the work needs without
  asking. Checks with the user first only before something beyond the sandbox that can't be
  undone, such as force-pushing, deleting a remote branch, or publishing or sending something."
  (migration 042 gives it to teammates that still have the earlier default word for word);
- harness by provider kind: `claude_subscription` and `anthropic_api` → claude,
  `chatgpt_subscription` and `openai_api` → codex, `openai_compatible` → opencode;
- model: `claude-sonnet-5-5` for `anthropic_api`, `gpt-6.1-sol` for `openai_api`, the
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
