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
| `browser.action`, `browser.credential`, `browser.control` | broker tools that need a decision; `fill_credential`; `browser_request_control` while the user drives | `internal/browser` ([browser](browser.md)) |
| `memory.share`, `memory.conflict` | `share`, `remember` with scope `shared`, "promote to shared"; consolidation | `internal/agentmem` ([memory](memory.md)) |
| `chat.permission` | a permission request from an ACP agent | `internal/chat` ([chat design](design/chat-mode.md)) |

Kinds other than `network` belong to an integration (`internal/integrations`): each declares
its approval kinds with a decide function, and the API hands the decision to the owner.

## API

- `GET /api/approvals?status=pending`: all environments, for the global stack.
- `GET /api/environments/{env}/approvals`
- `GET /api/environments/{env}/approvals/{id}`: one approval, pending or decided (the pages of
  a git push review and a memory conflict read it).
- `POST /api/environments/{env}/approvals/{id}/decide` with `action` `allow`, `deny` or
  `dismiss`. For `network`, allow and deny create a rule (`host`, `ports`, `scope`, and
  `expiresAt` for a rule that stops matching at that time), and dismiss only closes the
  request. `allow_once` and `deny_once` answer only the connections held for the request, with
  no rule; the approval then has `once: true`. Git approvals take an optional `note`;
  `browser.action` takes `remember` to allow or deny the action on that origin from now on.
- `POST /api/environments/{env}/approvals/{id}/revert` undoes a decision of the AI reviewer. Other
  decisions can't be reverted, and a reviewer's decision only once.

## Decided without asking

A network request that a security level or the curated list allowed, and the AI reviewer's
decisions, are decided approvals too, so Activity lists them. The `network.decided` field of the
payload says how: `open`, `curated` or `reviewer`. `network.reason` holds the curated entry's name or
the reviewer's reason. A reviewer's allow or deny has the `ruleId` of the rule it made. Revert deletes
that rule, sets `reverted`, and asks about the request from then on (see
[network](network.md#ai-reviewer-assisted)).

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
  `approvals`, `rules`, `secrets`, `builds`, `teammates`, `chats` and `tasks`. Events say what
  changed; clients refetch. Slow subscribers are disconnected.
- The approval stack (`web/src/components/approval-stack.tsx`) shows on every page, for
  every environment, with the environment's name on cards from other environments. Pages
  that list a kind themselves (network requests on the network page's Requests tab, a chat's
  own permission requests, Home its environment's approvals, which it shows as cards of its
  own) leave those out of the stack. Every place that shows an approval card itself claims
  its ID while it is on screen (`useClaimApprovals` in `web/src/lib/approval-claims.ts`): a
  teammate's conversation, a chat's permission card, the browser window's questions on its
  page, the Requests tab and Home. The stack leaves claimed IDs out, so a card shows in one
  place even when several views are on the page. More than one card, or a narrow page, folds
  the stack into an "N approvals" pill that opens the cards on click
  ([UI style](design/ui-style.md)). The pending count goes into the page title.
- A network approval's Options have two rows: who the decision covers and for how long. For a
  browser request, Who is "<teammate>'s browser", "All browsers" or "Browsers and sandboxes"
  (only the last leaves out `browserOnly`), and For is "Always", "1 hour", "This session" or
  "Once". "This session" makes no rule (`allow_site` for a navigation, `allow_session` with the
  card's host pattern otherwise; see [network](network.md#hold-and-ask)) and so turns Who off.
  For a sandbox's connection, Who is the scope and Any port, and For has no session. The
  Allow and Deny buttons name the lifetime, such as "Allow for 1 hour"
  (`decisionLabel` in `web/src/components/approval-decision.tsx`).
- A browser request's approval also offers its method and first directory, such as "GET
  /repos/*". The decision then carries `method` and `pathPrefix`, and the rule covers only
  those requests ([network](network.md#request-rules)).
- Allowing a browser navigation, with or without a rule, also approves its site for the
  browser session, so the page's own requests don't each ask.
- An API error shows its validation details: a body the server rejects names the field, and
  an unexpected property suggests restarting a Studio older than the page (`errorMessage` in
  `web/src/lib/api/client.ts`).
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
- The terminal UI ([terminal UI](tui.md)) lists the same approvals and decides network approvals
  through the same endpoint, with the same choices. It reviews git pushes and pull requests in
  its pager, with their commits and diff, and decides them there. Browser, memory and chat
  approvals open in the browser.
- Each kind has its own card: host and attempts with rule scope and pattern, commits and
  diff, browser action with its URL and an "always on this origin" choice, memory facts
  side by side, chat permission options. Every card names its kind in a chip and marks the
  kind's area with a colored left edge ([UI style](design/ui-style.md#primitives)).
