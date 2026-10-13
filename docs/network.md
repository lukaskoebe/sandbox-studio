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
   - `proxy` and `caddy` rules, and any web connection to a host a secret is bound to, are
     intercepted: TLS is terminated with a certificate from the environment's CA, then each
     HTTP request is checked, rewritten and sent on over a new connection to the same host.
     Non-web traffic under such a rule fails.
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
API, `approval` for rules created by approving or denying a request. `curated` (the shipped
list) and `reviewer` (the AI reviewer) are reserved for later. The origin is read-only and
set when the rule is created; rules from before origins existed are `manual`.

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
  `example.com`), then rules that name ports, then `deny` over `allow`.

## The Network page

The page (`web/src/routes/e.$env.network*.tsx`) has three tabs, each a route. The private
hosts list is edited under Settings → Network:

- **Rules** lists the environment's rules. Its search and scope, action, origin and unused
  filters are URL search params (`q`, `scope`, `action`, `origin`, `unused=1`), so a filtered
  view can be shared. Each rule shows its origin as a chip, and an unused rule an "unused"
  chip. Delete unused removes the unused rules the filters show, after a confirmation. Rules are grouped
  by registrable domain: the last two labels, or three under a short list of two-label
  suffixes such as `co.uk` (`web/src/lib/network-rules.ts`). No public suffix list is used.
- **Requests** lists the pending network approvals; Allow, Deny and Ignore work as on the
  approval card.
- **Activity** lists decided network approvals, newest first, up to 50.

`/e/$env/network` opens Requests when any wait for a decision, and Rules otherwise.

## Hold and ask

With no matching rule, the gateway holds the connection (the TLS ClientHello stays
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

Requests from one sandbox that arrive within 3 seconds of each other show as one card on the
approval stack, with Allow all, Review individually and Deny all (see
[approvals](approvals.md#realtime-and-ui)). Requests carry no information about the page that
made them yet, so the grouping is by sandbox and arrival time; grouping by page comes with
site-scoped approvals.

60 s comes from spike S6: it covers curl, git, npm and apt, and pip succeeds on retry. Node
`fetch` gives up after 10 s, so the UI must show the request quickly.

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
  needs an allow rule or an approval like any other host. Approvals, rules, the AI reviewer
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
