# Memory

What teammates remember, per environment: the store, search, embeddings,
API and UI, memory in agent sessions (hooks, tools, extraction) and consolidation (the
dream job and conflicts).

## Scopes

- `shared` is visible to every teammate; `teammate:<id>` only to that teammate.
- A new fact or page in `teammate:<id>`, and any write with an author teammate, must name a
  teammate of the same environment (422 otherwise). There is no foreign key, so the memory of
  a deleted teammate stays readable and editable.
- Nothing crosses environments: every table carries `environment_id`, and IDs from another
  environment are 404.

## Data

Tables live in the catalog database (`013_memory.sql`, all prefixed `memory_`):

- `sources`: kind (`user`, `verified`, `document`, `inferred`, `consolidation`), session
  ref, author teammate and a short evidence quote. Every fact write adds one.
- `facts`: kind (`preference`, `decision`, `fact`, `procedure`, `event`), entities,
  attribute, text, `observed_at`, validity, confidence, tier, support count, status
  (`active`, `superseded`, `disputed`, `retracted`), `supersedes`, author teammate and
  context (see [Search](#search)). Superseding marks the old fact `superseded` and ends its
  validity; a conflict marks both facts `disputed` in one scope, only the teammate's fact
  across scopes.
- `pages`: slug (unique per scope), title, kind, compiled truth, `always_load`, tier. Core
  (`always_load`) pages of one scope share 4000 characters; a write over that is refused
  with a list of the other core pages and how much room is left.
- `timeline`: dated entries per page, append-only (a trigger refuses updates).
- `conflicts`: two facts (one conflict per pair), verdict, the judge's reason, status and
  the inbox approval. Resolutions: `keep_a` or `keep_b` retracts the other fact;
  `keep_both` keeps both as tier `user`, optionally rewritten with a qualifier; `edit`
  writes one new `user` fact that supersedes both. Each adds a timeline entry.
- `dream_runs` and `judgments` (`016_memory_dream.sql`): consolidation runs and cached
  verdicts, with the contexts the judge named (`029_memory_consolidation.sql`).
- `chunks`: the searchable text of facts, pages (split at about 1200 characters) and
  timeline entries, indexed by FTS5 (`porter unicode61`) through triggers.
- `embeddings`: one int8 vector per chunk with its scale, dimensions and model name.

UI edits are tier `user`; editing an inferred fact raises it to `user`.

## Search

`GET /api/environments/{env}/memory/search?q=…&teammate=…`: shared only, or a teammate's
scope plus shared.

1. BM25 over FTS5 (terms ORed) and a brute-force int8 dot product over the vectors of the
   current model (similarity at least 0.2), up to 200 candidates each. Timeline hits count
   for their page.
2. Reciprocal-rank fusion, k = 60.
3. Times a tier boost (user 1.3, verified 1.2, document 1.1, inferred 1.0) and a recency
   boost `0.5 + 0.5 · 0.5^(age / half-life)`. Half-lives: preference 365 days, decision and
   procedure 180, fact 90, event 14, page 180.
4. Retracted facts never show; superseded and expired (`valid_until` past) facts only with
   `includeSuperseded`, marked. Disputed facts are marked. A fact with a context shows only
   when the query names every word of the context (words of three letters or more, without
   stopwords such as "the"), so "renart" surfaces a fact that holds in the renart repo and
   "api database" does not. Facts with a context are left out of the session-start pack and
   of the compiled truth of their page, which reach sessions without a query.
5. Shared wins: a teammate fact in an open conflict with a shared fact is marked
   `overriddenBy` and the shared fact is placed right above it (pulled in if search missed
   it) until the user resolves the conflict. A shared fact with a context does not win.

Every hit carries `why`: both ranks, the similarity, whether vectors were used
(`used`, `not_ready`, `not_embedded`), the RRF score, both boosts and a one-line summary.

## Retrieval eval

`internal/memory/testdata/eval/corpus.json` holds 27 facts and 4 pages, and 22 queries with
the items each should find. `TestRetrievalEval` (`internal/memory/eval_test.go`) runs the
queries through `Search` and reports recall@3 and MRR:

    go test ./internal/memory -run TestRetrievalEval -v

It measures the keyword side only. The eval never prepares an embedder, and unit tests do not
load the real model (a download and a native library), so the vector side is not in the
numbers. The keyword side
scores 0.909 on both today, and the test fails below 0.90 (`minRecallAt3`, `minMRR`). Two
queries miss on words alone: "postgres version" (the fact says PostgreSQL) and "which
relational engine holds our data", the paraphrase the vector side is for. When the corpus
changes, update the thresholds in the same commit and say why.

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
| `GET scopes` | shared plus teammate scopes with data, with counts and core usage |
| `GET/POST facts`, `GET/PUT/DELETE facts/{id}`, `POST facts/{id}/retract` | facts; list filters `scope`, `status` |
| `GET/POST pages`, `GET/PUT/DELETE pages/{id}`, `POST pages/{id}/timeline` | pages; `GET pages/{id}` includes the timeline |
| `GET search` | hybrid search with `why` |
| `GET conflicts`, `GET conflicts/{id}`, `POST conflicts/{id}/resolve` | conflicts, open first; the detail adds both facts' sources and timeline |
| `POST dream`, `GET dreams?scope=` | start consolidating a scope (202, 409 while one runs); runs with stats |
| `POST facts/{id}/promote` | propose a teammate fact for shared memory (`memory.share` approval) |
| `GET suggestions?scope=`, `POST suggestions/{id}/accept`, `POST suggestions/{id}/dismiss` | open suggestions of a scope; accept or dismiss one ([Suggestions](#suggestions)) |

| `GET sessions?teammate=`, `GET sessions/{id}/log` | agent sessions and what memory did in each, with reasons |
| `GET usage` | today's extraction calls and spend against the daily budget, utility model per provider, and `sharedJudge`: whose utility model consolidates shared memory |
| `GET quality` | the quality numbers of the environment ([Quality](#quality)) |
| `GET export?scope=`, `POST import` | a scope as one markdown file, and a file read back into a scope ([Export and import](#export-and-import)) |

The Memory page in the environment sidebar shows shared memory: the extraction budget line,
the [quality](#quality) numbers, and tabs for pages, facts, conflicts (with "Dream now", recent
runs and a sheet that shows both facts side by side with sources, tiers and timeline, and
resolves), search and sessions. Above the tabs are the Export and Import buttons
([Export and import](#export-and-import)) and the open [suggestions](#suggestions), each with
Promote or Accept, and Dismiss. A facts' context shows as a badge, and the fact form takes one.
A teammate's private memory has the same tabs on its teammate page, where its facts can be
proposed for shared memory ("promote to shared"). `memory.conflict` approvals show in the
approval stack with the same sheet.

## Browsable memory

Each sandbox of a teammate has read-only markdown copies of what the teammate can read, in
`~/memory`: `shared.md` (shared memory) and `yours.md` (the teammate's own). A copy holds the
pages with their compiled truth, core pages first, then the facts no page holds. It leaves out
what a query would not surface: superseded and retracted facts, facts that hold in a context
only, and text that looks like a credential, which is redacted. A copy is capped at 200 KiB.

Studio writes the copies through the guest's home-file path, the one the harness files use
(mode 0444). It writes them when a session starts, and every 15 seconds it rewrites the copies
of each connected sandbox whose memory changed. A sandbox that is not connected gets nothing
until it is. Each scope is one file rather than one file per page, because that path cannot
delete files: a per-page copy of a deleted page would stay behind. The managed instructions
tell the agent the copies exist.

## Suggestions

Consolidation proposes two things and never applies them. They are rows of `suggestions`
(`029_memory_consolidation.sql`), listed on the memory page of the scope they are about, and
the user accepts or dismisses each one. A fact has at most one suggestion of each kind, so a
decided one is never made again.

- **Promote** (teammate scopes only). A teammate fact that recurs: learned at least 3 times
  (`support_count`), or, when a dream first sees it, a near-duplicate in another teammate's
  scope. Not suggested when shared memory has a near-duplicate. Accepting proposes the fact
  through the `memory.share` approval of `POST facts/{id}/promote`, so the inbox still decides.
  The data model has no sandbox scope, so there is no sandbox-to-teammate suggestion.
- **Core page** (any scope). A fact that sessions had injected (a recall) in at least 5
  different sessions within 30 days, whose entity page exists and is not a core page yet.
  Accepting makes that page a core page if the scope's core budget has room; if not, the
  suggestion stays open and the response says why. Counts come from the session log, so
  they are what sessions actually received.

Dreams open suggestions: after new facts, nightly, or on "Dream now". Each run also notes
how many it opened (`suggested`).

## Export and import

A scope exports as one markdown file (`GET export?scope=`; the memory page downloads it).
It begins with front matter (`studio-memory: 1`, the scope and the export time). Then comes
a section per page, core pages first, and one per active fact. A section has front matter of
its own, and its body is given by a byte count (`bytes`), so text inside a body can never
pass for structure:

    ---
    studio-memory: 1
    scope: shared
    exported: 2026-10-13T09:00:00Z
    ---

    ## page project/api
    ---
    title: API
    kind: project
    core: true
    tier: user
    bytes: 26
    ---
    Deployed with make ship.

    ## fact
    ---
    kind: decision
    tier: user
    observed: 2026-09-02
    entities: ["project/api"]
    attribute: deploy.command
    context:
    bytes: 26
    ---
    Ship with make ship

Not exported: the timeline, superseded, retracted and disputed facts, and IDs.

`POST import` reads a file back into a scope (the memory page has an Import button), at most
2 MiB. The file must be for that scope. Everything is checked before anything is written: the
format version, every key (an unknown one refuses the file), every value, the slugs, kinds,
tiers, contexts and byte counts, and no text may follow a body that is not a section. What is
written gets tier `document` and the evidence "imported from a memory export"; the file's own
tiers are not trusted. A page the scope has under the same slug, or a fact whose text it has
already (ignoring case and spacing), is skipped. A core page that the core budget refuses comes
in as a plain page, and the result says so.

## Quality

`GET quality` counts what is stored, for the whole environment: facts per status, the live
facts that hold in a context, the cached judge verdicts per verdict, conflicts per status,
open suggestions per kind, the latest consolidation run of any scope, and for the last seven
days the agent sessions, the items put into them (context packs and recalls) and the facts
they wrote. The memory page shows them as a compact section. The retrieval eval is a test, so
its score is not in the numbers; run it with `go test ./internal/memory -run TestRetrievalEval`.

## In agent sessions

Code: `internal/agentmem` (host), `internal/agentcall` and `internal/agenthook` (guest),
`cmd/studio-agent` (`hook`, `mcp`). Harness wiring is in docs/harnesses.md.

**Identity.** Hooks and tools run `studio-agent`, which sends a call through
`/run/studio-agent/call.sock` to the guest daemon and on over the sandbox's own vsock
channel (stream kind `call`, at most 8 at once, 1 MiB each). Studio takes the sandbox,
environment and teammate from the channel, never from the payload. A teammate reads `shared`
and its own scope and writes only its own; tool arguments with unknown fields are refused.
A sandbox without a teammate gets no memory: hooks answer empty, tools fail.

**Hooks never block.** `studio-agent hook <event> --harness <h>` reads at most 1 MiB of
stdin, answers within 5 s (2.5 s at session end) and on any failure prints the neutral
answer (`{}`) and exits 0.

- *Session start* (also resume and compact) injects a context pack of at most 12 000
  characters inside `<studio-memory>` tags: core pages (teammate, then shared), the project
  page for the repository's normalized git remote (`project/<host/owner/repo>`, then
  `project/<repo>`, teammate scope first), shared facts
  other teammates changed since the teammate's previous session (or the last 7 days, at most
  10), and up to 5 open conflicts that touch them or the teammate's own facts. The soul is not
  in it: it is in the harness's instructions file ([harnesses.md](harnesses.md#files)).
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
The last two take only the teammate's own facts. `share`, and `remember` with scope
`shared`, create a pending `memory.share` approval with the author; approving it in the
inbox writes the fact to shared memory with that teammate as author, or reinforces a
duplicate. The approval is marked approved in the same transaction as the write: if the
write fails, the decision returns the error, the approval stays pending and can be approved
again. "Promote to shared" on the memory page creates the same approval.

**Extraction.** One host worker (queue of 64; a full queue fails the hook so the guest
resends) calls the provider's utility model directly with the real key from the vault,
which is never logged: `claude-haiku-4-5` for `anthropic_api`, `gpt-6-luna` for
`openai_api` (unconfirmed, S7), the endpoint's model for `openai_compatible`, none for
subscriptions (S8). The answer must be `{"facts": [...]}` with at most 20 facts of text,
kind, tier (`user` or `inferred`), entities, attribute and an evidence quote found in the
transcript; one bad fact, an unknown field or anything like a credential rejects the whole
answer. `user` tier stays only if the quote is from a user line. The prompt names the
session's sandbox: a fact about that sandbox's state (files, branches, tools, services)
says "In sandbox X, …", since the teammate's other sandboxes don't have it; preferences,
decisions and procedures name none. Facts go to the teammate's
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
ends included); a teammate's run also takes new shared facts.

1. Dedupe: near-duplicates in one scope (same normalized text, or the same kind with word
   overlap ≥ 0.8 for the same attribute, ≥ 0.9 otherwise) are merged into the older fact:
   sources and timeline move over, support adds up, the better tier stays.
2. Pairs: each new fact against up to 8 related active facts (same attribute or a shared
   entity) of its scope and shared; new shared facts against the teammate's facts. At most
   100 pairs per run; pairs with a conflict are skipped.
3. Judge: the teammate's utility model, as for extraction; none for subscriptions, S8, so
   those runs only dedupe and compile. The judge sees each fact's context, if it has one. Shared memory uses the teammate that has had a
   utility model the longest. Studio does not record when a teammate's provider was set, so
   that is the oldest teammate whose provider has one (by creation time, then ID); there is
   no fallback to another teammate if its key or client fails. The run records the teammate,
   `GET usage` reports it as `sharedJudge`, and the memory page shows "Consolidated with
   <teammate>'s model" on the shared scope. Calls count against the environment's daily
   budget, which extraction shares; there is no per-teammate budget. The answer must be
   `{"verdict", "reason"}` with a verdict of `no_conflict`, `duplicate`, `supersedes`,
   `contradiction` or `context_dependent`; `supersedes` needs B observed after A. Only
   `context_dependent` may also carry `contextA` and `contextB`: the context each fact holds
   in, if one does (at least one is needed). Anything else is counted as rejected and
   changes nothing. Valid verdicts are cached per pair (with the contexts the judge saw),
   model and prompt version.
4. Apply: `duplicate` merges (same scope and context). `supersedes` applies by itself, with
   a timeline entry on the entity page, when B's tier is at least A's and B is in A's scope
   or shared; a teammate fact never replaces a shared one. Otherwise, and for every
   `contradiction`, a conflict opens with a `memory.conflict` approval in the inbox.
   Dismissing the approval only hides it; the conflict stays open until resolved on the
   memory page. `context_dependent` opens nothing: both facts stay active, and each fact
   that holds in a context only gets that context (`context`, a timeline entry that names it
   but not the fact's text). A fact keeps a context it already has.
5. Suggestions: see [Suggestions](#suggestions).
6. Compiled truth: the touched entities' pages (`<entity>` or `topic/<entity>`, created at
   two facts) get a regenerated block between `<!-- studio:compiled -->` markers; text the
   user wrote outside it stays. A core page that would exceed the core budget keeps its old
   text and the run notes it.

Runs record their window, trigger, status (`running`, `done`, `stopped`, `failed`), stats
(facts, pairs, judged, cached, rejected, merged, superseded, conflicts, pages, tokens,
cost, model and teammate) and a note. An exhausted budget or the pair limit stops a run cleanly; the next run
covers the same window again, and cached verdicts make that cheap. A run interrupted by a
shutdown stays `running` and resumes; every step is idempotent.
