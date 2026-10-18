# Scheduled tasks (design)

Status: proposed 2026-10-18.

A task on a teammate's list can get a schedule. At the scheduled time, the teammate works on
that task in a chat of its own: once, on a date and time, or again and again (on some days at
a time, or every few hours). Examples: "every weekday at 09:00, read the CI failures of the
night and open a fix", "on 2026-10-24 at 14:00, check that the release went out", "every 4
hours from 8 to 20, update the dependency report".

## 1. What exists

- **Tasks** ([teammates](../teammates.md#tasks)).
  - Fields: a title, details (8 KiB of markdown), a status (`open`, `in_progress`, `done`),
    a due day, and who created it.
  - The teammate reads and changes its own tasks with `tasks_list`, `task_create`,
    `task_update_status` and `task_add_note`.
  - A due day is a reminder only: nothing runs on it.
- **Check-ins (heartbeats)** ([teammates](../teammates.md#check-ins-heartbeats)).
  - Each teammate has one schedule: every N hours, inside a window of hours.
  - When one is due, `heartbeat.Service` starts a chat in the teammate's latest sandbox,
    with every open task in the prompt. It starts or resumes that sandbox first.
  - It skips the check-in, and logs why, when there's no provider, approvals wait, and so on.
  - It skips quietly while a heartbeat or a user turn runs.

A check-in asks "is anything worth doing?" and lets the teammate pick. A scheduled task says
what to do and when. The two share the hard part: a ready sandbox, a planned chat, the skips.
So scheduled tasks reuse the check-in machinery, and they are not a second kind of object.

## 2. The schedule

A task has no schedule, or exactly one, in the Studio host's local time, like check-ins:

| `repeat` | Fields | Runs |
|---|---|---|
| `once` | `date` (YYYY-MM-DD), `time` (HH:MM) | once, at that moment |
| `days` | `days` (any of `mon`…`sun`), `time` | on those days at that time; all seven days is "daily" |
| `hours` | `everyHours` (1–24), `fromHour`, `toHour` | every N hours inside the window, counted from `fromHour` |

`paused: true` keeps the schedule and stops the runs. The web writes the sentence out: "Every
weekday at 09:00", "Once, Fri 24 Oct at 14:00", "Every 4 hours, 08:00–20:00".

A cron expression was considered. It's more flexible, but harder to read and to validate, and
these three forms cover the examples. The stored form leaves room for a `cron` repeat later.

## 3. A run

When a run is due, the scheduler:

1. **Checks the teammate**, the same way a check-in does: a provider with a key, a sandbox
   (the task's `sandboxId` if it has one, otherwise the latest), a VM. It starts or resumes the
   sandbox and waits up to 90 seconds for the agent.
2. **Waits instead of skipping** when it's busy: a user turn running in the sandbox, approvals
   waiting there, or the task's previous run still running. A scheduled time is a promise, so
   the scheduler tries again every minute for up to 30 minutes. Then it skips the run and logs
   why.
3. **Starts a new chat for the run.** The chat is titled after the task and the run's time
   ("CI triage · Mon 20 Oct 09:00"), and the chat list marks it as scheduled. Its prompt:
   - The fixed request: "Scheduled run of your task below. Do it now, then report what you
     did in a short final message. Record anything the next run should know with
     task_add_note."
   - The task, inside `<studio-tasks>` tags, fenced as check-ins are. The task's text is
     data.
4. **Logs the run**: started, done, skipped (with the reason) or missed, with the chat's ID.

Each run gets a new chat, so it starts with a clean context. What carries over from run to
run is the task's details (its notes) and the teammate's memory.

**Status.**

- A one-off task goes `in_progress` when its run starts. The teammate marks it `done` as
  usual.
- A repeating task stays `open`. When the teammate calls `task_update_status done` on it, only
  the current run is done: the run is logged done, and the task waits for its next time.
- When the user sets a repeating task to `done`, its schedule ends.

**Missed runs.** Studio may have been off at the scheduled time. If it starts again less than an
hour late, the latest missed time still runs. Older misses are logged as missed ("Studio was
not running"), and the schedule carries on. A paused schedule logs nothing.

**Check-ins leave scheduled tasks out.** A task with a schedule runs at its own times. A
check-in that also took it would run it twice. Its prompt says how many scheduled tasks there
are, and that they aren't its job.

## 4. Who may schedule

- **The user** sets, pauses and removes schedules: on the Tasks tab, and via the API.
- **The teammate can't.** In this version, `task_create` and the other tools take no schedule.
  Every run calls a model on the user's key, so an agent that could schedule itself could
  spend without limit. `tasks_list` shows each task's schedule and its next run, so the
  teammate knows the plan.
- **Later:** a teammate could propose a schedule as an approval, the way a memory share works.

**Bounds.**

- At most 20 scheduled tasks per teammate.
- `hours` runs at most once an hour.
- One run per task at a time.
- Each run starts one turn. Nothing loops by itself.

## 5. API and store

- `Task` gains:
  - `schedule` (the object above, or absent);
  - `sandboxId` (optional; empty means the latest sandbox);
  - read-only `nextRunAt` and `lastRun`: `{at, outcome, reason, chatId}`.
- `POST` and `PUT .../tasks` take `schedule` and `sandboxId`. Omitting `schedule` on a `PUT`
  removes it, since a `PUT` replaces the task's editable fields.
- `POST .../tasks/{task}/run` starts a run now, for "Run now". It obeys the same checks, but
  doesn't wait: busy is a 409 that says why.
- `GET .../tasks/{task}/runs` returns the task's run log, newest first, at most 100 runs.
- **Migration 040.**
  - `teammate_tasks` gains `schedule` (JSON text), `sandbox_id`, `next_run_at`.
  - A new table `task_runs`: `id`, `environment_id`, `teammate_id`, `task_id`, `due_at`, `at`,
    `outcome`, `reason`, `chat_id`.
  - `chats` gains `task_id`, which marks a scheduled run's chat as `heartbeat` marks a
    check-in's. Memory leaves those chats out, as it does heartbeats.
- `next_run_at` is computed when the schedule is saved and after each run. Each minute, the
  scheduler reads the tasks with `next_run_at <= now` and an unpaused schedule. It's
  `heartbeat.Service`, which now beats check-ins and runs tasks, sharing `ready`, the provider
  checks and the plan.
- Changes publish the `tasks` event, as now.

## 6. The web

- **The task dialog** gets a Schedule field: Off, Once, Days or Every N hours. Each choice shows
  its own inputs: a date, toggles for the weekdays, an hour window. A Sandbox picker defaults
  to "Latest sandbox".
- **The task list** shows a scheduled task's sentence and next run ("⟳ Weekdays 09:00 · next
  Mon 09:00"), and its last run with its outcome. A Run now button sits next to Edit.
- **The Activity list** on the Tasks tab already shows check-ins. It merges in the task runs:
  a started run links its chat, and a skipped or missed run says why.
- **The teammate's conversation** shows a run's turn like any chat turn, with a "Scheduled"
  marker.

## 7. The TUI

Home's chat rows ([tui-sessions](tui-sessions.md)) mark a scheduled run's chat with `⟳`. A run
that turns blocked or done is shown like any other session. The TUI has no task screens, so
schedules are set in the web.

## 8. Tests

- `Next` (the next run of a schedule after a time) gets table tests: DST changes, the end of
  a month, an `hours` window that ends at midnight, past `once` times.
- The scheduler gets tests with fakes for the clock, the sandbox, the chat and the harness, as
  check-ins do. They cover due, wait and then run, wait and then skip, missed, catch-up, a
  repeating task's done, and the check-in leaving scheduled tasks out.
- An e2e test of the web dialog: set a schedule, see the sentence and the next run, Run now.

No test calls a model.
