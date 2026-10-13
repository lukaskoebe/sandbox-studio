# Network, secrets and CA

Every TCP connection from a sandbox, including from containers inside it, goes through
Studio's gateway. Code: `internal/gateway` (SOCKS5, naming, interception), `internal/policy`
(rules and hold-and-ask), `internal/dnsproxy` (per-sandbox resolvers), `internal/caddyrule`
(Caddy rules), `internal/secrets` (vault), `internal/ca` (per-environment CA).

## The fence

microsandbox's own policy, fixed at creation (see [sandboxes](sandboxes.md#network)):
DNS only to the sandbox's resolver, TCP only to public addresses and only through the
gateway's SOCKS5 port, everything else denied. UDP is blocked, so QUIC falls back to TCP.
Private and host addresses are unreachable; host services are virtual hosts (below) or the
[agent channel](guest-agent.md). Company hosts behind a VPN are the exception: the gateway
dials them, for names on the [private hosts](#private-hosts-company-vpn) list.

## DNS

Each sandbox has its own loopback resolver (UDP and TCP, `dnsproxy`), so names can be mapped
back per sandbox. It forwards to the host's system resolvers and records A/AAAA answers for
IP→name lookups. Policy is applied at connect time, and the resolver shapes two answers:
a name that a [deny rule](#dns-level-deny) refuses gets NXDOMAIN, and a name on the
[private hosts](#private-hosts-company-vpn) list gets virtual addresses.

## Gateway pipeline

Per connection (`gateway.go`):

1. Identify the sandbox from its SOCKS5 user (the sandbox ID) and password (derived from
   the gateway key, which only Studio and microsandbox know).
2. Name the destination: the SOCKS domain if the client sent one, else the sandbox's DNS
   map, else sniff TLS SNI or the HTTP Host header. Sniffing waits up to 5 s on ports 80 and
   443, 300 ms when the name is already known, 1 s otherwise, because some protocols (SSH)
   wait for the server to speak first. A name the client sends is lower-cased and loses one
   trailing dot; then it must be a host name or IP address, with no spaces and no wildcard.
   A sniffed name that fails this is ignored; a SOCKS domain that fails it is refused.
3. Names under `*.studio.internal` (DNS answer `198.18.0.1`) are virtual hosts served by the
   gateway itself, e.g. `git.studio.internal` for [git review](git-review.md).
4. Ask the policy (below). Without a decision, the connection is held.
5. Act:
   - `allow`: dial the **name** (not the IP the sandbox picked) and splice the bytes. The dialer
     takes public addresses only, plus the private addresses of a private host.
   - `proxy` and `caddy` rules, any web connection to a host a secret is bound to, and every
     HTTPS connection of a browser VM are intercepted: TLS is terminated with a certificate from
     the environment's CA, then each HTTP request is checked, rewritten and sent on over a new
     connection to the rule's upstream (the same host unless the rule names another). Non-web
     traffic under such a rule fails.
   - `deny`, a dismissed approval, or no answer in time: HTTP and intercepted TLS get a
     readable 403 (`X-Sandbox-Studio: refused`) telling the agent what happened and whether
     to retry; other protocols are closed.

Upstream dials refuse non-public addresses, so neither the gateway nor Caddy upstreams reach
the host or private networks. The one exception is a name on the environment's
[private hosts](#private-hosts-company-vpn) list, which may reach its private addresses.
`DialPublic`, which Studio uses for its own forge connections, never makes that exception.

## Rules

A rule (`store.Rule`) has a host pattern, ports (empty = any port), an action (`allow`,
`proxy`, `caddy`, `deny`), a scope (sandbox, teammate or environment) and, for `proxy`,
headers to set (values may use `{secret.NAME}`) and where its requests go (see
[proxy rule overrides](#proxy-rule-overrides)); for `caddy`, a Caddyfile snippet.

A rule also records its origin: `manual` for rules written on the Rules page or through the
API, `approval` for rules created by approving or denying a request, and `reviewer` for a rule
the [AI reviewer](#ai-reviewer-assisted) made. `curated` is reserved: the curated list allows
hosts as a decision of the level, not as a rule, and Activity shows it (see
[security levels](#security-levels)). The origin is read-only and set when the rule is created;
rules from before origins existed are `manual`.

A rule can expire (`expiresAt`). From that moment it stops matching, and a sweep deletes it
within a minute. The approval card's Options offers "Allow for 1 hour"; the API takes
`expiresAt` when a rule is created and on an allow or deny decision (it must be in the
future). The Rules tab counts down the time left.

A rule counts the connections it decides (`hits`) and when it last did (`lastHitAt`). The
policy counts them in memory and writes the counts about once a minute and at shutdown, so
the figures can lag by a minute. A rule is unused when it has decided nothing in 30 days and
is at least 30 days old; the Rules tab marks those, and `unused=1` shows only them.

Matching (`policy/match.go`):

- An environment-wide `deny` always wins, so it works as a fence.
- Otherwise sandbox rules beat teammate rules, which beat environment rules. Teammate rules
  apply only to sandboxes the catalog says that teammate owns.
- Within a scope: the most specific host pattern wins (`*.example.com` also matches
  `example.com`), then rules that name ports, then rules that name a request, then `deny` over
  `allow`.

Only when no rule matches does a [security level](#security-levels) decide, and then the user.

### Request rules

An allow or deny rule can also name the requests it covers: `method` (`GET`, `HEAD`, `POST`,
`PUT`, `PATCH`, `DELETE` or `OPTIONS`; empty is any) and `pathPrefix` (a path ending in `*`,
such as `/repos/*`; empty is any path). Such a rule decides only requests on connections
Studio terminates, and a connection is terminated when a rule names its host's requests: the
browser VM's HTTPS always is, and so is a connection that a host rule allows when a request
rule names its host. The other requests of the host are decided by its host rules as before.
Connections are matched without request rules, so a connection is never decided by one.

Paths are matched after cleaning (dot segments and doubled slashes resolved). An allow covers
only a path that is already clean: `/repos/../x` isn't covered by `/repos/*`, since the upstream
might read it as `/x`. A deny matches the cleaned path, so it can't be walked around.

A browser request's approval offers the request's first directory as a rule, such as `GET
/repos/*` (see [approvals](approvals.md#network-approval-cards)).

## Trackers

A browser VM is denied the built-in tracker list without being asked: `internal/policy/trackers.txt`,
shipped with Studio and versioned. Its domains are measurement, audience, session-replay and
ad-delivery services, in the spirit of EasyPrivacy; an entry also covers its subdomains. The
check comes after an explicit rule (an allow rule for the host wins, a deny rule denies) and
before any approval, site approval or security level. The environment switch "Block trackers in the browser"
(`environments.block_trackers`, on by default) turns the list off. Each denial shows in Activity
as "Denied · tracker", one row per host with its attempts.

## System VMs

The VMs of subscription logins (`claude-login`, `codex-login`, `claude-utility` and
`codex-utility`, see [subscription logins](teammates.md#subscription-logins)) reach only the hosts
their kind needs, listed in `internal/policy/system.go`. The rules still decide first, so a deny
rule the user set holds. Any other host is refused without a hold and without an approval card.

A template build's VM (`build-<job>`) reaches the hosts its own steps download from on ports 80
and 443 without anyone being asked, at every security level: the Debian archives and Docker's
apt repository, mise's version lists, Node.js, Go, Python's version index and its precompiled builds on GitHub and the
Nix releases (`BuildHosts` in `internal/policy/system.go`). A deny rule still wins. Any other host,
such as one a spec's `setup` script fetches from, is held and asked as for any sandbox.

## The Network page

The page (`web/src/routes/e.$env.network*.tsx`) has three tabs, each a route. The private
hosts list is edited under Settings → Network:

- **Rules** starts with the environment's [security level](#security-levels), then lists the
  rules, the [curated list](#curated-list) with a switch for each entry, and the tracker list with
  its switch. The rules' search and scope, action, origin and unused
  filters are URL search params (`q`, `scope`, `action`, `origin`, `unused=1`), so a filtered
  view can be shared. Each rule shows its origin as a chip, and an unused rule an "unused"
  chip. Delete unused removes the unused rules the filters show, after a confirmation. Rules are grouped
  by registrable domain: the last two labels, or three under a short list of two-label
  suffixes such as `co.uk` (`web/src/lib/network-rules.ts`). No public suffix list is used.
- **Requests** lists the pending network approvals (connections and browser requests); Allow, Deny and Ignore work as on the
  approval card.
- **Activity** lists decided network approvals, newest first, up to 50: the ones you decided, and
  the ones a level, the curated list or the [AI reviewer](#ai-reviewer-assisted) allowed or denied.
  A reviewer's decision has a Revert.

`/e/$env/network` opens Requests when any wait for a decision, and Rules otherwise.

## Hold and ask

With no matching rule, and no [security level](#security-levels) allowing it, the gateway holds
the connection (the TLS ClientHello stays
buffered) for up to 60 s and raises a `network` approval. Repeated attempts for the same
subject merge into one pending approval with an attempt counter.

Answers (`policy.Resolve`): allow or deny with a new rule (scope sandbox, teammate or
environment; host pattern from the suggestions: the name or its registrable domain as a
wildcard, never a public suffix), or dismiss. Rules created this way cover 80 and 443 for web
traffic, else the exact port. A new or changed rule settles other pending approvals it
covers and wakes held connections, so an approved `npm install` finishes without a retry.

"Allow once" and "Deny once" (in the card's Options) create no rule. They decide the
connections held for this request, and the next connection to the host asks again. Activity
shows them as "Approved once" and "Denied once"; a connection denied once gets a 403 that says
the user denied it.

Connections from one sandbox that arrive within 3 seconds of each other show as one card on the
approval stack, with Allow all, Review individually and Deny all (see
[approvals](approvals.md#realtime-and-ui)). A browser's requests group by the page they belong
to instead: one sandbox's pending requests to one top-level site are one card, titled with the
site. A navigation keeps its own card, since it needs the site decision.

A browser VM's HTTPS is held differently. Its connections are terminated first, with no hold
(see [interception](#interception-and-secrets)), and each request that no rule covers is held
as a `network.request` approval. That approval names the request: method and path (never the
query), the top-level site it belongs to, and the Referer the browser sent, which is shown and
never trusted. A navigation's site is its own host; a subresource belongs to the page the
browser drove last (`browser.TopLevel`). Approvals are keyed by host and site, so a page's
requests to one host share one card.

A navigation's card offers "Allow site <site> for this browser session". That approves the
site for the browser session, not through a rule: its navigations are allowed, and so are its
subresources while every page the browser has open is on an approved site (a subresource's page
isn't known for certain when several are open, so none may be unapproved). The approved sites
end with the browser session: when the browser VM stops, idles out or is removed. The pending
requests of the page's site are approved with it.

60 s comes from spike S6: it covers curl, git, npm and apt, and pip succeeds on retry. Node
`fetch` gives up after 10 s, so the UI must show the request quickly.

## Security levels

Each environment has one level, which decides what happens when no rule matches. Explicit rules
always win, in either direction, and so do the tracker list and the sites a browser session was
approved for, which come before the level. Each teammate can override the level for its own
sandboxes; "Environment default" follows the environment. The level is set on the Network page's
Rules tab, in the teammate's settings, and in the setup's network step.

| Level | No rule matches → | For whom |
| --- | --- | --- |
| `locked` | ask for every host, as before levels | sensitive work |
| `guarded` (default for new environments) | allow the [curated list](#curated-list); ask for the rest | most users |
| `assisted` | allow the curated list; the [AI reviewer](#ai-reviewer-assisted) decides the rest, and asks when it is unsure | people who find prompts tiring |
| `open` | allow public names and public addresses, and log them in Activity as "Allowed · level Open" | throwaway experiments |

The decision table is `levelInput.decide` in `internal/policy/level.go`. It reads the level,
whether the host is on the curated list, whether the request carries a placeholder of a secret the
host isn't bound to, and whether the host is a name or a public address. The tests cover every
combination, and the engine tests cover the level with rules, the tracker list and teammates.

No level does these things:

- allow a request that carries a placeholder of a secret its host isn't bound to: the request is
  asked about, and the gateway refuses to send the placeholder's value anywhere else. The AI
  reviewer never sees such a request. A connection the gateway doesn't terminate isn't read, so its
  bytes pass as before: a placeholder there is only a random name that no host can turn into a
  secret;
- allow a private or otherwise non-public address, such as loopback, RFC 1918, carrier-grade NAT or
  link-local. The dialer refuses them anyway, unless a [private host](#private-hosts-company-vpn)
  names them;
- allow a [private host](#private-hosts-company-vpn), even at `open`: the AI reviewer never sees
  one either. Only a rule you make or an approval reaches a company host;
- lift the tracker list for a browser VM, or an explicit deny rule;
- send a secret to a host it isn't bound to. Secrets reach bound hosts at every level.

An allow by a level or the curated list makes no rule. It is recorded in Activity, with the
reason (the curated entry's name, or the level), and a host's repeated connections count as its
attempts. Changing the level applies to the connections made after the change; approvals that are
already waiting stay in the inbox.

Existing environments were made `locked` by migration 030, which is what Studio did before levels,
so an upgrade opens nothing up. New environments start at `guarded`. An environment import starts at
`guarded` as well, since the file carries neither the level nor the curated switches. Where no
provider has a utility model, `assisted` asks like `guarded`, and the Rules tab says so.

## Curated list

The curated list is development infrastructure that Studio ships as data:
`internal/policy/curated.json`, embedded in the binary and versioned by its `version` field. It has
package registries, toolchain downloads, container registries, forge read access and the CDNs they
use. Each entry has a stable `id`, a name, a group, its exact host names and its ports (80 and 443
only, and 80 only where the source needs it).

The `guarded`, `assisted` and `open` levels allow an entry's hosts on its ports when no rule matches.
The Rules tab lists the entries with a search and a switch for each one. A switched-off entry is
asked about like any other host. An environment stores its switched-off IDs (`curated_off`, migration
030).

To propose a change, open a pull request that edits `internal/policy/curated.json`: add or change an
entry with a stable ID, the exact host names, and only the ports its source needs; give the
reason in the description, showing that the hosts are development infrastructure and not trackers
or general content hosts; and bump `version` (`YYYY.M.N`). `TestCuratedListIsWellFormed` checks the
shape of the file.

## AI reviewer (assisted)

At `assisted`, a request that no rule and no curated entry covers goes to an AI reviewer before the
user is asked. The reviewer is the environment's utility model, the one the memory consolidation
uses: of the teammates whose provider has a utility model, the one that has had it longest
(`agentmem.EnvironmentUtility`). Without one, the request is asked about.

- **What it sees:** the host and port; the method and path, for requests Studio reads (a browser
  VM's HTTPS); the site of the page, for a browser request; and the sandbox's current task, its last
  user prompt, cut to 500 characters. Never secrets, bodies, headers other than the ones named here,
  or placeholders. A request that carries a placeholder is never sent: it is asked about.
- **How it is asked:** the request is one JSON object between `<request>` and `</request>`. The
  host, path, page and task are untrusted data, and the system prompt says so. JSON escaping keeps
  them from closing the tags. The answer must be exactly one JSON object,
  `{"decision": "allow" | "ask" | "deny", "reason": "..."}`, with nothing before or after it.
  Anything else, a timeout of 8 seconds, or an error asks the user.
- **What it decides:** an allow or a deny makes a rule of origin `reviewer` for the sandbox, the
  host and the ports an approval would use, which expires after 24 hours. Activity shows the
  decision and its reason, with a Revert. Reverting deletes the rule; the host is then asked about
  for the rest of the session, and not reviewed again for that task.
- **How often:** a question is asked once per sandbox, task and host:port for the session. Connections
  that arrive while a question is open wait for its answer. A new prompt is a new task, so the
  host is asked about again.
- **Costs:** the calls go to the provider, and are not counted in the memory extraction budget.
- **Tests** use a fake model. Nothing calls a real API in them.

## DNS-level deny

A deny rule that covers every port of a name refuses the name at lookup too: the sandbox's
lookups of it get NXDOMAIN, so tools fail at once instead of waiting on a held connection. The
rule may be scoped to the sandbox, its teammate or the environment. A name that some rule with
ports also covers is never refused at lookup, since that rule may allow it on other ports. A
name nothing refuses still resolves, so that it can be approved. Connections are still checked
when they are made, so a refusal here is only the faster answer. A refused lookup goes in the
sandbox's connection log, once a minute per name, with the rule that refused it.

## Private hosts (company VPN)

Upstream connections refuse non-public addresses, so a company host that resolves to a
private address, such as `git.company.local` behind a VPN, is unreachable by default. An
environment's private hosts list (Settings → Network) is the opt-in for such hosts:

- A connection to a listed name on a listed port may reach the private addresses that name
  resolves to: RFC 1918 (10/8, 172.16/12, 192.168/16), IPv6 unique local (fc00::/7) and
  carrier-grade NAT (100.64/10, which Tailscale uses). Public addresses stay reachable as before.
- Stays refused for every name: loopback, link-local (including the cloud metadata address
  169.254.169.254), unspecified and multicast addresses, `198.18.0.0/15` (Studio's virtual
  services), the host's own private addresses (so a dev server on `0.0.0.0` stays out of
  reach) and Studio's own ports.
- A name not on the list stays refused private addresses, whatever DNS says, so a name that
  rebinds to a private address is still blocked. An IP address is never a private host: a
  sandbox can't reach a private address by naming it.
- The list only lifts the address restriction. The policy still decides, so a private host
  needs an allow rule, an approval or a security level's allow like any other host. A level never
  allows a private address, so a listed name is decided like any other name. Approvals, rules,
  the AI reviewer
  and imports never add to the list. An environment file doesn't carry it, so an import
  starts without private hosts.
- An entry is a host name or a wildcard over a domain of its own: `git.company.local` or
  `*.company.local`. Not `*`, not an IP address, and not a public suffix such as `*.com`.
  Ports default to 80 and 443; other ports, such as 22 for SSH, must be listed.

**Names.** The sandbox asks its own resolver, which asks the host's resolvers, so the VPN's
split DNS applies (on Linux, through systemd-resolved's routing domains). Listed names never
go to the public fallback resolvers (1.1.1.1, 9.9.9.9), so internal names don't leak when the
host's resolver is down. microsandbox drops private answers before they reach a sandbox,
so for a listed name the resolver answers with a virtual address from `198.18.0.0/15`, which
the gateway maps back to the name, as it does for `*.studio.internal`. Lookups of listed names
get IPv4 addresses only. When the sandbox connects, the gateway dials the name, not the
address the sandbox holds: it resolves the name itself, with the host's resolver (Go's
`net.DefaultResolver`, which reads `/etc/resolv.conf` and `nsswitch.conf`, or the system
resolver through cgo), and checks each address it connects to. That is why the sandbox's
answers never matter for a private host, and why a `.local` name must resolve through
unicast DNS on the host. mDNS-only names (answered on the LAN by multicast) aren't supported.
On systemd-resolved, a `.local` name goes to the VPN's DNS only when the VPN link has a routing
domain for it. Check with `resolvectl query git.company.local`.

**Other protocols.** The gateway dials the name of any TCP connection that has one (from SOCKS,
the sandbox's DNS answers, or the TLS server name or HTTP Host), so `ssh` to a listed host works
on a listed port, such as 22, given a rule that allows the host. A connection by IP
address alone, for example through a hosts file entry, can't reach a private host, since the
gateway needs the name. This is untested here: no VPN was available.

**Setup.**

1. In Settings → Network, add `*.company.local` (or `git.company.local`). Add a port such as
   `22` only if a protocol other than web needs it.
2. On the host, check the name resolves: `resolvectl query git.company.local`.
3. Allow the host: approve the sandbox's request on the Requests tab, or add an allow rule for
   `*.company.local` on the Rules tab.
4. From a sandbox, check the connection: `curl -v https://git.company.local/`. A refusal says
   which rule or address refused it.

Removing an entry takes effect on the next connection. It doesn't close connections already
open.

## Proxy rule overrides

A `proxy` rule can change where its requests go. The rule editor offers these under Advanced:

- **Upstream**: a host or host:port to send the requests to, instead of the rule's host. The
  port defaults to the connection's. The upstream goes through the same dial guard: public
  addresses, or the private addresses of a [private host](#private-hosts-company-vpn). Secrets
  go only to the upstream they are bound to, so a placeholder of a secret bound only to the
  approved host is refused.
- **Host header**: the Host header sent upstream. Without it, the sandbox's Host header (the
  approved host) is sent.
- **SNI**: the TLS server name sent upstream, which the upstream's certificate must match.
  Without it, the upstream's name is used.
- **CA**: PEM certificates trusted for the upstream, besides the system's, for upstreams with a
  private CA. Up to eight certificates; a private key is refused.

The sandbox's Host header must still name the approved host, so a rule can't be used to reach
another site through a shared front end.

## Interception and secrets

On intercepted connections (`intercept.go`):

- placeholders are swapped for values in request headers (including Basic credentials),
  path and query, never in bodies (an upstream may store a body and hand it back);
- a request that carries a placeholder of a secret its host isn't bound to is never allowed by a
  [security level](#security-levels): a browser request is asked about, and the rewrite refuses
  it either way;
- a placeholder of a secret not bound to this host refuses the request, and so does one of a
  secret the sandbox doesn't see (another sandbox's or teammate's, or one a more specific
  secret of the same name overrides);
- the rule's headers are set;
- the Host header must match the approved host (no domain fronting);
- responses mask the values of the host's secrets (same length, so `Content-Length` holds,
  overlapping values included), and secret-bearing responses drop trailers. This masks
  literal values only; it can't detect an upstream that transforms a credential.

## Caddy rules

A `caddy` rule's config is the body of one site block, compiled by Caddy's Caddyfile adapter
and run as a library (no listeners, no admin endpoint, storage in `<data>/caddy`). The
compiler (`caddyrule/compile.go`) allows a fixed set of matchers, handlers and
`reverse_proxy` options. It refuses environment and file placeholders, `{$VAR}`, `import`
and `encode`; upstreams must be fixed addresses, dialed as the gateway dials (private hosts
included). Secrets
(`{secret.NAME}`) may appear only in `reverse_proxy` request header values, only for
`https` upstreams the secret is bound to. Caddy never sees them: it gets a marker that
Studio's transport fills in. The sandbox's own placeholders are not swapped under a Caddy
rule.

## Secrets vault

- Values are sealed in `studio.db` with a master key in the OS keychain (macOS Keychain,
  Windows Credential Manager, Secret Service; service "Sandbox Studio", account
  `master-key-<install id>`). Without a keychain the key is a 0600 file `<data>/vault.key`.
  The backend is recorded on first start and only that one is used afterwards; Studio never
  generates a replacement key.
- Each secret has hosts it is bound to: at least one, never a bare `*`.
- Sandboxes see the secret's name as an env var holding a placeholder,
  `studio-` plus 32 base32 characters (160 bits), generated once.
- Like a rule, a secret is scoped to the environment, to one teammate (every sandbox it
  owns) or to one sandbox. The scope is fixed at creation, and a name is unique per scope
  (migration `022_secret_scopes.sql`). A sandbox sees its environment's secrets, its
  teammate's and its own; of secrets sharing a name the most specific wins (sandbox over
  teammate over environment). `Vault.Env` and `Vault.Bindings` resolve this per sandbox, the
  gateway refuses the placeholder of any other secret, and a write reconfigures only the
  sandboxes its scope covers. The browser's `fill_credential` uses the same visibility for
  the sandbox driving it.
- Provider API keys and forge tokens are secrets too, managed by their owner (the secrets
  API answers 409 for them). They always belong to the environment.

## Per-environment CA

Each environment has its own CA (`internal/ca`): 10-year CA, 7-day leaf certificates renewed
a day early, at most 1024 cached leaves per environment. Private keys are stored in the
catalog sealed by the vault. Certificates and placeholders therefore never validate across
environments. How guests trust the CA: [guest agent](guest-agent.md#ca-trust-and-placeholders-in-the-guest).

## Connection log

The gateway keeps the last 500 connections per sandbox, in memory only (`connlog.go`):
host, port, verdict (`pending`, `open`, `allowed`, `denied`, `undecided`, `failed`), rule,
bytes. The sandbox dashboard and its Connections panel show it (`GET …/sandboxes/{id}/connections`).
