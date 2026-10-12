---
title: Memory
description: Let a teammate recall project notes and propose shared facts across sessions.
---

Memory is scoped to an environment. Each teammate has its own memory, and teammates in that environment can also use shared memory. A sandbox needs an owner teammate to use memory tools.

Studio can recall relevant notes when a supported agent session starts or when you send a prompt. Harness hooks can also send selected conversation text for fact extraction after a session event. Memory capture is best-effort; it does not block the agent session.

![The Memory page showing saved facts and pages](../../assets/screenshots/memory.jpg)

## Review memory in Studio

Open **Memory** in the environment sidebar for shared memory, which every teammate sees. Each teammate also keeps private memory on its own page, under its **Memory** tab. Either one lets you inspect and edit pages and facts, search them, see conflicts, and review what the agent session recalled or proposed.

- Teammate facts and pages are visible in that teammate's scope.
- A teammate can propose a fact for shared memory. The proposal appears in the approval inbox; it becomes shared only after you approve it.
- If facts disagree, Studio records a conflict for you to resolve. Dismissing its inbox item does not resolve the conflict.

## What leaves the host

Embeddings are generated locally. When automatic extraction or consolidation runs, Studio sends selected conversation text or facts to the utility model for the configured provider, using the host-side API key. The memory budget is shared across the environment and capped at $1 per day for priced models, or 200 calls per day for models without a known price. Check the Memory page's usage indicator to understand its model calls.

The real provider key is not sent to the guest or written in memory. Studio rejects extracted facts that contain credential-like values, but avoid including secrets in conversations in the first place.
