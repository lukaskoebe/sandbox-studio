---
title: Lead
description: Read the day your teammates had, written up at the end of it.
---

Lead is a teammate without a computer. At the end of each weekday it writes up the day in the environment: what waits for you, what each teammate finished, still has open or is stuck on, what shipped, and what it cost. Every number, state and link on the page comes from Studio's own records; a model only writes the sentences about them.

Lead only reads. It doesn't message a teammate, start or stop a computer, or answer a request.

## Read the day

Lead is at the top of **Teammates** in the sidebar. It opens on today:

- The caption says when the day was written, whether you asked for it, the model and what it cost.
- A short summary, then four numbers: what waits for you, pushes, turn time and cost.
- **Waiting on you** shows the requests waiting now, and you can answer them there. A past day shows what waited then.
- Each teammate's lines say what it finished, has open or is blocked on, each linking to the thread.
- Beside them: **Shipped** (pushes and pull requests), **Blockers** (a failed push, denied requests, a command that keeps failing, a nearly full disk, a scheduled run that was missed), **Costs** and **Tomorrow**, a few suggestions. Lead never acts on them.

The arrows next to the date go to the days before and after. **Copy as Markdown** copies the day, to paste into a note or a chat.

Before the day is written, the page shows today's facts so far. **Summarize now** writes the day right away; at the set time Lead writes it again with the rest of the day. If Studio wasn't running then, it writes the day when it starts, up to the day before.

Home shows **Today so far** under what needs you once a teammate has worked: each teammate's note, or the summary once the day is written, with **Open the day**.

## Settings

The gear next to **Summarize now** opens Lead's settings:

- **Name**: what the sidebar and the day call it.
- **Write the day at** and **On**: 18:00 on weekdays to start with, in Studio's time zone.
- **Skip days without turns**: no page on a day nobody worked.
- **Include your own computers**: off to start with, so Lead reads only your teammates' work.
- **Show costs** and **A heading for the day**.
- **Daily limit**: what Lead's model calls may cost a day, $1 to start with. A model without a price, such as a subscription's, stops at 60 calls a day instead. The notes through the day use at most part of it, so the day itself still gets written. When it runs out, the day keeps its facts and links but has no sentences.
- **When the day is written**: notify you only when something waits for you, every time, or never.

## The model

Lead calls the small, cheap model of your first teammate's AI provider that has one, the one memory uses. Through the day it keeps a short note of each teammate after its turns, and at the set time it writes the day from those notes and the facts. Without such a model, the day still has its facts and links.

## In the terminal

In [`studio tui`](../tui/), Lead is the first row under **Quiet** on Home once a teammate has worked today. The reading pane shows the day, and `w` writes it now.
