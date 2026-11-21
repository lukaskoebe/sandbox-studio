---
title: Lead
description: Read the day your teammates had, ask about it, have Lead message teammates once you approve, and give it standing orders.
---

Lead is a teammate without a computer. At the end of each weekday it writes up the day in the environment: what waits for you, what each teammate finished, still has open or is stuck on, what shipped, and what it cost. You can also ask it about a day, have it make a plan: messages to teammates that go out only once you approve them, and give it standing orders: plans it drafts on its own at a set time. Every number, state and link on the page comes from Studio's own records; a model only writes the sentences about them.

Lead never sends anything you haven't approved, except follow-ups in threads its own plan started if you allow those. It doesn't start or stop work on its own, and it doesn't answer a teammate's requests for you.

## Read the day

Lead is at the top of **Teammates** in the sidebar. It opens on today:

- The caption says when the day was written, whether you asked for it, the model and what it cost.
- A short summary, then four numbers: what waits for you, pushes, turn time and cost.
- **Needs you** says how many requests wait now, with a link to Home, where you answer them; the top bar lists them too. A past day shows what waited then.
- Each teammate's lines say what it finished, has open or is blocked on, each linking to the thread.
- Beside them: **Shipped** (pushes and pull requests), **Blockers** (a failed push, denied requests, a command that keeps failing, a nearly full disk, a scheduled run that was missed), **Costs** and **Tomorrow**, a few suggestions. Lead never acts on them.

The arrows next to the date go to the days before and after. **Copy as Markdown** copies the day, to paste into a note or a chat.

Before the day is written, the page shows today's facts so far. **Summarize now** writes the day right away; at the set time Lead writes it again with the rest of the day. If Studio wasn't running then, it writes the day when it starts, up to the day before.

Home shows **Today so far** under what needs you once a teammate has worked: each teammate's note, or the summary once the day is written, with **Open the day**. When something happened since you last looked at today, a line says what: who worked, pushes, new requests and scheduled runs.

## Ask Lead

The box under a day asks Lead about it, such as "What did Ada get done?" or "Why is the checkout fix stuck?". Lead answers in a few sentences from that day's facts and notes, what it wrote about the day, the days before and your questions so far, linking the threads, pushes and requests it speaks of. It answers one question at a time, and the questions and answers stay with the day.

Asking only reads: Lead doesn't pass your question on to a teammate. To have it ask them, make a plan. Questions count against the daily limit, and leave part of it for writing the day.

## Plans

A plan is something you want from your teammates, which Lead turns into messages for you to approve. Start one with **New plan** under Lead in the sidebar, or **Make a plan** next to one of a day's suggestions for tomorrow, and say what you want, such as "Ask Ada whether the coupon tests pass".

Lead answers in the plan's conversation:

- When something is unclear, it asks back, often with answers to pick.
- Otherwise it proposes the steps, knowing who is busy and which open pull requests have failing checks: a message in a teammate's thread, or a new thread on a teammate's computer, each with the exact text. Then it waits for their answers and tells you what they said.

Nothing goes out until you click **Approve and send**, on the plan or on its card on Home. The card shows the messages too. To change something, tell Lead in the box under the plan; it writes the plan again, and you approve the new one. **Don't send** or **Cancel plan** ends it.

Once approved, Studio sends each message as it is. A message to a thread that is busy waits for its turn to end, and a computer that's off is started. In the thread, the message shows as Lead's, approved by you, with a link back to the plan. When the teammates have answered, Lead writes what came of it, with links to their threads. Its open plans are listed under Lead in the sidebar.

A teammate Lead messages still asks you for pushes, pull requests and network requests as before. Lead can't steer or stop a teammate's work, or start a scheduled task's run. Plans count against the daily limit, as questions do.

### Follow-ups

When an answer leaves your request unfinished, Lead can follow up: one more message in the thread, such as asking for the slow tests too. A plan follows up at most twice, never just to thank or ask for status. A follow-up shows under the report as **Follow-up 1** and waits for you to **Approve and send** it, like the plan's first messages.

In Lead's settings, **Message teammates** can let follow-ups go without asking, but only in threads the plan itself started. A follow-up in a teammate's own thread still waits for you. The thread says "Lead, follow-up without asking" on such a message.

## Standing orders

A standing order is something you'd ask Lead for again and again, such as "Each morning, hand failing checks on open pull requests to whoever is idle". **Standing orders** under Lead in the sidebar lists them: add one with your words, a time and the days. When it comes due, Lead drafts the plan from your words, and it waits for your approval like any other plan; the plan says it came from your order. When there's nothing to do, the plan just says so.

- **Run now** drafts the plan right away, even when the order is off.
- An order skips a day while its plan from last time is still open, and says so.
- If Studio wasn't running at the time, Lead drafts it once Studio runs, that same day only.
- A done plan you made yourself offers **make this one a standing order**, with its words filled in.

An environment has up to 20 standing orders.

## Settings

The gear next to **Summarize now** opens Lead's settings:

- **Name**: what the sidebar and the day call it.
- **Write the day at** and **On**: 18:00 on weekdays to start with, in Studio's time zone.
- **Skip days without turns**: no page on a day nobody worked.
- **Include your own computers**: off to start with, so Lead reads only your teammates' work.
- **Show costs** and **A heading for the day**.
- **Daily limit**: what Lead's model calls may cost a day, $1 to start with. A model without a price, such as a subscription's, stops at 60 calls a day instead. The notes through the day and your questions use at most part of it, so the day itself still gets written. When it runs out, the day keeps its facts and links but has no sentences.
- **When the day is written**: notify you only when something waits for you, every time, or never.
- **Message teammates**: when you approve, always, or let follow-ups in threads Lead's plan started go without asking.

## The model

Lead calls the small, cheap model of your first teammate's AI provider that has one, the one memory uses. Through the day it keeps a short note of each teammate after its turns, and at the set time it writes the day from those notes and the facts. Without such a model, the day still has its facts and links.

## In the terminal

In [`studio tui`](../tui/), Lead is the first row under **Quiet** on Home once a teammate has worked today. The reading pane shows the day, and `w` writes it now. Asking Lead, making plans and standing orders are in the web app for now. A plan or a follow-up waiting for approval shows in the inbox with its messages, and Enter opens it in the browser.
