# Memory that links

Memory today is a database, not a file ([memory](../memory.md)): facts with kinds, entities
and tiers, pages with compiled truth and a timeline, hybrid search, and nightly consolidation.
The single file is only the browsable copy each sandbox gets: `~/memory/yours.md` and
`shared.md`, at most 200 KiB each. Agents reach memory mostly through the session-start pack
(12 000 characters), the prompt hook (5 hits) and the tools.

That holds up while memory is small. As it grows, four things break down:

- **Nothing connects.** A page can't point at another page, and a fact names its entities, but
  nothing turns that into a way from one to the other. The teammate finds a related page only
  if search happens to surface it.
- **The agent can't build structure.** Agents write facts (`remember`, `correct`, `forget`).
  Pages are written by the user or by consolidation, which compiles one page per entity once it
  has two facts. A teammate that learns how a whole subsystem fits together can only leave
  facts about it.
- **The browsable copy is one long file.** At 200 KiB it is too long to read and is cut off.
  `grep` finds a line, not the page around it.
- **Nobody tidies.** Consolidation merges duplicates and finds contradictions. Nothing finds
  pages nobody links to, links to pages that don't exist, or entities with many facts and no
  page.

Two designs solve this for similar memories. Studio can borrow from both without becoming
either.

- **[gbrain](https://github.com/garrytan/gbrain)** (Garry Tan) keeps one markdown page per
  entity: compiled truth on top, an append-only timeline below. Studio's pages already have
  that shape. Links are extracted from the text by regex, with no model call, and are typed
  (`works_at`, `founded`, …). Backlinks raise a page in search, and queries can follow the
  graph a few hops.
- **[The LLM wiki](https://gist.github.com/karpathy/442a6bf555914893e9891c11519de94f)** (Andrej
  Karpathy) has three layers: immutable sources, a wiki of markdown pages the model owns, and a
  schema file that says how to keep it. Ingesting one source touches 10 to 15 pages, and a good
  answer to a question is filed back as a page. An `index.md` catalogs every page, and a lint
  pass finds contradictions, stale claims, orphans and missing cross-references.

## What changes

### 1. Links

`[[slug]]` (or `[[slug|text]]`) in a page's compiled truth, a timeline entry or a fact's text
links to the page with that slug in the same scope, or in shared memory. Studio extracts links
when the text is written. That takes a regex, not a model call. Links go into a new
`memory_links` table: scope, source (page, timeline entry or fact), target slug, and whether
the target exists.

A fact's entities count as links too: `project/api` links the fact to the page `project/api`
once that page exists.

Links are untyped. The sentence around a link says how two things relate, and a coding
teammate's memory is about projects, services and decisions, not about people and companies,
so gbrain's types don't carry over. A type can be added later as `[[slug|depends on]]`
without changing the table.

### 2. Backlinks

Every page shows **Linked from**: the pages and facts that link to it, newest first. Search
raises a page by how many pages link to it, on a log scale (as gbrain does), after the tier
and recency boosts. A link to a missing page makes it a *wanted page*, which lint lists.

### 3. The index

Studio keeps an `index` page per scope. It lists every page with its title, kind and the
first sentence of its compiled truth, grouped by kind (projects, topics, people, procedures).
Studio regenerates it when a page changes, and nobody edits it by hand. The session-start pack
includes the index's headings and titles, at most 1500 characters, so the teammate knows what
exists and opens a page with `memory_get`. The pack's size stays the same as memory grows.

### 4. A folder instead of a file

`~/memory` becomes a folder:

```
~/memory/
  index.md              the catalog, one line per page
  pages/<slug>.md       one page: compiled truth, timeline, "Linked from"
  facts.md              facts that no page holds, newest first
  shared/…              the same for shared memory
```

Links become relative markdown links, so `grep -r`, `cat` and an editor follow them. The copy
is one file per scope today because the guest's home-file path can't delete a file, and a
deleted page's copy would stay behind. The folder needs a guest call that writes the whole
folder to `~/.memory.new` and renames it over `~/memory`. That call can delete, and a reader
never sees half a copy. The 200 KiB cap becomes 64 KiB per page and 4 MiB in all.

### 5. Teammates write pages

A new tool, `memory_page(slug, title?, kind?, compiled?, timeline?)`, creates a page or updates
one in the teammate's own scope. Its links are extracted like any other.

- A new page, a teammate's own page, or a timeline entry is written at once, with tier
  `inferred` and the teammate as author.
- New compiled truth for a page the user wrote (tier `user`) is not written. It becomes a
  suggestion beside "promote" and "core page", which the user accepts or declines. The
  user's edits still outrank anything inferred.
- Shared pages are written as for facts: through a `memory.share` approval.
- At most 20 page writes per session. A compiled truth is at most 8000 characters, and the
  core budget is unchanged.

The managed instructions gain one paragraph, the schema of Karpathy's design: keep one page
per project, service or recurring topic; link pages with `[[slug]]`; after real work, update
the pages it touched rather than only adding facts; file a worked-out answer as a page.

### 6. Lint

Consolidation gets a seventh step that changes nothing by itself. Its findings are shown under
the memory panel's **Needs you**, each with an action:

| Finding | Action |
|---|---|
| A link to a page that doesn't exist | create the page, or remove the link |
| A page nothing links to, not core, untouched for 30 days | link it from the index's section, or delete it |
| An entity with 3 or more active facts and no page | create the page (consolidation compiles it) |
| An `event` fact older than 90 days that a page's compiled truth still states as current | edit the page |
| Two pages whose compiled truth contradicts | the existing conflict flow |

The checks run on the database, so they cost no model calls except the contradiction check,
which already exists.

### 7. One hop in retrieval

`memory_get(slug)` answers with the page and, after it, the titles and first sentences of the
pages it links to and the pages that link to it. A search hit on a fact with an entity names
the entity's page. The teammate can follow a link with one more `memory_get`. Studio follows
none on its own.

## What doesn't change

- **No raw-source layer.** Studio keeps each fact's source (session, evidence quote,
  author), not whole documents. A teammate that reads a document files what it learned as
  pages and facts.
