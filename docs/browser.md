# Browser broker

Agents browse the web through a real Chromium. It runs in a separate microVM per teammate,
and Studio is the only client: agents get MCP tools, never CDP. This doc covers the
implementation.

Code:
- `internal/browser`: the broker, policy, redaction, credentials, history, live view relay and
  ephemeral sessions (`ephemeral.go`).
- `internal/sandboxes/browser.go`: the VM lifecycle.
- `internal/guest/browser.go`: the guest side, which runs agent-browser.
- `internal/agentproto/browser.go`: the wire format.
- `internal/agentcall/browser.go`: the MCP tools.
- `internal/api/browser.go`: the API.
- `cmd/studio/browser.go`: the wiring.
- `images/browser`: the image.
- The UI: `browser-window.tsx` (the window), `browser-rail.tsx` (activity and sites),
  `browser-panel.tsx` (the side panel), `browser-browsing.tsx` (the conversation's card),
  `browser-live.tsx` and `browser-approval.tsx` in `web/src/components`, and
  `web/src/lib/browser-sites.ts`.

## One browser VM per teammate

Each teammate gets one browser VM: a sandbox row of kind `browser`, named `browser-<teammate>`.
The API, terminals and previews hide it.

Why per teammate:

- **The profile is the identity.** Cookies and logins belong to whoever logged in. A teammate
  is the unit that owns accounts. Its sandboxes are the same "person" working on several
  things, so they share a session. Two teammates never share cookies.
- **Per environment would mix identities.** Per sandbox would lose logins on every new
  sandbox and multiply 2 GiB Chromium VMs.
- **One profile, one browser.** Agent calls are serialized per teammate. Only one sandbox
  drives at a time, and egress is attributed to it.

**Lifecycle:**

- **Quiet.** Chromium's own calls to Google's services are turned off by managed policies
  or denied without asking ([network](network.md#chromiums-own-requests)), so a page's
  approvals are about the page.
- **Created lazily.** The first browser call, opening a page in the UI, or Wake up in the
  browser window's options menu creates the VM from the browser image: 2 CPUs, 2 GiB of
  memory and a 2 GiB disk for the profile.
- **Stopped when idle.** A VM with no call and no live viewer for 10 minutes stops. The
  profile disk stays.
- **Deleted on demand.** Reset profile… in the options menu removes the VM and its profile
  but keeps the history and the site rules. Deleting the teammate also drops its history,
  and its rules go with it. Deleting a teammate is refused while it still has agent
  sandboxes.

The image is `sandbox-studio-browser`, at the same version as the base image. Build it with
`make image-browser`.

## Isolation and egress

- **Only Studio reaches the VM, through its studio-agent channel:**
  - Commands use the `browser` request kind: a bounded argv for agent-browser plus an
    optional stdin for `batch`.
  - The live view uses `KindTCP` to the guest's loopback port 9223.
  - Nothing listens on the VM beyond loopback. No CDP port is ever exposed.
  - The VM's loopback also holds the forwarded ports of the driving sandbox (see
    [localhost](#localhost)); those connections go out over the channel, not a network.
- **The guest checks every command against its own allow-list.** Only these commands
  pass: open, snapshot, click, fill, type, press, select, scroll, wait, back, forward,
  reload, upload, download, screenshot, and `get text|attr|url|title`. The flags are
  allow-listed too, and no arg may start with `-` otherwise.
  - This is a second line of defense behind the broker.
  - It refuses eval, network, cookies, storage, state, HAR and `get value` even if the
    host were confused.
  - `close` passes only as the whole command on an ephemeral slot, where it ends the session.
    It never reaches the persistent profile.
- **Egress goes through the same gateway, MITM CA and rules.** The VM's HTTPS is always
  terminated with the environment's CA (agent sandboxes' connections are spliced unless a rule
  or a secret needs them checked), so its requests can be checked one by one.
  - While an agent drives, the gateway's `Driver` hook evaluates the browser VM's
    connections with the driving sandbox's rules. Domain approvals name that sandbox.
  - While the user drives (takeover), the browser VM's own rules apply: its teammate's rules
    plus the environment's.
  - The tracker list applies to the VM's requests before any approval (see
    [network](network.md#trackers)). The environment's security level decides the requests no
    rule covers after that, and never allows a request that carries a placeholder (see
    [security levels](network.md#security-levels)).
  - Site approvals last the browser session, and cover a page's requests once the user allows
    a navigation to the site, by a rule or for the session (see
    [network](network.md#hold-and-ask)). A page that is loading belongs to the site of its
    navigation. So do host patterns allowed for the
    session. Both end when the VM stops, and neither covers the sandboxes' own connections.
  - A rule from a browser request's card is browser-only unless the user picks the sandboxes
    too (see [rules](network.md#rules)).

## Localhost

`localhost` in the browser is the agent sandbox that drives it, so an agent opens the dev
server it just started at `http://localhost:5173/` like it would on a laptop. Nothing about
the page's origin changes: cookies, service workers and CORS see `localhost`, and the
browser treats it as a secure context.

- **Before each of a sandbox's browser calls**, the broker lists the ports it listens on
  (`ports`, the same list previews use) and sends them to the browser VM (`forwards`). A
  server started since is there by the next call; one that stopped is gone. The ports a
  browser VM uses itself (agent-browser's live views, 9222 to 9225) are never forwarded.
- **The VM's guest listens on `127.0.0.1` and `::1` at exactly those ports**, skipping one a
  VM process already holds. Each connection becomes a `forward` stream to the host.
- **The host connects it to the same port of the sandbox** the ports came from, over that
  sandbox's channel like a preview (`KindTCP`). `browser.Service.DialForward` checks the
  stream's VM is a browser VM, that the port is one it was told to forward, and that the
  sandbox still exists in the VM's environment, for the VM's teammate. Anything else closes
  the connection, which the browser shows as a refused one.

What this does and doesn't allow:

- Only the driving sandbox, and only the ports it listened on when it last called. When
  another of the teammate's sandboxes calls, localhost becomes that one. During a takeover
  it stays the last driver's.
- The connection reaches the sandbox's loopback directly, so the gateway doesn't see it.
  The browser's [policy](#policy-checked-before-anything-runs) still applies: opening a
  `http://localhost:…` page, clicking and filling on it, are checked against its origin's
  rules like any other.
- The sandbox doesn't reach the browser VM: forwards go from the browser to the sandbox only.

## Browser TLS trust

Before launching agent-browser, the guest imports the configured environment CA into the
agent user's `~/.pki/nssdb` with `/usr/bin/certutil`. The CA gets `C,,` trust, which marks it
trusted for SSL only. The NSS database is separate from `/workspace/browser-profile`, so
certificate updates do not alter persistent cookies or logins. A missing CA fails the
browser command with a retryable configuration error. This avoids `--ca-cert`, which
agent-browser 0.39.0 rejects alongside `--profile`. Chromium certificate verification stays
enabled.

## Tools

`studio-agent mcp` adds these tools. Each one is forwarded over the call channel as a
`browser_*` method:

- `open(url, ephemeral?)`, `snapshot`, `click(ref)`, `fill(ref, text)`, `type(ref, text)`, `press(key)`,
  `select(ref, value)`, `scroll(direction, px)`, `get_text(ref?)`
- `screenshot`, which returns MCP image content
- `wait(ms | text | url)`, `back`
- `fill_credential(ref, name)`
- `close(session)`, which ends an ephemeral session
- `browser_downloads()` and `browser_download_to_workspace(name, session?)`, see
  [Downloads](#downloads)

Every tool takes an optional `session`, the ID that `open(ephemeral: true)` returned. Without
it a tool drives the persistent profile. See [Ephemeral sessions](#ephemeral-sessions).

**Identity comes from the channel only.** The hub knows which sandbox's agent connection
carried the call. That gives the teammate, and the teammate gives the browser. A
`teammate`, `sandbox` or `env` field in the arguments is ignored. A sandbox without a teammate
has no browser. A browser VM cannot call tools.

**Text first.** `browser_open` answers with the address and title, and the agent reads the page
with `browser_snapshot`. A screenshot's JPEG is at most 180 KiB, so its reply fits one stream
window of the channel (see [guest agent](guest-agent.md#limits-of-the-channel-on-msbs-vsock)).
Larger screenshots are dropped, and the tool says the screenshot failed or was too large.

## Lost replies

A command's reply can fail to reach Studio, when the channel to the browser VM breaks or the VM
restarts. The command may still have run, so the tools say what Studio knows instead of a bare
error (`TestLostReplyIsCheckedAgainstThePage`):

- **`browser_open`** asks the page for its address, once, after a second. If the page is on the
  origin the open asked for, the open went through: the result says so, without the screenshot
  and title its reply carried, and the action log records it as `ok`.
- **Page-changing actions** (click, fill, type, press, select) say that the reply did not arrive,
  where the page is, and that the action may have run. They fail, so the agent takes a
  `browser_snapshot` before it acts again.
- **Other tools** fail with the reply's error and point to `browser_snapshot`.
- When the page cannot be asked either, the result says that it could not be checked.

## Policy, checked before anything runs

The order is:

1. the hard deny list;
2. deny patterns;
3. uploads and downloads, which always ask;
4. read-only actions, which are allowed;
5. allow patterns;
6. `open`, which is allowed because the gateway still gates the network;
7. ask for anything that changes the page.

Unknown actions are denied.

| Action | Verdict |
| --- | --- |
| eval, addscript, setcontent, route/network, HAR, cookies, storage, state | always denied; there is no tool for them and the guest refuses them |
| `get value` on a password-like field | denied (there is no get value tool) |
| snapshot, scroll, get_text, screenshot, wait, back | allowed |
| upload, download | `browser.action` approval every time; a pattern can't allow them |
| open | allowed unless a deny pattern matches |
| click, fill, type, press, select | allowed or denied by a matching pattern, else a `browser.action` approval |

- **Patterns** match on action (or `*`), origin, element role and label.
  - Origins can be `*`, `https://example.com`, `https://*.example.com` or `example.com`
    (any scheme), with an optional port. URLs with a path, user info or a
    non-http(s) scheme are rejected.
  - The label match is a glob, case-insensitive.
  - Deny beats allow.
  - Text typed into a sensitive field is logged only as a character count.
  - Kinds: `allow`, `deny`, `sensitive`. A sensitive pattern marks fields for redaction.
- **Approvals.** A `browser.action` approval shows the teammate, sandbox, action, origin,
  element and text. "Always for <action> on this origin" stores an allow pattern. The
  calling tool waits up to 30 s for the decision. After that the agent gets a "waiting for
  approval" answer, and an approval granted later holds for 5 minutes for the same action
  on the same element.

## Redaction

Every snapshot goes through `Redact` before it reaches the agent or the history. A value
is blanked to `[redacted]` for:

- `textbox` refs whose `type` is `password`;
- `autocomplete` values `current-password`, `new-password`, `one-time-code` or `cc-*`;
- fields matched by a `sensitive` pattern.

The broker probes the type and autocomplete of each textbox ref with `get attr` in one
`batch`, capped at 50 refs. A valued line whose ref was not probed, or whose probe failed, is
blanked as well. So are the children of a sensitive node, in case Chromium exposes an
input's text as a StaticText child. Text the
agent reads (`get_text`, page text) is scrubbed of every credential value that was filled
in that session.

The fixtures in `internal/browser/browser_test.go` use agent-browser 0.39's real output:
`- role "name" [attrs, ref=eN]: value` lines in the `{success,data,error}` envelope.
Parsing that is not confirmed against a live Chromium is marked `// verify (live)`.

## Credentials

`fill_credential(ref, name)`:

1. Checks that `name` is a vault secret the driving sandbox sees (its environment's, its
   teammate's or its own) and that the page's host is bound to it.
2. Raises a `browser.credential` approval showing the teammate, field, origin and credential
   name.
3. On Fill, Studio unseals the value and runs `fill @ref <value>` directly in the VM.
4. The agent gets "filled <name>". It never sees the value.

The value never enters:

- tool results, errors and logs (a failing fill is reported without its arguments);
- the action log (it records the credential name);
- snapshots (the field is blanked by redaction);
- the screenshot of that action (no screenshot is taken while a credential is in a field
  on the page).

`TestCredentialNeverLeaks` checks all of these with a sentinel secret.

## Live view and takeover

The browser is one window, in three places: big on the teammate's **Browser** tab, with its
**Activity** and **Sites** beside it; as a side panel beside a teammate-owned sandbox's
terminal (**Browser** in the workspace toolbar); and beside the teammate's conversation, from
**Watch** on the browsing card in the thread or the rail's **Now**. The panel's expand button
opens the teammate's tab. Drag the panel's divider (or focus it and use the arrow keys) to
resize it; on phones it fills the screen. Closing it hides the viewer without stopping the
browser or handing control back.

The window has, top to bottom:

- **Tabs.** The teammate's own profile, then each open throwaway session with its sandbox,
  each named by its page's site. A dot marks a tab with a question waiting. A throwaway tab's
  X ends the session (`DELETE …/browser/sessions/{session}`).
- **Toolbar.** Back, Forward and Reload (`POST …/browser/go {to, session}`), the address bar,
  Downloads and the options menu (Put to sleep / Wake up, Network rules, Reset profile…). The
  shield in the address bar opens the site's info: what the teammate may do there (Ask
  first, Allowed, Blocked), its hidden fields and other rules.
- **Driver line.** Who drives and what now: the teammate drives, is waiting for you, isn't
  using the browser, you drive (with **Hand back**), or a throwaway session's sandbox.
- **Page.** The live view. The teammate's questions (`browser.action`,
  `browser.credential`) show on the page as cards with their answers, and leave the approval
  stack while they do. A sleeping browser shows its last page faded, with a field to open a
  page, which wakes it.

Browsing yourself takes the wheel: typing an address and pressing Enter, or Back, Forward and
Reload, take over first and then act. **Take the wheel** on the driver line, or on the page
while hovering it, takes over without navigating.

During takeover, tap the remote page to focus the mobile keyboard. The controls below
the page provide Ctrl, Esc, Tab, arrows, Backspace and Enter. Ctrl applies to the next key.
Composed text and paste go through the same filtered input channel; handing back or
disconnecting clears the local input buffer.

- **Live view.** `GET .../browser/live` is a WebSocket that Studio relays to agent-browser's
  stream server on guest port 9223, through the agent channel, like terminals.
  - Frames go to the UI as they are: `{type:"frame",data:<base64 JPEG>,metadata}`.
  - The UI may send `config`/`ack`. `input_mouse` and `input_keyboard` are forwarded only
    while the user drives.
  - Messages are re-encoded from an allow-listed shape, so nothing else reaches the
    stream.
- **Takeover.** Taking the wheel pauses agents. Agent calls wait up to 20 s, then fail with "the
  user is driving the browser…". The user types passwords straight into the page. Hand
  back resumes agent calls.
- **What the agent hears after a handback.** The session's `userTurn` (`turn.go`) keeps
  what Studio saw while the user drove: the pages the browser was on (from navigation and
  the stream's URL messages, 10 at most and a count of the rest) and downloads the user
  sent to a sandbox. Live-view clicks and keys reach the page as raw input and are not
  kept, as they may be a password. On handback it becomes a note, scrubbed of filled
  credentials, that the next agent call to that session gets in front of its result or
  error, once: how long the user drove, those pages and downloads, where the page is now,
  and that refs are stale.
- **Open a website.** Enter a URL in **Browser address** and press Enter; it takes the wheel
  first when an agent drives. Addresses without a scheme use HTTPS. A new browser starts on
  `about:blank`; the live view explains how to open its first page. The address follows
  page changes without replacing a draft while the user is typing.
  `POST .../browser/navigate` accepts only HTTP(S) URLs and requires a running browser
  under user takeover. Navigation is serialized with agent commands, recorded as a user
  action, and remains subject to the gateway's network rules and approvals.
- **Back, forward, reload.** `POST .../browser/go` with `to` `back`, `forward` or `reload`
  moves in the session's history under user takeover, like navigate, and answers with the
  status; anything else is 422.
- **Pointer input.** A click maps through the frame it was drawn from: that frame's metadata
  (device width, offset and page scale), its shape, and the letterbox of the canvas. The
  page's height comes from the frame's shape, not the metadata: agent-browser 0.39 reports
  the window size it launched Chromium with (1280×720), while new headless Chromium takes
  the height of its own UI off the window, so the page and its frames are 1280×577. Mapping
  through 720 put clicks below the pointer, by up to 143 px at the bottom of the page. Presses in the bars
  and above the page are not sent. A drag that leaves the canvas still releases at its edge. On touch, the tap is sent before the hidden input takes
  focus, so the keyboard does not move the page under the tap. Wheel deltas are pixels, with
  lines and pages converted.

## Ephemeral sessions

An agent can open a throwaway session next to the persistent profile:

- **Open.** `browser_open(url, ephemeral: true)` starts a second agent-browser session in the
  teammate's browser VM and returns its ID, `eph-` and eight hex digits. The session starts
  with an empty profile. Other tools take the ID as `session`.
- **Own storage.** Each session is its own agent-browser session, with its own daemon,
  Chromium, stream port and profile. Profiles and downloads live on a tmpfs that the guest
  mounts at `/run/studio-browser`, one 640 MiB mount for both slots. They never reach
  `/workspace` or the home directory, so a session's cookies, logins and downloads never
  reach the persistent profile.
- **Limit.** A teammate has at most two ephemeral sessions, across its sandboxes. A third
  `open` is refused until one is closed.
- **End.** A session ends on `browser_close`, after 10 minutes with no agent call and no user
  input in it, when the sandbox that opened it stops or is deleted, when the chat that opened
  it is deleted or its agent session is lost (orphaned), and when the browser VM stops (idle
  stop, Stop, delete, or a VM restart). Studio follows sandbox and chat events for this, and
  the idle sweep checks again every minute. A session the user has taken over is left alone
  until it is handed back. The action log records why a session ended. A call to an ended
  session says so, and the agent opens a new one. Studio's restart forgets the sessions; their
  profiles go with the VM.
- **Chat.** A session opened while exactly one chat of its sandbox is running belongs to that
  chat. See [Limitations](#limitations).
- **Ownership.** Only the sandbox that opened a session can drive it. Another sandbox of the
  same teammate is refused.
- **Policy and approvals.** The policy is the persistent profile's: the same deny and allow
  patterns, the same `browser.action` and `browser.credential` approvals. An approval names
  the session. A one-time grant holds for its session only. An "Always" rule is per origin for
  the teammate, so it covers every session.
- **Live view.** Each open ephemeral session is a tab of the window, after the profile's.
  Taking the wheel, Hand back and the toolbar act on the selected tab. Taking over one session
  pauses agent calls to that session only. A picked session that closes falls back to the
  profile. The user ends a session with its tab's X or **End session**
  (`DELETE …/browser/sessions/{session}`), which closes it as `browser_close` does and logs it
  as the user's.
- **History.** The action log files each session's actions under its ID, and the teammate's
  Browser tab marks them as ephemeral. Egress is attributed to the VM and its driving sandbox, not to a
  session, so the connection history does not split by session.

Two agent-browser sessions in one VM have not run live yet; see
[Verified vs pending live](#verified-vs-pending-live).

## Downloads

A file the browser saves lands in its session's downloads folder: `/workspace/downloads` for
the persistent profile (on the VM's disk, so it outlives a stop), and the session's tmpfs
folder for an ephemeral session (gone when the session closes). Studio hands one to a sandbox
on request:

- **List.** The guest lists a session's folder: regular files only, newest first, at most 100.
  Hidden files, Chromium's `.crdownload` partials, links and folders are not downloads. A
  folder that does not exist yet has none. `browser_downloads` lists the persistent profile
  and the ephemeral sessions the calling sandbox opened; the browser window's **Downloads**
  button lists every session of the teammate's browser, while the browser runs.
- **Copy.** A copy streams the file from the browser VM through the host into the target
  sandbox's `downloads/` folder, with `Hub.Upload` (the upload path, the same name checks and
  the 100 MiB limit). The sandbox's guest creates `downloads/` when it is missing and keeps an
  existing file: a taken name gets a free one, `name (2).ext`. The host copies bytes only: a
  download is never opened, run or extracted on the host, and the agent's tool result says it
  is data from the web.
- **Who.** An agent copies a download of the teammate's persistent profile, or of an ephemeral
  session its own sandbox opened (another sandbox of the teammate is refused, as for driving
  it). The user sends any of the teammate's downloads to any of the teammate's sandboxes from
  the Browser page (`POST …/browser/downloads/send`). A stopped browser has no downloads to
  list or copy. Each copy is in the action log as `send download`, with the outcome.

## History

- **Action log.** Each action is logged: actor, sandbox, action, target, URL, outcome and
  a JPEG screenshot of at most 180 KiB. The log is stored in sqlite (migration 017) and under
  `<data>/browser/`.
- **Bounds.** The log keeps 500 actions per teammate and 200 MiB of screenshots overall,
  dropping the oldest first.
- **Replay.** The Browser tab's **Activity** groups the log by session (one session per VM
  start, and one per throwaway session), newest first, with a picture of each step that
  changed the page. Clicking a step shows the page as it was after it, in the window, with
  previous and next steps and **Back to live**. The side panel folds the latest session's
  steps under the window, and the conversation's browsing card shows its last three.
- **Sites.** The Browser tab's **Sites** lists every origin with a rule or a visit. Each has
  a picker for clicks and typing, which sets or removes one rule for every action on the whole
  origin (`action: "*"`), and chips for its narrower rules and hidden fields, each removable.
  **Add a rule or hide a field** takes a rule for one action, role or label glob, or a
  sensitive field.

## Stealth mode

For sites that turn automated browsers away, a teammate's browser can run in stealth mode:
**Browser** in the teammate's settings (`PUT …/teammates/{teammate}/browser/stealth {on}`,
the `browser_stealth` column). Off by default.

What changes, all of it Chromium's own behavior rather than injected scripts:

| | Default | Stealth |
|---|---|---|
| User agent | `… HeadlessChrome/<major>.0.0.0 …` | `… Chrome/<major>.0.0.0 …`, the one desktop Chromium sends on Linux |
| Client hints (`Sec-CH-UA`, `navigator.userAgentData`) | Chromium's brands | the same brands |
| `navigator.webdriver` | `true` | `false` (`--disable-blink-features=AutomationControlled`) |
| `screen` | 800×600, smaller than the window | 1920×1080 |
| Agents' clicks | the pointer jumps to the target | agent-browser's `--input-mode human`: a curved path to it, about 130 ms more per click |

- **How.** `BrowserCommand.Stealth` carries the teammate's setting on every command; the
  broker sets it in one place (`Service.run`). In stealth mode the guest starts Chromium
  through a script it writes to `/run/studio-browser/chromium-stealth` (root-owned, on the
  tmpfs it mounts there), so it works in browser VMs made from any image. The script adds
  Chromium's switches itself: agent-browser's `--args` splits at commas, which a user agent
  has, and its `--user-agent` sets a CDP override without client hints, so the page saw a
  Chrome without brands. The screen switch comes after agent-browser's own, so it wins.
- **Switching.** The other executable changes agent-browser's launch options, so its next
  command restarts the browser, with the same profile and logins. Every command of a session,
  `close` included, must carry the same setting: a command with other launch options makes
  agent-browser relaunch Chromium first.
- **What it does not do.** The WebGL renderer is still SwiftShader, as in any VM without a
  GPU; the page is still driven over CDP, which sites that look deeper detect; CAPTCHAs are
  not solved. Stronger options are another engine: agent-browser's Obscura engine has stealth
  of its own, but rejects `--profile` and `--ca-cert` and runs headless only without a
  screencast, so it could keep neither logins, the environment CA nor the live view. Camoufox
  (Firefox) would replace agent-browser, which drives CDP engines only.

## Verified vs pending live

**Verified with fakes** (`go test ./internal/browser ./internal/agentcall ./internal/api
./cmd/studio`):

- policy table and pattern validation;
- redaction fixtures;
- credential sentinel and refusals;
- identity from the channel;
- the paused gate;
- approve and remember;
- history and screenshot bounds;
- idle stop;
- live view relay and input filtering;
- MCP tool list and image results;
- the API (status, takeover, patterns, approval view, remember, teammate delete);
- ephemeral sessions: the lifecycle, the limit, idle expiry on a fake clock, close, the VM
  stop, the end of the owning chat or sandbox (`owners_test.go`), ownership, takeover, tool arguments, approval and history marks, and the live view
  port of each session;
- the guest's slots: argv, environment, stream port and session checks (`internal/guest`);
- the browser VM's HTTPS: termination, per-request holds with the request's method, path and
  site, site approvals for the session, the tracker list and request rules (`internal/gateway`,
  `internal/policy`, `internal/browser`), and the site card in e2e through the request hook;
- downloads: the guest's listing and reads (regular files only, no links, partials or hidden
  names, the upload size limit), the folder creation of an upload, the Hub's stream, the tools'
  identity and ownership, the copy into `downloads/` and its log entry, and the API;
- lost replies: an open that ran, a click whose outcome is unknown and an unreachable browser
  (`internal/browser`);
- the channel's write limit: a 740 KB reply through the guest's yamux session, with every
  write under 16 KiB (`internal/guest`, `TestChannelWritesStayBounded`);
- localhost: the guest's listeners and their relay, the reserved and taken ports
  (`internal/guest`), and a page on a forwarded port through the Hub, the broker and the fake
  guests, with a port that stopped and refused forwards (`internal/e2e`,
  `TestBrowserLocalhostIsTheDrivingSandbox`).

**Verified live on Linux amd64 (2026-10-11):**

- Stop then Start from the port-3000 development UI succeeds with the existing profile.
- agent-browser 0.39.0 launches with the guest's executable, profile and download flags.
- Chromium loads `https://git.studio.internal/` using the environment CA in its NSS
  database and still rejects an unrelated self-signed certificate.
- A fresh live-view connection receives frames and renders them in the UI.
- The address form opens a page during takeover, with the actual page content visible in
  the streamed canvas. A blank start page shows guidance; navigation without takeover
  and non-HTTP(S) addresses are refused. Draft preservation after a failed navigation
  was also checked with a mocked error response.

**Verified live on Linux amd64 (2026-10-15):** localhost. A browser call from a sandbox
opened `http://localhost:5173/` served there by `python3 -m http.server` on `127.0.0.1`, and
its snapshot showed the page; a server on `::1` only opened at both `localhost` and
`127.0.0.1`; once the server stopped, the open failed with `ERR_CONNECTION_REFUSED`.

**Verified on Linux amd64 with agent-browser 0.39.0 and Chrome for Testing 153, outside a
browser VM (2026-10-15):**

- Frame metadata: the page was 1280×577 (`innerHeight` 577, `outerHeight` 720), frames
  were 1280×577 JPEGs, and their metadata said `deviceHeight` 720.
- Stealth mode through the same script: `navigator.webdriver` false, the user agent without
  `HeadlessChrome`, the brands kept in `navigator.userAgentData` and in the `Sec-CH-UA`
  request headers, the screen 1920×1080; `--input-mode human` clicks were trusted events with
  a curved path (8 moves instead of 1), 162 ms instead of 33 ms.

**Pending a live run:**

- Browser commands with screenshots over the channel, since the 2026-10-13 fix. The channel
  limits were measured on a scratch Studio with file reads of the same reply shape (see
  [guest agent](guest-agent.md#limits-of-the-channel-on-msbs-vsock)). The browser VM was not
  run then: a screenshot-carrying `open` or `click` should succeed, and a failing one should
  say what the page is.
- The browser VM's HTTPS through the gateway's terminated connections: Chromium's pages load
  with the environment CA only, and its requests carry the `Sec-Fetch-*` headers the request
  decisions read (see [network](network.md#hold-and-ask)). The site approvals and tracker
  denials were checked with fakes and a fake runtime, not in a page.
- The browser image and agent-browser's native binary on arm64.
- Chromium's downloads into the folder with agent-browser 0.39's `--download-path` flag: a
  file the page saves should show in `browser_downloads`, and copy into `downloads/`.
- Stealth mode in a browser VM: Debian's `chromium --version` gives the user agent's major
  version, and switching restarts the browser with the live view still connected.
- Snapshot details from a live Chromium:
  - password fields show no AX value or a masked one;
  - static text children don't carry values;
  - `get text` returns `{text}` and `get attr` returns `{value}`.
- `wait <ms>` and `scroll <dir> <px>` argument forms.
- Egress from Chromium goes through the gateway like other sandboxes, including
  QUIC/HTTP3 fallback.
- Two agent-browser sessions in one VM. Each ephemeral slot runs with its own `--session`,
  `--profile` on the tmpfs and `AGENT_BROWSER_STREAM_PORT`. `--session` is the documented way
  to run several browsers, but 0.39.0 has not run two yet. If a live run shows it cannot,
  `browser_open(ephemeral: true)` must refuse with an explanation; that fallback is not built.
- Ephemeral cleanup: `close`, then removing the session's socket and pid files under
  `~/.agent-browser`, then killing processes whose environment or command line names the
  session or its profile. The file names and the environment match are not checked against
  0.39.0.
- The tmpfs mount at `/run/studio-browser` in the guest, and the memory of two more Chromium
  processes in the 2 GiB VM.
- Live mouse mapping in a browser VM since the frame-shape fix, and the Playwright run that
  clicks known targets at several viewport sizes and zoom levels.

## Limitations

- Background traffic from an idle page (polling, websockets) is attributed to the last
  sandbox that drove.
- A "sensitive" pattern blanks a field's value line. A textarea whose own text forges
  snapshot lines could still slip text past it.
- Releases build and publish the browser image and pin it by digest
  (`version.BrowserImage`). With another digest-pinned base image, Studio uses
  `sandbox-studio-browser:<version>` from the same registry.
- A credential fill runs inside the approval's decide request, bounded by the command
  timeout.
- An ephemeral session is another Chromium in the same VM, so heavy use competes with the
  persistent profile for memory.
- A browser's requests are attributed to the page the broker drove last (its top-level site),
  which is exact while one page is open. A site approval covers a subresource only while every
  web page the browser has open is on an approved site.
- A tool call names its sandbox, not its chat, so a session's chat is a guess: the one chat
  of the sandbox that was running when the session opened. With none or several running, the
  session belongs to no chat and ends only by the other rules. A terminal session that runs
  an agent outside a chat has no chat either.
