# Memory

What personas remember, per environment (PLAN.md §6.7): the store, search, embeddings,
API and UI, memory in agent sessions (hooks, tools, extraction) and consolidation (the
dream job and conflicts).

## Scopes

- `shared` is visible to every persona; `persona:<id>` only to that persona.
- A new fact or page in `persona:<id>`, and any write with an author persona, must name a
  persona of the same environment (422 otherwise). There is no foreign key, so the memory of
  a deleted persona stays readable and editable.
- Nothing crosses environments: every table carries `environment_id`, and IDs from another
  environment are 404.

## Data

Tables live in the catalog database (`013_memory.sql`, all prefixed `memory_`):

- `sources`: kind (`user`, `verified`, `document`, `inferred`, `consolidation`), session
  ref, author persona and a short evidence quote. Every fact write adds one.
- `facts`: kind (`preference`, `decision`, `fact`, `procedure`, `event`), entities,
  attribute, text, `observed_at`, validity, confidence, tier, support count, status
  (`active`, `superseded`, `disputed`, `retracted`), `supersedes` and author persona.
  Superseding marks the old fact `superseded` and ends its validity; a conflict marks both
  facts `disputed` in one scope, only the persona's fact across scopes.
- `pages`: slug (unique per scope), title, kind, compiled truth, `always_load`, tier. Core
  (`always_load`) pages of one scope share 4000 characters; a write over that is refused
  with a list of the other core pages and how much room is left.
- `timeline`: dated entries per page, append-only (a trigger refuses updates).
- `conflicts`: two facts (one conflict per pair), verdict, the judge's reason, status and
  the inbox approval. Resolutions: `keep_a` or `keep_b` retracts the other fact;
  `keep_both` keeps both as tier `user`, optionally rewritten with a qualifier; `edit`
  writes one new `user` fact that supersedes both. Each adds a timeline entry.
- `dream_runs` and `judgments` (`016_memory_dream.sql`): consolidation runs and cached
  verdicts.
- `chunks`: the searchable text of facts, pages (split at about 1200 characters) and
  timeline entries, indexed by FTS5 (`porter unicode61`) through triggers.
- `embeddings`: one int8 vector per chunk with its scale, dimensions and model name.

UI edits are tier `user`; editing an inferred fact raises it to `user`.

## Search

`GET /api/environments/{env}/memory/search?q=…&persona=…`: shared only, or a persona's
scope plus shared.

1. BM25 over FTS5 (terms ORed) and a brute-force int8 dot product over the vectors of the
   current model (similarity at least 0.2), up to 200 candidates each. Timeline hits count
   for their page.
2. Reciprocal-rank fusion, k = 60.
3. Times a tier boost (user 1.3, verified 1.2, document 1.1, inferred 1.0) and a recency
   boost `0.5 + 0.5 · 0.5^(age / half-life)`. Half-lives: preference 365 days, decision and
   procedure 180, fact 90, event 14, page 180.
4. Retracted facts never show; superseded and expired (`valid_until` past) facts only with
   `includeSuperseded`, marked. Disputed facts are marked.
5. Shared wins: a persona fact in an open conflict with a shared fact is marked
   `overriddenBy` and the shared fact is placed right above it (pulled in if search missed
   it) until the user resolves the conflict.

Every hit carries `why`: both ranks, the similarity, whether vectors were used
(`used`, `not_ready`, `not_embedded`), the RRF score, both boosts and a one-line summary.

## Embeddings

- `memory.Embedder` has a production implementation (`Llama`: embeddinggemma-300m Q8_0
  through llama.cpp, loaded with yzma) and a deterministic `FakeEmbedder` for tests.
- One background worker embeds new chunks from a bounded queue (256); when it overflows, or
  at startup, it sweeps for chunks without a vector of the current model. Writes never
  wait for it, and search uses BM25 alone until the model is ready.
- The first chunk to embed activates the model: the worker downloads llama.cpp `b11429`
  for the platform and the GGUF at a pinned Hugging Face revision into
  `<data>/memory/`, verifying the pinned SHA-256 of each (about 335 MB in all). An unused
  memory downloads nothing.
