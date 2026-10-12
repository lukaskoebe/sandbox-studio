# UI style (design)

Status: two mockup directions for N4, awaiting decision D3 (PLAN.md). The mockups are dev-only
static pages with fake data (`/mockups`, see [frontend.md](../frontend.md#development)).
Their tokens live in `web/src/mockups/mockups.css` and apply only under `<html
data-direction>`, so the production theme in `web/src/styles.css` is unchanged.

Today's UI is stock shadcn: `base-mira` with the stone base color, a 0.45 rem radius, Inter
and a green primary. That is clean but interchangeable. Whichever direction wins has to add
three things shadcn does not give:

- **Identifiers are monospace.** Hosts, ports, sandbox and template IDs, commands and paths
  always use the mono stack, so they read as data.
- **Areas have one hue each.** Accents say what kind of thing you are looking at. They never
  say how urgent it is.
- **Status has its own fixed set.** Every status dot comes with a word.

## Accents and status

One hue per area. Use them in nav icons, approval cards (the left rule and the tile), kind
chips, section icon tiles of that area, and the lineage view. Not in body text, buttons or
backgrounds. Both directions share these tokens.

| Area | Hue | Light | Dark |
|---|---|---|---|
| Network | sky | `oklch(0.6 0.13 235)` | `oklch(0.75 0.12 230)` |
| Git | violet | `oklch(0.55 0.19 295)` | `oklch(0.74 0.14 295)` |
| Browser | orange | `oklch(0.64 0.16 48)` | `oklch(0.78 0.14 55)` |
| Memory | teal | `oklch(0.57 0.1 185)` | `oklch(0.77 0.1 185)` |
| Chat and agent | green | `oklch(0.55 0.14 150)` | `oklch(0.76 0.15 150)` |

Status is separate and never used for an area:

| Status | Light | Dark |
|---|---|---|
| Running | `oklch(0.63 0.17 150)` | `oklch(0.74 0.17 150)` |
| Waiting for you | `oklch(0.78 0.15 86)` | `oklch(0.84 0.14 88)` |
| Failed | `oklch(0.58 0.21 27)` | `oklch(0.7 0.18 25)` |
| Stopped | `oklch(0.7 0.01 255)` (hollow dot) | `oklch(0.55 0.01 255)` |

## Shared rules

- **Type.** Inter for UI text. The system mono stack (`ui-monospace`, SF Mono, JetBrains Mono,
  Menlo, Consolas) for identifiers. No new font files; if one is ever added, it is
  JetBrains Mono for Direction A's identifiers.
- **Dark mode.** A three-way control (system, light, dark) at the foot of the sidebar,
  `?theme=` in the URL. Dark neutrals are tinted, never pure black. Accent and status hues keep
  their roles and get lighter.
- **Panels, not sheets, for watching.** Connections, Browser and Changes sit beside the
  terminal or chat as side panels, toggled from the workspace toolbar and remembered per page
  (N3). On phones a panel takes the full screen. Sheets remain only for short detail opened
  out of context, such as a git push review.
- **Approvals.** The same content in both directions: the kind (a rule and a tile in A, a chip
  in B), a title sentence, the target in mono, and Deny / Allow. Grouped requests list their
  hosts and allow the whole site at once.

## Direction A: "Workbench"

Pitch: a tool you keep open all day. Dense, tabular, graphite neutrals, identifiers in mono.

- **Density.** Rows 34 px (`--wb-row` 2.125 rem), table headers 40 px, the sandbox status bar
  26 px. Labels are 10 px uppercase with wide tracking. Body text is 13 px.
- **Lists over cards.** Sandboxes, teammates, ports, connections and sessions are ruled lists
  or tables. Cards are used only where an approval needs its rule, tile and buttons, and for
  the setup checklist.
- **Section headers.** A 20 px square tile with a small icon (in the area's hue where the
  section belongs to one), a 13 px semibold title, and a count.
- **Status bar.** Each sandbox has one slim bar along the bottom of its workspace: state, ID,
  template, CPU, memory, ports, pending approvals, uptime, generation.
- **Home.** A stat strip (sandboxes, approvals, teammates, egress, host), the sandbox table
  with CPU and memory bars, approvals beside it, then activity.
- **Tokens.** Background `oklch(0.993 0.001 255)` / `oklch(0.168 0.006 255)`. Card
  `oklch(1 0 0)` / `oklch(0.198 0.007 255)`. Border `oklch(0.912 0.005 255)` / `oklch(1 0 0 /
  8.5%)`. Foreground `oklch(0.21 0.008 255)` / `oklch(0.94 0.004 255)`. Muted text
  `oklch(0.52 0.014 255)` / `oklch(0.67 0.012 255)`. Primary `oklch(0.56 0.15 150)` /
  `oklch(0.72 0.17 150)`. Sidebar `oklch(0.97 0.003 255)` / `oklch(0.148 0.006 255)`. Radius
  0.3 rem (buttons 3 to 4 px). Terminal `oklch(0.2 0.008 255)` / `oklch(0.13 0.006 255)`.

## Direction B: "Calm studio"

Pitch: a studio you share with a team. Warm paper neutrals around the green, room to breathe,
teammates shown as people.

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

## Open points for the decision

- **Two greens.** Agent green (about 150 degrees) and running green are close. The status dot
  always has a word, and agent green appears only on chat icons and chips. If they blur in
  review, shift the agent hue, not the status green.
- **Two ambers, apart by hue.** Browser is orange (48 degrees) and waiting is amber (86
  degrees). If a browser approval and a waiting sandbox still read alike, move browser redder.
- **Mockups use the shadcn primitives unchanged** (Button, DropdownMenu, Sidebar, Checkbox).
  The look comes from the tokens, the layout and the copy. A chosen direction changes
  `styles.css` and the layouts page by page behind the e2e suite (N4).
