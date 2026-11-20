# Lead

Lead is a teammate without a computer that writes up each environment's day: what waits for
the user, what each teammate finished, has open or is blocked on, what shipped, and what it
cost. Studio fills in every number, state and link from its own records; the environment's
utility model only writes the sentences. Lead is read-only: it has no tool that acts on a
teammate, a computer or an approval.

Code: `internal/lead` (the service, facts, prompts and views), `internal/store/lead.go` and
`067_lead.sql`, `internal/api/lead.go`; the web's `components/lead/` and `lib/lead.ts`; the
TUI's `internal/tui/lead.go`. Design: the meta-agent concept and mocks (phases 2–4, Ask Lead,
plans and standing orders, are still to come).

## Data

Days are local dates (`YYYY-MM-DD`) in Studio's time zone, since the day is the user's.

- `lead_settings`, one row per environment, made on first read: the name (Lead), when the day
  is written (`write_at`, minutes after midnight, 18:00) and on which days (`days`, a bit per
  weekday, Monday bit 0, weekdays by default), `skip_quiet` (no day without turns),
  `own_computers` (the user's own computers are part of the day, off), `show_costs`,
  `heading`, `daily_limit` (USD × 10^6, $1) and `notify` (`waiting`, `always`, `never`).
- `lead_days`: a written day: when and why (`schedule` or `now`), the model and what its call
  cost, the facts (JSON), the sentences (JSON), a note when it has none, and a digest of what
  the sentences were written from.
- `lead_notes`: Lead's short note of each teammate's day, and the newest summary or turn it
  has taken in. No foreign key to teammates, so a day keeps the notes of a teammate deleted
  since. The user's own computers have no note; the day has their facts only.
- `lead_usage`: each day's calls, tokens and cost.

## How a day is written

`Service.Run` ticks every minute. Before `write_at` on a day it is written on, a tick folds
what is new into the notes; then it writes the day that is due: today once past `write_at`,
else yesterday if Studio wasn't running at its time (one day back, no further). A day is due
when it isn't written, or was written before its time (Summarize now earlier on). A day
without turns is looked at once per `write_at` and skipped when `skip_quiet` is on. A
scheduled write that fails for a reason that may pass (not a missing model or the limit)
tries again every 15 minutes, three times, and its note says when. **Summarize now**
(`POST …/lead/summarize`) writes today's day at once, in the background; a second one while a
write runs is 409. A write has five minutes, its folds two, and the day is saved even when the
request that asked for it is gone.

1. **Fold.** Each teammate's note takes in memory's session summaries and the digests of the
   turns that ended since its last fold (the prompt, the answer and the tool calls' titles),
   at most every 15 minutes, and always before today's day is written. A fold reads at most
   100 turns per teammate and sends the newest 30. Without a model, an empty note takes
   memory's latest summary as it is.
2. **Facts.** `gather` reads the day from Studio's records: each teammate's threads with turns
   that day (turn count and time, state, cost by the agent's reported session cost), pushes and
   pull requests, scheduled task runs, blockers (a thread that stopped early, a task run that
   was missed or skipped, a failed push, denied requests, commands that failed again and
   again, a nearly full disk) and what waits for the user now. Each item gets a ref,
   such as `push:2`, in the order the page shows them.
3. **Sentences.** One call to the utility model with the facts and the notes, as JSON between
   tags, asks for a heading, a two or three sentence summary, up to five lines per teammate
   (done, open or blocked) and up to three suggestions for tomorrow. `parseProse` drops unknown
   teammates and refs, checks the states, takes refs out of the heading and cuts every text to
   one line of its size. Credential-like strings are redacted from what the model reads and
   writes. A day whose facts and notes match the digest keeps its sentences without a call.

Without a model, over the limit or after a failed call, the day is saved with its facts and a
note saying why. The Markdown escapes every text that isn't Studio's own (the sentences,
names, notes, thread titles, branches), so a link or picture in one stays text; the TUI
unescapes it to show.

## The model and its budget

Lead calls the utility model of the first teammate's provider that has one, as memory does
(`agentmem.EnvironmentUtility`). Every call is counted in `lead_usage`. A priced model stops
when the estimated next call would pass `daily_limit`; an unpriced one after 60 calls a day.
The notes keep the rest for the day itself: folds stop at 60% of the limit, or 40 calls
unpriced. Calls are counted one at a time, so a fold and Summarize now can't both pass the
same check. The provider's key never appears in an error.

## API

Under `/api/environments/{env}/lead`:

- `GET` is Lead: settings, today so far (notes, counts, and once written its summary with the
  refs written out as names), the written days newest first, whether it is writing, today's
  usage, the model, and Studio's time zone and offset.
- `GET /days/{day}` is one day's page: written or, for today, the facts so far, with the
  sentences, the cost of Lead's calls, the day as Markdown and the written days before and
  after it. Facts that hide costs (`show_costs` off) carry none.
- `POST /summarize` writes today's day now (202, 409 while writing).
- `PUT /settings` replaces the settings.

The `lead` event topic says when a day began or finished being written, or the settings
changed; the web invalidates Lead's queries and the day's page on it and on the approvals and
chats topics.

## Web

- The sidebar has Lead at the top of Teammates, with "writing" or when today was written; on
  Lead's pages the last days it wrote are listed under it.
- `/e/$env/lead` is today, by Studio's date (`today.day`), not the browser's; `/lead/$day` a
  written day, `/lead/settings` its settings, saved one after another. A day:
  the caption (written when, on request or not, the model, the cost), the summary, four
  numbers (today's waiting count is the live one, as the cards below), what waits for you
  (today's live approval cards; a past day's snapshot), each
  teammate's lines with links to its threads, and beside them what shipped, blockers, costs
  and suggestions for tomorrow. **Copy as Markdown** copies the day.
- Home's **Today so far** card shows the notes or, once written, the summary, with Summarize
  now and Open the day.
- `useLeadNotify` notifies when today's day is written while the page is open, as the
  `notify` setting says.

## TUI

Home lists Lead first under QUIET once a teammate worked today or the day is written: its
summary and when it was written, or today's counts so far. The reading pane is the day as
Markdown; `w` writes it now.
