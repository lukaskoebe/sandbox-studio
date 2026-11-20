# Lead

Lead is a teammate without a computer that writes up each environment's day: what waits for
the user, what each teammate finished, has open or is blocked on, what shipped, and what it
cost, and answers the user's questions about a day. Studio fills in every number, state and
link from its own records; the environment's utility model only writes the sentences. On the
user's request Lead also makes plans: messages to teammates that Studio sends only once the
user approved them as they stand. Beyond that it has no tool that acts on a teammate, a
computer or an approval.

Code: `internal/lead` (the service, facts, prompts and views; `ask.go` for questions,
`plan.go` for plans, `integration.go` for the plan's approval), `internal/store/lead.go`,
`067_lead.sql`, `068_lead_asks.sql` and `069_lead_plans.sql`, `internal/api/lead.go` and
`leadplans.go` (which sends the messages); the web's `components/lead/` and `lib/lead.ts`;
the TUI's `internal/tui/lead.go`. Design: the meta-agent concept and mocks (phase 4,
standing orders, is still to come).

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
- `lead_asks`: the user's questions about a day and Lead's answers: the state (`answering`,
  `answered`, `failed`), the answer, the facts its refs point at (JSON, pruned to them), a note
  when it has no answer, the model and cost. Questions left answering when Studio stopped are
  failed when it starts.
- `lead_seen`: when the user last looked at today, for Home's since you looked.
- `lead_plans`: a plan: its title, state (`drafting`, `asking`, `proposed`, `running`,
  `done`, `cancelled`, `failed`), its steps (JSON, each with its state), the `lead.plan`
  approval it waits on and when it was approved. `lead_plan_items` is its conversation:
  the user's messages (`you`), Lead's replies (`lead`, with a proposal's steps or a question's
  options), Lead's `report` and Studio's `note`s, each reply with its model and cost.

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

## Questions

`Ask` takes a question about a day that has come, one at a time per environment (409 while
one is answered), saves it as answering and answers in the background within 2 minutes. The
model gets the day's facts (stored for a written past day, live for today or an unwritten
one) and notes, what was written about it with refs as names, the summaries of up to 5 days
before, the 6 questions before this one with their answers (refs as names) and the question,
each in its tags and JSON-quoted, and the time now. It answers in at most 5 sentences and 3
paragraphs, plain text, citing refs. The answer is cleaned as the day's sentences are (refs
that aren't in the facts dropped, secrets redacted, 1200 characters at most), and its refs
keep the facts they point at, so an answer still links after the day moves on. Without a
model or over the limit the question is saved failed, with a note saying why.

## Plans

The user asks Lead for something that takes teammates (`NewPlan`, from New plan under Lead
or Make a plan on a day's suggestion). Lead drafts a plan in the background within 2 minutes,
from a roster of the environment's teammates (not the user's own computers), their computers
and their 30 latest threads (title, whether a turn runs, the last prompt), and the plan's
conversation so far. It answers with a reply and either a question with up to 4 answers to
pick, or up to 6 steps: a **message** to a thread, or **start** a new thread on a teammate's
computer, each with its text. `parsePlan` keeps only refs from the roster, so Lead can't
write to a thread or computer the roster doesn't name. Studio adds two steps of its own:
**wait** for the teammates to answer and **tell** the user what they said.

A plan with steps is `proposed` and waits on one `lead.plan` approval, whose payload has the
messages as they stand and a digest of them. Every message the user sends to the plan
(`PlanMessage`) withdraws that approval and has Lead write the plan again, so an approval only
ever allows what the user saw. Allowing it (on the plan page, its card or Home) runs
`Approve`, which checks the digest; denying or dismissing it cancels the plan with nothing
sent. **Cancel** cancels at any point; messages already sent stay.

Approved, the plan runs its steps in order. A message goes in with `chat.Queue`, so it waits
behind a turn that runs, and a start makes a titled thread; both start or resume the
computer if it's off. The user's item carries `lead` (the name, plan and approval time), so
the thread shows the message as Lead's, approved by the user. **Wait** polls every 10 s until
each thread has finished the turn Lead's message started, for at most 6 hours. **Tell**
gives the model each thread's answer (its first 1500 characters) and has it write the report,
with links to the threads; without a model, the report lists the answers as they are. A plan
whose messages all failed is `failed`, else `done`. When Studio starts, a plan left drafting
asks the user to send their message again, and a running one carries on (a message that was
being sent is marked failed rather than sent twice).

