# Browser broker

Agents browse the web through a real Chromium. It runs in a separate microVM per persona,
and Studio is the only client: agents get MCP tools, never CDP. This doc covers the M7
implementation of PLAN §6.8.

Code:
- `internal/browser`: the broker, policy, redaction, credentials, history and live view relay.
- `internal/sandboxes/browser.go`: the VM lifecycle.
- `internal/guest/browser.go`: the guest side, which runs agent-browser.
- `internal/agentproto/browser.go`: the wire format.
- `internal/agentcall/browser.go`: the MCP tools.
- `internal/api/browser.go`: the API.
- `cmd/studio/browser.go`: the wiring.
- `images/browser`: the image.
- The UI: `web/src/routes/e.$env.browser.tsx`, plus `browser-live.tsx` and
  `browser-approval.tsx` in `web/src/components`.

## One browser VM per persona

Each persona gets one browser VM: a sandbox row of kind `browser`, named `browser-<persona>`.
The API, terminals and previews hide it.

Why per persona:

- **The profile is the identity.** Cookies and logins belong to whoever logged in. A persona
  is the unit that owns accounts. Its sandboxes are the same "person" working on several
  things, so they share a session. Two personas never share cookies.
- **Per environment would mix identities.** Per sandbox would lose logins on every new
  sandbox and multiply 2 GiB Chromium VMs.
- **One profile, one browser.** Agent calls are serialized per persona. Only one sandbox
  drives at a time, and egress is attributed to it.

**Lifecycle:**

- **Created lazily.** The first browser call, or Start in the UI, creates the VM from the
  browser image: 2 CPUs, 2 GiB of memory and a 2 GiB disk for the profile.
- **Stopped when idle.** A VM with no call and no live viewer for 10 minutes stops. The
  profile disk stays.
- **Deleted on demand.** Delete in the UI removes the VM and its profile but keeps the
  history. Deleting the persona also drops its history, and its rules go with it. Deleting
  a persona is refused while it still has agent sandboxes.

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
- **Egress goes through the same gateway, MITM CA and rules.**
  - While an agent drives, the gateway's `Driver` hook evaluates the browser VM's
    connections with the driving sandbox's rules. Domain approvals name that sandbox.
  - While the user drives (takeover), the browser VM's own rules apply: its persona's rules
    plus the environment's.

## Tools

`studio-agent mcp` adds these tools. Each one is forwarded over the call channel as a
`browser_*` method:

- `open(url)`, `snapshot`, `click(ref)`, `fill(ref, text)`, `type(ref, text)`, `press(key)`,
  `select(ref, value)`, `scroll(direction, px)`, `get_text(ref?)`
- `screenshot`, which returns MCP image content
- `wait(ms | text | url)`, `back`
- `fill_credential(ref, name)`

**Identity comes from the channel only.** The hub knows which sandbox's agent connection
carried the call. That gives the persona, and the persona gives the browser. A
`persona`, `sandbox` or `env` field in the arguments is ignored. A sandbox without a persona
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
- **Approvals.** A `browser.action` approval shows the persona, sandbox, action, origin,
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

1. Checks that `name` is a vault secret of the environment that the persona may use.
2. Raises a `browser.credential` approval showing the persona, field, origin and credential
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

## History

- **Action log.** Each action is logged: actor, sandbox, action, target, URL, outcome and
  a JPEG screenshot. The log is stored in sqlite (migration 017) and under
  `<data>/browser/`.
- **Bounds.** The log keeps 500 actions per persona and 200 MiB of screenshots overall,
  dropping the oldest first.
- **Replay.** The Browser page groups the log by session (one session per VM start) and
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
- the API (status, takeover, patterns, approval view, remember, persona delete).

**Pending a live run.** None of the following is confirmed against a real VM:

- The image builds on amd64 and arm64, and npm `agent-browser@0.39.0` ships its native
  binary for both.
- The daemon starts with `--executable-path`, `--profile` and `--download-path` as the
  guest passes them, and Chromium trusts the environment CA through the NSS db
  (`certutil`).
- The stream server listens on 9223 and starts frames on connect. The input message
  fields and the frame metadata (`deviceWidth`/`deviceHeight` for click mapping) are
  agent-browser's.
- Snapshot details from a live Chromium:
  - password fields show no AX value or a masked one;
  - static text children don't carry values;
  - `get text` returns `{text}` and `get attr` returns `{value}`.
- `wait <ms>` and `scroll <dir> <px>` argument forms.
- Egress from Chromium goes through the gateway like other sandboxes, including
  QUIC/HTTP3 fallback.

## Limitations

- Downloads stay in the VM's download directory; nothing hands them to the agent yet.
- Background traffic from an idle page (polling, websockets) is attributed to the last
  sandbox that drove.
- A "sensitive" pattern blanks a field's value line. A textarea whose own text forges
  snapshot lines could still slip text past it.
- The release workflow does not build or publish the browser image yet. With a
  digest-pinned base image, Studio uses `sandbox-studio-browser:<version>`.
- A credential fill runs inside the approval's decide request, bounded by the command
  timeout.
- Browser approvals show in the notification text as generic approvals.
