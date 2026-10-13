# Browser broker

Agents browse the web through a real Chromium. It runs in a separate microVM per teammate,
and Studio is the only client: agents get MCP tools, never CDP. This doc covers the
implementation (milestone M7).

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
- The UI: `web/src/routes/e.$env.browser.tsx`, plus `browser-live.tsx` and
  `browser-approval.tsx` in `web/src/components`.

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

- **Created lazily.** The first browser call, or Start in the UI, creates the VM from the
  browser image: 2 CPUs, 2 GiB of memory and a 2 GiB disk for the profile.
- **Stopped when idle.** A VM with no call and no live viewer for 10 minutes stops. The
  profile disk stays.
- **Deleted on demand.** Delete in the UI removes the VM and its profile but keeps the
  history. Deleting the teammate also drops its history, and its rules go with it. Deleting
  a teammate is refused while it still has agent sandboxes.

The image is `sandbox-studio-browser`, at the same version as the base image. Build it with
`make image-browser`.

## Isolation and egress

- **Only Studio reaches the VM, through its studio-agent channel:**
  - Commands use the `browser` request kind: a bounded argv for agent-browser plus an
    optional stdin for `batch`.
  - The live view uses `KindTCP` to the guest's loopback port 9223.
  - Nothing listens on the VM beyond loopback. No CDP port is ever exposed.
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
    its site (see [network](network.md#hold-and-ask)). They end when the VM stops.

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

Every tool takes an optional `session`, the ID that `open(ephemeral: true)` returned. Without
it a tool drives the persistent profile. See [Ephemeral sessions](#ephemeral-sessions).

**Identity comes from the channel only.** The hub knows which sandbox's agent connection
carried the call. That gives the teammate, and the teammate gives the browser. A
`teammate`, `sandbox` or `env` field in the arguments is ignored. A sandbox without a teammate
has no browser. A browser VM cannot call tools.

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

On a teammate-owned sandbox, **Browser** opens the same teammate browser beside the
workspace. Drag the divider (or focus it and use the arrow keys) to resize it. On phones
the panel fills the workspace. Closing it hides the viewer without stopping the browser
or handing control back; **Hand back** remains an explicit action.

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
- **Takeover.** "Take over" pauses agents. Agent calls wait up to 20 s, then fail with "the
  user is driving the browser…". The user types passwords straight into the page. Hand
  back resumes agent calls.
- **Open a website.** While taking over, enter a URL in **Browser address** and press
  Enter or **Open**. Addresses without a scheme use HTTPS. A new browser starts on
  `about:blank`; the live view explains how to open its first page. The address follows
  page changes without replacing a draft while the user is typing.
  `POST .../browser/navigate` accepts only HTTP(S) URLs and requires a running browser
  under user takeover. Navigation is serialized with agent commands, recorded as a user
  action, and remains subject to the gateway's network rules and approvals.
- **Pointer input.** A click maps through the frame it was drawn from: that frame's metadata
  (device size, offset and page scale) and the letterbox of the canvas. Presses in the bars
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
  input in it, when the sandbox that opened it is gone, and when the browser VM stops (idle
  stop, Stop, delete, or a VM restart). A call to an ended session says so, and the agent opens
  a new one. Studio's restart forgets the sessions; their profiles go with the VM.
- **Ownership.** Only the sandbox that opened a session can drive it. Another sandbox of the
  same teammate is refused.
- **Policy and approvals.** The policy is the persistent profile's: the same deny and allow
  patterns, the same `browser.action` and `browser.credential` approvals. An approval names
  the session. A one-time grant holds for its session only. An "Always" rule is per origin for
  the teammate, so it covers every session.
- **Live view.** While an ephemeral session is open, the live view and the Browser panel show
  a session switcher: **Profile**, then each open ephemeral session. Take over, Hand back and
  the address form act on the selected session. Taking over one session pauses agent calls to
  that session only. A picked session that closes falls back to Profile.
- **History.** The action log files each session's actions under its ID, and the teammate's
  Browser tab marks them as ephemeral. Egress is attributed to the VM and its driving sandbox, not to a
  session, so the connection history does not split by session.

Two agent-browser sessions in one VM have not run live yet; see
[Verified vs pending live](#verified-vs-pending-live).

## History

- **Action log.** Each action is logged: actor, sandbox, action, target, URL, outcome and
  a JPEG screenshot. The log is stored in sqlite (migration 017) and under
  `<data>/browser/`.
- **Bounds.** The log keeps 500 actions per teammate and 200 MiB of screenshots overall,
  dropping the oldest first.
- **Replay.** The teammate's Browser tab groups the log by session (one session per VM start) and
  steps through the screenshots.

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
  stop, ownership, takeover, tool arguments, approval and history marks, and the live view
  port of each session;
- the guest's slots: argv, environment, stream port and session checks (`internal/guest`);
- the browser VM's HTTPS: termination, per-request holds with the request's method, path and
  site, site approvals for the session, the tracker list and request rules (`internal/gateway`,
  `internal/policy`, `internal/browser`), and the site card in e2e through the request hook.

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

**Pending a live run:**

- The browser VM's HTTPS through the gateway's terminated connections: Chromium's pages load
  with the environment CA only, and its requests carry the `Sec-Fetch-*` headers the request
  decisions read (see [network](network.md#hold-and-ask)). The site approvals and tracker
  denials were checked with fakes and a fake runtime, not in a page.
- The browser image and agent-browser's native binary on arm64.
- Input message fields and frame metadata (`deviceWidth`/`deviceHeight`) for click mapping.
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
- Live mouse mapping in a real browser, and the Playwright run that clicks known targets at
  several viewport sizes and zoom levels (PLAN N2).

## Limitations

- Downloads stay in the VM's download directory; nothing hands them to the agent yet.
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
- Ephemeral sessions do not end with the agent's chat or session, which PLAN N11 also
  proposed: the broker gets no signal from the agent's MCP server. They end as described
  above.