- Vectors record the model name. A new name (a new pin) makes the sweep re-embed
  everything in the background; old vectors are ignored meanwhile.

## API

All under `/api/environments/{env}/memory`, behind the usual guard:

| | |
|---|---|
| `GET scopes` | shared plus persona scopes with data, with counts and core usage |
| `GET/POST facts`, `GET/PUT/DELETE facts/{id}`, `POST facts/{id}/retract` | facts; list filters `scope`, `status` |
| `GET/POST pages`, `GET/PUT/DELETE pages/{id}`, `POST pages/{id}/timeline` | pages; `GET pages/{id}` includes the timeline |
| `GET search` | hybrid search with `why` |
| `GET conflicts`, `GET conflicts/{id}`, `POST conflicts/{id}/resolve` | conflicts, open first; the detail adds both facts' sources and timeline |
| `POST dream`, `GET dreams?scope=` | start consolidating a scope (202, 409 while one runs); runs with stats |
| `POST facts/{id}/promote` | propose a persona fact for shared memory (`memory.share` approval) |

| `GET sessions?persona=`, `GET sessions/{id}/log` | agent sessions and what memory did in each, with reasons |
| `GET usage` | today's extraction calls and spend against the daily budget, utility model per provider |

The Memory page in the environment sidebar has a scope switcher, the extraction budget line,
and tabs for pages, facts (with "promote to shared" on persona facts), conflicts (with
"Dream now", recent runs and a sheet that shows both facts side by side with sources, tiers
and timeline, and resolves), search and sessions. `memory.conflict` approvals show in the
approval stack with the same sheet.

## In agent sessions

Code: `internal/agentmem` (host), `internal/agentcall` and `internal/agenthook` (guest),
`cmd/studio-agent` (`hook`, `mcp`). Harness wiring is in docs/harnesses.md.

**Identity.** Hooks and tools run `studio-agent`, which sends a call through
`/run/studio-agent/call.sock` to the guest daemon and on over the sandbox's own vsock
channel (stream kind `call`, at most 8 at once, 1 MiB each). Studio takes the sandbox,
environment and persona from the channel, never from the payload. A persona reads `shared`
and its own scope and writes only its own; tool arguments with unknown fields are refused.
A sandbox without a persona gets no memory: hooks answer empty, tools fail.

**Hooks never block.** `studio-agent hook <event> --harness <h>` reads at most 1 MiB of
stdin, answers within 5 s (2.5 s at session end) and on any failure prints the neutral
answer (`{}`) and exits 0.

- *Session start* (also resume and compact) injects a context pack of at most 12 000
  characters inside `<studio-memory>` tags: the soul (4000), core pages (persona, then
  shared), the project page for the repository's normalized git remote
  (`project/<host/owner/repo>`, then `project/<repo>`, persona scope first), shared facts
  other personas changed since the persona's previous session (or the last 7 days, at most
  10), and up to 5 open conflicts that touch them or the persona's own facts.
- *Prompt* runs hybrid search and injects at most 5 hits, about 500 tokens (2000
  characters), each with similarity ≥ 0.45 or at least 2 matching prompt words, and none
  already given to the session.
- *Pre-compact, stop, session end* send the transcript delta for extraction. The guest
  keeps a per-session offset (`~/.local/state/studio-agent/transcripts.json`) that moves
  only when Studio accepted the delta. It reads only regular files under the harness's
  state dirs, only complete lines, at most 256 KiB at a time, and condenses them to user and
  assistant text (64 KiB), without tool calls or anything in `<studio-memory>` tags. A stop
  sends only after 5 minutes and 2 KiB of new text; pre-compact and session end always do.

Every injection and write goes to the session log (migration `015_memory_sessions.sql`)
with the item, its score and why: the search explanation plus the threshold it passed, or
the extraction's model, tokens, cost and any rejection.