Lead still only messages teammates: a teammate it messages asks the user for pushes, pull
requests and requests as before, and steering, stopping or a task's run aren't plan steps.

## The model and its budget

Lead calls the utility model of the first teammate's provider that has one, as memory does
(`agentmem.EnvironmentUtility`). Every call is counted in `lead_usage`. A priced model stops
when the estimated next call would pass `daily_limit`; an unpriced one after 60 calls a day.
The notes and questions keep the rest for the day itself: folds stop at 60% of the limit, or
40 calls unpriced, and questions and plans at 80%, or 50 calls. Calls are counted one at a time, so a fold and Summarize now can't both pass the
same check. The provider's key never appears in an error.

## API

Under `/api/environments/{env}/lead`:

- `GET` is Lead: settings, today so far (notes, counts, and once written its summary with the
  refs written out as names), the written days newest first, whether it is writing, today's
  usage, the model, and Studio's time zone and offset.
- `GET /days/{day}` is one day's page: written or, for today, the facts so far, with the
  sentences, the cost of Lead's calls, the day as Markdown and the written days before and
  after it. Facts that hide costs (`show_costs` off) carry none.
- `GET /days/{day}/asks` is the questions about a day, oldest first; `POST` asks one (202 with
  it answering, 409 while another is, 422 for a day that hasn't come).
- `POST /seen` records that the user looked at today. `GET`'s `today.since` counts what
  happened since then (teammates with a turn, threads, pushes and pull requests, requests
  that began to wait, task runs), when it was today.
- `POST /summarize` writes today's day now (202, 409 while writing).
- `PUT /settings` replaces the settings.
- `GET /plans` is the 30 latest plans; `POST /plans` makes one from the user's message (202,
  drafting). `GET /plans/{plan}` is a plan with its conversation; `POST /plans/{plan}/messages`
  sends it another message (409 while Lead drafts or carries it out, or once it is closed),
  and `POST /plans/{plan}/cancel` cancels it. A pending approval of kind `lead.plan` carries
  `leadPlan`: the plan, its title, Lead's name and the messages.

The `lead` event topic says when a day began or finished being written, a question was asked
or answered, a plan changed (its ID is `plan:` and the plan's), or the settings changed; the web invalidates Lead's queries and the day's page on it and on the approvals and
chats topics.

## Web

- The sidebar has Lead at the top of Teammates, with "writing" or when today was written, and
  its open plans under it with their state; on Lead's pages New plan and the last days it
  wrote are listed under it.
- `/e/$env/lead` is today, by Studio's date (`today.day`), not the browser's; `/lead/$day` a
  written day, `/lead/settings` its settings, saved one after another. A day:
  the caption (written when, on request or not, the model, the cost), the summary, four
  numbers (today's waiting count is the live one, as the cards below), what waits for you
  (today's live approval cards; a past day's snapshot), each
  teammate's lines with links to its threads, and beside them what shipped, blockers, costs
  and suggestions for tomorrow. **Copy as Markdown** copies the day.
- Under the day, the questions asked about it with Lead's answers (`ask.tsx`), and a box to
  ask another (Enter sends; one at a time). Opening and leaving today records the look.
- Home's **Today so far** card shows the notes or, once written, the summary, with Summarize
  now and Open the day, and "Since you looked at …" when something happened since. It keeps
  what it showed while Home is open, and records the look.
- `/lead/plans/new` (with an optional `text`) asks Lead for a plan; `/lead/plans/$plan` is the
  plan's conversation: the user's messages, Lead's replies with the answers to pick, the steps
  with Approve and send and their states once approved, notes, and the report with links to
  the threads, with a box for a change or another message at the bottom. The plan page claims
  its approval, so the floating stack leaves it out there; elsewhere the stack and Home show
  its card (`plan-approval.tsx`) with the messages and Approve and send.
- Each of a day's suggestions for tomorrow has **Make a plan**, which opens a new plan with
  the suggestion as its text. A thread shows a message from a plan with "Lead, approved by you
  at …" and a link to the plan (`FromLead` in `chat-items.tsx`).
- `useLeadNotify` notifies when today's day is written while the page is open, as the
  `notify` setting says.

## TUI

Home lists Lead first under QUIET once a teammate worked today or the day is written: its
summary and when it was written, or today's counts so far. The reading pane is the day as
Markdown; `w` writes it now. Questions and plans are web only for now; a plan's approval
shows in the inbox with what goes to whom, and enter opens the plan in the browser.
