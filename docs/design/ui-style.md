# UI style (design)

Status: Chosen: A Workbench (2026-10-13), accents toned down (PLAN.md D3). The app theme in
`web/src/styles.css` is direction A. This page covers its tokens, the
shared primitives and the rules for rolling it out page by page behind the e2e suite (N4).
Direction B stays in the dev-only mockups (`/mockups`, see
[frontend.md](../frontend.md#development)) for comparison. Its tokens live in
`web/src/mockups/mockups.css` with their original, untoned accents.

The stock shadcn look (`base-mira`, stone base color, 0.45 rem radius, a green primary) is
clean but interchangeable. The chosen direction adds three things shadcn does not give:

- **Identifiers are monospace.** Hosts, ports, sandbox and template IDs, commands and paths
  always use the mono stack, so they read as data.
- **Areas have one hue each.** Accents say what kind of thing you are looking at. They never
  say how urgent it is.
- **Status has its own fixed set.** Every status dot comes with a word.

## Accents and status

One hue per area: network sky, git violet, browser orange, memory teal, chat and agent green.
They mark nav icons, the icons of the workspace's panel buttons, approval cards (the rule on the
left edge and the icon tile), kind chips, section icon tiles of that area, and the lineage view.
Never body text, buttons or card backgrounds.

The accents are about 20 to 25 % less chroma than in the mockups; hue and lightness are
unchanged. Tints are softer too: icon tiles use 11 % of the hue (was 14 %). Icons on their
tile reach 3.1 to 4.5:1 in light and 6.4 to 7.5:1 in dark.

| Area | Hue | Token | Light | Dark |
|---|---|---|---|---|
| Network | sky | `--acc-network` | `oklch(0.6 0.1 235)` | `oklch(0.75 0.09 230)` |
| Git | violet | `--acc-git` | `oklch(0.55 0.15 295)` | `oklch(0.74 0.11 295)` |
| Browser | orange | `--acc-browser` | `oklch(0.64 0.12 48)` | `oklch(0.78 0.11 55)` |
| Memory | teal | `--acc-memory` | `oklch(0.57 0.08 185)` | `oklch(0.77 0.08 185)` |
| Chat and agent | green | `--acc-chat` | `oklch(0.55 0.11 150)` | `oklch(0.76 0.12 150)` |

Chroma before, light and dark: network 0.13 and 0.12, git 0.19 and 0.14, browser 0.16 and 0.14,
memory 0.10 and 0.10, chat 0.14 and 0.15.

Status is separate and never used for an area:

| Status | Token | Light | Dark |
|---|---|---|---|
| Running | `--st-running` | `oklch(0.63 0.17 150)` | `oklch(0.74 0.17 150)` |
| Waiting for you | `--st-waiting` | `oklch(0.78 0.15 86)` | `oklch(0.84 0.14 88)` |
| Failed | `--st-failed` | `oklch(0.58 0.21 27)` | `oklch(0.7 0.18 25)` |
| Stopped | `--st-stopped` (hollow dot) | `oklch(0.7 0.01 255)` | `oklch(0.55 0.01 255)` |

Status words are body text (17.7:1 on white in light, 15.3:1 in dark) and never set in a
status color. The dots reach 3:1 against the card except waiting and stopped in light (2.0:1
and 2.7:1). The word carries those states, so the dots stay as they are.

## Shared rules

- **Type.** Inter for UI text. The mono stack (`--font-mono`: `ui-monospace`, SF Mono,
  JetBrains Mono, Menlo, Consolas) for identifiers. No new font files.
- **Dark mode.** A three-way control (system, light, dark) for the sidebar footer, see
  `ThemeToggle` below. The choice is stored in the browser as `studio.theme`; "system" follows
  the OS preference. Dark neutrals are tinted, never pure black. Accent and status hues keep
  their roles and get lighter.
- **Panels, not sheets, for watching.** Connections, Browser and Changes sit beside the
  terminal or chat as side panels, toggled from the workspace toolbar and remembered per page
  (N3). On phones a panel takes the full screen. Sheets remain only for short detail opened
  out of context, such as a git push review.
- **Approvals.** Every kind has the same card: the rule on the left edge in the area's hue, a
  `KindChip` with the kind's name, a details line (source, environment, age), a title sentence
  (hosts in `Ident`), and Deny / Allow. Grouped requests list their hosts and allow the
  whole site at once. Browser URLs are in mono.

## Direction A: the app theme

Pitch: a tool you keep open all day. Dense, tabular, graphite neutrals, identifiers in mono.

- **Density.** Table rows 34 px (`--wb-row` 2.125 rem) and table headers 40 px (`--wb-header`).
  The shared table component uses both by default. The sandbox status bar is 26 px
  (`--wb-statusbar`). Labels are 10 px uppercase with wide tracking. Body text is 13 px.
- **Lists over cards.** Sandboxes, teammates, ports, connections and sessions are ruled lists
  or tables. Cards are used only where an approval needs its rule, tile and buttons, and for
  the setup checklist.
- **Section headers.** A 20 px square tile with a small icon (in the area's hue where the
  section belongs to one), a 13 px semibold title, and a count.
- **Status bar.** Each sandbox has one slim bar along the bottom of its workspace: state, ID,
  template, ports, pending approvals, generation. CPU, memory and uptime wait for the API to
  report them.
- **Home.** A stat strip (sandboxes running, approvals waiting, teammates working), the
  sandbox table (name and ID, owner, template, status, ports, approvals waiting) with the
  family tree under it, and the approvals and teammates on the right. Each number is one the
  API already gives. Egress, host load and an activity feed wait for endpoints that do not
  exist yet.
- **Presence.** Teammates use the status colors: working is running (pulsing), waiting for
  you is waiting, idle is stopped.
- **Tokens** (`web/src/styles.css`). Background `oklch(0.993 0.001 255)` / `oklch(0.168 0.006
  255)`. Card `oklch(1 0 0)` / `oklch(0.198 0.007 255)`. Border `oklch(0.912 0.005 255)` /
  `oklch(1 0 0 / 8.5%)`. Foreground `oklch(0.21 0.008 255)` / `oklch(0.94 0.004 255)`. Muted
  text `oklch(0.52 0.014 255)` / `oklch(0.67 0.012 255)`. Primary `oklch(0.53 0.15 150)` /
  `oklch(0.72 0.17 150)`. The light primary was `0.56` lightness: button labels were 4.2:1, now
  4.8:1 (AA needs 4.5:1). Sidebar `oklch(0.97 0.003 255)` / `oklch(0.148 0.006 255)`. Radius
  0.3 rem (buttons 3 to 4 px). Terminal `oklch(0.2 0.008 255)` / `oklch(0.13 0.006 255)`.

## Primitives

Shared pieces for the rollout. Use them instead of local styling.

- **`lib/accents.ts`.** The `Area` type and `ruleClass(area)` / `tileClass(area)`, which give
  the rule and the tile of an area. Without an area they return the neutral look.
- **`SectionHeader`** (`components/section-header.tsx`). The title row of a section or panel:
  tile, title, count and actions. Pass `area` only where the section belongs to one area. It
  renders an h2; the page title stays the h1.
- **`Ident`** (`components/ident.tsx`). Any identifier in mono: host, port, sandbox or
  template ID, path, command, URL. `copy` adds a copy button for values people paste elsewhere.
- **`KindChip`** (`components/kind-chip.tsx`). An approval kind's name with its icon on the
  area's tile. Approval cards pass `approvalKind(a).area`; unknown kinds have none.
- **`StatusDot`** (`components/status-dot.tsx`). One of the four statuses, always with its
  word. `label` replaces the word for a state the four do not name, and `pulse` marks a state
  in progress. Sandbox phases map onto it in `components/status-badge.tsx` (`phaseDot`).
  Dense lists show the dot alone (`compact`): the sidebar is the one place that does, and the
  word becomes its tooltip and the text screen readers read.
- **`WaitingCount`** (`components/waiting-count.tsx`). The number of approvals waiting for you,
  on a chip in the waiting color. It shows nothing at zero.
- **`ThemeToggle`** (`components/theme-toggle.tsx`). System, light and dark, stored by
  `lib/theme.ts`. It sits in the sidebar footer.
- **Table rows and headers** (`components/ui/table.tsx`). Workbench heights by default; pages
  do not set their own.

## Direction B: "Calm studio" (not chosen)

Pitch: a studio you share with a team. Warm paper neutrals around the green, room to breathe,
teammates shown as people. Kept in the mockups only.

- **Density.** List rows 40 to 48 px with 16 px side padding. Cards use 20 px padding, a 16 px
  radius and a soft shadow. The content column is 60 rem wide and centred in an inset panel.
- **Titles.** A page title of 28 px semibold, one intro line in muted text, and an eyebrow
  above it where it helps ("What's happening").
- **People.** Teammates have avatars with a presence dot (working, waiting for you, idle) on
  Home and in the sidebar. Sandboxes show their owner's avatar without a dot.
- **Cards only for actions.** "Needs you", approvals, the setup checklist and waiting
  connections are cards. Everything else is a divided list.
- **Panels.** The workspace toggles panels with a segmented control. The Connections panel
  groups "Waiting for you" as cards and keeps "Allowed" and "Blocked" as plain lists.
- **Tokens.** Background `oklch(0.983 0.006 85)` / `oklch(0.192 0.008 65)`. Card `oklch(0.997
  0.003 85)` / `oklch(0.228 0.009 65)`. Border `oklch(0.905 0.013 80)` / `oklch(0.95 0.02 85 /
  9%)`. Foreground `oklch(0.25 0.014 60)` / `oklch(0.94 0.01 85)`. Muted text `oklch(0.53 0.02
  65)` / `oklch(0.7 0.016 75)`. Primary `oklch(0.5 0.11 155)` / `oklch(0.72 0.12 155)`.
  Sidebar `oklch(0.963 0.01 82)` / `oklch(0.168 0.007 65)`. Radius 0.7 rem. Shadow `0 1px 2px
  oklch(0.4 0.03 60 / 6%), 0 4px 16px oklch(0.4 0.03 60 / 6%)`, and a heavier one in dark.
  Terminal `oklch(0.972 0.01 85)` / `oklch(0.165 0.007 65)`.

## Rollout status

Pages move over one at a time, each behind the e2e suite (N4). A page that has moved uses the
primitives above and keeps the e2e selectors it had.

- **Done: sidebar.** Compact rows. Sandboxes show their name in mono, their status dot and the
  approvals waiting on them. Teammates show presence. Nav icons take their area's hue, and the
  theme toggle sits in the footer.
- **Done: Home.** Stat strip, sandbox table (stacks below the large breakpoint), family tree,
  approvals summary and teammates, with a `{/* setup */}` slot at the top of the main column.
  Not shown: an activity feed (no data source yet), and egress or host load (no endpoint).
  The template column shows the template's ID until the API names templates.
- **Done: sandbox dashboard and workspace.** Header with the name and ID in mono, sections
  with headers and area tiles (connections and chats only), ports and connections as lists,
  and a status line on the workspace. CPU, memory and uptime are not shown until the API
  reports them.
- **Not yet: every other page.** They keep their old layout until they move over.

## Open points

- **Two greens.** Chat green (chroma 0.11 light, 0.12 dark) and running green (0.17) are
  further apart than before the toning. If they still blur on Home, shift the chat hue, not
  the status green.
- **Two ambers, apart by hue.** Browser is orange (48 degrees) and waiting is amber (86
  degrees). If a browser approval and a waiting sandbox still read alike, move browser redder.
- **Faint waiting dot in light.** Amber on white reaches only 2.0:1 for the dot. Reaching 3:1
  turns it brown, so the word carries the state for now.