**Tools** (`studio-agent mcp`, a stdio MCP server): `memory_search`, `memory_get` (id or
slug), `remember(text, kind, entity?, scope, source?)`, `share(fact_id | text)`,
`correct(fact_id, text)` (a new fact supersedes the old) and `forget(fact_id)` (retracts).
The last two take only the persona's own facts. `share`, and `remember` with scope
`shared`, create a pending `memory.share` approval with the author; approving it in the
inbox writes the fact to shared memory with that persona as author, or reinforces a
duplicate.

**Extraction.** One host worker (queue of 64; a full queue fails the hook so the guest
resends) calls the provider's utility model directly with the real key from the vault,
which is never logged: `claude-haiku-4-5` for `anthropic_api`, `gpt-6-luna` for
`openai_api` (unconfirmed, S7), the endpoint's model for `openai_compatible`, none for
subscriptions (S8). The answer must be `{"facts": [...]}` with at most 20 facts of text,
kind, tier (`user` or `inferred`), entities, attribute and an evidence quote found in the
transcript; one bad fact, an unknown field or anything like a credential rejects the whole
answer. `user` tier stays only if the quote is from a user line. Facts go to the persona's
scope; a fact with the same text, the same attribute and similarity ≥ 0.75, similarity
≥ 0.92 or word overlap ≥ 0.8 reinforces the existing one instead.

**Budget.** Per environment and UTC day: $1.00 for priced models (each call's cost is
estimated before it is made), 200 calls for unpriced ones. Skipped runs are counted.

## Consolidation

Code: `internal/agentmem/dream.go` (the job) and `internal/memory/consolidate.go`
(merge, supersede, compiled truth).

**Triggers.** A scheduler checks every minute: a scope with 25 new facts since its last
finished run, a scope with new facts after 03:00 local time that has not run since, and
any interrupted run. Automatic runs of one scope are at least an hour apart. "Dream now"
(`POST dream`) starts one at once. One dream per scope runs at a time (an in-process lock
plus a unique index on running rows).

**A run** covers the facts created between the end of the last finished run and now (both
ends included); a persona's run also takes new shared facts.

1. Dedupe: near-duplicates in one scope (same normalized text, or the same kind with word
   overlap ≥ 0.8 for the same attribute, ≥ 0.9 otherwise) are merged into the older fact:
   sources and timeline move over, support adds up, the better tier stays.
2. Pairs: each new fact against up to 8 related active facts (same attribute or a shared
   entity) of its scope and shared; new shared facts against the persona's facts. At most
   100 pairs per run; pairs with a conflict are skipped.
3. Judge: the persona's utility model and budget, as for extraction (the first persona with
   one for the shared scope; none for subscriptions, S8, so those runs only dedupe and
   compile). The answer must be exactly `{"verdict", "reason"}` with a verdict of
   `no_conflict`, `duplicate`, `supersedes` or `contradiction`; `supersedes` needs B
   observed after A. Anything else is counted as rejected and changes nothing. Valid
   verdicts are cached per pair, model and prompt version.
4. Apply: `duplicate` merges (same scope). `supersedes` applies by itself, with a timeline
   entry on the entity page, when B's tier is at least A's and B is in A's scope or shared;
   a persona fact never replaces a shared one. Otherwise, and for every `contradiction`, a
   conflict opens with a `memory.conflict` approval in the inbox. Dismissing the approval
   only hides it; the conflict stays open until resolved on the memory page.
5. Compiled truth: the touched entities' pages (`<entity>` or `topic/<entity>`, created at
   two facts) get a regenerated block between `<!-- studio:compiled -->` markers; text the
   user wrote outside it stays. A core page that would exceed the core budget keeps its old
   text and the run notes it.

Runs record their window, trigger, status (`running`, `done`, `stopped`, `failed`), stats
(facts, pairs, judged, cached, rejected, merged, superseded, conflicts, pages, tokens,
cost) and a note. An exhausted budget or the pair limit stops a run cleanly; the next run
covers the same window again, and cached verdicts make that cheap. A run interrupted by a
shutdown stays `running` and resumes; every step is idempotent.
