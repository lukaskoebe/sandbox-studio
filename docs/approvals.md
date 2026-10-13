# Approvals

Every action Studio holds for the user is a row in one `approvals` table: kind, subject,
typed payload, status (`pending`, `approved`, `denied`, `dismissed`), attempts and, for
network approvals, the rule it created. A repeated request with the same subject while one
is pending raises `attempts` instead of adding a row.

## Kinds

| Kind | Raised by | Decided by |
|---|---|---|
| `network` | a held connection to a host no rule covers | `internal/policy` ([network](network.md#hold-and-ask)) |
| `network.request` | a request on a connection the gateway terminates (a browser VM's HTTPS) that no rule covers | `internal/policy` ([network](network.md#hold-and-ask)) |
| `git.push`, `git.pr` | a push to the virtual remote; a PR proposal after an approved push | `internal/gitreview` ([git review](git-review.md)) |
| `browser.action`, `browser.credential` | broker tools that need a decision; `fill_credential` | `internal/browser` ([browser](browser.md)) |
| `memory.share`, `memory.conflict` | `share`, `remember` with scope `shared`, "promote to shared"; consolidation | `internal/agentmem` ([memory](memory.md)) |
| `chat.permission` | a permission request from an ACP agent | `internal/chat` ([chat design](design/chat-mode.md)) |

Kinds other than `network` belong to an integration (`internal/integrations`): each declares
its approval kinds with a decide function, and the API hands the decision to the owner.

## API

- `GET /api/approvals?status=pending`: all environments, for the global stack.
- `GET /api/environments/{env}/approvals`
- `POST /api/environments/{env}/approvals/{id}/decide` with `action` `allow`, `deny` or
  `dismiss`. For `network`, allow and deny create a rule (`host`, `ports`, `scope`, and
  `expiresAt` for a rule that stops matching at that time), and dismiss only closes the
  request. `allow_once` and `deny_once` answer only the connections held for the request, with
  no rule; the approval then has `once: true`. Git approvals take an optional `note`;
  `browser.action` takes `remember` to allow or deny the action on that origin from now on.

## Network approval cards

The card offers the host pattern (the name itself, or its registrable domain as a wildcard)
and the scope of the rule. Without an owner the scopes are the environment and the one
sandbox. For a sandbox a teammate owns, the card also offers the teammate's sandboxes, and
starts with the scope the teammate sets (`approvalScope`, [teammates](teammates.md)). The
response's `teammate` field carries the owner and that scope; the user can still pick another.

The pattern level is remembered per teammate: `exact` (the host itself) or `wildcard` (its
registrable domain), whichever the last allow or deny of one of its sandboxes used. Its next
card preselects that level (`teammate.patternLevel`). Other patterns, such as `*.org`, don't
change it.

## Realtime and UI

- `internal/events` publishes change notices as SSE at `/api/events`, with topics such as
  `approvals`, `rules`, `secrets`, `builds`, `teammates` and `chats`. Events say what
  changed; clients refetch. Slow subscribers are disconnected.
- The approval stack (`web/src/components/approval-stack.tsx`) shows on every page, for
  every environment, with the environment's name on cards from other environments. Pages
  that list a kind themselves (network requests on the network page's Requests tab, a chat's
  own permission requests) leave those out of the stack. More than one card, or a narrow
  page, folds the stack into an "N approvals" pill that opens the cards on click
  ([UI style](design/ui-style.md)). The pending count goes into the page title.
- A browser request's approval also offers its method and first directory, such as "GET
  /repos/*". The decision then carries `method` and `pathPrefix`, and the rule covers only
  those requests ([network](network.md#request-rules)).
- A browser navigation to a new site offers "Allow site <site> for this browser session", which
  approves the site for the session without a rule (`allow_site`).
- Network requests from one sandbox that arrive within 3 seconds of each other are one card,
  "<sandbox> wants N hosts", with the hosts listed, Allow all, Deny all, and Review
  individually (which turns them into the per-host cards). A browser's requests group by the
  page they belong to instead: one sandbox's pending requests to one top-level site are one
  card, "<site> wants N hosts"; a navigation keeps its own card. The pill counts requests, not
  cards. Allow all and Deny all decide each host with a rule of its own, with the scope
  chosen under Options. The grouping is client-side (`web/src/lib/approval-groups.ts`), so
  the API has no group. The server records arrival to the second, so the window is
  approximate.
- New approvals raise a browser notification (Notification API) once the user allows it.
- Each kind has its own card: host and attempts with rule scope and pattern, commits and
  diff, browser action with its URL and an "always on this origin" choice, memory facts
  side by side, chat permission options. Every card names its kind in a chip and marks the
  kind's area with a colored left edge ([UI style](design/ui-style.md#primitives)).
