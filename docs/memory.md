# Memory

What personas remember, per environment (PLAN.md §6.7). This is the core: store, search,
embeddings, API and UI. Hooks, automatic extraction, consolidation and the agent tools come
later.

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
  Superseding marks the old fact (same scope) `superseded`; a conflict marks both
  `disputed`.
- `pages`: slug (unique per scope), title, kind, compiled truth, `always_load`, tier. Core
  (`always_load`) pages of one scope share 4000 characters; a write over that is refused
  with a list of the other core pages and how much room is left.
- `timeline`: dated entries per page, append-only (a trigger refuses updates).
- `conflicts`: two facts, verdict and status. Resolving only records the decision
  (`keep_a`, `keep_b`, `keep_both`, `dismiss`) and a note.
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
| `GET conflicts`, `POST conflicts/{id}/resolve` | conflicts, open first |

The Memory page in the environment sidebar has a scope switcher and tabs for pages,
facts, conflicts and search.