- **No git for memory.** Supersedes, the append-only timeline and export/import already give
  history and a portable copy.
- **Search stays SQLite.** FTS5 with int8 embeddings is already hybrid with RRF, and backlinks
  add one more boost.

## The memory panel

The panel shows the same structure ([feedback plan](feedback-v0.8.md#one-memory-panel)):
pages render as markdown with their links clickable, **Linked from** under each page, the
index as the Pages list, and lint findings under **Needs you**.
Mockups H and I in the "Studio feedback mockups" artifact show both.

## Phases

1. Links, backlinks and the index page, with pages rendered in the panel. M.
2. The `~/memory` folder and the guest call that replaces it. S–M.
3. `memory_page` and the paragraph in the instructions, with suggestions for user pages. M.
4. Lint findings under Needs you. M.
5. One hop in `memory_get`, and backlinks in search. S.

Each phase adds cases to the retrieval eval ([memory](../memory.md#retrieval-eval)). Phase 1
adds cases where the answer is on a page linked from the hit. Phase 3 adds a scripted session
that should update two pages and link them.

**Built** on `feedback-v0.8`, all five phases ([memory](../memory.md#links)), with these
changes from the plan:

- **No links table.** Links are read from the text whenever a page, a list, the lint or a
  search with page hits needs them (`internal/memory/links.go`). Pages, timeline rows with
  `[[` and facts with entities or `[[` are loaded once per call; at the sizes memory has
  (hundreds of pages) that is cheaper than keeping a table in step with every edit, and an
  import, a dream or a hand edit can't leave it stale.
- **The index is generated, not stored.** `GET index` and the folder's `index.md` build it on
  each call, and the session pack gets a 1500-character "Pages in memory" list instead of a
  core index page, so it takes nothing from the core budget.
- **Page proposals are approvals, not suggestions.** A shared page, or new compiled truth for
  a page the user wrote, becomes a `memory.share` approval with the proposal, like a shared
  fact: it shows in the inbox, and approving it writes the page in the same transaction.
- **No lint for events that should be on a page.** It needs a model call per scope; the
  dream already files facts with entities onto their pages.
- **Eval cases** for linked pages and a scripted page-writing session are still to be added.
  The keyword side still scores 0.909: the corpus has no links, so the backlink boost
  changes nothing in it.
